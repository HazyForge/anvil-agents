package agentctl

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func fakeControlApp(backend *fakeBackend) (App, *strings.Builder, *strings.Builder) {
	var output, errorOutput strings.Builder
	app := App{
		Out: &output,
		Err: &errorOutput,
		Factory: func(KubeOptions) (Backend, error) {
			return backend, nil
		},
	}
	return app, &output, &errorOutput
}

func TestControlListTableAndApplicationFilter(t *testing.T) {
	backend := &fakeBackend{
		defaultNamespace: "default",
		controls: []agentsv1alpha1.AgentRunControl{
			{ObjectMeta: metav1.ObjectMeta{Name: "hazy-trade"}, Spec: agentsv1alpha1.AgentRunControlSpec{ApplicationRef: agentsv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"}, LaunchPolicy: agentsv1alpha1.AgentRunControlLaunchPolicyPaused}, Status: agentsv1alpha1.AgentRunControlStatus{Phase: agentsv1alpha1.AgentRunControlPhasePaused, AffectedScheduleCount: 5}},
			{ObjectMeta: metav1.ObjectMeta{Name: "other-app"}, Spec: agentsv1alpha1.AgentRunControlSpec{ApplicationRef: agentsv1alpha1.ApplicationReferenceSpec{Name: "other-app"}, LaunchPolicy: agentsv1alpha1.AgentRunControlLaunchPolicyAllowed}, Status: agentsv1alpha1.AgentRunControlStatus{Phase: agentsv1alpha1.AgentRunControlPhaseAllowed}},
		},
	}
	app, output, _ := fakeControlApp(backend)
	if err := app.Run(context.Background(), []string{"control", "list", "--application", "hazy-trade"}); err != nil {
		t.Fatalf("control list: %v", err)
	}
	text := output.String()
	for _, expected := range []string{"hazy-trade", "Paused", "5"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("control list output missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "other-app") {
		t.Fatalf("control list --application filter leaked other application:\n%s", text)
	}
}

func TestControlPauseClientDryRunDoesNotLoadKubernetes(t *testing.T) {
	var output, errorOutput strings.Builder
	factoryCalled := false
	app := App{
		Out: &output,
		Err: &errorOutput,
		Factory: func(KubeOptions) (Backend, error) {
			factoryCalled = true
			return nil, errors.New("unexpected factory call")
		},
	}
	err := app.Run(context.Background(), []string{
		"control", "pause",
		"--application", "hazy-trade",
		"--reason", "maintainer requested a bounded review window",
		"--for", "4h",
		"--source-name", "event-42",
		"--source-url", "https://github.com/HazyForge/hazy-trade/pull/123",
		"--dry-run", "client",
		"--output", "yaml",
	})
	if err != nil {
		t.Fatalf("control pause client dry-run: %v", err)
	}
	if factoryCalled {
		t.Fatal("client dry-run loaded Kubernetes")
	}
	for _, expected := range []string{
		"apiVersion: control.anvil.hazyforge.io/v1alpha1",
		"kind: AgentRunControl",
		"name: hazy-trade",
		"launchPolicy: Paused",
		"reason: maintainer requested a bounded review window",
		"kind: PullRequest",
		"name: event-42",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("rendered pause missing %q:\n%s", expected, output.String())
		}
	}
}

func TestControlPauseCreatesBoundedControl(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "default"}
	app, output, _ := fakeControlApp(backend)
	if err := app.Run(context.Background(), []string{
		"control", "pause",
		"--application", "hazy-trade",
		"--reason", "maintainer requested a bounded review window",
		"--for", "48h",
		"--source-name", "event-42",
		"--source-actor", "octocat",
	}); err != nil {
		t.Fatalf("control pause: %v", err)
	}
	if backend.createdControl == nil {
		t.Fatal("control pause did not create a control")
	}
	created := backend.createdControl
	if created.Name != "hazy-trade" {
		t.Fatalf("created control name = %q", created.Name)
	}
	if created.Spec.LaunchPolicy != agentsv1alpha1.AgentRunControlLaunchPolicyPaused {
		t.Fatalf("created launchPolicy = %q", created.Spec.LaunchPolicy)
	}
	if created.Spec.ExpiresAt == nil || !created.Spec.ExpiresAt.After(time.Now().Add(47*time.Hour)) {
		t.Fatalf("created expiresAt = %v, want ~48h in the future", created.Spec.ExpiresAt)
	}
	if created.Spec.Source == nil || created.Spec.Source.Kind != "Operator" || created.Spec.Source.Name != "event-42" || created.Spec.Source.Actor != "octocat" {
		t.Fatalf("created source = %#v", created.Spec.Source)
	}
	if !strings.Contains(output.String(), "agentruncontrol.control.anvil.hazyforge.io/hazy-trade") {
		t.Fatalf("pause output = %q", output.String())
	}
}

func TestControlPauseUpdatesExistingControlPreservingConcurrency(t *testing.T) {
	existing := &agentsv1alpha1.AgentRunControl{
		ObjectMeta: metav1.ObjectMeta{Name: "manager-hazy-trade"},
		Spec: agentsv1alpha1.AgentRunControlSpec{
			ApplicationRef:    agentsv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
			LaunchPolicy:      agentsv1alpha1.AgentRunControlLaunchPolicyAllowed,
			MaxConcurrentRuns: 1,
		},
	}
	backend := &fakeBackend{defaultNamespace: "default", controlGet: existing}
	app, _, _ := fakeControlApp(backend)
	if err := app.Run(context.Background(), []string{
		"control", "pause",
		"--application", "hazy-trade",
		"--control-name", "manager-hazy-trade",
		"--reason", "repeated launch failures exceed the safety threshold",
		"--for", "2h",
	}); err != nil {
		t.Fatalf("control pause update: %v", err)
	}
	if len(backend.updatedControls) != 1 {
		t.Fatalf("updated controls = %d, want 1", len(backend.updatedControls))
	}
	updated := backend.updatedControls[0]
	if updated.Spec.LaunchPolicy != agentsv1alpha1.AgentRunControlLaunchPolicyPaused {
		t.Fatalf("updated launchPolicy = %q", updated.Spec.LaunchPolicy)
	}
	if updated.Spec.MaxConcurrentRuns != 1 {
		t.Fatalf("updated maxConcurrentRuns = %d, want preserved 1", updated.Spec.MaxConcurrentRuns)
	}
	if updated.Spec.ExpiresAt == nil {
		t.Fatal("updated control lost expiresAt")
	}
}

func TestControlPauseRequiresReason(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "default"}
	app, _, _ := fakeControlApp(backend)
	err := app.Run(context.Background(), []string{
		"control", "pause",
		"--application", "hazy-trade",
	})
	if err == nil || !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("error = %v, want missing reason", err)
	}
}

