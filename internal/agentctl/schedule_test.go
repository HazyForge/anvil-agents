package agentctl

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func fakeScheduleApp(backend *fakeBackend) (App, *strings.Builder, *strings.Builder) {
	var output, errOut strings.Builder
	return App{
		In:  strings.NewReader(""),
		Out: &output,
		Err: &errOut,
		Factory: func(KubeOptions) (Backend, error) {
			return backend, nil
		},
		PollInterval: time.Nanosecond,
	}, &output, &errOut
}

func TestScheduleListTableAndApplicationFilter(t *testing.T) {
	next := metav1.NewTime(time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC))
	backend := &fakeBackend{
		defaultNamespace: "hazy-trade",
		schedules: []agentsv1alpha1.AgentSchedule{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "hazy-trade-production-auditor", Namespace: "hazy-trade"},
				Spec: agentsv1alpha1.AgentScheduleSpec{
					ApplicationRef:  &agentsv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
					IntervalSeconds: 3600,
				},
				Status: agentsv1alpha1.AgentScheduleStatus{
					Phase:     agentsv1alpha1.AgentSchedulePhaseScheduled,
					NextRunAt: &next,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "other-schedule", Namespace: "other"},
				Spec: agentsv1alpha1.AgentScheduleSpec{
					ApplicationRef:  &agentsv1alpha1.ApplicationReferenceSpec{Name: "other-app"},
					IntervalSeconds: 1800,
					Suspend:         true,
				},
				Status: agentsv1alpha1.AgentScheduleStatus{Phase: agentsv1alpha1.AgentSchedulePhaseSuspended},
			},
		},
	}
	app, output, _ := fakeScheduleApp(backend)
	if err := app.Run(context.Background(), []string{"schedule", "list", "-A", "--application", "hazy-trade"}); err != nil {
		t.Fatalf("schedule list: %v", err)
	}
	got := output.String()
	if !strings.Contains(got, "hazy-trade-production-auditor") {
		t.Fatalf("list missing schedule: %s", got)
	}
	if strings.Contains(got, "other-schedule") {
		t.Fatalf("list included filtered schedule: %s", got)
	}
}

func TestScheduleSuspendSetsSpecAndAnnotations(t *testing.T) {
	backend := &fakeBackend{
		defaultNamespace: "hazy-trade",
		schedules: []agentsv1alpha1.AgentSchedule{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "hazy-trade-backlog-worker-1h", Namespace: "hazy-trade"},
				Spec: agentsv1alpha1.AgentScheduleSpec{
					ApplicationRef:  &agentsv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
					IntervalSeconds: 3600,
				},
			},
		},
	}
	app, output, _ := fakeScheduleApp(backend)
	if err := app.Run(context.Background(), []string{
		"schedule", "suspend", "hazy-trade-backlog-worker-1h",
		"--reason", "Operator paused Hazy Trade agents for 48h",
	}); err != nil {
		t.Fatalf("schedule suspend: %v", err)
	}
	if len(backend.updatedSchedules) != 1 {
		t.Fatalf("updated schedules = %d, want 1", len(backend.updatedSchedules))
	}
	updated := backend.updatedSchedules[0]
	if !updated.Spec.Suspend {
		t.Fatal("expected suspend=true")
	}
	if updated.Annotations[schedulePauseReasonAnnotation] != "Operator paused Hazy Trade agents for 48h" {
		t.Fatalf("pause reason = %q", updated.Annotations[schedulePauseReasonAnnotation])
	}
	if strings.TrimSpace(updated.Annotations[schedulePauseChangedAtAnnotation]) == "" {
		t.Fatal("expected pause-changed-at annotation")
	}
	if !strings.Contains(output.String(), "agentschedule.control.anvil.hazyforge.io/hazy-trade-backlog-worker-1h") {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestScheduleResumeClearsSuspend(t *testing.T) {
	backend := &fakeBackend{
		defaultNamespace: "hazy-trade",
		schedules: []agentsv1alpha1.AgentSchedule{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "hazy-trade-backlog-worker-1h", Namespace: "hazy-trade"},
				Spec: agentsv1alpha1.AgentScheduleSpec{
					ApplicationRef:  &agentsv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
					IntervalSeconds: 3600,
					Suspend:         true,
				},
			},
		},
	}
	app, _, _ := fakeScheduleApp(backend)
	if err := app.Run(context.Background(), []string{
		"schedule", "resume", "hazy-trade-backlog-worker-1h",
		"--reason", "Operator resumed Hazy Trade schedules",
	}); err != nil {
		t.Fatalf("schedule resume: %v", err)
	}
	if len(backend.updatedSchedules) != 1 {
		t.Fatalf("updated schedules = %d, want 1", len(backend.updatedSchedules))
	}
	if backend.updatedSchedules[0].Spec.Suspend {
		t.Fatal("expected suspend=false")
	}
}

func TestScheduleRunNowSetsAnnotationAndTemplate(t *testing.T) {
	backend := &fakeBackend{
		defaultNamespace: "hazy-trade",
		schedules: []agentsv1alpha1.AgentSchedule{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hazy-trade-production-auditor",
					Namespace: "hazy-trade",
					Annotations: map[string]string{
						agentsv1alpha1.AgentScheduleRunTemplateAnnotation: "stale",
					},
				},
				Spec: agentsv1alpha1.AgentScheduleSpec{IntervalSeconds: 3600},
			},
		},
	}
	app, _, _ := fakeScheduleApp(backend)
	if err := app.Run(context.Background(), []string{
		"schedule", "run-now", "hazy-trade-production-auditor",
		"--template", "primary",
	}); err != nil {
		t.Fatalf("schedule run-now: %v", err)
	}
	if len(backend.updatedSchedules) != 1 {
		t.Fatalf("updated schedules = %d, want 1", len(backend.updatedSchedules))
	}
	updated := backend.updatedSchedules[0]
	if strings.TrimSpace(updated.Annotations[agentsv1alpha1.AgentScheduleRunNowAnnotation]) == "" {
		t.Fatal("expected run-now annotation")
	}
	if updated.Annotations[agentsv1alpha1.AgentScheduleRunTemplateAnnotation] != "primary" {
		t.Fatalf("template annotation = %q", updated.Annotations[agentsv1alpha1.AgentScheduleRunTemplateAnnotation])
	}
}

func TestScheduleSuspendRequiresReason(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "hazy-trade"}
	app, _, _ := fakeScheduleApp(backend)
	err := app.Run(context.Background(), []string{"schedule", "suspend", "name"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
