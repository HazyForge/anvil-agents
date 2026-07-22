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
	Name                string                                              `json:"name"`
	Namespace           string                                              `json:"namespace"`
	UID                 string                                              `json:"uid"`
	ResourceVersion     string                                              `json:"resourceVersion"`
	CreatedAt           time.Time                                           `json:"createdAt"`
	Phase               agentsv1alpha1.AgentRunPhase                        `json:"phase,omitempty"`
	Backend             string                                              `json:"backend,omitempty"`
	Intent              string                                              `json:"intent,omitempty"`
	Source              AgentRunSourceView                                  `json:"source"`
	Application         string                                              `json:"application,omitempty"`
	ApplicationTarget   string                                              `json:"applicationTarget,omitempty"`
	Job                 *agentsv1alpha1.NamespacedObjectReference           `json:"job,omitempty"`
	RunnerPod           *agentsv1alpha1.NamespacedObjectReference           `json:"runnerPod,omitempty"`
	StartedAt           *metav1.Time                                        `json:"startedAt,omitempty"`
	CompletedAt         *metav1.Time                                        `json:"completedAt,omitempty"`
	Conditions          []metav1.Condition                                  `json:"conditions,omitempty"`
	Decision            *agentsv1alpha1.AgentRunDecisionStatus              `json:"decision,omitempty"`
	Reports             []agentsv1alpha1.AgentRunStatusReport               `json:"reports,omitempty"`
	EffectSummary       *agentsv1alpha1.AgentRunExternalEffectSummaryStatus `json:"effectSummary,omitempty"`
	Effects             []agentsv1alpha1.AgentRunExternalEffectReceipt      `json:"effects,omitempty"`
	ResolvedComposition *agentsv1alpha1.AgentRunResolvedCompositionStatus   `json:"resolvedComposition,omitempty"`
	PullRequestURL      string                                              `json:"pullRequestURL,omitempty"`
	Failure             *agentsv1alpha1.AgentRunFailureStatus               `json:"failure,omitempty"`
	Error               string                                              `json:"error,omitempty"`
	Output              string                                              `json:"output,omitempty"`
	Archived            bool                                                `json:"archived"`
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
		EffectSummary:       copyEffectSummary(run.Status.EffectSummary),
		Effects:             copyEffects(run.Status.Effects),
		ResolvedComposition: run.Status.ResolvedComposition.DeepCopy(),
		PullRequestURL:      run.Status.PullRequestURL,
		Failure:             copyFailure(run.Status.Failure),
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

func copyEffectSummary(summary *agentsv1alpha1.AgentRunExternalEffectSummaryStatus) *agentsv1alpha1.AgentRunExternalEffectSummaryStatus {
	if summary == nil {
		return nil
	}
	copy := *summary
	return &copy
}

func copyFailure(failure *agentsv1alpha1.AgentRunFailureStatus) *agentsv1alpha1.AgentRunFailureStatus {
	if failure == nil {
		return nil
	}
	copy := *failure
	if failure.AgentContainerExitCode != nil {
		exitCode := *failure.AgentContainerExitCode
		copy.AgentContainerExitCode = &exitCode
	}
	return &copy
}

func copyEffects(effects []agentsv1alpha1.AgentRunExternalEffectReceipt) []agentsv1alpha1.AgentRunExternalEffectReceipt {
	if len(effects) == 0 {
		return nil
	}
	copied := make([]agentsv1alpha1.AgentRunExternalEffectReceipt, len(effects))
	for index := range effects {
		copied[index] = effects[index]
		copied[index].StartedAt = effects[index].StartedAt.DeepCopy()
		copied[index].CompletedAt = effects[index].CompletedAt.DeepCopy()
		copied[index].VerifiedAt = effects[index].VerifiedAt.DeepCopy()
	}
	return copied
}

func agentRunTerminal(phase agentsv1alpha1.AgentRunPhase) bool {
	return phase == agentsv1alpha1.AgentRunPhaseSucceeded ||
		phase == agentsv1alpha1.AgentRunPhaseFailed ||
		phase == agentsv1alpha1.AgentRunPhaseNeedsHuman
}
