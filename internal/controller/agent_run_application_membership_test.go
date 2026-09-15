package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestApplicationQueueIsolatesConflictingNeighborScopes(t *testing.T) {
	ctx := context.Background()
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "conflicting-profile", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentRunProfileSpec{Scope: controlv1alpha1.AgentRunScopeSpec{ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "profile-app"}}},
	}
	neighbor := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "older-neighbor", Namespace: "agents", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Minute))},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
			Scope:      controlv1alpha1.AgentRunScopeSpec{ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "direct-app"}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(newAgentControlTestScheme(t)).WithObjects(profile, neighbor).Build()
	r := &AgentRunReconciler{Client: c, CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{ApplicationMaxConcurrentRuns: 1}}}
	for _, application := range []string{"unrelated-app", "direct-app", "profile-app"} {
		t.Run(application, func(t *testing.T) {
			target := &controlv1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: "new-chat", Namespace: "agents", CreationTimestamp: metav1.Now()},
				Spec:       controlv1alpha1.AgentRunSpec{Scope: controlv1alpha1.AgentRunScopeSpec{ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: application}}},
			}
			blocked, err := r.agentRunQueuedBehindApplication(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			if application == "unrelated-app" && blocked != nil {
				t.Fatal("invalid unrelated run blocked valid chat")
			}
			if application != "unrelated-app" && (blocked == nil || blocked.Name != neighbor.Name) {
				t.Fatal("conflicting scope lost its conservative concurrency reservation")
			}
		})
	}
	if _, err := r.agentRunApplicationName(ctx, neighbor); err == nil {
		t.Fatal("invalid run's own launch scope was accepted")
	}
	readFailure := errors.New("profile read unavailable")
	r.Client = interceptor.NewClient(c, interceptor.Funcs{Get: func(ctx context.Context, inner client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*controlv1alpha1.AgentRunProfile); ok {
			return readFailure
		}
		return inner.Get(ctx, key, obj, opts...)
	}})
	if _, err := agentRunMayUseApplication(ctx, r.Client, neighbor, "unrelated-app"); !errors.Is(err, readFailure) {
		t.Fatalf("unknown scope lookup error was hidden: %v", err)
	}
}
