package runnercapabilities

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	// This runner implements the latest stable MCP lifecycle. The draft
	// protocol is intentionally not advertised because its lifecycle is not
	// compatible with initialize/initialized preflight yet.
	mcpProtocolVersion = "2025-11-25"
	mcpMaxMessageBytes = 2 << 20
)

// MCPManifest is the normalized, secret-free runtime representation emitted by
// the controller. Header values are deliberately resolved from the environment
// only inside the runner process.
type MCPManifest []MCPServer

type MCPServer struct {
	Name          string       `json:"name"`
	Description   string       `json:"description,omitempty"`
	Transport     MCPTransport `json:"transport"`
	ToolAllowlist []string     `json:"toolAllowlist,omitempty"`
	SpecDigest    string       `json:"specDigest,omitempty"`
}

type MCPTransport struct {
	Stdio          *MCPStdio          `json:"stdio,omitempty"`
	StreamableHTTP *MCPStreamableHTTP `json:"streamableHTTP,omitempty"`
}

type MCPStdio struct {
	Command     []string `json:"command"`
	RequiredEnv []string `json:"requiredEnv,omitempty"`
}

type MCPHTTPHeader struct {
	Name   string `json:"name"`
	EnvVar string `json:"envVar"`
}

type MCPStreamableHTTP struct {
	Endpoint    string          `json:"endpoint"`
	Headers     []MCPHTTPHeader `json:"headers,omitempty"`
	RequiredEnv []string        `json:"requiredEnv,omitempty"`
}

type MCPPreflightOptions struct {
	HTTPClient *http.Client
	Timeout    time.Duration
}

type MCPPreflightResult struct {
	Name      string
	Transport string
	ToolCount int
}

var mcpEnvNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func ParseMCPManifest(raw []byte) (MCPManifest, error) {
	var manifest MCPManifest
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(raw), mcpMaxMessageBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode MCP manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("decode MCP manifest: trailing data")
	}
	seenServers := map[string]struct{}{}
	for index := range manifest {
		server := &manifest[index]
		server.Name = strings.TrimSpace(server.Name)
		if server.Name == "" {
			return nil, fmt.Errorf("MCP server %d has an empty name", index)
		}
		if _, exists := seenServers[server.Name]; exists {
			return nil, fmt.Errorf("MCP server %q is duplicated", server.Name)
		}
		seenServers[server.Name] = struct{}{}
		if (server.Transport.Stdio == nil) == (server.Transport.StreamableHTTP == nil) {
			return nil, fmt.Errorf("MCP server %q must configure exactly one transport", server.Name)
		}
		if server.Transport.Stdio != nil && (len(server.Transport.Stdio.Command) == 0 || strings.TrimSpace(server.Transport.Stdio.Command[0]) == "") {
			return nil, fmt.Errorf("MCP server %q has an empty stdio command", server.Name)
		}
		if server.Transport.Stdio != nil {
			for _, argument := range server.Transport.Stdio.Command {
				if strings.IndexByte(argument, 0) >= 0 {
					return nil, fmt.Errorf("MCP server %q has an invalid stdio argument", server.Name)
				}
			}
			if err := validateMCPEnvironmentNames(server.Transport.Stdio.RequiredEnv); err != nil {
				return nil, fmt.Errorf("MCP server %q: %w", server.Name, err)
			}
		}
		if server.Transport.StreamableHTTP != nil {
			endpoint := strings.TrimSpace(server.Transport.StreamableHTTP.Endpoint)
			parsed, err := url.Parse(endpoint)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return nil, fmt.Errorf("MCP server %q streamable HTTP endpoint must use HTTPS", server.Name)
			}
			server.Transport.StreamableHTTP.Endpoint = endpoint
			if err := validateMCPEnvironmentNames(server.Transport.StreamableHTTP.RequiredEnv); err != nil {
				return nil, fmt.Errorf("MCP server %q: %w", server.Name, err)
			}
			seenHeaders := map[string]struct{}{}
			for _, header := range server.Transport.StreamableHTTP.Headers {
				name := strings.TrimSpace(header.Name)
				if name == "" || strings.ContainsAny(name, "\r\n:") || !mcpEnvNamePattern.MatchString(header.EnvVar) {
					return nil, fmt.Errorf("MCP server %q has an invalid environment-backed header", server.Name)
				}
				key := strings.ToLower(name)
				if _, exists := seenHeaders[key]; exists {
					return nil, fmt.Errorf("MCP server %q has duplicate header %q", server.Name, name)
				}
				seenHeaders[key] = struct{}{}
			}
		}
		seenTools := map[string]struct{}{}
		for _, name := range server.ToolAllowlist {
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("MCP server %q has an empty tool allowlist entry", server.Name)
			}
			if _, exists := seenTools[name]; exists {
				return nil, fmt.Errorf("MCP server %q has duplicate tool allowlist entry %q", server.Name, name)
			}
			seenTools[name] = struct{}{}
		}
	}
	return manifest, nil
}

