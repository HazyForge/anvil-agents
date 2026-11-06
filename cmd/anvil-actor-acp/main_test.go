package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hazyforge/anvil-agents/internal/actoracp"
)

// This is an inert copy of the Go test binary, not an installed agent or CLI.
func TestACPStdioHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "acp-inert-helper" {
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	turns := 0
	for {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
			} `json:"params"`
		}
		if decoder.Decode(&request) != nil {
			os.Exit(0)
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1}
		case "session/new":
			result = map[string]any{"sessionId": "native-inert-session"}
		case "session/prompt":
			if request.Params.SessionID != "native-inert-session" || os.Getenv("KUBECONFIG") != "" || os.Getenv("ANVIL_AGENTS_ACCESS_TOKEN") != "" {
				os.Exit(2)
			}
			turns++
			text := "first process reply"
			if turns == 2 {
				text = "same process second reply"
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": request.Params.SessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}}})
			result = map[string]any{"stopReason": "end_turn"}
		default:
			os.Exit(3)
		}
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}
}

func TestNativeStdioProcessBridgeTwoTurnsAndEnvironment(t *testing.T) {
	t.Setenv("KUBECONFIG", "INERT_FORBIDDEN_KUBECONFIG")
	t.Setenv("ANVIL_AGENTS_ACCESS_TOKEN", "INERT_FORBIDDEN_TOKEN")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	workdir := t.TempDir()
	transport, err := startHarness(ctx, executable, workdir, []string{"-test.run=^TestACPStdioHelper$", "--", "acp-inert-helper"})
	if err != nil {
		t.Fatal(err)
	}
	client := actoracp.NewClient(transport)
	t.Cleanup(func() { _ = client.Close() })
	adapter, err := actoracp.NewServer(ctx, client, actoracp.Config{Workdir: workdir})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(adapter)
	defer server.Close()
	for i, want := range []string{"first process reply", "same process second reply"} {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "session/prompt", "params": map[string]any{"sessionId": "anvil-thread", "prompt": []any{map[string]any{"type": "text", "text": "inert prompt"}}}})
		response, err := server.Client().Post(server.URL, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || !bytes.Contains(raw, []byte(want)) || !bytes.Contains(raw, []byte(`"stopReason":"end_turn"`)) || bytes.Contains(raw, []byte(`"error"`)) {
			t.Fatalf("turn %d %s %v", i, raw, err)
		}
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if transport.cmd.ProcessState == nil {
		t.Fatal("owned harness not reaped")
	}
}

func TestStdioInvocationRequiresOperatorAbsolutePaths(t *testing.T) {
	for _, args := range [][2]string{{"relative", "/tmp"}, {"/inert-executable", "relative"}} {
		_, err := startHarness(context.Background(), args[0], args[1], nil)
		if err == nil || !strings.Contains(err.Error(), "absolute paths") {
			t.Fatalf("invocation %v %v", args, err)
		}
	}
}
