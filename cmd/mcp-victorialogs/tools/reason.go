package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	reasonDescription = "Optional free-form context from the agent: the user's underlying task, what this call is trying to answer, or why specific params were chosen. Logged for review; never affects results. No secrets or customer personal data."
	reasonLogMax      = 300
)

// AddReasonToAllTools declares the optional `reason` argument on every
// registered tool. Call it once, after all tools are registered.
func AddReasonToAllTools(s *server.MCPServer) {
	for _, t := range s.ListTools() {
		if t.Tool.InputSchema.Properties == nil {
			t.Tool.InputSchema.Properties = map[string]any{}
		}
		if _, ok := t.Tool.InputSchema.Properties["reason"]; ok {
			continue
		}
		t.Tool.InputSchema.Properties["reason"] = map[string]any{
			"type":        "string",
			"description": reasonDescription,
		}
		s.AddTools(*t)
	}
}

// ReasonMiddleware strips `reason` from every tool call before the handler
// (it is never a reason to reject a call: any type, any tool) and writes one
// "tool call" line per call.
func ReasonMiddleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		reason := takeReason(&req)
		start := time.Now()
		res, err := next(ctx, req)
		outcome := "ok"
		if err != nil || (res != nil && res.IsError) {
			outcome = "error"
		}
		user, source := accessUser(req.Header)
		slog.Info("tool call", "server", "victorialogs", "tool", req.Params.Name, "outcome", outcome,
			"latency_ms", time.Since(start).Milliseconds(), "user", user, "user_source", source,
			"reason", reason, "reason_present", reason != "", "cf_ray", req.Header.Get("Cf-Ray"))
		return res, err
	}
}

// takeReason removes "reason" from the call arguments and returns it trimmed
// and clipped for the log. Arguments that are not an object pass untouched.
func takeReason(req *mcp.CallToolRequest) string {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return ""
	}
	raw, ok := args["reason"]
	if !ok {
		return ""
	}
	rest := make(map[string]any, len(args)-1)
	for k, v := range args {
		if k != "reason" {
			rest[k] = v
		}
	}
	req.Params.Arguments = rest
	s, ok := raw.(string)
	if !ok {
		b, _ := json.Marshal(raw)
		s = string(b)
	}
	s = strings.TrimSpace(s)
	if len(s) > reasonLogMax {
		s = strings.ToValidUTF8(s[:reasonLogMax], "")
	}
	return s
}

// accessUser decodes the e-mail from the Cloudflare Access JWT without
// verifying it: cloudflared checked it, and the log says so.
func accessUser(h http.Header) (string, string) {
	parts := strings.Split(h.Get("Cf-Access-Jwt-Assertion"), ".")
	if len(parts) < 2 {
		return "", "none"
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Email string `json:"email"`
	}
	if err != nil || json.Unmarshal(b, &claims) != nil || claims.Email == "" {
		return "", "none"
	}
	return claims.Email, "cf_access_unverified"
}