func validateMCPEnvironmentNames(names []string) error {
	seen := map[string]struct{}{}
	for _, name := range names {
		if !mcpEnvNamePattern.MatchString(name) {
			return fmt.Errorf("required environment name %q is invalid", name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("required environment name %q is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func ValidateMCPBackend(backend string, hasServers bool) error {
	if !hasServers {
		return nil
	}
	switch strings.TrimSpace(backend) {
	case "codex", "openCode", "hermesAgent", "openClaw", "grokBuild":
		return nil
	default:
		return fmt.Errorf("MCP capabilities are unsupported by backend %q", strings.TrimSpace(backend))
	}
}

func PreflightMCP(ctx context.Context, backend string, manifest MCPManifest, options MCPPreflightOptions) ([]MCPPreflightResult, error) {
	if err := ValidateMCPBackend(backend, len(manifest) > 0); err != nil {
		return nil, err
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	results := make([]MCPPreflightResult, 0, len(manifest))
	for _, server := range manifest {
		serverCtx, cancel := context.WithTimeout(ctx, timeout)
		var tools []string
		var transport string
		var err error
		switch {
		case server.Transport.Stdio != nil:
			transport = "stdio"
			tools, err = preflightMCPStdio(serverCtx, server)
		case server.Transport.StreamableHTTP != nil:
			transport = "streamableHTTP"
			tools, err = preflightMCPHTTP(serverCtx, server, options.HTTPClient)
		default:
			err = errors.New("no transport configured")
		}
		cancel()
		if err != nil {
			// Do not include child stderr, response bodies, headers, or environment
			// values. Those surfaces may contain credentials.
			return nil, fmt.Errorf("MCP server %q %s preflight failed: %w", server.Name, transport, err)
		}
		if err := requireAllowedTools(server.ToolAllowlist, tools); err != nil {
			return nil, fmt.Errorf("MCP server %q tool allowlist failed: %w", server.Name, err)
		}
		results = append(results, MCPPreflightResult{Name: server.Name, Transport: transport, ToolCount: len(tools)})
	}
	return results, nil
}

func requiredEnvironment(names []string) error {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return errors.New("required environment contains an empty name")
		}
		if value, ok := os.LookupEnv(name); !ok || value == "" {
			return fmt.Errorf("required environment variable %s is missing", name)
		}
	}
	return nil
}

type mcpRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code int `json:"code"`
	} `json:"error,omitempty"`
}

type mcpInitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
}

func initializeRequest() mcpRequest {
	return mcpRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    "anvil-agent-capabilities",
			"version": "v1alpha1",
		},
	}}
}

func initializedNotification() mcpRequest {
	return mcpRequest{JSONRPC: "2.0", Method: "notifications/initialized"}
}

func toolsListRequest() mcpRequest {
	return mcpRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list", Params: map[string]any{}}
}

func preflightMCPStdio(ctx context.Context, server MCPServer) ([]string, error) {
	transport := server.Transport.Stdio
	if err := requiredEnvironment(transport.RequiredEnv); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, transport.Command[0], transport.Command[1:]...)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, errors.New("open stdio input")
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, errors.New("open stdio output")
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, errors.New("start stdio server")
	}
	defer func() {
		_ = stdin.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()

	encoder := json.NewEncoder(stdin)
	decoder := json.NewDecoder(io.LimitReader(stdout, mcpMaxMessageBytes))
	if err := encoder.Encode(initializeRequest()); err != nil {
		return nil, errors.New("send initialize")
	}
	initialize, err := readMCPResponse(decoder, 1)
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err := validateMCPInitializeResult(initialize.Result); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err := encoder.Encode(initializedNotification()); err != nil {
		return nil, errors.New("send initialized notification")
	}
	if err := encoder.Encode(toolsListRequest()); err != nil {
		return nil, errors.New("send tools/list")
	}
	response, err := readMCPResponse(decoder, 2)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	return parseMCPTools(response.Result)
}

func readMCPResponse(decoder *json.Decoder, expectedID int) (mcpResponse, error) {
	for attempts := 0; attempts < 64; attempts++ {
		var response mcpResponse
		if err := decoder.Decode(&response); err != nil {
			return mcpResponse{}, errors.New("invalid or unavailable JSON-RPC response")
		}
		if len(response.ID) == 0 {
			continue
		}
		var id int
		if err := json.Unmarshal(response.ID, &id); err != nil || id != expectedID {
			continue
		}
		if response.Error != nil {
			return mcpResponse{}, fmt.Errorf("JSON-RPC error %d", response.Error.Code)
		}
		return response, nil
	}
	return mcpResponse{}, errors.New("matching JSON-RPC response not received")
}

