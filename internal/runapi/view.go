package runapi

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

type AgentRunListResponse struct {
	Items []AgentRunView `json:"items"`
}

type AgentRunView struct {
	Name                string                                            `json:"name"`
	Namespace           string                                            `json:"namespace"`
	UID                 string                                            `json:"uid"`
	ResourceVersion     string                                            `json:"resourceVersion"`
	CreatedAt           time.Time                                         `json:"createdAt"`
	Phase               agentsv1alpha1.AgentRunPhase                      `json:"phase,omitempty"`
	Backend             string                                            `json:"backend,omitempty"`
	Model               string                                            `json:"model,omitempty"`
	Intent              string                                            `json:"intent,omitempty"`
	Source              AgentRunSourceView                                `json:"source"`
	Application         string                                            `json:"application,omitempty"`
	ApplicationTarget   string                                            `json:"applicationTarget,omitempty"`
	Job                 *agentsv1alpha1.NamespacedObjectReference         `json:"job,omitempty"`
	RunnerPod           *agentsv1alpha1.NamespacedObjectReference         `json:"runnerPod,omitempty"`
	StartedAt           *metav1.Time                                      `json:"startedAt,omitempty"`
	CompletedAt         *metav1.Time                                      `json:"completedAt,omitempty"`
	Conditions          []metav1.Condition                                `json:"conditions,omitempty"`
	Decision            *agentsv1alpha1.AgentRunDecisionStatus            `json:"decision,omitempty"`
	Reports             []agentsv1alpha1.AgentRunStatusReport             `json:"reports,omitempty"`
	ResolvedComposition *agentsv1alpha1.AgentRunResolvedCompositionStatus `json:"resolvedComposition,omitempty"`
	PullRequestURL      string                                            `json:"pullRequestURL,omitempty"`
	Error               string                                            `json:"error,omitempty"`
	Output              string                                            `json:"output,omitempty"`
	Archived            bool                                              `json:"archived"`
}

type AgentRunSourceView struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
}

func NewAgentRunView(run *agentsv1alpha1.AgentRun, includeOutput bool) AgentRunView {
	if run == nil {
		return AgentRunView{}
	}
	view := AgentRunView{
		Name:            run.Name,
		Namespace:       run.Namespace,
		UID:             string(run.UID),
		ResourceVersion: run.ResourceVersion,
		CreatedAt:       run.CreationTimestamp.Time,
		Phase:           run.Status.Phase,
		Backend:         run.Status.Backend,
		Model:           firstNonEmpty(run.Status.Model, agentRunSpecModel(run)),
		Intent:          run.Status.Intent,
		Source: AgentRunSourceView{
			APIVersion: run.Spec.SourceRef.APIVersion,
			Kind:       run.Spec.SourceRef.Kind,
			Namespace:  run.Spec.SourceRef.Namespace,
			Name:       run.Spec.SourceRef.Name,
		},
		Job:                 copyReference(run.Status.JobRef),
		RunnerPod:           copyReference(run.Status.RunnerPodRef),
		StartedAt:           run.Status.StartedAt.DeepCopy(),
		CompletedAt:         run.Status.CompletedAt.DeepCopy(),
		Conditions:          append([]metav1.Condition(nil), run.Status.Conditions...),
		Decision:            copyDecision(run.Status.Decision),
		Reports:             append([]agentsv1alpha1.AgentRunStatusReport(nil), run.Status.Reports...),
		ResolvedComposition: run.Status.ResolvedComposition.DeepCopy(),
		PullRequestURL:      run.Status.PullRequestURL,
		Error:               run.Status.Error,
		Archived:            run.Status.Archive != nil && run.Status.Archive.ArchivedAt != nil,
	}
	if run.Spec.Scope.ApplicationRef != nil {
		view.Application = run.Spec.Scope.ApplicationRef.Name
	}
	if run.Spec.Scope.ApplicationTargetRef != nil {
		view.ApplicationTarget = run.Spec.Scope.ApplicationTargetRef.Name
	}
	if run.Status.ResolvedComposition != nil && run.Status.ResolvedComposition.Scope != nil {
		view.Application = firstNonEmpty(view.Application, run.Status.ResolvedComposition.Scope.Application)
		view.ApplicationTarget = firstNonEmpty(view.ApplicationTarget, run.Status.ResolvedComposition.Scope.ApplicationTarget)
	}
	if includeOutput {
		view.Output = run.Status.Output
	}
	return view
}

func copyReference(reference *agentsv1alpha1.NamespacedObjectReference) *agentsv1alpha1.NamespacedObjectReference {
	if reference == nil {
		return nil
	}
	copy := *reference
	return &copy
}

func copyDecision(decision *agentsv1alpha1.AgentRunDecisionStatus) *agentsv1alpha1.AgentRunDecisionStatus {
	if decision == nil {
		return nil
	}
	copy := *decision
	return &copy
}

func agentRunTerminal(phase agentsv1alpha1.AgentRunPhase) bool {
	return phase == agentsv1alpha1.AgentRunPhaseSucceeded ||
		phase == agentsv1alpha1.AgentRunPhaseFailed ||
		phase == agentsv1alpha1.AgentRunPhaseNeedsHuman
}

// agentRunSpecModel falls back to the run's inline harness model when status
// was written before status.model existed (profile-only models stay empty).
func agentRunSpecModel(run *agentsv1alpha1.AgentRun) string {
	if run == nil {
		return ""
	}
	backend := run.Spec.Harness.Backend
	switch backend.Kind {
	case agentsv1alpha1.AgentRunHarnessBackendCodex, "":
		if backend.Codex != nil {
			return backend.Codex.Model
		}
	case agentsv1alpha1.AgentRunHarnessBackendOpenCode:
		if backend.OpenCode != nil {
			return backend.OpenCode.Model
		}
	case agentsv1alpha1.AgentRunHarnessBackendHermesAgent:
		if backend.HermesAgent != nil {
			return backend.HermesAgent.Model
		}
	case agentsv1alpha1.AgentRunHarnessBackendOpenClaw:
		if backend.OpenClaw != nil {
			return backend.OpenClaw.Model
		}
	case agentsv1alpha1.AgentRunHarnessBackendGrokBuild:
		if backend.GrokBuild != nil {
			return backend.GrokBuild.Model
		}
	case agentsv1alpha1.AgentRunHarnessBackendPiAgent:
		if backend.PiAgent != nil {
			return backend.PiAgent.Model
		}
	}
	return ""
}
