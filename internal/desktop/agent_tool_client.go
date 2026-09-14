package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"
)

type agentToolClientSession struct {
	URL        string    `json:"url"`
	Capability string    `json:"capability"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

var toolCapabilityPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)

// RunAgentTool is the native harness's small, provider-independent Anvil client.
// Its private session file holds only a short-lived loopback capability; the
// user's OIDC credentials remain exclusively in the Desktop server's memory.
func RunAgentTool(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-tool", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	file := flags.String("session-file", "", "Private Anvil tool session")
	fail := func(message string) int { _, _ = fmt.Fprintln(stderr, message); return 1 }
	if err := flags.Parse(args); err != nil || *file == "" || flags.NArg() < 1 || flags.NArg() > 2 {
		return fail("Usage: anvil-desktop agent-tool --session-file PATH ACTION [JSON_ARGS|-]")
	}
	handle, err := os.Open(*file)
	if err != nil {
		return fail("Anvil tool session is unavailable. Start a new Desktop turn.")
	}
	raw, err := io.ReadAll(io.LimitReader(handle, 4097))
	_ = handle.Close()
	var session agentToolClientSession
	if err != nil || len(raw) > 4096 || json.Unmarshal(raw, &session) != nil || !toolCapabilityPattern.MatchString(session.Capability) || !time.Now().Before(session.ExpiresAt) {
		return fail("Anvil tool session is invalid or expired. Start a new Desktop turn.")
	}
	endpoint, err := url.Parse(session.URL)
	if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "/local/v1/agent-tool" || !net.ParseIP(endpoint.Hostname()).IsLoopback() {
		return fail("Anvil tool session must use the local Desktop endpoint.")
	}
	argumentJSON := []byte("{}")
	if flags.NArg() == 2 {
		argumentJSON = []byte(flags.Arg(1))
		if flags.Arg(1) == "-" {
			argumentJSON, err = io.ReadAll(io.LimitReader(stdin, (64<<10)+1))
		}
	}
	var arguments map[string]json.RawMessage
	if err != nil || len(argumentJSON) > 64<<10 || json.Unmarshal(argumentJSON, &arguments) != nil || arguments == nil {
		return fail("Tool arguments must be a JSON object of at most 64KiB.")
	}
	body, err := json.Marshal(struct {
		Action string                     `json:"action"`
		Args   map[string]json.RawMessage `json:"args"`
	}{flags.Arg(0), arguments})
	if err != nil {
		return fail("Could not encode Anvil tool arguments.")
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, session.URL, bytes.NewReader(body))
	if err != nil {
		return fail("Could not create Anvil tool request.")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Anvil-Agent-Capability", session.Capability)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // A loopback capability must never pass through a proxy.
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return fail("Anvil tool connection did not complete. Check delivery before retrying a write; reuse the same requestId.")
	}
	defer response.Body.Close()
	output, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(output) > 1<<20 || !json.Valid(output) {
		return fail("Anvil tool returned an incomplete or invalid response. Check delivery before retrying a write.")
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return fail("Anvil tool redirects are not allowed.")
	}
	_, _ = stdout.Write(output)
	_, _ = fmt.Fprintln(stdout)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 1
	}
	return 0
}
