package runnercapabilities

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPreflightMCPStdio(t *testing.T) {
	t.Setenv("MCP_TEST_HELPER", "1")
	t.Setenv("MCP_REQUIRED", "present")
	manifest := MCPManifest{{
		Name: "local",
		Transport: MCPTransport{Stdio: &MCPStdio{
			Command:     []string{os.Args[0], "-test.run=TestMCPStdioHelper", "--"},
			RequiredEnv: []string{"MCP_REQUIRED"},
		}},
		ToolAllowlist: []string{"search"},
	}}
	results, err := PreflightMCP(context.Background(), "codex", manifest, MCPPreflightOptions{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("preflight stdio: %v", err)
	}
	if len(results) != 1 || results[0].Transport != "stdio" || results[0].ToolCount != 2 {
		t.Fatalf("results = %#v", results)
	}
}

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("MCP_TEST_HELPER") != "1" {
		return
	}
	reader := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	initialized := false
	for reader.Scan() {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(reader.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]any{}}})
		case "notifications/initialized":
			initialized = true
		case "tools/list":
			if !initialized {
				os.Exit(3)
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": []map[string]string{{"name": "search"}, {"name": "read"}}}})
		}
	}
	os.Exit(0)
}

func TestPreflightMCPStreamableHTTP(t *testing.T) {
	t.Setenv("MCP_BEARER", "top-secret")
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "top-secret" {
			t.Errorf("authorization header = %q", got)
		}
		if requests > 1 && r.Header.Get("Mcp-Session-Id") != "session-1" {
			t.Errorf("session header missing on request %d", requests)
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":%q,"capabilities":{}}}`, mcpProtocolVersion)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"tools\":[{\"name\":\"query\"}]}}\n\n")
		}
	}))
	defer server.Close()

	manifest := MCPManifest{{Name: "remote", Transport: MCPTransport{StreamableHTTP: &MCPStreamableHTTP{
		Endpoint: server.URL,
		Headers:  []MCPHTTPHeader{{Name: "Authorization", EnvVar: "MCP_BEARER"}},
	}}, ToolAllowlist: []string{"query"}}}
	results, err := PreflightMCP(context.Background(), "openCode", manifest, MCPPreflightOptions{HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("preflight HTTP: %v", err)
	}
	if requests != 3 || len(results) != 1 || results[0].ToolCount != 1 {
		t.Fatalf("requests=%d results=%#v", requests, results)
	}
}

func TestMCPPreflightFailsClosedAndRedacts(t *testing.T) {
	t.Setenv("PRIVATE_MCP_HEADER", "do-not-print-me")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "do-not-print-me provider body", http.StatusUnauthorized)
	}))
	defer server.Close()
	manifest := MCPManifest{{Name: "remote", Transport: MCPTransport{StreamableHTTP: &MCPStreamableHTTP{
		Endpoint: server.URL,
		Headers:  []MCPHTTPHeader{{Name: "Authorization", EnvVar: "PRIVATE_MCP_HEADER"}},
	}}}}
	_, err := PreflightMCP(context.Background(), "codex", manifest, MCPPreflightOptions{HTTPClient: server.Client()})
	if err == nil || !strings.Contains(err.Error(), "HTTP status 401") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "do-not-print-me") {
		t.Fatalf("error leaked a secret or response body: %v", err)
	}
	if err := ValidateMCPBackend("custom", true); err == nil {
		t.Fatal("custom backend accepted MCP capabilities")
	}
}

func TestMCPAllowlistAndRequiredEnvironment(t *testing.T) {
	if err := requireAllowedTools([]string{"missing"}, []string{"present"}); err == nil {
		t.Fatal("missing allowlisted tool was accepted")
	}
	_ = os.Unsetenv("MCP_MISSING_ENV")
	if err := requiredEnvironment([]string{"MCP_MISSING_ENV"}); err == nil || !strings.Contains(err.Error(), "MCP_MISSING_ENV") {
		t.Fatalf("required environment error = %v", err)
	}
}

func TestParseMCPManifestRejectsUnknownAndInsecure(t *testing.T) {
	for _, raw := range []string{
		`[{"name":"x","transport":{"stdio":{"command":["x"]}},"unknown":true}]`,
		`[{"name":"x","transport":{"streamableHTTP":{"endpoint":"http://example.invalid"}}}]`,
		`[{"name":"x","transport":{}}]`,
	} {
		if _, err := ParseMCPManifest([]byte(raw)); err == nil {
			t.Fatalf("invalid manifest accepted: %s", raw)
		}
	}
}
