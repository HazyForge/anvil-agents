package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/localrouting"
	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
)

const maxLocalManagerJSON = 256 << 10

// LocalManagerStatus reports configuration, never provider credentials. Manual
// routing remains available when classification is disabled or unconfigured.
type LocalManagerStatus struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

func (s *Server) localManagerStatus() LocalManagerStatus {
	status := LocalManagerStatus{Enabled: s.opts.SubjectRoutingEnabled}
	switch {
	case !status.Enabled:
		status.Reason = "disabled"
	case s.opts.SubjectDecisionProvider == nil:
		status.Reason = "provider_unavailable"
	default:
		status.Available = true
		status.Reason = "configured"
	}
	return status
}

func (s *Server) localManagerProvider() subjectrouting.Provider {
	if !s.opts.SubjectRoutingEnabled {
		return nil
	}
	return s.opts.SubjectDecisionProvider
}

func (s *Server) handleLocalManagerScenarios(writer http.ResponseWriter, _ *http.Request) {
	scenarios, err := localrouting.Scenarios()
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "scenarios_unavailable", "local manager scenarios are unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"scenarios": scenarios, "localManager": s.localManagerStatus()})
}

// Fixture replay and live evaluation both run the same policy, and neither
// mode launches a native harness. Live mode is an explicit model request.
func (s *Server) handleLocalManagerBench(writer http.ResponseWriter, request *http.Request) {
	var body localrouting.BenchOptions
	if !decodeLocalManagerRequest(writer, request, &body, maxLocalManagerJSON) {
		return
	}
	if body.Mode != "fixture" && body.Mode != "live" {
		writeError(writer, http.StatusBadRequest, "invalid_mode", "choose fixture or live evaluation")
		return
	}
	var provider subjectrouting.Provider
	if body.Mode == "live" {
		provider = s.localManagerProvider()
		if provider == nil {
			writeError(writer, http.StatusServiceUnavailable, "provider_unavailable", "live evaluation requires the host classifier to be enabled and configured")
			return
		}
	}
	ctx, cancel := context.WithTimeout(request.Context(), 90*time.Second)
	defer cancel()
	result, err := localrouting.RunBench(ctx, provider, body)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if result.Totals.Turns > 0 {
				// A host deadline must not erase completed provider evidence. The
				// report carries complete=false and the interruption reason.
				writeJSON(writer, http.StatusOK, result)
			} else {
				writeError(writer, http.StatusGatewayTimeout, "evaluation_interrupted", "evaluation stopped before any turn completed")
			}
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_scenario", "evaluation could not complete; check scenario selection and bounds")
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

// Routing only plans a local action. The browser owns the conversation registry
// and must reread its immutable harness/runtime/workdir binding before dispatch.
func (s *Server) handleLocalManagerRoute(writer http.ResponseWriter, request *http.Request) {
	var body localrouting.Request
	if !decodeLocalManagerRequest(writer, request, &body, maxLocalManagerJSON) {
		return
	}
	if localrouting.Validate(body) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_body", "invalid or oversized local conversation inventory, message, or context")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	emit := localManagerStream(writer, cancel)
	requestID := uuid.NewString()
	if err := emit("inventory", map[string]any{"requestId": requestID, "count": len(body.Conversations), "scope": "browser_owned"}); err != nil {
		return
	}
	runtime := s.localManagerRuntime(ctx, body)
	if err := emit("classifying", map[string]any{"requestId": requestID, "available": s.localManagerStatus().Available, "manual": body.TargetID != ""}); err != nil {
		return
	}
	result, err := localrouting.Route(ctx, s.localManagerProvider(), body, runtime)
	if ctx.Err() != nil {
		return
	}
	if err != nil && result.Action.Kind == "" {
		_ = emit("error", map[string]string{"code": "routing_unavailable", "message": "local routing could not produce a decision; choose a conversation manually"})
		return
	}
	if emit("decision", result.Decision) != nil || emit("action", result.Action) != nil {
		return
	}
	_ = emit("result", struct {
		localrouting.Result
		RequestID string               `json:"requestId"`
		Snapshot  localrouting.Request `json:"snapshot"`
		Runtime   localrouting.Runtime `json:"runtime"`
	}{Result: result, RequestID: requestID, Snapshot: localrouting.SafeSnapshot(body), Runtime: runtime})
}

func decodeLocalManagerRequest(writer http.ResponseWriter, request *http.Request, body any, limit int64) bool {
	defer request.Body.Close()
	if !localChatRequest(request) {
		writeError(writer, http.StatusForbidden, "invalid_origin", "local manager requires a same-origin JSON request")
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		writeError(writer, http.StatusBadRequest, "invalid_body", "local manager request is too large")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(body) != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(writer, http.StatusBadRequest, "invalid_body", "invalid local manager request fields")
		return false
	}
	return true
}

func localManagerStream(writer http.ResponseWriter, cancel context.CancelFunc) func(string, any) error {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(writer)
	return func(event string, data any) error {
		_ = control.SetWriteDeadline(time.Now().Add(10 * time.Second))
		payload, err := json.Marshal(data)
		if err == nil {
			_, err = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event, payload)
		}
		if err == nil {
			err = control.Flush()
		}
		if err != nil {
			cancel()
		}
		return err
	}
}

// Resolve only catalog paths relevant to this request. Unlike the snapshot's
// display discovery this does not run harness --version commands, much less a
// harness turn. WSL may use its existing bounded PATH lookup transport.
func (s *Server) localManagerRuntime(ctx context.Context, body localrouting.Request) localrouting.Runtime {
	prefs := s.currentPrefs()
	d := s.opts.Discoverer
	wsl := probeWSL(ctx, d)
	d.Target = resolveHarnessTarget(prefs, wsl)
	d.Distro = resolveWSLDistro(prefs, wsl)
	if d.Target == HarnessTargetWSL && !d.useWSL() {
		// A host already inside WSL executes in its own distro, matching
		// resolveDelegate rather than an unrelated saved preference.
		d.Distro = wsl.DefaultDistro
	}
	runtime := localrouting.Runtime{Target: d.Target, AvailableHarnesses: []string{}, Now: time.Now().UTC()}
	if d.Target == HarnessTargetWSL {
		runtime.WSLDistro = d.Distro
	}
	s.mu.Lock()
	runtime.Busy = len(s.chatWorkspaces) > 0
	s.mu.Unlock()
	requested := map[string]bool{body.DefaultHarness: true}
	for _, conversation := range body.Conversations {
		requested[conversation.Harness] = true
	}
	look := d.LookPath
	if look == nil {
		look = defaultLookPath(searchPATH(d.Path))
	}
	for _, tool := range Catalog() {
		if !requested[tool.ID] || !tool.Delegatable() {
			continue
		}
		for _, binary := range tool.Binaries {
			var path string
			var err error
			if d.useWSL() {
				path, err = d.wslLookPath(ctx, d.Distro, binary)
			} else {
				path, err = look(binary)
			}
			if err == nil && strings.TrimSpace(path) != "" {
				runtime.AvailableHarnesses = append(runtime.AvailableHarnesses, tool.ID)
				break
			}
		}
	}
	return runtime
}
