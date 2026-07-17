package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/archive"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const agentRunArchiveRetryInterval = 5 * time.Minute

// NewAgentRunArchiveStore returns the optional durable AgentRun archive store configured for the operator.
func NewAgentRunArchiveStore(_ context.Context, options *Options) (archive.AgentRunArchiveStore, error) {
	if options == nil || strings.TrimSpace(options.AgentRunArchiveDatabaseURL) == "" {
		return nil, nil
	}
	return &lazyAgentRunArchiveStore{databaseURL: strings.TrimSpace(options.AgentRunArchiveDatabaseURL)}, nil
}

type lazyAgentRunArchiveStore struct {
	databaseURL string
	mu          sync.Mutex
	store       archive.AgentRunArchiveStore
}

func (s *lazyAgentRunArchiveStore) ArchiveAgentRun(ctx context.Context, record archive.AgentRunArchiveRecord) (archive.AgentRunArchiveResult, error) {
	store, err := s.open(ctx)
	if err != nil {
		return archive.AgentRunArchiveResult{}, err
	}
	return store.ArchiveAgentRun(ctx, record)
}

func (s *lazyAgentRunArchiveStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store != nil {
		s.store.Close()
		s.store = nil
	}
}

func (s *lazyAgentRunArchiveStore) open(ctx context.Context) (archive.AgentRunArchiveStore, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store != nil {
		return s.store, nil
	}
	store, err := archive.OpenPostgresAgentRunArchiveStore(ctx, s.databaseURL)
	if err != nil {
		return nil, err
	}
	s.store = store
	return store, nil
}

func (r *AgentRunReconciler) reconcileTerminalAgentRun(ctx context.Context, run *controlv1alpha1.AgentRun) (ctrl.Result, error) {
	if run == nil {
		return ctrl.Result{}, nil
	}
	if r.AgentRunArchive != nil && !agentRunArchived(run) {
		return r.archiveTerminalAgentRun(ctx, run)
	}
	if r.terminalRetentionElapsed(run) {
		if err := r.Delete(ctx, run); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return ctrl.Result{}, nil
	}
	if requeueAfter := r.terminalRetentionRequeueAfter(run); requeueAfter > 0 {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	return ctrl.Result{}, nil
}

func (r *AgentRunReconciler) archiveTerminalAgentRun(ctx context.Context, run *controlv1alpha1.AgentRun) (ctrl.Result, error) {
	original := run.DeepCopy()
	archivedAt := time.Now().UTC()
	record, err := archive.NewAgentRunArchiveRecord(run, archivedAt)
	if err == nil {
		var result archive.AgentRunArchiveResult
		result, err = r.AgentRunArchive.ArchiveAgentRun(ctx, record)
		if err == nil {
			when := metav1.NewTime(result.ArchivedAt)
			run.Status.Archive = &controlv1alpha1.AgentRunArchiveStatus{
				Store:      result.Store,
				ArchivedAt: &when,
				Digest:     result.Digest,
			}
			if patchErr := r.Status().Patch(ctx, run, client.MergeFrom(original)); patchErr != nil {
				if apierrors.IsConflict(patchErr) {
					return ctrl.Result{Requeue: true}, nil
				}
				return ctrl.Result{}, patchErr
			}
			if r.terminalRetentionElapsed(run) {
				return ctrl.Result{Requeue: true}, nil
			}
			if requeueAfter := r.terminalRetentionRequeueAfter(run); requeueAfter > 0 {
				return ctrl.Result{RequeueAfter: requeueAfter}, nil
			}
			return ctrl.Result{}, nil
		}
	}
	run.Status.Archive = &controlv1alpha1.AgentRunArchiveStatus{
		Store: archive.AgentRunArchiveStorePostgres,
		Error: agentRunArchiveErrorMessage(err),
	}
	if patchErr := r.Status().Patch(ctx, run, client.MergeFrom(original)); patchErr != nil {
		if apierrors.IsConflict(patchErr) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, patchErr
	}
	return ctrl.Result{RequeueAfter: agentRunArchiveRetryInterval}, nil
}

func agentRunArchived(run *controlv1alpha1.AgentRun) bool {
	if run == nil || run.Status.Archive == nil {
		return false
	}
	return strings.TrimSpace(run.Status.Archive.Store) != "" &&
		run.Status.Archive.ArchivedAt != nil &&
		strings.TrimSpace(run.Status.Archive.Digest) != "" &&
		strings.TrimSpace(run.Status.Archive.Error) == ""
}

func (r *AgentRunReconciler) terminalRetentionElapsed(run *controlv1alpha1.AgentRun) bool {
	elapsed, _ := r.terminalRetentionWindow(run)
	return elapsed
}

func (r *AgentRunReconciler) terminalRetentionRequeueAfter(run *controlv1alpha1.AgentRun) time.Duration {
	_, requeueAfter := r.terminalRetentionWindow(run)
	return requeueAfter
}

func (r *AgentRunReconciler) terminalRetentionWindow(run *controlv1alpha1.AgentRun) (bool, time.Duration) {
	if run == nil || !agentRunPhaseTerminal(run.Status.Phase) || !agentRunArchived(run) {
		return false, 0
	}
	if run.Spec.SituationRef != nil {
		return false, 0
	}
	if r.CommonReconcilerOptions.Options == nil || r.CommonReconcilerOptions.Options.AgentRunTerminalRetention <= 0 {
		return false, 0
	}
	completedAt := run.Status.CompletedAt
	if completedAt == nil || completedAt.IsZero() {
		return false, 0
	}
	deadline := completedAt.Time.Add(r.CommonReconcilerOptions.Options.AgentRunTerminalRetention)
	requeueAfter := time.Until(deadline)
	if requeueAfter <= 0 {
		return true, 0
	}
	return false, requeueAfter
}

func agentRunArchiveErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	const max = 1000
	message := strings.TrimSpace(fmt.Sprintf("%v", err))
	if len(message) <= max {
		return message
	}
	return message[:max]
}
