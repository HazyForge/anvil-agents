package runnercapabilities

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	mcpManagedBegin = "# BEGIN ANVIL AGENTRUN MCP (managed)"
	mcpManagedEnd   = "# END ANVIL AGENTRUN MCP (managed)"
)

var nativeMCPNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ConfigureNativeMCP replaces the runtime-managed MCP projection for a
// backend. It never resolves environment values into the config: native
// environment-placeholder syntax is used instead.
func ConfigureNativeMCP(backend, path string, manifest MCPManifest) error {
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("native MCP config path must be absolute")
	}
	for _, server := range manifest {
		if !nativeMCPNamePattern.MatchString(server.Name) {
			return fmt.Errorf("MCP server name %q cannot be represented safely by native adapters", server.Name)
		}
	}
	var contents []byte
	var stateTransaction *managedMCPStateTransaction
	var err error
	switch backend {
	case "codex":
		var rendered []byte
		rendered, err = renderCodexMCP(manifest)
		if err == nil {
			contents, err = configureTOML(path, rendered)
		}
	case "grokBuild":
		var rendered []byte
		rendered, err = renderGrokMCP(manifest)
		if err == nil {
			contents, err = configureTOML(path, rendered)
		}
	case "hermesAgent":
		contents, stateTransaction, err = configureHermesYAML(path, manifest)
	case "openClaw":
		contents, stateTransaction, err = configureOpenClawJSON(path, manifest)
	case "openCode":
		var rendered map[string]any
		rendered, err = renderOpenCodeMCP(manifest)
		if err == nil {
			contents, err = json.MarshalIndent(rendered, "", "  ")
		}
	case "piAgent":
		if len(manifest) > 0 {
			return errors.New("backend piAgent does not provide a native MCP client adapter")
		}
		return nil
	default:
		return fmt.Errorf("MCP capabilities are unsupported by backend %q", backend)
	}
	if err != nil {
		return err
	}
	if stateTransaction != nil {
		if err := stateTransaction.begin(); err != nil {
			return err
		}
	}
	if err := writeAtomicConfig(path, append(contents, '\n')); err != nil {
		return err
	}
	if stateTransaction != nil {
		return stateTransaction.commit()
	}
	return nil
}

func configureTOML(path string, rendered []byte) ([]byte, error) {
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("read native MCP config")
	}
	clean, err := removeManagedTOML(existing)
	if err != nil {
		return nil, err
	}
	clean = bytes.TrimSpace(clean)
	if len(rendered) == 0 {
		return clean, nil
	}
	if regexp.MustCompile(`(?m)^\s*\[+\s*mcp_servers(?:\.|\]|\s)`).Match(clean) {
		return nil, errors.New("native config already contains unmanaged mcp_servers; refusing ambiguous capability merge")
	}
	var out bytes.Buffer
	if len(clean) > 0 {
		out.Write(clean)
		out.WriteString("\n\n")
	}
	out.WriteString(mcpManagedBegin + "\n")
	out.Write(rendered)
	out.WriteString(mcpManagedEnd + "\n")
	return out.Bytes(), nil
}

func removeManagedTOML(existing []byte) ([]byte, error) {
	text := string(existing)
	start := strings.Index(text, mcpManagedBegin)
	if start < 0 {
		if strings.Contains(text, mcpManagedEnd) {
			return nil, errors.New("native MCP config contains an unmatched managed marker")
		}
		return existing, nil
	}
	endRelative := strings.Index(text[start:], mcpManagedEnd)
	if endRelative < 0 {
		return nil, errors.New("native MCP config contains an unmatched managed marker")
	}
	end := start + endRelative + len(mcpManagedEnd)
	if strings.Contains(text[end:], mcpManagedBegin) {
		return nil, errors.New("native MCP config contains multiple managed sections")
	}
	return []byte(text[:start] + text[end:]), nil
}

