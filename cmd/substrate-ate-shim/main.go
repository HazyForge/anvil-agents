// Command substrate-ate-shim is a thin spike-only bridge between the Anvil
// live Substrate client and ateapi. Upstream Substrate exposes actor
// lifecycle (CreateActor/ResumeActor/SuspendActor/PauseActor/GetActor) as
// gRPC on ate-api-server plus the atenet-router HTTP data plane; it exposes
// no generic actor REST API. The Anvil LiveClient speaks the router data
// plane directly for resume/probe, and speaks to this shim for the
// control-plane operations, which the shim executes through `kubectl ate`
// (which already speaks ateapi gRPC+mTLS/jwt wherever kubectl works).
//
// The shim is a Kind-spike adapter, not a product API: it binds loopback by
// default, has no auth of its own, and must never be exposed beyond the
// machine driving the latency comparison. Contract (JSON, all responses
// carry atespace/name/id/state):
//
//	PUT  /shim/v1/actors/{atespace}/{name}			create (201) or reuse on AlreadyExists (200, reused:true)
//	POST /shim/v1/actors/{atespace}/{name}:resume		resume (200)
//	POST /shim/v1/actors/{atespace}/{name}:suspend	suspend (200)
//	POST /shim/v1/actors/{atespace}/{name}:pause		pause (200)
//	GET  /shim/v1/actors/{atespace}/{name}			describe (200, 404 when ateapi reports NotFound)
//
// Usage:
//
//	go run ./cmd/substrate-ate-shim -listen 127.0.0.1:8081 -default-template ate-demo-counter/counter
//	ANVIL_AGENTS_SUBSTRATE_SHIM_ENDPOINT=http://127.0.0.1:8081 hack/substrate-latency-compare.sh --live ...
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	shimPrefix     = "/shim/v1/actors/"
	shimBodyLimit  = 64 << 10
	kubectlTimeout = 120 * time.Second
	stderrTruncate = 500
)

// actorResponse is the JSON body returned for every shim operation. State
// uses the Anvil Active/Suspended/Paused vocabulary so the live client can
// map it without parsing kubectl output itself.
type actorResponse struct {
	Atespace string `json:"atespace"`
	Name     string `json:"name"`
	ID       string `json:"id"`
	State    string `json:"state"`
	Reused   bool   `json:"reused,omitempty"`
}

type createRequest struct {
	ActorTemplate string            `json:"actorTemplate"`
	ActorClass    string            `json:"actorClass"`
	Pool          string            `json:"pool"`
	HarnessKind   string            `json:"harnessKind"`
	Labels        map[string]string `json:"labels"`
}

// Runner executes kubectl ate. Production uses os/exec; tests stub it.
type Runner interface {
	Run(ctx context.Context, args ...string) (stdout string, err error)
}

type execRunner struct {
	kubectl string
}

func (r execRunner) Run(ctx context.Context, args ...string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, kubectlTimeout)
	defer cancel()
	cmd := exec.CommandContext(callCtx, r.kubectl, args...)
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		trimmed := strings.TrimSpace(stderr.String())
		if len(trimmed) > stderrTruncate {
			trimmed = trimmed[:stderrTruncate] + "…"
		}
		if trimmed == "" {
			trimmed = strings.TrimSpace(stdout.String())
			if len(trimmed) > stderrTruncate {
				trimmed = trimmed[:stderrTruncate] + "…"
			}
		}
		return stdout.String(), fmt.Errorf("kubectl ate %s: %w: %s", strings.Join(args, " "), err, trimmed)
	}
	return stdout.String(), nil
}

type server struct {
	runner          Runner
	defaultTemplate string
	logger          *log.Logger
}

func validLabel(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
			continue
		}
		return false
	}
	return value[0] != '-' && value[len(value)-1] != '-'
}

