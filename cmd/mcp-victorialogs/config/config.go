package config

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/VictoriaMetrics/VictoriaLogs/lib/logstorage"
)

type Config struct {
	serverMode         string
	listenAddr         string
	entrypoint         string
	bearerToken        string
	customHeaders      map[string]string
	passthroughHeaders []string
	disabledTools      map[string]bool
	heartbeatInterval  time.Duration
	defaultTenantID    logstorage.TenantID

	entryPointURL *url.URL

	// Multi-contour support. contours maps a contour name (e.g. "infra",
	// "omega", "omicron") to its VictoriaLogs entrypoint URL. The contour
	// named defaultContour is used when a tool call omits the "contour"
	// argument. The VL_INSTANCE_ENTRYPOINT value is always registered under
	// defaultContour, so single-instance setups keep working unchanged.
	contours       map[string]*url.URL
	defaultContour string
	// contourSelectionHint is free-text guidance returned by the list_contours
	// tool to help pick the right contour (set via VL_CONTOUR_SELECTION_HINT).
	contourSelectionHint string

	// Logging configuration
	logFormat string
	logLevel  string
}

func InitConfig() (*Config, error) {
	disabledTools := os.Getenv("MCP_DISABLED_TOOLS")
	disabledToolsMap := make(map[string]bool)
	if disabledTools != "" {
		for _, tool := range strings.Split(disabledTools, ",") {
			tool = strings.Trim(tool, " ,")
			if tool != "" {
				disabledToolsMap[tool] = true
			}
		}
	}

	customHeaders := os.Getenv("VL_INSTANCE_HEADERS")
	customHeadersMap := make(map[string]string)
	if customHeaders != "" {
		for _, header := range strings.Split(customHeaders, ",") {
			header = strings.TrimSpace(header)
			if header != "" {
				parts := strings.SplitN(header, "=", 2)
				if len(parts) == 2 {
					key := strings.TrimSpace(parts[0])
					value := strings.TrimSpace(parts[1])
					if key != "" && value != "" {
						customHeadersMap[key] = value
					}
				}
			}
		}
	}

	var passthroughHeaders []string
	passthroughHeadersStr := os.Getenv("MCP_PASSTHROUGH_HEADERS")
	if passthroughHeadersStr != "" {
		for _, h := range strings.Split(passthroughHeadersStr, ",") {
			h = strings.TrimSpace(h)
			if h != "" {
				passthroughHeaders = append(passthroughHeaders, h)
			}
		}
	}

	heartbeatInterval := 30 * time.Second
	heartbeatIntervalStr := os.Getenv("MCP_HEARTBEAT_INTERVAL")
	if heartbeatIntervalStr != "" {
		interval, err := time.ParseDuration(heartbeatIntervalStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse MCP_HEARTBEAT_INTERVAL: %w", err)
		}
		if interval < 0 {
			return nil, fmt.Errorf("MCP_HEARTBEAT_INTERVAL must be a non-negative")
		}
		heartbeatInterval = interval
	}

	logFormat := strings.ToLower(os.Getenv("MCP_LOG_FORMAT"))
	if logFormat == "" {
		logFormat = "text"
	}
	if logFormat != "text" && logFormat != "json" {
		return nil, fmt.Errorf("MCP_LOG_FORMAT must be 'text' or 'json'")
	}

	logLevel := strings.ToLower(os.Getenv("MCP_LOG_LEVEL"))
	if logLevel == "" {
		logLevel = "info"
	}
	if logLevel != "debug" && logLevel != "info" && logLevel != "warn" && logLevel != "error" {
		return nil, fmt.Errorf("MCP_LOG_LEVEL must be 'debug', 'info', 'warn' or 'error'")
	}

	result := &Config{
		serverMode:         strings.ToLower(os.Getenv("MCP_SERVER_MODE")),
		listenAddr:         os.Getenv("MCP_LISTEN_ADDR"),
		entrypoint:         os.Getenv("VL_INSTANCE_ENTRYPOINT"),
		bearerToken:        os.Getenv("VL_INSTANCE_BEARER_TOKEN"),
		customHeaders:      customHeadersMap,
		passthroughHeaders: passthroughHeaders,
		disabledTools:      disabledToolsMap,
		heartbeatInterval:  heartbeatInterval,
		logFormat:          logFormat,
		logLevel:           logLevel,
		defaultTenantID:    logstorage.TenantID{AccountID: 0, ProjectID: 0},
	}
	// Left for backward compatibility
	if result.listenAddr == "" {
		result.listenAddr = os.Getenv("MCP_SSE_ADDR")
	}
	if result.entrypoint == "" {
		return nil, fmt.Errorf("VL_INSTANCE_ENTRYPOINT is not set")
	}
	if result.serverMode != "" && result.serverMode != "stdio" && result.serverMode != "sse" && result.serverMode != "http" {
		return nil, fmt.Errorf("MCP_SERVER_MODE must be 'stdio', 'sse' or 'http'")
	}
	if result.serverMode == "" {
		result.serverMode = "stdio"
	}
	if result.listenAddr == "" {
		result.listenAddr = "localhost:8081"
	}

	defaultTenantID := strings.ToLower(os.Getenv("VL_DEFAULT_TENANT_ID"))
	if defaultTenantID != "" {
		tenantID, err := logstorage.ParseTenantID(defaultTenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to parse VL_DEFAULT_TENANT_ID %q: %w", defaultTenantID, err)
		}
		result.defaultTenantID = tenantID
	}

	var err error

	result.entryPointURL, err = url.Parse(result.entrypoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL from VL_INSTANCE_ENTRYPOINT: %w", err)
	}

	// Multi-contour map. VL_INSTANCE_ENTRYPOINT is always registered under the
	// default contour name (VL_DEFAULT_CONTOUR, "default" if unset). Additional
	// contours come from VL_CONTOURS, a comma-separated list of name=url pairs,
	// e.g. "omega=http://127.0.0.1:9471,omicron=http://127.0.0.1:9472".
	result.defaultContour = strings.TrimSpace(os.Getenv("VL_DEFAULT_CONTOUR"))
	if result.defaultContour == "" {
		result.defaultContour = "default"
	}
	result.contours = map[string]*url.URL{result.defaultContour: result.entryPointURL}

	contoursStr := os.Getenv("VL_CONTOURS")
	if contoursStr != "" {
		for _, pair := range strings.Split(contoursStr, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid VL_CONTOURS entry %q: expected name=url", pair)
			}
			name := strings.TrimSpace(parts[0])
			rawURL := strings.TrimSpace(parts[1])
			if name == "" || rawURL == "" {
				return nil, fmt.Errorf("invalid VL_CONTOURS entry %q: empty name or url", pair)
			}
			u, err := url.Parse(rawURL)
			if err != nil {
				return nil, fmt.Errorf("failed to parse URL for contour %q: %w", name, err)
			}
			result.contours[name] = u
		}
	}

	result.contourSelectionHint = strings.TrimSpace(os.Getenv("VL_CONTOUR_SELECTION_HINT"))

	return result, nil
}

