package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/VictoriaMetrics/mcp-victorialogs/cmd/mcp-victorialogs/config"
)

func call(t *testing.T, s *server.MCPServer, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	resp, ok := s.HandleMessage(context.Background(), msg).(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("%s: not a result response", tool)
	}
	res, ok := resp.Result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("%s: result is %T", tool, resp.Result)
	}
	return res
}

func TestReasonDeclaredStrippedNeverRejected(t *testing.T) {
	var vlQuery string
	vl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vlQuery = r.URL.RawQuery
		_, _ = w.Write([]byte("{}"))
	}))
	defer vl.Close()
	t.Setenv("VL_INSTANCE_ENTRYPOINT", vl.URL)
	cfg, err := config.InitConfig()
	if err != nil {
		t.Fatal(err)
	}

	s := server.NewMCPServer("t", "0", server.WithToolHandlerMiddleware(ReasonMiddleware))
	RegisterToolQuery(s, cfg)
	RegisterToolFlags(s, cfg)
	var seen map[string]any
	s.AddTool(mcp.NewTool("echo"), func(_ context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		seen = r.GetArguments()
		return mcp.NewToolResultText("ok"), nil
	})
	AddReasonToAllTools(s)

	for name, tool := range s.ListTools() {
		p, ok := tool.Tool.InputSchema.Properties["reason"].(map[string]any)
		if !ok || p["description"] != reasonDescription {
			t.Errorf("%s: reason not declared: %v", name, tool.Tool.InputSchema.Properties["reason"])
		}
		for _, req := range tool.Tool.InputSchema.Required {
			if req == "reason" {
				t.Errorf("%s: reason is required", name)
			}
		}
	}

	for _, reason := range []any{"why", "", 42, map[string]any{"x": 1}, nil} {
		if res := call(t, s, "echo", map[string]any{"a": 1, "reason": reason}); res.IsError {
			t.Errorf("reason %v rejected", reason)
		}
		if _, ok := seen["reason"]; ok || seen["a"] != float64(1) {
			t.Errorf("handler saw %v", seen)
		}
	}
	if res := call(t, s, "echo", map[string]any{"a": 1}); res.IsError || seen["a"] != float64(1) {
		t.Errorf("call without reason: %v %v", res, seen)
	}

	if res := call(t, s, "query", map[string]any{"query": "*", "start": "1", "reason": "why"}); res.IsError {
		t.Errorf("query with reason rejected: %v", res.Content)
	}
	if vlQuery == "" {
		t.Error("query did not reach VictoriaLogs")
	}
}

func TestDefaultQueryTimeout(t *testing.T) {
	var got []string
	vl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path+" timeout="+r.URL.Query().Get("timeout"))
		_, _ = w.Write([]byte("{}"))
	}))
	defer vl.Close()
	t.Setenv("VL_INSTANCE_ENTRYPOINT", vl.URL)
	t.Setenv("VL_QUERY_TIMEOUT", "45s")
	cfg, err := config.InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	s := server.NewMCPServer("t", "0")
	RegisterToolQuery(s, cfg)
	RegisterToolHits(s, cfg)
	RegisterToolFlags(s, cfg)

	call(t, s, "query", map[string]any{"query": "*", "start": "1"})
	call(t, s, "query", map[string]any{"query": "*", "start": "1", "timeout": "5s"})
	call(t, s, "hits", map[string]any{"query": "*", "start": "1"})
	call(t, s, "flags", map[string]any{})
	want := []string{
		"/select/logsql/query timeout=45s",
		"/select/logsql/query timeout=5s",
		"/select/logsql/hits timeout=45s",
		"/flags timeout=",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// A select that outlives VL_QUERY_TIMEOUT behind a retrying balancer must end
// at timeout+grace with a timeout error, both when the connection hangs and
// when vmauth answers 502 "all the N backends ... are unavailable".
func TestDefaultQueryTimeoutCutsRetry(t *testing.T) {
	old := queryTimeoutGrace
	queryTimeoutGrace = 200 * time.Millisecond
	defer func() { queryTimeoutGrace = old }()
	vl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/select/logsql/hits" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`all the 2 backends for the user "unauthorized" are unavailable`))
			return
		}
		<-r.Context().Done() // hang like vmauth retrying on the second backend
	}))
	defer vl.Close()
	t.Setenv("VL_INSTANCE_ENTRYPOINT", vl.URL)
	t.Setenv("VL_QUERY_TIMEOUT", "100ms")
	cfg, err := config.InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	s := server.NewMCPServer("t", "0")
	RegisterToolStatsQuery(s, cfg)
	RegisterToolHits(s, cfg)

	for _, tool := range []string{"stats_query", "hits"} {
		start := time.Now()
		res := call(t, s, tool, map[string]any{"query": "*", "time": "1", "start": "1"})
		if el := time.Since(start); el > 2*time.Second {
			t.Errorf("%s: took %v, want ~300ms", tool, el)
		}
		text := res.Content[0].(mcp.TextContent).Text
		if !res.IsError || !strings.Contains(text, "VL_QUERY_TIMEOUT=100ms") {
			t.Errorf("%s: want timeout error, got %q", tool, text)
		}
	}
}
