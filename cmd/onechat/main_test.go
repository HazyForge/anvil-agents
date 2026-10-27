package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazyforge/anvil-agents/internal/onechat"
	"github.com/hazyforge/anvil-agents/internal/onechat/host"
)

func TestStandaloneCommandsRequireStateLock(t *testing.T) {
	dir := t.TempDir()
	// Missing cloud credentials make an accidental manager open fail locally,
	// without contacting a provider or launching a harness.
	config := onechat.Config{StateDir: dir, Cloud: onechat.CloudConfig{BaseURL: "https://cloud.invalid"}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "onechat.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := host.LockState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	for _, args := range [][]string{{"sessions"}, {"say", "continue work"}} {
		t.Run(args[0], func(t *testing.T) {
			err := run(context.Background(), append([]string{"--config", path}, args...))
			if err == nil || !strings.Contains(err.Error(), "onechat host") || !strings.Contains(err.Error(), "Anvil Agents Desktop") || !strings.Contains(err.Error(), "stop the host") {
				t.Fatalf("locked command should direct the user to Desktop or stop the host: %v", err)
			}
		})
	}
}