func validTemplate(template string) bool {
	parts := strings.Split(strings.TrimSpace(template), "/")
	if len(parts) != 2 || !validLabel(parts[0]) {
		return false
	}
	for _, segment := range strings.Split(parts[1], ".") {
		if !validLabel(segment) {
			return false
		}
	}
	return true
}

// splitPath parses /shim/v1/actors/{atespace}/{name}[:action]. The action
// uses a gRPC-style custom-verb suffix so atespace/name routing stays a plain
// prefix split with no router dependency.
func splitPath(path string) (atespace, name, action string, ok bool) {
	rest, found := strings.CutPrefix(path, shimPrefix)
	if !found {
		return "", "", "", false
	}
	segments := strings.Split(rest, "/")
	if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
		return "", "", "", false
	}
	name, action, _ = strings.Cut(segments[1], ":")
	if name == "" {
		return "", "", "", false
	}
	return segments[0], name, action, true
}

func containsFold(haystack string, needles ...string) bool {
	lowered := strings.ToLower(haystack)
	for _, needle := range needles {
		if strings.Contains(lowered, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func alreadyExists(output string, err error) bool {
	if err == nil {
		return false
	}
	return containsFold(output+err.Error(), "already exists", "alreadyexists")
}

func notFound(output string, err error) bool {
	if err == nil {
		return false
	}
	return containsFold(output+err.Error(), "not found", "notfound", "no such actor", "does not exist")
}

// kubectlStatus maps the STATUS_* token from `kubectl ate get actor` onto the
// Anvil actor states. Transitional workflows report the plane they are
// heading away from holding toward: resuming/pausing actors are resident,
// suspending actors are leaving.
func kubectlStatus(output string) (string, error) {
	for _, token := range strings.Fields(output) {
		switch token {
		case "STATUS_RUNNING", "STATUS_RESUMING":
			return "Active", nil
		case "STATUS_SUSPENDED", "STATUS_SUSPENDING":
			return "Suspended", nil
		case "STATUS_PAUSED", "STATUS_PAUSING":
			return "Paused", nil
		case "STATUS_CRASHED":
			return "", fmt.Errorf("actor is CRASHED")
		}
	}
	return "", fmt.Errorf("kubectl ate get actor: no STATUS token in output")
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}

func (s *server) describe(ctx context.Context, atespace, name string) (actorResponse, int, string) {
	stdout, err := s.runner.Run(ctx, "ate", "get", "actor", name, "--atespace", atespace)
	if err != nil {
		if notFound(stdout, err) {
			return actorResponse{}, http.StatusNotFound, fmt.Sprintf("substrate actor not found: %s/%s", atespace, name)
		}
		return actorResponse{}, http.StatusBadGateway, fmt.Sprintf("kubectl ate get actor %s: %v", name, truncErr(err))
	}
	state, err := kubectlStatus(stdout)
	if err != nil {
		return actorResponse{}, http.StatusBadGateway, fmt.Sprintf("kubectl ate get actor %s: %v", name, err)
	}
	return actorResponse{Atespace: atespace, Name: name, ID: atespace + "/" + name, State: state}, http.StatusOK, ""
}

func truncErr(err error) error {
	msg := err.Error()
	if len(msg) > stderrTruncate {
		return fmt.Errorf("%s…", msg[:stderrTruncate])
	}
	return err
}

func (s *server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	atespace, name, action, ok := splitPath(request.URL.Path)
	if !ok || !validLabel(atespace) || !validLabel(name) {
		writeError(writer, http.StatusNotFound, "unknown shim path")
		return
	}
	ctx := request.Context()
	switch {
	case request.Method == http.MethodPut && action == "":
		s.handleCreate(writer, request, ctx, atespace, name)
	case request.Method == http.MethodGet && action == "":
		response, status, message := s.describe(ctx, atespace, name)
		if message != "" {
			writeError(writer, status, message)
			return
		}
		writeJSON(writer, status, response)
	case request.Method == http.MethodPost && (action == "resume" || action == "suspend" || action == "pause"):
		s.handleLifecycle(writer, ctx, atespace, name, action)
	default:
		writeError(writer, http.StatusNotFound, "unknown shim path")
	}
}

func (s *server) handleCreate(writer http.ResponseWriter, request *http.Request, ctx context.Context, atespace, name string) {
	raw, err := io.ReadAll(io.LimitReader(request.Body, shimBodyLimit+1))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "read request body")
		return
	}
	var body createRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeError(writer, http.StatusBadRequest, "decode request body")
			return
		}
	}
	template := strings.TrimSpace(body.ActorTemplate)
	if template == "" {
		template = strings.TrimSpace(s.defaultTemplate)
	}
	if !validTemplate(template) {
		writeError(writer, http.StatusBadRequest, "actorTemplate namespace/name is required (request body or shim -default-template)")
		return
	}
	stdout, err := s.runner.Run(ctx, "ate", "create", "actor", name, "--atespace", atespace, "--template", template)
	if err != nil {
		if alreadyExists(stdout, err) {
			// Upstream CreateActor is a conditional write: duplicates keep
			// the existing record, so a retry converges on warm reuse.
			response, status, message := s.describe(ctx, atespace, name)
			if message != "" {
				writeError(writer, status, message)
				return
			}
			response.Reused = true
			writeJSON(writer, http.StatusOK, response)
			return
		}
		if notFound(stdout, err) {
			writeError(writer, http.StatusBadRequest, fmt.Sprintf("atespace or template not found for %s/%s", atespace, name))
			return
		}
		writeError(writer, http.StatusBadGateway, fmt.Sprintf("kubectl ate create actor %s: %v", name, truncErr(err)))
		return
	}
	_ = stdout
	// A fresh upstream actor starts SUSPENDED; the first router request
	// restores it, which is the cold path the latency harness measures.
	writeJSON(writer, http.StatusCreated, actorResponse{Atespace: atespace, Name: name, ID: atespace + "/" + name, State: "Suspended"})
}