func TestControlPauseRejectsForAndIndefiniteTogether(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "default"}
	app, _, _ := fakeControlApp(backend)
	err := app.Run(context.Background(), []string{
		"control", "pause",
		"--application", "hazy-trade",
		"--reason", "human hold",
		"--indefinite",
		"--for", "2h",
	})
	if err == nil || !strings.Contains(err.Error(), "either --for or --indefinite") {
		t.Fatalf("error = %v, want conflicting duration flags", err)
	}
}

func TestControlResumeUpdatesNamedControlToAllowed(t *testing.T) {
	expires := metav1.NewTime(time.Now().Add(time.Hour))
	backend := &fakeBackend{
		defaultNamespace: "default",
		controls: []agentsv1alpha1.AgentRunControl{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "hazy-trade"},
				Spec: agentsv1alpha1.AgentRunControlSpec{
					ApplicationRef: agentsv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
					LaunchPolicy:   agentsv1alpha1.AgentRunControlLaunchPolicyPaused,
					Reason:         "maintainer hold",
					ExpiresAt:      &expires,
				},
			},
		},
	}
	app, output, _ := fakeControlApp(backend)
	if err := app.Run(context.Background(), []string{
		"control", "resume",
		"--application", "hazy-trade",
		"--reason", "verified maintainer resume",
	}); err != nil {
		t.Fatalf("control resume: %v", err)
	}
	if len(backend.updatedControls) != 1 {
		t.Fatalf("updated controls = %d, want 1", len(backend.updatedControls))
	}
	updated := backend.updatedControls[0]
	if updated.Spec.LaunchPolicy != agentsv1alpha1.AgentRunControlLaunchPolicyAllowed {
		t.Fatalf("resumed launchPolicy = %q", updated.Spec.LaunchPolicy)
	}
	if updated.Spec.ExpiresAt != nil {
		t.Fatalf("resumed control still has expiresAt = %v", updated.Spec.ExpiresAt)
	}
	if updated.Spec.Source == nil || updated.Spec.Source.Kind != "Operator" {
		t.Fatalf("resumed source = %#v", updated.Spec.Source)
	}
	if !strings.Contains(output.String(), "no active Paused AgentRunControls") {
		t.Fatalf("resume output = %q", output.String())
	}
}

