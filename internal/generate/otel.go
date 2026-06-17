package generate

import (
	"fmt"
	"strings"
)

// OTelConfig is the generated instrumentation config for a stack.
type OTelConfig struct {
	// InstallCmd is the command to install OTel packages.
	InstallCmd string
	// StartScript is the modified start command / wrapper.
	StartScript string
	// EnvVars is the env snippet to add (export statements).
	EnvVars string
	// DocSnippet is the code snippet to paste into the app (may be empty for zero-code).
	DocSnippet string
	// ServiceName is the OTEL service name to use for verification.
	ServiceName string
	// CollectorEndpoint is the OTLP endpoint to target.
	CollectorEndpoint string
}

type Config struct {
	ServiceName       string
	CollectorHTTP     string // e.g. http://localhost:4318
	CollectorGRPC     string // e.g. http://localhost:4317
	Framework         string
}

// Generate returns an OTelConfig for the given stack and target SigNoz endpoint.
func Generate(lang, framework, serviceName, collectorHTTP string) OTelConfig {
	cfg := Config{
		ServiceName:   serviceName,
		CollectorHTTP: collectorHTTP,
		Framework:     framework,
	}

	switch lang {
	case "javascript", "typescript":
		return nodeConfig(cfg)
	case "python":
		return pythonConfig(cfg)
	case "go":
		return goConfig(cfg)
	default:
		return genericConfig(cfg)
	}
}

func nodeConfig(cfg Config) OTelConfig {
	pkg := "@opentelemetry/auto-instrumentations-node"
	return OTelConfig{
		ServiceName:       cfg.ServiceName,
		CollectorEndpoint: cfg.CollectorHTTP,
		InstallCmd:        fmt.Sprintf("npm install --save %s", pkg),
		StartScript: fmt.Sprintf(
			"OTEL_SERVICE_NAME=%s OTEL_EXPORTER_OTLP_ENDPOINT=%s node --require @opentelemetry/auto-instrumentations-node/register ./index.js",
			cfg.ServiceName, cfg.CollectorHTTP,
		),
		EnvVars: strings.Join([]string{
			fmt.Sprintf("export OTEL_SERVICE_NAME=%s", cfg.ServiceName),
			fmt.Sprintf("export OTEL_EXPORTER_OTLP_ENDPOINT=%s", cfg.CollectorHTTP),
			"export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
			"export NODE_OPTIONS=--require @opentelemetry/auto-instrumentations-node/register",
		}, "\n"),
		DocSnippet: "# No code changes needed — zero-code auto-instrumentation via NODE_OPTIONS",
	}
}

func pythonConfig(cfg Config) OTelConfig {
	return OTelConfig{
		ServiceName:       cfg.ServiceName,
		CollectorEndpoint: cfg.CollectorHTTP,
		InstallCmd:        "pip install opentelemetry-distro opentelemetry-exporter-otlp && opentelemetry-bootstrap -a install",
		StartScript: fmt.Sprintf(
			"OTEL_SERVICE_NAME=%s OTEL_EXPORTER_OTLP_ENDPOINT=%s opentelemetry-instrument python app.py",
			cfg.ServiceName, cfg.CollectorHTTP,
		),
		EnvVars: strings.Join([]string{
			fmt.Sprintf("export OTEL_SERVICE_NAME=%s", cfg.ServiceName),
			fmt.Sprintf("export OTEL_EXPORTER_OTLP_ENDPOINT=%s", cfg.CollectorHTTP),
			"export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		}, "\n"),
		DocSnippet: "# No code changes needed — zero-code auto-instrumentation via opentelemetry-instrument",
	}
}

func goConfig(cfg Config) OTelConfig {
	return OTelConfig{
		ServiceName:       cfg.ServiceName,
		CollectorEndpoint: cfg.CollectorHTTP,
		InstallCmd:        "go get go.opentelemetry.io/otel go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp",
		StartScript:       fmt.Sprintf("OTEL_SERVICE_NAME=%s go run .", cfg.ServiceName),
		EnvVars: strings.Join([]string{
			fmt.Sprintf("export OTEL_SERVICE_NAME=%s", cfg.ServiceName),
			fmt.Sprintf("export OTEL_EXPORTER_OTLP_ENDPOINT=%s", cfg.CollectorHTTP),
			"export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		}, "\n"),
		DocSnippet: goSnippet(cfg.ServiceName, cfg.CollectorHTTP),
	}
}

func genericConfig(cfg Config) OTelConfig {
	return OTelConfig{
		ServiceName:       cfg.ServiceName,
		CollectorEndpoint: cfg.CollectorHTTP,
		EnvVars: strings.Join([]string{
			fmt.Sprintf("OTEL_SERVICE_NAME=%s", cfg.ServiceName),
			fmt.Sprintf("OTEL_EXPORTER_OTLP_ENDPOINT=%s", cfg.CollectorHTTP),
			"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		}, "\n"),
		DocSnippet: "# Configure your OTel SDK to export to " + cfg.CollectorHTTP,
	}
}

func goSnippet(serviceName, endpoint string) string {
	return fmt.Sprintf(`// Add to main() before serving:
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func initTracer(ctx context.Context) (func(), error) {
    exp, _ := otlptracehttp.New(ctx,
        otlptracehttp.WithEndpoint(%q),
        otlptracehttp.WithInsecure(),
    )
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exp),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceName(%q),
        )),
    )
    otel.SetTracerProvider(tp)
    return func() { tp.Shutdown(ctx) }, nil
}`, endpoint, serviceName)
}
