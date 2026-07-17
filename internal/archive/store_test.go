package archive

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestNewAgentRunArchiveRecordIncludesAuditFields(t *testing.T) {
	t.Parallel()

	completedAt := metav1.NewTime(time.Date(2026, 7, 14, 16, 13, 0, 0, time.UTC))
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "agentrun-anvil-primaris-agent-manager",
			Namespace:         "anvilhub",
			UID:               types.UID("run-uid"),
			ResourceVersion:   "44",
			CreationTimestamp: metav1.NewTime(completedAt.Add(-30 * time.Minute)),
			Labels:            map[string]string{"control.anvil.hazyforge.io/agent-run-source-name": "manager"},
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "AgentSchedule", Name: "anvil-primaris-agent-manager-1h"},
			ScheduleRef: &controlv1alpha1.NamespacedObjectReference{
				Name: "anvil-primaris-agent-manager-1h",
			},
			IssueTracking: &controlv1alpha1.AgentRunIssueTrackingSpec{
				Provider:   controlv1alpha1.AgentRunIssueTrackingProviderGitHub,
				Repository: "HazyForge/anvil-primaris",
				Issues:     []controlv1alpha1.AgentRunIssueRef{{Number: 415}},
			},
		},
		Status: controlv1alpha1.AgentRunStatus{
			Phase:          controlv1alpha1.AgentRunPhaseNeedsHuman,
			Backend:        "codex",
			Intent:         "proposeChange",
			CompletedAt:    &completedAt,
			PullRequestURL: "https://github.com/HazyForge/anvil-primaris/pull/459",
			Decision: &controlv1alpha1.AgentRunDecisionStatus{
				Classification: "durable code/config defect",
				Action:         "no mutation",
				Summary:        "Operator replied Yes.",
			},
			Reports: []controlv1alpha1.AgentRunStatusReport{{
				Type:       "decision",
				Summary:    "Operator replied Yes.",
				NeedsHuman: true,
			}},
			Output: "bounded manager output",
		},
	}

	record, err := NewAgentRunArchiveRecord(run, completedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewAgentRunArchiveRecord returned error: %v", err)
	}
	if record.Digest == "" || !strings.HasPrefix(record.Digest, "sha256:") {
		t.Fatalf("digest = %q, want sha256", record.Digest)
	}
	if record.ScheduleName != "anvil-primaris-agent-manager-1h" {
		t.Fatalf("schedule name = %q", record.ScheduleName)
	}
	if !strings.Contains(string(record.Spec), `"issueTracking"`) || !strings.Contains(string(record.Spec), `"number":415`) {
		t.Fatalf("spec archive missing issue context: %s", string(record.Spec))
	}
	if !strings.Contains(string(record.Status), `"pullRequestURL"`) || !strings.Contains(string(record.Status), "Operator replied Yes.") {
		t.Fatalf("status archive missing decision fields: %s", string(record.Status))
	}
	var reports []controlv1alpha1.AgentRunStatusReport
	if err := json.Unmarshal(record.Reports, &reports); err != nil {
		t.Fatalf("unmarshal reports: %v", err)
	}
	if len(reports) != 1 || !reports[0].NeedsHuman {
		t.Fatalf("reports = %#v, want NeedsHuman report", reports)
	}
	if record.Output != "bounded manager output" {
		t.Fatalf("output = %q", record.Output)
	}
}
