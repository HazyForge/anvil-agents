package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestProfileMayCreateAgentAllowlist(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		profile *controlv1alpha1.AgentRunProfile
		want    bool
	}{
		{name: "nil", profile: nil, want: false},
		{name: "wrapper", profile: &controlv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "anvil-desktop-wrapper"}}, want: true},
		{name: "desktop-manager", profile: &controlv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "desktop-manager"}}, want: true},
		{name: "hazy-trade-agent-manager", profile: &controlv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "hazy-trade-agent-manager"}}, want: true},
		{name: "suffix", profile: &controlv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "payments-agent-manager"}}, want: true},
		{name: "role-manager", profile: &controlv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "coord", Labels: map[string]string{createAgentRoleLabel: "manager"}}}, want: true},
		{name: "allow-label", profile: &controlv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "special", Labels: map[string]string{createAgentAllowLabel: "allow"}}}, want: true},
		{name: "peer", profile: &controlv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "scout"}}, want: false},
		{name: "chat-role-is-not-authority", profile: &controlv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "reviewer", Labels: map[string]string{"control.anvil.hazyforge.io/chat-role": "project-manager"}}}, want: false},
		{name: "name-contains-manager", profile: &controlv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "release-manager"}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := profileMayCreateAgent(tc.profile); got != tc.want {
				t.Fatalf("profileMayCreateAgent(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestAgentRunCompositionBakesCreateAgentForManagerOnly(t *testing.T) {
	t.Parallel()

	harness := testAgentHarnessProfile("runtime", controlv1alpha1.AgentRunHarnessBackendCodex, "creds")
	manager := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "desktop-manager", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentRunProfileSpec{HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: harness.Name}},
	}
	peer := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "scout", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: harness.Name},
			SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{
				Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "stolen-create"}},
			},
		},
	}
	stolen := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "stolen-create", Namespace: "agents", UID: "stolen-uid", Generation: 1},
		Spec: controlv1alpha1.AgentSkillSetSpec{
			Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{{
				Name:    createAgentSkillName,
				Content: "Peers must not create AgentRunProfiles.",
			}},
		},
	}

	managerRun := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "mgr-1", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentRunSpec{ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: manager.Name}},
	}
	peerRun := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "peer-1", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentRunSpec{ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: peer.Name}},
	}

	reconciler := testCompositionReconciler(t, harness, manager, peer, stolen)
	effective, _, phase, reason, message, err := reconciler.resolveAgentRunComposition(context.Background(), managerRun)
	if err != nil || phase != "" {
		t.Fatalf("manager resolve: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	found := false
	for _, skill := range effective.Spec.Harness.SkillInjections {
		if skill.Name == createAgentSkillName {
			found = true
			if skill.Content != createAgentSkillContent {
				t.Fatalf("baked create-agent content = %q", skill.Content)
			}
		}
	}
	if !found {
		t.Fatalf("manager missing create-agent skill: %#v", effective.Spec.Harness.SkillInjections)
	}

	peerEffective, _, phase, reason, message, err := reconciler.resolveAgentRunComposition(context.Background(), peerRun)
	if err != nil || phase != "" {
		t.Fatalf("peer resolve: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	for _, skill := range peerEffective.Spec.Harness.SkillInjections {
		if skill.Name == createAgentSkillName {
			t.Fatalf("peer kept create-agent skill: %#v", peerEffective.Spec.Harness.SkillInjections)
		}
	}
}