func renderCodexMCP(manifest MCPManifest) ([]byte, error) {
	var out bytes.Buffer
	for _, server := range manifest {
		fmt.Fprintf(&out, "[mcp_servers.%s]\n", tomlQuoteKey(server.Name))
		if stdio := server.Transport.Stdio; stdio != nil {
			fmt.Fprintf(&out, "command = %s\n", tomlString(stdio.Command[0]))
			if len(stdio.Command) > 1 {
				fmt.Fprintf(&out, "args = %s\n", tomlArray(stdio.Command[1:]))
			}
			if len(stdio.RequiredEnv) > 0 {
				fmt.Fprintf(&out, "env_vars = %s\n", tomlArray(stdio.RequiredEnv))
			}
		} else {
			http := server.Transport.StreamableHTTP
			fmt.Fprintf(&out, "url = %s\n", tomlString(http.Endpoint))
			if len(http.Headers) > 0 {
				out.WriteString("env_http_headers = " + tomlHeaders(http.Headers, "") + "\n")
			}
		}
		out.WriteString("enabled = true\nrequired = true\n")
		if len(server.ToolAllowlist) > 0 {
			fmt.Fprintf(&out, "enabled_tools = %s\n", tomlArray(server.ToolAllowlist))
		}
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

func renderGrokMCP(manifest MCPManifest) ([]byte, error) {
	var out bytes.Buffer
	for _, server := range manifest {
		if len(server.ToolAllowlist) > 0 {
			return nil, fmt.Errorf("backend grokBuild cannot enforce tool allowlist for MCP server %q", server.Name)
		}
		fmt.Fprintf(&out, "[mcp_servers.%s]\n", tomlQuoteKey(server.Name))
		if stdio := server.Transport.Stdio; stdio != nil {
			fmt.Fprintf(&out, "command = %s\n", tomlString(stdio.Command[0]))
			if len(stdio.Command) > 1 {
				fmt.Fprintf(&out, "args = %s\n", tomlArray(stdio.Command[1:]))
			}
		} else {
			http := server.Transport.StreamableHTTP
			if len(http.Headers) > 0 {
				return nil, fmt.Errorf("backend grokBuild cannot represent environment-backed headers for MCP server %q", server.Name)
			}
			fmt.Fprintf(&out, "url = %s\n", tomlString(http.Endpoint))
		}
		out.WriteString("enabled = true\n\n")
	}
	return out.Bytes(), nil
}

func configureHermesYAML(path string, manifest MCPManifest) ([]byte, *managedMCPStateTransaction, error) {
	root := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &root); err != nil {
			return nil, nil, errors.New("parse Hermes native config")
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, errors.New("read Hermes native config")
	}
	servers, err := existingObjectMap(root["mcp_servers"])
	if err != nil {
		return nil, nil, errors.New("Hermes native mcp_servers is not an object")
	}
	transaction, err := replaceManagedMCPServers(path, servers, manifest, func(server MCPServer) map[string]any {
		entry := map[string]any{"enabled": true}
		if stdio := server.Transport.Stdio; stdio != nil {
			entry["command"] = stdio.Command[0]
			entry["args"] = stdio.Command[1:]
			env := map[string]string{}
			for _, name := range stdio.RequiredEnv {
				env[name] = "${" + name + "}"
			}
			if len(env) > 0 {
				entry["env"] = env
			}
		} else {
			entry["url"] = server.Transport.StreamableHTTP.Endpoint
			entry["headers"] = placeholderHeaders(server.Transport.StreamableHTTP.Headers, "${", "}")
		}
		if len(server.ToolAllowlist) > 0 {
			entry["tools"] = map[string]any{"include": server.ToolAllowlist}
		}
		return entry
	})
	if err != nil {
		return nil, nil, err
	}
	if len(servers) == 0 {
		delete(root, "mcp_servers")
	} else {
		root["mcp_servers"] = servers
	}
	contents, err := yaml.Marshal(root)
	return contents, transaction, err
}

func configureOpenClawJSON(path string, manifest MCPManifest) ([]byte, *managedMCPStateTransaction, error) {
	root := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, nil, errors.New("parse OpenClaw native config")
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, errors.New("read OpenClaw native config")
	}
	mcp, err := existingObjectMap(root["mcp"])
	if err != nil {
		return nil, nil, errors.New("OpenClaw native mcp is not an object")
	}
	servers, err := existingObjectMap(mcp["servers"])
	if err != nil {
		return nil, nil, errors.New("OpenClaw native mcp.servers is not an object")
	}
	transaction, err := replaceManagedMCPServers(path, servers, manifest, func(server MCPServer) map[string]any {
		entry := map[string]any{}
		if stdio := server.Transport.Stdio; stdio != nil {
			entry["command"] = stdio.Command[0]
			entry["args"] = stdio.Command[1:]
			env := map[string]string{}
			for _, name := range stdio.RequiredEnv {
				env[name] = "${" + name + "}"
			}
			if len(env) > 0 {
				entry["env"] = env
			}
		} else {
			entry["url"] = server.Transport.StreamableHTTP.Endpoint
			entry["transport"] = "streamable-http"
			entry["headers"] = placeholderHeaders(server.Transport.StreamableHTTP.Headers, "${", "}")
		}
		if len(server.ToolAllowlist) > 0 {
			entry["toolFilter"] = map[string]any{"include": server.ToolAllowlist}
		}
		return entry
	})
	if err != nil {
		return nil, nil, err
	}
	if len(servers) == 0 {
		delete(mcp, "servers")
	} else {
		mcp["servers"] = servers
	}
	if len(mcp) == 0 {
		delete(root, "mcp")
	} else {
		root["mcp"] = mcp
	}
	contents, err := json.MarshalIndent(root, "", "  ")
	return contents, transaction, err
}

