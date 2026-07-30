package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunToolInstallEmptyManifest(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "tools.json")
	if err := os.WriteFile(manifest, []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"tools", "install", "--manifest", manifest, "--cache-root", filepath.Join(root, "cache"), "--bin-dir", filepath.Join(root, "bin")}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunMCPUnsupportedBackendFailsClosed(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "mcp.json")
	contents := `[{"name":"local","transport":{"stdio":{"command":["missing-server"]}}}]`
	if err := os.WriteFile(manifest, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"mcp", "preflight", "--manifest", manifest, "--backend", "custom"}, &stdout, &stderr)
	if code != exitMCP || !strings.Contains(stderr.String(), "unsupported") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunDoesNotReflectManifestContents(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "bad.json")
	if err := os.WriteFile(manifest, []byte(`{"token":"private-value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"mcp", "preflight", "--manifest", manifest, "--backend", "codex"}, &stdout, &stderr)
	if code != exitMCP {
		t.Fatalf("code=%d", code)
	}
	if strings.Contains(stderr.String(), "private-value") {
		t.Fatalf("manifest contents leaked: %q", stderr.String())
	}
}
