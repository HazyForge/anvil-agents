package runapi

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/archive"
)

// AgentRunPurgeRequest removes terminal live AgentRun CRs that are already
// durable in PostgreSQL. History remains in anvilhub_agent_run_archives.
type AgentRunPurgeRequest struct {
	// KeepLatest retains this many newest runs (all phases) after purge.
	// Defaults to 20.
	KeepLatest int `json:"keepLatest"`
	// KeepPerSchedule retains this many newest runs per schedule/source name.
	// Defaults to 3.
	KeepPerSchedule int `json:"keepPerSchedule"`
	// OlderThan is an optional Go duration (for example "6h"). When set, only
	// terminal runs completed before now-OlderThan are eligible for purge.
	OlderThan string `json:"olderThan,omitempty"`
	// ScheduleName optionally limits purge to one schedule or source name.
	ScheduleName string `json:"scheduleName,omitempty"`
	// DryRun reports what would be deleted without mutating the cluster.
	DryRun bool `json:"dryRun"`
}

type AgentRunPurgeSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type AgentRunPurgeResponse struct {
	Deleted   []string            `json:"deleted"`
	Skipped   []AgentRunPurgeSkip `json:"skipped,omitempty"`
	Kept      int                 `json:"kept"`
	DryRun    bool                `json:"dryRun"`
	ArchiveOK bool                `json:"archiveStoreAvailable"`
}

