package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/VictoriaMetrics/VictoriaLogs/lib/logstorage"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/VictoriaMetrics/mcp-victorialogs/cmd/mcp-victorialogs/config"
)

func CreateSelectRequest(ctx context.Context, cfg *config.Config, tcr mcp.CallToolRequest, path ...string) (*http.Request, error) {
	accountID, projectID, err := GetToolReqTenant(tcr)
	if err != nil {
		return nil, fmt.Errorf("failed to get tenant: %v", err)
	}

	selectURL, err := getSelectURL(ctx, cfg, tcr, path...)
	if err != nil {
		return nil, fmt.Errorf("failed to get select URL: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, selectURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	bearerToken := cfg.BearerToken()
	if bearerToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", bearerToken))
	}

	// Add custom headers from configuration
	for key, value := range cfg.CustomHeaders() {
		req.Header.Set(key, value)
	}

	// Apply passthrough headers from the incoming MCP request
	for _, name := range cfg.PassthroughHeaders() {
		if value := tcr.Header.Get(name); value != "" {
			req.Header.Set(name, value)
		}
	}

	defaultTenantID := cfg.DefaultTenantID()
	if accountID == "" {
		accountID = strconv.FormatUint(uint64(defaultTenantID.AccountID), 10)
	}
	if projectID == "" {
		projectID = strconv.FormatUint(uint64(defaultTenantID.ProjectID), 10)
	}

	req.Header.Set("AccountID", accountID)
	req.Header.Set("ProjectID", projectID)

	return req, nil
}

func CreateAdminRequest(ctx context.Context, cfg *config.Config, tcr mcp.CallToolRequest, path ...string) (*http.Request, error) {
	selectURL, err := getRootURL(ctx, cfg, tcr, path...)
	if err != nil {
		return nil, fmt.Errorf("failed to get select URL: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, selectURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	bearerToken := cfg.BearerToken()
	if bearerToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", bearerToken))
	}

	// Add custom headers from configuration
	for key, value := range cfg.CustomHeaders() {
		req.Header.Set(key, value)
	}

	// Apply passthrough headers from the incoming MCP request
	for _, name := range cfg.PassthroughHeaders() {
		if value := tcr.Header.Get(name); value != "" {
			req.Header.Set(name, value)
		}
	}

	return req, nil
}

func getRootURL(_ context.Context, cfg *config.Config, tcr mcp.CallToolRequest, path ...string) (string, error) {
	base, err := cfg.EntryPointURLForContour(getContour(tcr))
	if err != nil {
		return "", err
	}
	return base.JoinPath(path...).String(), nil
}

func getSelectURL(_ context.Context, cfg *config.Config, tcr mcp.CallToolRequest, path ...string) (string, error) {
	base, err := cfg.EntryPointURLForContour(getContour(tcr))
	if err != nil {
		return "", err
	}
	return base.JoinPath("select", "logsql").JoinPath(path...).String(), nil
}

// getContour reads the optional "contour" tool argument. Empty string means the
// default contour (resolved in config.EntryPointURLForContour).
func getContour(tcr mcp.CallToolRequest) string {
	contour, _ := GetToolReqParam[string](tcr, "contour", false)
	return contour
}

// WithContour returns the shared optional "contour" tool option. It lets a
// single MCP server route a tool call to one of several VictoriaLogs instances
// (contours/clusters) configured via VL_CONTOURS. Omitting it uses the default
// contour.
func WithContour() mcp.ToolOption {
	return mcp.WithString("contour",
		mcp.Title("Contour / cluster"),
		mcp.Description("Which logs contour (VictoriaLogs cluster) to query, e.g. infra, omega, omicron. Omit to use the default contour."),
	)
}

func GetTextBodyForRequest(req *http.Request, _ *config.Config) *mcp.CallToolResult {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to do request: %v", err))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to read response body: %v", err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return mcp.NewToolResultError(fmt.Sprintf("unexpected response status code %v: %s", resp.StatusCode, string(body)))
	}
	return mcp.NewToolResultText(string(body))
}

type ToolReqParamType interface {
	string | float64 | bool | []string | []any
}

func GetToolReqParam[T ToolReqParamType](tcr mcp.CallToolRequest, param string, required bool) (T, error) {
	var value T
	matchArg, ok := tcr.GetArguments()[param]
	if ok {
		value, ok = matchArg.(T)
		if !ok {
			return value, fmt.Errorf("%s has wrong type: %T", param, matchArg)
		}
	} else if required {
		return value, fmt.Errorf("%s param is required", param)
	}
	return value, nil
}

func GetToolReqTenant(tcr mcp.CallToolRequest) (string, string, error) {
	tenant, err := GetToolReqParam[string](tcr, "tenant", false)
	if err != nil {
		return "", "", fmt.Errorf("failed to get tenant: %v", err)
	}
	if tenant == "" {
		return "", "", nil
	}
	tenantID, err := logstorage.ParseTenantID(tenant)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse tenant %q: %v", tenant, err)
	}
	accountID := strconv.FormatUint(uint64(tenantID.AccountID), 10)
	projectID := strconv.FormatUint(uint64(tenantID.ProjectID), 10)
	return accountID, projectID, nil
}

func ptr[T any](v T) *T {
	return &v
}
