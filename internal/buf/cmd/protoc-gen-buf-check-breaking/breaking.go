package breaking

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/bufbuild/buf/internal/buf/bufpb"
	"github.com/bufbuild/buf/internal/buf/cmd/internal"
	"github.com/bufbuild/buf/internal/pkg/analysis"
	"github.com/bufbuild/buf/internal/pkg/bytepool"
	"github.com/bufbuild/buf/internal/pkg/cli/cliplugin"
	"github.com/bufbuild/buf/internal/pkg/encodingutil"
	"github.com/bufbuild/buf/internal/pkg/errs"
	"github.com/bufbuild/buf/internal/pkg/logutil"
	plugin_go "github.com/golang/protobuf/protoc-gen-go/plugin"
)

const defaultTimeout = 10 * time.Second

// Main is the main.
func Main() {
	cliplugin.Main(cliplugin.HandlerFunc(Handle))
}

// Handle implements the handler.
//
// Public so this can be used in the cmdtesting package.
func Handle(
	stderr io.Writer,
	request *plugin_go.CodeGeneratorRequest,
) ([]*plugin_go.CodeGeneratorResponse_File, error) {
	externalConfig := &externalConfig{}
	if err := encodingutil.UnmarshalJSONOrYAMLStrict([]byte(request.GetParameter()), externalConfig); err != nil {
		return nil, err
	}
	if externalConfig.AgainstInput == "" {
		// this is actually checked as part of ReadImageEnv but just in case
		return nil, errs.NewUserError(`"against_input" is required`)
	}
	timeout := externalConfig.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	logger, err := logutil.NewLogger(stderr, externalConfig.LogLevel, externalConfig.LogFormat)
	if err != nil {
		return nil, err
	}

	files := request.FileToGenerate
	if !externalConfig.LimitToInputFiles {
		files = nil
	}
	envReader := internal.NewBufosEnvReader(logger, bytepool.NewNoPoolSegList(), "against_input", "against_input_config")
	againstEnv, err := envReader.ReadImageEnv(
		ctx,
		nil, // cannot read against input from stdin, this is for the CodeGeneratorRequest
		externalConfig.AgainstInput,
		encodingutil.GetJSONStringOrStringValue(externalConfig.AgainstInputConfig),
		files, // limit to the input files if specified
		true,  // allow files in the against input to not exist
		!externalConfig.ExcludeImports,
	)
	if err != nil {
		return nil, err
	}
	envReader = internal.NewBufosEnvReader(logger, bytepool.NewNoPoolSegList(), "", "input_config")
	config, err := envReader.GetConfig(ctx, encodingutil.GetJSONStringOrStringValue(externalConfig.InputConfig))
	if err != nil {
		return nil, err
	}
	image, err := bufpb.CodeGeneratorRequestToImage(request)
	if err != nil {
		return nil, err
	}
	annotations, err := internal.NewBufbreakingHandler(logger).BreakingCheck(
		ctx,
		config.Breaking,
		againstEnv.Image,
		image,
	)
	if err != nil {
		return nil, err
	}
	asJSON, err := internal.IsFormatJSON("error_format", externalConfig.ErrorFormat)
	if err != nil {
		return nil, err
	}
	return nil, analysis.AnnotationsToUserError(annotations, asJSON)
}

type externalConfig struct {
	AgainstInput       string          `json:"against_input,omitempty" yaml:"against_input,omitempty"`
	AgainstInputConfig json.RawMessage `json:"against_input_config,omitempty" yaml:"against_input_config,omitempty"`
	InputConfig        json.RawMessage `json:"input_config,omitempty" yaml:"input_config,omitempty"`
	LimitToInputFiles  bool            `json:"limit_to_input_files,omitempty" yaml:"limit_to_input_files,omitempty"`
	ExcludeImports     bool            `json:"exclude_imports,omitempty" yaml:"exclude_imports,omitempty"`
	LogLevel           string          `json:"log_level,omitempty" yaml:"log_level,omitempty"`
	LogFormat          string          `json:"log_format,omitempty" yaml:"log_format,omitempty"`
	ErrorFormat        string          `json:"error_format,omitempty" yaml:"error_format,omitempty"`
	Timeout            time.Duration   `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}
