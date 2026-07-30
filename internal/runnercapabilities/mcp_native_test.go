package runnercapabilities

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeTestManifest() MCPManifest {
	return MCPManifest{
		{
			Name: "local-tools",
			Transport: MCPTransport{Stdio: &MCPStdio{
				Command:     []string{"tool-server", "--label", "two words", "--literal=$VALUE"},
				RequiredEnv: []string{"LOCAL_MCP_TOKEN"},
			}},
			ToolAllowlist: []string{"read.file"},
		},
		{
			Name: "remote-tools",
			Transport: MCPTransport{StreamableHTTP: &MCPStreamableHTTP{
				Endpoint: "https://mcp.example.test/api",
				Headers:  []MCPHTTPHeader{{Name: "Authorization", EnvVar: "REMOTE_MCP_TOKEN"}},
			}},
			ToolAllowlist: []string{"search"},
		},
	}
}

func TestConfigureNativeMCPSupportedBackends(t *testing.T) {
	t.Setenv("REMOTE_MCP_TOKEN", "must-not-be-rendered")
	for _, backend := range []string{"codex", "hermesAgent", "openClaw", "openCode"} {
		t.Run(backend, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "nested", "config")
			if err := ConfigureNativeMCP(backend, path, nativeTestManifest()); err != nil {
				t.Fatalf("configure: %v", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			if strings.Contains(text, "must-not-be-rendered") {
				t.Fatal("native config contains environment value")
			}
			for _, want := range []string{"tool-server", "two words", "--literal=$VALUE", "REMOTE_MCP_TOKEN", "search"} {
				if !strings.Contains(text, want) {
					t.Fatalf("native config missing %q:\n%s", want, text)
				}
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("native config mode = %v err=%v", info.Mode().Perm(), err)
			}
		})
	}
}

func TestCodexNativeMCPUsesAllowlistAndArgv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := ConfigureNativeMCP("codex", path, nativeTestManifest()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	for _, want := range []string{
		`args = ["--label", "two words", "--literal=$VALUE"]`,
		`env_vars = ["LOCAL_MCP_TOKEN"]`,
		`enabled_tools = ["read.file"]`,
		`env_http_headers = { "Authorization" = "REMOTE_MCP_TOKEN" }`,
		"required = true",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Codex config missing %q:\n%s", want, text)
		}
	}
}

func TestNativeMCPAllowlistShapes(t *testing.T) {
	for _, backend := range []string{"hermesAgent", "openClaw", "openCode"} {
		path := filepath.Join(t.TempDir(), backend+".config")
		if err := ConfigureNativeMCP(backend, path, nativeTestManifest()); err != nil {
			t.Fatalf("%s: %v", backend, err)
		}
		raw, _ := os.ReadFile(path)
		if backend == "openCode" {
			var root map[string]any
			if err := json.Unmarshal(raw, &root); err != nil {
				t.Fatal(err)
			}
			tools := root["tools"].(map[string]any)
			if tools["local-tools_*"] != false || tools["local-tools_read_file"] != true {
				t.Fatalf("OpenCode allowlist = %#v", tools)
			}
		}
	}
}

func TestNativeMCPUnsupportedCombinationsFailClosed(t *testing.T) {
	manifest := nativeTestManifest()
	if err := ConfigureNativeMCP("piAgent", filepath.Join(t.TempDir(), "pi"), manifest); err == nil {
		t.Fatal("Pi accepted MCP without a native adapter")
	}
	if err := ConfigureNativeMCP("grokBuild", filepath.Join(t.TempDir(), "grok"), manifest); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("Grok allowlist error = %v", err)
	}
	manifest[0].ToolAllowlist = nil
	manifest[1].ToolAllowlist = nil
	if err := ConfigureNativeMCP("grokBuild", filepath.Join(t.TempDir(), "grok"), manifest); err == nil || !strings.Contains(err.Error(), "environment-backed headers") {
		t.Fatalf("Grok header error = %v", err)
	}
	manifest = manifest[:1]
	if err := ConfigureNativeMCP("grokBuild", filepath.Join(t.TempDir(), "grok"), manifest); err != nil {
		t.Fatalf("Grok stdio config: %v", err)
	}
}

func TestNativeMCPTOMLRejectsUnmanagedServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[mcp_servers.existing]\ncommand=\"bad\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureNativeMCP("codex", path, nativeTestManifest()); err == nil {
		t.Fatal("unmanaged MCP server was silently merged")
	}
}

func TestNativeMCPManagedProjectionIsClearedWithoutRemovingUnmanagedConfig(t *testing.T) {
	for _, backend := range []string{"codex", "hermesAgent", "openClaw"} {
		t.Run(backend, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			switch backend {
			case "codex":
				if err := os.WriteFile(path, []byte("model = \"gpt-test\"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "hermesAgent":
				if err := os.WriteFile(path, []byte("model:\n  name: gpt-test\nmcp_servers:\n  unmanaged:\n    command: keep\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "openClaw":
				if err := os.WriteFile(path, []byte(`{"model":"gpt-test","mcp":{"servers":{"unmanaged":{"command":"keep"}}}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			manifest := MCPManifest{{Name: "managed", Transport: MCPTransport{Stdio: &MCPStdio{Command: []string{"server"}}}}}
			if err := ConfigureNativeMCP(backend, path, manifest); err != nil {
				t.Fatal(err)
			}
			if err := ConfigureNativeMCP(backend, path, MCPManifest{}); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(path)
			text := string(raw)
			managedSurvived := strings.Contains(text, `"managed"`) || strings.Contains(text, "\n  managed:") || strings.Contains(text, `mcp_servers."managed"`)
			if managedSurvived {
				t.Fatalf("managed server survived empty projection:\n%s", text)
			}
			if backend != "codex" && !strings.Contains(text, "unmanaged") {
				t.Fatalf("unmanaged server was removed:\n%s", text)
			}
			if !strings.Contains(text, "gpt-test") {
				t.Fatalf("unrelated config was removed:\n%s", text)
			}
		})
	}
}