func (s *server) handleLifecycle(writer http.ResponseWriter, ctx context.Context, atespace, name, action string) {
	opDefault := map[string]string{"resume": "Active", "suspend": "Suspended", "pause": "Paused"}[action]
	stdout, err := s.runner.Run(ctx, "ate", action, "actor", name, "--atespace", atespace)
	if err != nil {
		if notFound(stdout, err) {
			writeError(writer, http.StatusNotFound, fmt.Sprintf("substrate actor not found: %s/%s", atespace, name))
			return
		}
		writeError(writer, http.StatusBadGateway, fmt.Sprintf("kubectl ate %s actor %s: %v", action, name, truncErr(err)))
		return
	}
	_ = stdout
	// Prefer the observed post-op state; fall back to the operation default
	// when the follow-up read fails so a successful transition still reports.
	response, status, _ := s.describe(ctx, atespace, name)
	if status != http.StatusOK {
		response = actorResponse{Atespace: atespace, Name: name, ID: atespace + "/" + name, State: opDefault}
	}
	writeJSON(writer, http.StatusOK, response)
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8081", "Address to bind the shim HTTP server to (keep loopback; the shim has no auth).")
	kubectl := flag.String("kubectl", "kubectl", "kubectl binary used to reach ateapi (KUBECONFIG env selects the Kind cluster).")
	defaultTemplate := flag.String("default-template", os.Getenv("ATESHIM_DEFAULT_TEMPLATE"), "Default namespace/name ActorTemplate for creates without an explicit template.")
	flag.Parse()

	srv := &server{
		runner:          execRunner{kubectl: strings.TrimSpace(*kubectl)},
		defaultTemplate: strings.TrimSpace(*defaultTemplate),
		logger:          log.New(os.Stderr, "substrate-ate-shim: ", log.LstdFlags),
	}
	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}
	srv.logger.Printf("listening on %s", *listen)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		srv.logger.Fatalf("serve: %v", err)
	}
}