func (c *Config) IsStdio() bool {
	return c.serverMode == "stdio"
}

func (c *Config) IsSSE() bool {
	return c.serverMode == "sse"
}

func (c *Config) ServerMode() string {
	return c.serverMode
}

func (c *Config) ListenAddr() string {
	return c.listenAddr
}

func (c *Config) BearerToken() string {
	return c.bearerToken
}

func (c *Config) EntryPointURL() *url.URL {
	return c.entryPointURL
}

// EntryPointURLForContour returns the VictoriaLogs entrypoint URL for the given
// contour name. An empty name resolves to the default contour. An unknown name
// returns an error listing the available contours.
func (c *Config) EntryPointURLForContour(name string) (*url.URL, error) {
	if name == "" {
		name = c.defaultContour
	}
	u, ok := c.contours[name]
	if !ok {
		return nil, fmt.Errorf("unknown contour %q, available: %s", name, strings.Join(c.Contours(), ", "))
	}
	return u, nil
}

// Contours returns the sorted list of configured contour names.
func (c *Config) Contours() []string {
	names := make([]string, 0, len(c.contours))
	for name := range c.contours {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DefaultContour returns the contour name used when a tool call omits "contour".
func (c *Config) DefaultContour() string {
	return c.defaultContour
}

// ContourSelectionHint returns free-text guidance on how to choose a contour,
// surfaced by the list_contours tool (set via VL_CONTOUR_SELECTION_HINT).
func (c *Config) ContourSelectionHint() string {
	return c.contourSelectionHint
}

func (c *Config) IsToolDisabled(toolName string) bool {
	if c.disabledTools == nil {
		return false
	}
	disabled, ok := c.disabledTools[toolName]
	return ok && disabled
}

func (c *Config) HeartbeatInterval() time.Duration {
	return c.heartbeatInterval
}

func (c *Config) CustomHeaders() map[string]string {
	return c.customHeaders
}

func (c *Config) PassthroughHeaders() []string {
	return c.passthroughHeaders
}

func (c *Config) LogFormat() string {
	return c.logFormat
}

func (c *Config) LogLevel() string {
	return c.logLevel
}

func (c *Config) DefaultTenantID() logstorage.TenantID {
	return c.defaultTenantID
}
