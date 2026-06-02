package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/VictoriaMetrics/mcp-victorialogs/cmd/mcp-victorialogs/config"
)

// ToolNameContours is exported so it can be referenced from main when deciding
// whether to register the tool.
const ToolNameContours = "list_contours"

var (
	toolContours = mcp.NewTool(ToolNameContours,
		mcp.WithDescription("List the available log contours (VictoriaLogs clusters/environments) and guidance on which one to use. Call this FIRST, before any logs query, to decide the `contour` argument for the other tools. Returns the default contour, the available contour names, and free-text selection guidance."),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "List log contours",
			ReadOnlyHint:    ptr(true),
			DestructiveHint: ptr(false),
			OpenWorldHint:   ptr(false),
		}),
	)
)

type contoursResult struct {
	DefaultContour    string   `json:"default_contour"`
	Contours          []string `json:"contours"`
	SelectionGuidance string   `json:"selection_guidance,omitempty"`
}

func toolContoursHandler(_ context.Context, cfg *config.Config, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result := contoursResult{
		DefaultContour:    cfg.DefaultContour(),
		Contours:          cfg.Contours(),
		SelectionGuidance: cfg.ContourSelectionHint(),
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal contours: %v", err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

func RegisterToolContours(s *server.MCPServer, c *config.Config) {
	if c.IsToolDisabled(ToolNameContours) {
		return
	}
	s.AddTool(toolContours, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return toolContoursHandler(ctx, c, request)
	})
}
