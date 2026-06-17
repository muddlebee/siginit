package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/muddlebee/siginit/internal/signoz"
)

// QuerySigNoz verifies whether telemetry from a service has arrived.
// This is the moat: the LLM cannot declare success — SigNoz is the ground truth.
type QuerySigNoz struct {
	Client *signoz.Client
}

func (t *QuerySigNoz) Name() string        { return "query_signoz" }
func (t *QuerySigNoz) ReadOnly() bool       { return true }
func (t *QuerySigNoz) Description() string {
	return "Check whether traces from a service have arrived in SigNoz. Returns span count and whether the service appears in the services list. Call this after starting the instrumented app."
}
func (t *QuerySigNoz) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["service_name"],
		"properties": {
			"service_name": {"type": "string", "description": "The OTEL service.name to verify"},
			"lookback_minutes": {"type": "number", "description": "How many minutes back to search (default 5)"}
		}
	}`)
}

func (t *QuerySigNoz) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		ServiceName      string  `json:"service_name"`
		LookbackMinutes  float64 `json:"lookback_minutes"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.ServiceName == "" {
		return "", fmt.Errorf("service_name is required")
	}
	if params.LookbackMinutes <= 0 {
		params.LookbackMinutes = 5
	}

	end := time.Now()
	start := end.Add(-time.Duration(params.LookbackMinutes * float64(time.Minute)))

	result, err := t.Client.Verify(ctx, params.ServiceName, start, end)
	if err != nil {
		return "", err
	}

	out := map[string]any{
		"service_found": result.ServiceFound,
		"span_count":    result.SpanCount,
		"all_services":  result.Services,
		"window":        fmt.Sprintf("last %.0f minutes", params.LookbackMinutes),
	}
	if result.ServiceFound {
		out["verdict"] = fmt.Sprintf("SUCCESS: service %q is visible in SigNoz with %d spans", params.ServiceName, result.SpanCount)
	} else {
		out["verdict"] = fmt.Sprintf("NOT YET: service %q not found in SigNoz (checked %d known services)", params.ServiceName, len(result.Services))
	}

	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}
