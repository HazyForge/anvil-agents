package controller

import (
	"context"
	"errors"
	"fmt"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type agentRunScopeConflict struct {
	direct, profile string
}

func (e *agentRunScopeConflict) Error() string {
	return fmt.Sprintf("run scope applicationRef %q conflicts with AgentRunProfile applicationRef %q", e.direct, e.profile)
}

// An invalid neighboring run must not block unrelated applications. Retain a
// conservative concurrency reservation for BOTH of its conflicting scopes;
// resolving the run's own launch scope still rejects the conflict.
func agentRunMayUseApplication(ctx context.Context, reader client.Reader, run *controlv1alpha1.AgentRun, application string) (bool, error) {
	name, err := resolveAgentRunApplicationName(ctx, reader, run)
	if err == nil {
		return name == application, nil
	}
	var conflict *agentRunScopeConflict
	if errors.As(err, &conflict) {
		return conflict.direct == application || conflict.profile == application, nil
	}
	return false, err
}
