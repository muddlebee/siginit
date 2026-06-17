package signoz

import (
	"context"
	"fmt"
	"net"
	"time"
)

// Layer names for diagnostic output.
const (
	LayerCollectorTCP = "collector_tcp"
	LayerHealth       = "signoz_health"
	LayerAuth         = "signoz_auth"
	LayerServices     = "signoz_services"
	LayerSpans        = "span_count"
)

// DiagResult is one diagnostic check.
type DiagResult struct {
	Layer   string
	OK      bool
	Message string
	Fix     string // suggested fix when !OK
}

// DoctorConfig is the input to the Doctor check.
type DoctorConfig struct {
	CollectorAddr string // e.g. localhost:4318
	ServiceName   string
	Email         string
	Password      string
	LookbackMins  float64
}

// Doctor runs all diagnostic layers in order and returns the results.
func Doctor(ctx context.Context, c *Client, cfg DoctorConfig) []DiagResult {
	var results []DiagResult

	// Layer 1: TCP dial to collector.
	if cfg.CollectorAddr != "" {
		r := diagTCP(cfg.CollectorAddr)
		results = append(results, r)
		if !r.OK {
			return results // can't go further without collector
		}
	}

	// Layer 2: SigNoz health.
	if err := c.Health(ctx); err != nil {
		results = append(results, DiagResult{
			Layer:   LayerHealth,
			OK:      false,
			Message: fmt.Sprintf("health check failed: %s", err),
			Fix:     "Is SigNoz running? Check: docker ps | grep signoz",
		})
		return results
	}
	results = append(results, DiagResult{Layer: LayerHealth, OK: true, Message: "SigNoz is healthy"})

	// Layer 3: Auth.
	tok, err := c.Login(ctx, cfg.Email, cfg.Password)
	if err != nil {
		results = append(results, DiagResult{
			Layer:   LayerAuth,
			OK:      false,
			Message: fmt.Sprintf("login failed: %s", err),
			Fix:     "Check credentials. If first run: use --register to create the admin account.",
		})
		return results
	}
	c.token = tok
	results = append(results, DiagResult{Layer: LayerAuth, OK: true, Message: "authenticated"})

	// Layer 4: Services list.
	lookback := cfg.LookbackMins
	if lookback <= 0 {
		lookback = 15
	}
	end := time.Now()
	start := end.Add(-time.Duration(lookback * float64(time.Minute)))
	services, err := c.Services(ctx, start, end)
	if err != nil {
		results = append(results, DiagResult{
			Layer:   LayerServices,
			OK:      false,
			Message: fmt.Sprintf("services list failed: %s", err),
			Fix:     "SigNoz returned an error. Check signoz logs: docker logs signoz",
		})
		return results
	}

	if cfg.ServiceName != "" {
		found := false
		for _, s := range services {
			if s == cfg.ServiceName {
				found = true
				break
			}
		}
		if !found {
			results = append(results, DiagResult{
				Layer:   LayerServices,
				OK:      false,
				Message: fmt.Sprintf("service %q not in SigNoz (known: %v)", cfg.ServiceName, services),
				Fix:     fmt.Sprintf("Is your app running with OTEL_SERVICE_NAME=%s? Is the exporter pointing to the collector?", cfg.ServiceName),
			})
		} else {
			results = append(results, DiagResult{Layer: LayerServices, OK: true, Message: fmt.Sprintf("service %q found", cfg.ServiceName)})
		}
	} else {
		results = append(results, DiagResult{Layer: LayerServices, OK: true, Message: fmt.Sprintf("%d services visible", len(services))})
	}

	// Layer 5: Span count.
	if cfg.ServiceName != "" {
		count, err := c.CountSpans(ctx, cfg.ServiceName, start, end)
		if err != nil {
			results = append(results, DiagResult{
				Layer:   LayerSpans,
				OK:      false,
				Message: fmt.Sprintf("span query failed: %s", err),
				Fix:     "Check SigNoz query service logs.",
			})
		} else if count == 0 {
			results = append(results, DiagResult{
				Layer:   LayerSpans,
				OK:      false,
				Message: fmt.Sprintf("0 spans from %q in last %.0f minutes", cfg.ServiceName, lookback),
				Fix:     "Spans not arriving. Check: (1) app is running, (2) OTel SDK is initialized, (3) exporter endpoint is correct, (4) collector logs show received spans.",
			})
		} else {
			results = append(results, DiagResult{Layer: LayerSpans, OK: true, Message: fmt.Sprintf("%d spans from %q", count, cfg.ServiceName)})
		}
	}

	return results
}

func diagTCP(addr string) DiagResult {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return DiagResult{
			Layer:   LayerCollectorTCP,
			OK:      false,
			Message: fmt.Sprintf("cannot reach collector at %s: %s", addr, err),
			Fix:     fmt.Sprintf("Is the OTel collector running? Check: docker ps | grep otel-collector. Expected address: %s", addr),
		}
	}
	conn.Close()
	return DiagResult{Layer: LayerCollectorTCP, OK: true, Message: fmt.Sprintf("collector reachable at %s", addr)}
}