func preflightMCPHTTP(ctx context.Context, server MCPServer, client *http.Client) ([]string, error) {
	transport := server.Transport.StreamableHTTP
	if err := requiredEnvironment(transport.RequiredEnv); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{
			Timeout: 20 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				// Environment-backed headers can contain credentials. Do not let a
				// registry or intermediary redirect them to a different endpoint.
				return errors.New("MCP endpoint redirects are not allowed")
			},
		}
	}
	headers := make(http.Header, len(transport.Headers)+4)
	for _, header := range transport.Headers {
		name := strings.TrimSpace(header.Name)
		envName := strings.TrimSpace(header.EnvVar)
		if name == "" || envName == "" || strings.ContainsAny(name, "\r\n") {
			return nil, errors.New("invalid environment-backed header")
		}
		value, ok := os.LookupEnv(envName)
		if !ok || value == "" {
			return nil, fmt.Errorf("required environment variable %s is missing", envName)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("environment variable %s contains an invalid header value", envName)
		}
		headers.Set(name, value)
	}

	initialize, sessionID, err := sendMCPHTTPRequest(ctx, client, transport.Endpoint, headers, "", initializeRequest(), true)
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if initialize.Error != nil {
		return nil, fmt.Errorf("initialize JSON-RPC error %d", initialize.Error.Code)
	}
	if err := validateMCPResponseEnvelope(initialize, 1); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err := validateMCPInitializeResult(initialize.Result); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if _, _, err := sendMCPHTTPRequest(ctx, client, transport.Endpoint, headers, sessionID, initializedNotification(), false); err != nil {
		return nil, fmt.Errorf("initialized notification: %w", err)
	}
	response, _, err := sendMCPHTTPRequest(ctx, client, transport.Endpoint, headers, sessionID, toolsListRequest(), true)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("tools/list JSON-RPC error %d", response.Error.Code)
	}
	if err := validateMCPResponseEnvelope(response, 2); err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	return parseMCPTools(response.Result)
}

func validateMCPResponseEnvelope(response mcpResponse, expectedID int) error {
	if response.JSONRPC != "2.0" {
		return errors.New("response has an invalid JSON-RPC version")
	}
	var id int
	if err := json.Unmarshal(response.ID, &id); err != nil || id != expectedID {
		return errors.New("response has an unexpected JSON-RPC id")
	}
	return nil
}

func validateMCPInitializeResult(raw json.RawMessage) error {
	var result mcpInitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return errors.New("result is invalid")
	}
	if result.ProtocolVersion != mcpProtocolVersion {
		return fmt.Errorf("unsupported negotiated protocol version %q", result.ProtocolVersion)
	}
	return nil
}

func sendMCPHTTPRequest(ctx context.Context, client *http.Client, endpoint string, headers http.Header, sessionID string, request mcpRequest, expectResponse bool) (mcpResponse, string, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return mcpResponse{}, sessionID, errors.New("encode JSON-RPC request")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return mcpResponse{}, sessionID, errors.New("create HTTP request")
	}
	for name, values := range headers {
		for _, value := range values {
			httpRequest.Header.Add(name, value)
		}
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json, text/event-stream")
	httpRequest.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	if sessionID != "" {
		httpRequest.Header.Set("Mcp-Session-Id", sessionID)
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return mcpResponse{}, sessionID, errors.New("HTTP request unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return mcpResponse{}, sessionID, fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	if next := strings.TrimSpace(response.Header.Get("Mcp-Session-Id")); next != "" {
		sessionID = next
	}
	if !expectResponse || response.StatusCode == http.StatusAccepted {
		return mcpResponse{}, sessionID, nil
	}
	limited, err := io.ReadAll(io.LimitReader(response.Body, mcpMaxMessageBytes+1))
	if err != nil || len(limited) > mcpMaxMessageBytes {
		return mcpResponse{}, sessionID, errors.New("HTTP response unavailable or oversized")
	}
	var decoded mcpResponse
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		decoded, err = decodeMCPSSE(limited)
	} else {
		err = json.Unmarshal(limited, &decoded)
	}
	if err != nil {
		return mcpResponse{}, sessionID, errors.New("invalid JSON-RPC response")
	}
	return decoded, sessionID, nil
}

func decodeMCPSSE(raw []byte) (mcpResponse, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), mcpMaxMessageBytes)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var response mcpResponse
		if err := json.Unmarshal(bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:"))), &response); err == nil {
			return response, nil
		}
	}
	return mcpResponse{}, errors.New("SSE response did not contain JSON-RPC data")
}

func parseMCPTools(raw json.RawMessage) ([]string, error) {
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("tools/list result is invalid")
	}
	tools := make([]string, 0, len(result.Tools))
	seen := map[string]struct{}{}
	for _, item := range result.Tools {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return nil, errors.New("tools/list returned an empty tool name")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("tools/list returned duplicate tool %q", name)
		}
		seen[name] = struct{}{}
		tools = append(tools, name)
	}
	return tools, nil
}

func requireAllowedTools(allowlist, tools []string) error {
	available := make(map[string]struct{}, len(tools))
	for _, name := range tools {
		available[name] = struct{}{}
	}
	for _, allowed := range allowlist {
		if _, ok := available[allowed]; !ok {
			return fmt.Errorf("required tool %q was not reported", allowed)
		}
	}
	return nil
}
