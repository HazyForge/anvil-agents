package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// localChatRequest rejects browser cross-origin execution and DNS rebinding.
// Non-browser loopback clients may omit Origin, but must still send JSON.
func localChatRequest(request *http.Request) bool {
	host := request.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return false
	}
	site := request.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if origin := request.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		}
		if err != nil || parsed.Scheme != scheme || !strings.EqualFold(parsed.Host, request.Host) || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return false
		}
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

func (s *Server) handleChatStream(writer http.ResponseWriter, request *http.Request) {
	defer request.Body.Close()
	if !localChatRequest(request) {
		writeError(writer, http.StatusForbidden, "invalid_origin", "local chat requires a same-origin JSON request")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxDelegateJSON+1))
	if err != nil || len(raw) > maxDelegateJSON {
		writeError(writer, http.StatusBadRequest, "invalid_body", "unable to read local chat request")
		return
	}
	var body DelegateRequest
	if json.Unmarshal(raw, &body) != nil || strings.TrimSpace(body.Prompt) == "" || len(body.Prompt) > maxPromptBytes {
		writeError(writer, http.StatusBadRequest, "invalid_body", "local chat requires a harness and a prompt of at most 64KiB")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), delegateTimeout(body.TimeoutSeconds))
	defer cancel()
	tool, target, err := s.resolveDelegate(ctx, body.Harness)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_harness", "the selected local harness is unavailable")
		return
	}
	d := s.opts.Discoverer
	d.Target = target.Mode
	d.Distro = target.Distro
	workdir, err := s.chatWorkdir(ctx, d, target, body.Workdir)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_workdir", "working directory must be an existing absolute directory, or left empty for the Desktop workspace")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(writer)
	emit := func(event string, data any) error {
		// A stalled reader cannot keep a subprocess or output-copy goroutine alive.
		_ = control.SetWriteDeadline(time.Now().Add(10 * time.Second))
		payload, marshalErr := json.Marshal(data)
		if marshalErr != nil {
			return marshalErr
		}
		if _, writeErr := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event, payload); writeErr != nil {
			cancel()
			return writeErr
		}
		if flushErr := control.Flush(); flushErr != nil {
			cancel()
			return flushErr
		}
		return nil
	}
	if err := emit("started", map[string]string{"harness": tool.ID, "target": target.Mode, "wslDistro": target.Distro, "workdir": workdir}); err != nil {
		return
	}
	lines := &jsonLineWriter{emit: func(line string) error { return emit("stdout", map[string]string{"line": line}) }}
	result, err := runDelegateWithOptions(ctx, d, tool, target, body.Prompt, delegateTimeout(body.TimeoutSeconds), delegateOptions{Workdir: workdir, Stdout: lines})
	if request.Context().Err() != nil {
		return
	}
	if err != nil {
		_ = emit("error", map[string]string{"code": "delegate_failed", "message": "the local harness could not finish this turn"})
		return
	}
	_ = emit("result", result)
}

// jsonLineWriter retains at most one bounded line and skips oversized/non-JSON
// output. Partial writes and an incomplete final line never reach the browser.
type jsonLineWriter struct {
	pending  []byte
	dropping bool
	emit     func(string) error
}

func (w *jsonLineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		segment := p
		if i >= 0 {
			segment = p[:i]
		}
		if !w.dropping {
			if len(w.pending)+len(segment) > maxDelegateCapture {
				w.pending = w.pending[:0]
				w.dropping = true
			} else {
				w.pending = append(w.pending, segment...)
			}
		}
		if i < 0 {
			break
		}
		if !w.dropping && json.Valid(w.pending) {
			if err := w.emit(string(w.pending)); err != nil {
				return 0, err
			}
		}
		w.pending = w.pending[:0]
		w.dropping = false
		p = p[i+1:]
	}
	return n, nil
}

const wslWorkspaceScript = `if [ -z "$ANVIL_DESKTOP_WORKDIR" ]; then
  ANVIL_DESKTOP_WORKDIR="$HOME/.local/share/anvil-agents-desktop/workspace"
  umask 077
  mkdir -p -- "$ANVIL_DESKTOP_WORKDIR" || exit 125
fi
case "$ANVIL_DESKTOP_WORKDIR" in /*) ;; *) exit 125 ;; esac
cd -- "$ANVIL_DESKTOP_WORKDIR" || exit 125
pwd -P`

func (s *Server) chatWorkdir(ctx context.Context, d Discoverer, target invokeTarget, requested string) (string, error) {
	if strings.ContainsRune(requested, '\x00') {
		return "", fmt.Errorf("invalid directory")
	}
	if target.Mode == HarnessTargetWSL && d.useWSL() {
		if requested != "" && !path.IsAbs(requested) {
			return "", fmt.Errorf("relative WSL directory")
		}
		probeCtx, cancel := context.WithTimeout(ctx, wslProbeTimeout)
		defer cancel()
		result, err := d.wslRunner().Run(probeCtx, WSLRequest{Distro: target.Distro, Argv: []string{"/bin/bash", "-c", wslWorkspaceScript}, Env: []string{"ANVIL_DESKTOP_WORKDIR=" + requested}})
		actual := strings.TrimSuffix(result.Stdout, "\n")
		if err != nil || result.ExitCode != 0 || result.TimedOut || !path.IsAbs(actual) || strings.ContainsRune(actual, '\x00') {
			return "", fmt.Errorf("WSL directory unavailable")
		}
		return actual, nil
	}
	if requested == "" {
		prefs, err := prefsPath(s.opts.ConfigDir)
		if err != nil {
			return "", err
		}
		requested, err = filepath.Abs(filepath.Join(filepath.Dir(prefs), "workspace"))
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(requested, 0o700); err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(requested) {
		return "", fmt.Errorf("relative directory")
	}
	info, err := os.Stat(requested)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("directory unavailable")
	}
	return filepath.EvalSymlinks(requested)
}