func (server *Server) handlePurgeRuns(writer http.ResponseWriter, request *http.Request) {
	namespace := request.PathValue("namespace")
	principal := principalFromContext(request.Context())
	if !server.config.Runs.PurgeEnabled {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if !server.authorizer.Allowed(principal, PermissionRunsPurge, namespace) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if server.writes == nil {
		writeAPIError(writer, http.StatusServiceUnavailable, "purge_unavailable", "AgentRun write client is unavailable")
		return
	}

	var body AgentRunPurgeRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", "request body must be a JSON purge object")
		return
	}
	if body.KeepLatest < 0 || body.KeepLatest > 500 {
		writeAPIError(writer, http.StatusBadRequest, "invalid_keep_latest", "keepLatest must be between 0 and 500")
		return
	}
	if body.KeepPerSchedule < 0 || body.KeepPerSchedule > 100 {
		writeAPIError(writer, http.StatusBadRequest, "invalid_keep_per_schedule", "keepPerSchedule must be between 0 and 100")
		return
	}
	if body.KeepLatest == 0 {
		body.KeepLatest = 20
	}
	if body.KeepPerSchedule == 0 {
		body.KeepPerSchedule = 3
	}
	var olderThan time.Duration
	if raw := strings.TrimSpace(body.OlderThan); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			writeAPIError(writer, http.StatusBadRequest, "invalid_older_than", "olderThan must be a positive Go duration such as 6h")
			return
		}
		olderThan = parsed
	}
	scheduleFilter := strings.TrimSpace(body.ScheduleName)

	list := &agentsv1alpha1.AgentRunList{}
	if err := server.runs.List(request.Context(), list, client.InNamespace(namespace)); err != nil {
		server.log.Error(err, "list AgentRuns for purge", "subject", principal.Subject, "namespace", namespace)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "AgentRun state is unavailable")
		return
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].CreationTimestamp.After(list.Items[j].CreationTimestamp.Time)
	})

	keep := map[string]struct{}{}
	for i := range list.Items {
		run := &list.Items[i]
		if !agentRunPhaseTerminal(run.Status.Phase) {
			keep[run.Name] = struct{}{}
		}
	}
	for i := 0; i < len(list.Items) && i < body.KeepLatest; i++ {
		keep[list.Items[i].Name] = struct{}{}
	}
	perSchedule := map[string]int{}
	for i := range list.Items {
		run := &list.Items[i]
		key := agentRunScheduleKey(run)
		if perSchedule[key] >= body.KeepPerSchedule {
			continue
		}
		keep[run.Name] = struct{}{}
		perSchedule[key]++
	}

	now := time.Now().UTC()
	response := AgentRunPurgeResponse{
		Deleted:   make([]string, 0),
		Skipped:   make([]AgentRunPurgeSkip, 0),
		DryRun:    body.DryRun,
		ArchiveOK: server.archives != nil,
	}

	for i := range list.Items {
		run := &list.Items[i]
		if _, ok := keep[run.Name]; ok {
			continue
		}
		if scheduleFilter != "" && agentRunScheduleKey(run) != scheduleFilter {
			continue
		}
		if !agentRunPhaseTerminal(run.Status.Phase) {
			response.Skipped = append(response.Skipped, AgentRunPurgeSkip{Name: run.Name, Reason: "not_terminal"})
			continue
		}
		if run.Spec.SituationRef != nil {
			response.Skipped = append(response.Skipped, AgentRunPurgeSkip{Name: run.Name, Reason: "situation_linked"})
			continue
		}
		if !agentRunSuccessfullyArchived(run) {
			response.Skipped = append(response.Skipped, AgentRunPurgeSkip{Name: run.Name, Reason: "not_archived"})
			continue
		}
		if olderThan > 0 {
			completed := run.Status.CompletedAt
			if completed == nil || completed.IsZero() || now.Sub(completed.Time) < olderThan {
				response.Skipped = append(response.Skipped, AgentRunPurgeSkip{Name: run.Name, Reason: "too_new"})
				continue
			}
		}
		if server.archives != nil {
			ok, err := server.archives.HasAgentRunArchive(request.Context(), run.Namespace, run.Name, string(run.UID))
			if err != nil {
				server.log.Error(err, "verify AgentRun archive before purge", "namespace", namespace, "agentRun", run.Name)
				response.Skipped = append(response.Skipped, AgentRunPurgeSkip{Name: run.Name, Reason: "archive_verify_failed"})
				continue
			}
			if !ok {
				response.Skipped = append(response.Skipped, AgentRunPurgeSkip{Name: run.Name, Reason: "missing_postgres_row"})
				continue
			}
		}
		if body.DryRun {
			response.Deleted = append(response.Deleted, run.Name)
			continue
		}
		if err := server.writes.Delete(request.Context(), run); err != nil {
			if apierrors.IsNotFound(err) {
				response.Deleted = append(response.Deleted, run.Name)
				continue
			}
			server.log.Error(err, "delete archived AgentRun", "subject", principal.Subject, "namespace", namespace, "agentRun", run.Name)
			response.Skipped = append(response.Skipped, AgentRunPurgeSkip{Name: run.Name, Reason: "delete_failed"})
			continue
		}
		response.Deleted = append(response.Deleted, run.Name)
	}
	response.Kept = len(list.Items) - len(response.Deleted)
	server.log.Info("AgentRun purge",
		"subject", principal.Subject,
		"namespace", namespace,
		"deleted", len(response.Deleted),
		"skipped", len(response.Skipped),
		"dryRun", body.DryRun,
	)
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) handleListArchives(writer http.ResponseWriter, request *http.Request) {
	namespace := request.PathValue("namespace")
	principal := principalFromContext(request.Context())
	if !server.authorizer.Allowed(principal, PermissionRunsRead, namespace) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if server.archives == nil {
		writeAPIError(writer, http.StatusServiceUnavailable, "archive_unavailable", "PostgreSQL AgentRun archive is not configured on this API")
		return
	}
	limit := 50
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeAPIError(writer, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	items, err := server.archives.ListAgentRunArchives(request.Context(), namespace, limit)
	if err != nil {
		server.log.Error(err, "list AgentRun archives", "subject", principal.Subject, "namespace", namespace)
		writeAPIError(writer, http.StatusServiceUnavailable, "archive_unavailable", "AgentRun archive is temporarily unavailable")
		return
	}
	if items == nil {
		items = []archive.AgentRunArchiveListItem{}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func agentRunPhaseTerminal(phase agentsv1alpha1.AgentRunPhase) bool {
	switch phase {
	case agentsv1alpha1.AgentRunPhaseSucceeded, agentsv1alpha1.AgentRunPhaseFailed, agentsv1alpha1.AgentRunPhaseNeedsHuman:
		return true
	default:
		return false
	}
}

func agentRunSuccessfullyArchived(run *agentsv1alpha1.AgentRun) bool {
	if run == nil || run.Status.Archive == nil {
		return false
	}
	a := run.Status.Archive
	return strings.TrimSpace(a.Store) != "" &&
		a.ArchivedAt != nil &&
		!a.ArchivedAt.IsZero() &&
		strings.TrimSpace(a.Digest) != "" &&
		strings.TrimSpace(a.Error) == ""
}

func agentRunScheduleKey(run *agentsv1alpha1.AgentRun) string {
	if run == nil {
		return "none"
	}
	if run.Spec.ScheduleRef != nil && strings.TrimSpace(run.Spec.ScheduleRef.Name) != "" {
		return strings.TrimSpace(run.Spec.ScheduleRef.Name)
	}
	if strings.TrimSpace(run.Spec.SourceRef.Name) != "" {
		return strings.TrimSpace(run.Spec.SourceRef.Name)
	}
	return "none"
}