func existingObjectMap(value any) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("value is not an object")
	}
	return object, nil
}

type managedMCPState struct {
	Version  int      `json:"version"`
	Pending  bool     `json:"pending,omitempty"`
	Managed  []string `json:"managed,omitempty"`
	Previous []string `json:"previous,omitempty"`
	Desired  []string `json:"desired,omitempty"`
}

type managedMCPStateTransaction struct {
	path     string
	previous []string
	desired  []string
}

func (transaction *managedMCPStateTransaction) write(state managedMCPState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return errors.New("encode native MCP managed state")
	}
	if err := writeAtomicConfig(transaction.path, append(raw, '\n')); err != nil {
		return errors.New("write native MCP managed state")
	}
	return nil
}

func (transaction *managedMCPStateTransaction) begin() error {
	return transaction.write(managedMCPState{Version: 1, Pending: true, Previous: transaction.previous, Desired: transaction.desired})
}

func (transaction *managedMCPStateTransaction) commit() error {
	return transaction.write(managedMCPState{Version: 1, Managed: transaction.desired})
}

func readManagedMCPNames(statePath string) ([]string, error) {
	raw, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(bytes.TrimSpace(raw)) == 0) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("read native MCP managed state")
	}
	var legacy []string
	if json.Unmarshal(raw, &legacy) == nil && legacy != nil {
		return legacy, nil
	}
	var state managedMCPState
	if err := json.Unmarshal(raw, &state); err != nil || state.Version != 1 {
		return nil, errors.New("parse native MCP managed state")
	}
	names := append([]string(nil), state.Managed...)
	if state.Pending {
		names = append(names, state.Previous...)
		names = append(names, state.Desired...)
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if _, exists := seen[name]; !exists {
			seen[name] = struct{}{}
			result = append(result, name)
		}
	}
	return result, nil
}

