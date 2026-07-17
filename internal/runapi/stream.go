package runapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

type streamLogLine struct {
	Pod       string    `json:"pod"`
	PodUID    string    `json:"podUID"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Line      string    `json:"line"`
	Bytes     int       `json:"-"`
}

type streamLogDone struct {
	Pod   string
	Error error
}

type runStatusResult struct {
	Run   *agentsv1alpha1.AgentRun
	Error error
}

type streamEvent struct {
	Type    string        `json:"type"`
	Code    string        `json:"code,omitempty"`
	Message string        `json:"message,omitempty"`
	Run     *AgentRunView `json:"run,omitempty"`
}

func (server *Server) handleRunEvents(writer http.ResponseWriter, request *http.Request) {
	run, principal, ok := server.authorizedRun(writer, request, PermissionRunsRead, PermissionRunsStream)
	if !ok {
		return
	}
	release, ok := server.limiter.acquire(principal.Subject)
	if !ok {
		writer.Header().Set("Retry-After", "5")
		writeAPIError(writer, http.StatusTooManyRequests, "stream_limit", "too many active streams")
		return
	}
	defer release()

	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeAPIError(writer, http.StatusInternalServerError, "stream_unsupported", "HTTP streaming is unavailable")
		return
	}
	tailLines, err := server.requestTailLines(request)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_tail_lines", err.Error())
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store, no-transform")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	sse := newSSEWriter(writer, flusher)
	server.log.Info("AgentRun stream opened", "subject", principal.Subject, "issuer", principal.Issuer, "namespace", run.Namespace, "agentRun", run.Name)
	defer server.log.Info("AgentRun stream closed", "subject", principal.Subject, "namespace", run.Namespace, "agentRun", run.Name)
	if lastID := strings.TrimSpace(request.Header.Get("Last-Event-ID")); lastID != "" {
		if err := sse.write("reset", "", map[string]string{
			"reason":          "exact replay is unavailable; a fresh snapshot and bounded log tail follow",
			"previousEventID": lastID,
		}); err != nil {
			return
		}
	}
	view := NewAgentRunView(run, true)
	if err := sse.write("snapshot", "status:"+run.ResourceVersion, streamEvent{Type: "snapshot", Run: &view}); err != nil {
		return
	}
	initialRunUID := run.UID

	streamTimer := time.NewTimer(server.config.Stream.MaxDuration.Duration)
	defer streamTimer.Stop()
	tokenTimer := time.NewTimer(time.Until(principal.Expiry))
	defer tokenTimer.Stop()
	heartbeat := time.NewTicker(server.config.Stream.HeartbeatInterval.Duration)
	defer heartbeat.Stop()
	statusResults := make(chan runStatusResult, 1)
	go server.pollRunStatus(
		request.Context(),
		types.NamespacedName{Namespace: run.Namespace, Name: run.Name},
		server.config.Stream.StatusPollInterval.Duration,
		statusResults,
	)

	logLines := make(chan streamLogLine, 32)
	logDone := make(chan streamLogDone, 4)
	var logCancel context.CancelFunc
	currentPod := ""
	finishedPod := ""
	retryLogsAt := time.Time{}
	logActive := false
	logBudget := streamByteBudget{max: server.config.Stream.MaxLogBytes}
	startLogs := func(run *agentsv1alpha1.AgentRun) {
		podName := ""
		if run.Status.RunnerPodRef != nil {
			podName = strings.TrimSpace(run.Status.RunnerPodRef.Name)
		}
		if podName == "" || podName == finishedPod || (podName == currentPod && (logActive || time.Now().Before(retryLogsAt))) {
			return
		}
		if logCancel != nil {
			logCancel()
		}
		logCtx, cancel := context.WithCancel(request.Context())
		logCancel = cancel
		if podName != currentPod {
			finishedPod = ""
			retryLogsAt = time.Time{}
		}
		currentPod = podName
		logActive = true
		go server.followLogs(logCtx, run.DeepCopy(), tailLines, logLines, logDone)
	}
	defer func() {
		if logCancel != nil {
			logCancel()
		}
	}()
	startLogs(run)

	for {
		if agentRunTerminal(run.Status.Phase) && !logActive && len(logLines) == 0 {
			terminalView := NewAgentRunView(run, true)
			_ = sse.write("terminal", "status:"+run.ResourceVersion, streamEvent{Type: "terminal", Run: &terminalView})
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-tokenTimer.C:
			_ = sse.write("error", "", streamEvent{Type: "error", Code: "token_expired", Message: "access token expired; reconnect with a refreshed token"})
			return
		case <-streamTimer.C:
			_ = sse.write("complete", "", streamEvent{Type: "complete", Code: "stream_duration_reached", Message: "reconnect to continue streaming"})
			return
		case <-heartbeat.C:
			if err := sse.heartbeat(); err != nil {
				return
			}
		case line := <-logLines:
			if line.Pod != currentPod {
				continue
			}
			if !logBudget.add(int64(line.Bytes)) {
				if logCancel != nil {
					logCancel()
				}
				logActive = false
				finishedPod = currentPod
				_ = sse.write("reset", "", streamEvent{Type: "reset", Code: "log_limit_reached", Message: "log byte limit reached; reconnect for another bounded tail"})
				continue
			}
			id := fmt.Sprintf("log:%s:%d", line.PodUID, time.Now().UnixNano())
			if err := sse.write("log", id, line); err != nil {
				return
			}
		case done := <-logDone:
			if done.Pod != currentPod {
				continue
			}
			logActive = false
			if done.Error == nil {
				finishedPod = currentPod
			} else if errors.Is(done.Error, ErrLogsPending) {
				retryLogsAt = time.Now().Add(server.config.Stream.StatusPollInterval.Duration)
			} else if !errors.Is(done.Error, context.Canceled) {
				retryLogsAt = time.Now().Add(5 * time.Second)
				if err := sse.write("error", "", streamEvent{Type: "error", Code: "logs_unavailable", Message: "runner logs are temporarily unavailable"}); err != nil {
					return
				}
			}
		case result := <-statusResults:
			if apierrors.IsNotFound(result.Error) {
				_ = sse.write("terminal", "", streamEvent{Type: "terminal", Code: "deleted", Message: "AgentRun was deleted"})
				return
			}
			if result.Error != nil {
				if writeErr := sse.write("error", "", streamEvent{Type: "error", Code: "kubernetes_unavailable", Message: "AgentRun status is temporarily unavailable"}); writeErr != nil {
					return
				}
				continue
			}
			latest := result.Run
			if latest.UID != initialRunUID {
				_ = sse.write("terminal", "", streamEvent{Type: "terminal", Code: "replaced", Message: "AgentRun was replaced by a different resource"})
				return
			}
			if latest.ResourceVersion != run.ResourceVersion {
				run = latest
				statusView := NewAgentRunView(run, true)
				if err := sse.write("status", "status:"+run.ResourceVersion, streamEvent{Type: "status", Run: &statusView}); err != nil {
					return
				}
			}
			startLogs(run)
		}
	}
}

func (server *Server) pollRunStatus(ctx context.Context, key types.NamespacedName, interval time.Duration, results chan<- runStatusResult) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	readTimeout := min(interval, 5*time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			readCtx, cancel := context.WithTimeout(ctx, readTimeout)
			latest := &agentsv1alpha1.AgentRun{}
			err := server.runs.Get(readCtx, key, latest)
			cancel()
			result := runStatusResult{Run: latest, Error: err}
			select {
			case results <- result:
			case <-ctx.Done():
				return
			default:
			}
		}
	}
}

type streamByteBudget struct {
	used int64
	max  int64
}

func (budget *streamByteBudget) add(bytes int64) bool {
	if bytes < 0 || budget.used > budget.max-bytes {
		return false
	}
	budget.used += bytes
	return true
}

func (server *Server) requestTailLines(request *http.Request) (int64, error) {
	tailLines := server.config.Stream.DefaultTailLines
	if raw := strings.TrimSpace(request.URL.Query().Get("tailLines")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 || parsed > server.config.Stream.MaxTailLines {
			return 0, fmt.Errorf("tailLines must be between 0 and %d", server.config.Stream.MaxTailLines)
		}
		tailLines = parsed
	}
	return tailLines, nil
}

func (server *Server) followLogs(ctx context.Context, run *agentsv1alpha1.AgentRun, tailLines int64, lines chan<- streamLogLine, done chan<- streamLogDone) {
	podName := ""
	if run.Status.RunnerPodRef != nil {
		podName = run.Status.RunnerPodRef.Name
	}
	finish := func(err error) {
		select {
		case done <- streamLogDone{Pod: podName, Error: err}:
		case <-ctx.Done():
		}
	}
	options := corev1.PodLogOptions{Follow: true, Timestamps: true, TailLines: &tailLines}
	stream, pod, err := server.logs.Open(ctx, run, options)
	if err != nil {
		finish(err)
		return
	}
	defer stream.Close()
	scanner := bufio.NewScanner(io.LimitReader(stream, server.config.Stream.MaxLogBytes+1))
	scanner.Buffer(make([]byte, 64*1024), server.config.Stream.MaxLineBytes)
	for scanner.Scan() {
		raw := scanner.Text()
		timestamp, line := parseKubernetesLogLine(raw)
		event := streamLogLine{Pod: pod.Name, PodUID: string(pod.UID), Timestamp: timestamp, Line: line, Bytes: len(raw) + 1}
		select {
		case lines <- event:
		case <-ctx.Done():
			finish(ctx.Err())
			return
		}
	}
	finish(scanner.Err())
}

func parseKubernetesLogLine(raw string) (time.Time, string) {
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) == 2 {
		if timestamp, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
			return timestamp, parts[1]
		}
	}
	return time.Time{}, raw
}

type sseWriter struct {
	writer  http.ResponseWriter
	flusher http.Flusher
}

func newSSEWriter(writer http.ResponseWriter, flusher http.Flusher) *sseWriter {
	return &sseWriter{writer: writer, flusher: flusher}
}

func (writer *sseWriter) write(event, id string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	controller := http.NewResponseController(writer.writer)
	_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
	defer controller.SetWriteDeadline(time.Time{})
	if id != "" {
		if _, err := fmt.Fprintf(writer.writer, "id: %s\n", strings.ReplaceAll(id, "\n", "")); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(writer.writer, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	writer.flusher.Flush()
	return nil
}

func (writer *sseWriter) heartbeat() error {
	controller := http.NewResponseController(writer.writer)
	_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
	defer controller.SetWriteDeadline(time.Time{})
	if _, err := fmt.Fprintf(writer.writer, ": heartbeat %s\n\n", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	writer.flusher.Flush()
	return nil
}
