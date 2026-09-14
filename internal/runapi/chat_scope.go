package runapi

import (
	"context"
	"fmt"
	"strings"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"k8s.io/apimachinery/pkg/types"
)

// resolveChatApplication preserves profile scope. A standalone harness has no
// profile scope, so its explicitly attached scoped homes may supply one opaque
// application key, only when every scoped home agrees. It never invents a key
// from a namespace, provider, harness name or user-supplied metadata.
func (s *Server) resolveChatApplication(ctx context.Context, thread chat.Thread) (string, error) {
	application := ""
	harness := chatHarness(thread)
	refs := []agents.AgentRunDataVolumeRef{}
	if thread.ProfileName != "" {
		profile := &agents.AgentRunProfile{}
		if err := s.runs.Get(ctx, types.NamespacedName{Namespace: thread.Namespace, Name: thread.ProfileName}, profile); err != nil {
			return "", fmt.Errorf("profile is unavailable in this namespace")
		}
		if profile.Spec.Scope.ApplicationRef != nil {
			application = strings.TrimSpace(profile.Spec.Scope.ApplicationRef.Name)
		}
		refs = append(refs, profile.Spec.Harness.Execution.DataVolumeRefs...)
		if harness == "" && profile.Spec.HarnessProfileRef != nil {
			harness = profile.Spec.HarnessProfileRef.Name
		}
	}
	if harness != "" {
		selected := &agents.AgentHarnessProfile{}
		if err := s.runs.Get(ctx, types.NamespacedName{Namespace: thread.Namespace, Name: harness}, selected); err != nil {
			return "", fmt.Errorf("harness profile is unavailable in this namespace")
		}
		refs = append(refs, selected.Spec.Execution.DataVolumeRefs...)
	}
	scopedHome := ""
	for _, ref := range refs {
		if ref.Namespace != "" && ref.Namespace != thread.Namespace {
			return "", fmt.Errorf("chat data volumes must be in the selected namespace")
		}
		volume := &agents.AgentDataVolume{}
		if err := s.runs.Get(ctx, types.NamespacedName{Namespace: thread.Namespace, Name: ref.Name}, volume); err != nil {
			return "", fmt.Errorf("data volume %q is unavailable in this namespace", ref.Name)
		}
		if volume.Spec.ApplicationRef == nil || strings.TrimSpace(volume.Spec.ApplicationRef.Name) == "" {
			continue
		}
		scope := strings.TrimSpace(volume.Spec.ApplicationRef.Name)
		if scopedHome != "" && scopedHome != scope {
			return "", fmt.Errorf("selected harness data volumes have conflicting application scopes")
		}
		scopedHome = scope
	}
	if thread.ProfileName != "" {
		if scopedHome != "" && scopedHome != application {
			return "", fmt.Errorf("agent profile application scope does not match its scoped data volumes")
		}
		return application, nil
	}
	return scopedHome, nil
}
