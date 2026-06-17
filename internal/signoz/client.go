package signoz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client speaks to a running SigNoz instance.
type Client struct {
	BaseURL    string
	token      string
	httpClient *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL:    baseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) WithToken(tok string) *Client {
	c.token = tok
	return c
}

// Health checks GET /api/v1/health — no auth required.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/api/v1/health", nil)
	return err
}

// Register creates the first admin user + org.
// POST /api/v1/register (open access — only works when no users exist).
func (c *Client) Register(ctx context.Context, name, email, password, orgName string) error {
	body := map[string]any{
		"name":           name,
		"email":          email,
		"password":       password,
		"orgDisplayName": orgName,
		"orgName":        orgName,
	}
	_, err := c.do(ctx, http.MethodPost, "/api/v1/register", body)
	return err
}

// OrgContext fetches available orgs from GET /api/v2/sessions/context (open, no auth).
// Returns the ID of the first org found, or "" if none.
func (c *Client) OrgContext(ctx context.Context, email string) (string, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v2/sessions/context?email="+email+"&ref=", nil)
	if err != nil {
		return "", fmt.Errorf("session context: %w", err)
	}
	var envelope struct {
		Data struct {
			Orgs []struct {
				ID string `json:"id"`
			} `json:"orgs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("session context: decode: %w", err)
	}
	if len(envelope.Data.Orgs) == 0 {
		return "", nil
	}
	return envelope.Data.Orgs[0].ID, nil
}

// Login authenticates via POST /api/v2/sessions/email_password and returns an access token.
// It auto-fetches the orgId from /api/v2/sessions/context if not already known.
func (c *Client) Login(ctx context.Context, email, password string) (string, error) {
	orgID, err := c.OrgContext(ctx, email)
	if err != nil {
		return "", fmt.Errorf("login: get org context: %w", err)
	}

	body := map[string]any{
		"email":    email,
		"password": password,
		"orgId":    orgID,
	}
	raw, err := c.do(ctx, http.MethodPost, "/api/v2/sessions/email_password", body)
	if err != nil {
		return "", err
	}

	var envelope struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("login: decode response: %w", err)
	}
	if envelope.Data.AccessToken == "" {
		return "", fmt.Errorf("login: empty access token (status=%s)", envelope.Status)
	}
	return envelope.Data.AccessToken, nil
}

// Services returns the list of services seen by SigNoz.
// GET /api/v1/services/list (ViewAccess — requires token).
func (c *Client) Services(ctx context.Context, start, end time.Time) ([]string, error) {
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()
	path := fmt.Sprintf("/api/v1/services/list?start=%d&end=%d", startMs, endMs)
	raw, err := c.doAuthed(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, fmt.Errorf("services: decode: %w", err)
	}
	return names, nil
}

// VerifyResult is what the verification engine returns.
type VerifyResult struct {
	ServiceFound bool
	SpanCount    int64
	Services     []string
}

// Verify checks whether traces from serviceName have arrived in [start, end].
// It tries services/list first (fast), then falls back to query_range (authoritative).
func (c *Client) Verify(ctx context.Context, serviceName string, start, end time.Time) (*VerifyResult, error) {
	services, err := c.Services(ctx, start, end)
	if err != nil {
		return nil, fmt.Errorf("verify: list services: %w", err)
	}

	result := &VerifyResult{Services: services}
	for _, s := range services {
		if s == serviceName {
			result.ServiceFound = true
			break
		}
	}

	// Count spans regardless so the TUI can show a number.
	count, err := c.CountSpans(ctx, serviceName, start, end)
	if err != nil {
		// Non-fatal: services list is the primary signal.
		count = -1
	}
	result.SpanCount = count
	return result, nil
}

// CountSpans queries POST /api/v5/query_range for span count of a service.
func (c *Client) CountSpans(ctx context.Context, serviceName string, start, end time.Time) (int64, error) {
	startMs := uint64(start.UnixMilli())
	endMs := uint64(end.UnixMilli())

	body := map[string]any{
		"schemaVersion": "v1",
		"start":         startMs,
		"end":           endMs,
		"requestType":   "time_series",
		"compositeQuery": map[string]any{
			"queries": []map[string]any{
				{
					"type": "builder_query",
					"spec": map[string]any{
						"name":         "A",
						"signal":       "traces",
						"stepInterval": "60s",
						"aggregations": []map[string]any{
							{"expression": "count()", "alias": "count"},
						},
						"filter": map[string]any{
							"expression": fmt.Sprintf("service.name = '%s'", serviceName),
						},
					},
				},
			},
		},
		"noCache": true,
	}

	raw, err := c.doAuthed(ctx, http.MethodPost, "/api/v5/query_range", body)
	if err != nil {
		return 0, err
	}

	// The response is a nested structure; sum all series values.
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, fmt.Errorf("count_spans: decode: %w", err)
	}
	return sumSeriesValues(envelope), nil
}

// sumSeriesValues traverses the v5 query_range response and sums all span counts.
// Response path: data → data → results[] → aggregations[] → series[] → values[] → value
func sumSeriesValues(resp map[string]any) int64 {
	var total int64
	outerData, _ := resp["data"].(map[string]any)
	if outerData == nil {
		return 0
	}
	innerData, _ := outerData["data"].(map[string]any)
	if innerData == nil {
		return 0
	}
	results, _ := innerData["results"].([]any)
	for _, r := range results {
		rm, _ := r.(map[string]any)
		aggregations, _ := rm["aggregations"].([]any)
		for _, a := range aggregations {
			am, _ := a.(map[string]any)
			series, _ := am["series"].([]any)
			for _, s := range series {
				sm, _ := s.(map[string]any)
				points, _ := sm["values"].([]any)
				for _, p := range points {
					pm, _ := p.(map[string]any)
					if v, ok := pm["value"].(float64); ok {
						total += int64(v)
					}
				}
			}
		}
	}
	return total
}

// do sends an unauthenticated request and returns the raw body.
func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	return c.request(ctx, method, path, body, "")
}

// doAuthed sends an authenticated request (bearer token).
func (c *Client) doAuthed(ctx context.Context, method, path string, body any) ([]byte, error) {
	if c.token == "" {
		return nil, fmt.Errorf("not authenticated: call Login first")
	}
	return c.request(ctx, method, path, body, c.token)
}

func (c *Client) request(ctx context.Context, method, path string, body any, token string) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, truncate(string(raw), 200))
	}
	return raw, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