func TestControlResumeSkipsExpiredAndAllControls(t *testing.T) {
	expired := metav1.NewTime(time.Now().Add(-time.Hour))
	active := metav1.NewTime(time.Now().Add(time.Hour))
	backend := &fakeBackend{
		defaultNamespace: "default",
		controls: []agentsv1alpha1.AgentRunControl{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "hazy-trade-expired"},
				Spec: agentsv1alpha1.AgentRunControlSpec{
					ApplicationRef: agentsv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
					LaunchPolicy:   agentsv1alpha1.AgentRunControlLaunchPolicyPaused,
					Reason:         "expired hold",
					ExpiresAt:      &expired,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "hazy-trade-safety"},
				Spec: agentsv1alpha1.AgentRunControlSpec{
					ApplicationRef: agentsv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
					LaunchPolicy:   agentsv1alpha1.AgentRunControlLaunchPolicyPaused,
					Reason:         "security hold",
					ExpiresAt:      &active,
				},
			},
		},
	}
	app, _, _ := fakeControlApp(backend)
	if err := app.Run(context.Background(), []string{
		"control", "resume",
		"--application", "hazy-trade",
		"--all-controls",
		"--reason", "verified broad resume",
	}); err != nil {
		t.Fatalf("control resume --all-controls: %v", err)
	}
	if len(backend.updatedControls) != 1 {
		t.Fatalf("updated controls = %d, want only the active hold", len(backend.updatedControls))
	}
	if backend.updatedControls[0].Name != "hazy-trade-safety" {
		t.Fatalf("updated control = %q, want hazy-trade-safety", backend.updatedControls[0].Name)
	}
}

func TestControlResumeNoActivePauseReportsWithoutError(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "default"}
	app, output, _ := fakeControlApp(backend)
	if err := app.Run(context.Background(), []string{
		"control", "resume",
		"--application", "hazy-trade",
		"--reason", "verified resume",
	}); err != nil {
		t.Fatalf("control resume with no pause: %v", err)
	}
	if !strings.Contains(output.String(), "has no active Paused AgentRunControls") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestControlPauseRequiresApplication(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "default"}
	app, _, _ := fakeControlApp(backend)
	err := app.Run(context.Background(), []string{"control", "pause", "--reason", "hold"})
	if err == nil || !strings.Contains(err.Error(), "--application is required") {
		t.Fatalf("error = %v, want missing application", err)
	}
}

func TestControlUnknownSubcommand(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "default"}
	app, _, _ := fakeControlApp(backend)
	err := app.Run(context.Background(), []string{"control", "frobnicate"})
	if err == nil || !strings.Contains(err.Error(), "unknown control command") {
		t.Fatalf("error = %v, want unknown control command", err)
	}
}
