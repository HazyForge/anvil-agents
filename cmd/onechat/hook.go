package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hazyforge/anvil-agents/internal/onechat"
	"github.com/hazyforge/anvil-agents/internal/onechat/wire"
)

// A missing daemon must never break Claude's run. The deadline covers stdin,
// the socket exchange and stdout, including pipes that never close or drain.
func runHook(ctx context.Context, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runClaudeHook(ctx, args)
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
	return nil
}

func runClaudeHook(ctx context.Context, args []string) {
	if len(args) == 0 || args[0] != "claude" {
		return
	}
	flags := flag.NewFlagSet("onechat hook claude", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sessionID := flags.String("session", "", "onechat session ID")
	stateDir := flags.String("state-dir", "", "daemon state directory")
	runID := flags.String("run-id", "", "current session run ID")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *sessionID == "" || *stateDir == "" || *runID == "" {
		return
	}
	dir, err := cliPath(*stateDir)
	if err != nil {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, wire.MaxMessageSize+1))
	if err != nil || len(raw) > wire.MaxMessageSize || ctx.Err() != nil {
		return
	}
	hook, err := onechat.ParseClaudeHook(raw)
	if err != nil {
		return
	}
	hook.RunID = *runID
	client, err := openDaemonRPC(ctx, filepath.Join(dir, "onechat.sock"), "onechat Claude hook")
	if err != nil {
		return
	}
	defer client.Close()
	raw, err = client.call("session.hook", map[string]any{"sessionId": *sessionID, "hook": hook})
	if err != nil || ctx.Err() != nil {
		return
	}
	var response struct {
		Output     json.RawMessage `json:"output"`
		DeliveryID string          `json:"deliveryId,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&response) != nil || !validHookOutput(response.Output, hook) {
		return
	}
	var compact bytes.Buffer
	if json.Compact(&compact, response.Output) != nil || compact.String() == "{}" {
		return
	}
	if response.DeliveryID == "" || len(response.DeliveryID) > 64 || ctx.Err() != nil {
		return
	}
	output := append(compact.Bytes(), '\n')
	if n, err := os.Stdout.Write(output); err != nil || n != len(output) {
		return
	}
	// The mailbox advances only after the hook output reached stdout. If this
	// acknowledgement fails, retrying may repeat delivery but cannot lose work.
	_, _ = client.call("session.hook.ack", map[string]string{"sessionId": *sessionID, "runId": *runID, "deliveryId": response.DeliveryID})
}

func validHookOutput(raw []byte, hook onechat.SessionHook) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	if len(fields) == 0 {
		return true
	}
	if hook.HookEventName == "PostToolUse" && len(fields) == 1 {
		value, ok := fields["hookSpecificOutput"]
		if !ok {
			return false
		}
		var output struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		}
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.DisallowUnknownFields()
		return decoder.Decode(&output) == nil && output.Event == "PostToolUse" && strings.TrimSpace(output.Context) != ""
	}
	if hook.HookEventName == "Stop" && !hook.StopHookActive && len(fields) == 2 {
		var output struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		return decoder.Decode(&output) == nil && output.Decision == "block" && strings.TrimSpace(output.Reason) != ""
	}
	return false
}