func replaceManagedMCPServers(path string, servers map[string]any, manifest MCPManifest, render func(MCPServer) map[string]any) (*managedMCPStateTransaction, error) {
	statePath := path + ".anvil-mcp-managed.json"
	managed, err := readManagedMCPNames(statePath)
	if err != nil {
		return nil, err
	}
	for _, name := range managed {
		delete(servers, name)
	}
	next := make([]string, 0, len(manifest))
	for _, server := range manifest {
		if _, exists := servers[server.Name]; exists {
			return nil, fmt.Errorf("native config already contains unmanaged MCP server %q", server.Name)
		}
		servers[server.Name] = render(server)
		next = append(next, server.Name)
	}
	return &managedMCPStateTransaction{path: statePath, previous: managed, desired: next}, nil
}

func renderOpenCodeMCP(manifest MCPManifest) (map[string]any, error) {
	servers := map[string]any{}
	tools := map[string]any{}
	normalizedServers := map[string]string{}
	for _, server := range manifest {
		normalizedServer := normalizeOpenCodeTool(server.Name)
		if prior, exists := normalizedServers[normalizedServer]; exists {
			return nil, fmt.Errorf("OpenCode normalizes MCP server names %q and %q to the same tool prefix", prior, server.Name)
		}
		normalizedServers[normalizedServer] = server.Name
		entry := map[string]any{"enabled": true}
		if stdio := server.Transport.Stdio; stdio != nil {
			entry["type"] = "local"
			entry["command"] = stdio.Command
			env := map[string]string{}
			for _, name := range stdio.RequiredEnv {
				env[name] = "{env:" + name + "}"
			}
			if len(env) > 0 {
				entry["environment"] = env
			}
		} else {
			entry["type"] = "remote"
			entry["url"] = server.Transport.StreamableHTTP.Endpoint
			entry["oauth"] = false
			entry["headers"] = placeholderHeaders(server.Transport.StreamableHTTP.Headers, "{env:", "}")
		}
		servers[server.Name] = entry
		if len(server.ToolAllowlist) > 0 {
			tools[normalizedServer+"_*"] = false
			normalizedTools := map[string]string{}
			for _, name := range server.ToolAllowlist {
				normalized := normalizeOpenCodeTool(name)
				if prior, exists := normalizedTools[normalized]; exists {
					return nil, fmt.Errorf("OpenCode normalizes MCP tools %q and %q from server %q to the same name", prior, name, server.Name)
				}
				normalizedTools[normalized] = name
				tools[normalizedServer+"_"+normalized] = true
			}
		}
	}
	root := map[string]any{"$schema": "https://opencode.ai/config.json", "mcp": servers}
	if len(tools) > 0 {
		root["tools"] = tools
	}
	return root, nil
}

func placeholderHeaders(headers []MCPHTTPHeader, prefix, suffix string) map[string]string {
	out := map[string]string{}
	for _, header := range headers {
		out[header.Name] = prefix + header.EnvVar + suffix
	}
	return out
}

func tomlHeaders(headers []MCPHTTPHeader, _ string) string {
	parts := make([]string, 0, len(headers))
	for _, header := range headers {
		parts = append(parts, tomlString(header.Name)+" = "+tomlString(header.EnvVar))
	}
	sort.Strings(parts)
	return "{ " + strings.Join(parts, ", ") + " }"
}

func tomlString(value string) string   { return strconv.Quote(value) }
func tomlQuoteKey(value string) string { return strconv.Quote(value) }
func tomlArray(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = tomlString(value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func normalizeOpenCodeTool(value string) string {
	var out strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			out.WriteRune(r)
		} else {
			out.WriteByte('_')
		}
	}
	return out.String()
}

func writeAtomicConfig(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errors.New("create native MCP config directory")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".mcp-config-*")
	if err != nil {
		return errors.New("create native MCP config")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return errors.New("secure native MCP config")
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return errors.New("write native MCP config")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close native MCP config")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return errors.New("publish native MCP config")
	}
	return nil
}
