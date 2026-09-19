package runapi

import (
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestChatAgentRunCreateRejectionMessageIncludesStatus(t *testing.T) {
	t.Parallel()

	err := apierrors.NewForbidden(schema.GroupResource{Group: "control.anvil.hazyforge.io", Resource: "agentruns"}, "chat-turn-test", fmt.Errorf("admission webhook denied the request: example policy"))
	got := chatAgentRunCreateRejectionMessage(err)
	if !strings.HasPrefix(got, "AgentRun creation was rejected: ") {
		t.Fatalf("prefix = %q", got)
	}
	if !strings.Contains(got, "admission webhook denied the request") {
		t.Fatalf("detail missing from %q", got)
	}

	invalid := apierrors.NewInvalid(schema.GroupKind{Group: "control.anvil.hazyforge.io", Kind: "AgentRun"}, "chat-turn-test", nil)
	got = chatAgentRunCreateRejectionMessage(invalid)
	if !strings.Contains(got, "AgentRun creation was rejected:") {
		t.Fatalf("invalid message = %q", got)
	}

	long := apierrors.NewForbidden(schema.GroupResource{Group: "control.anvil.hazyforge.io", Resource: "agentruns"}, "chat-turn-test", fmt.Errorf("%s", strings.Repeat("x", 600)))
	got = chatAgentRunCreateRejectionMessage(long)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis truncation, got %q", got)
	}
	if len(got) > len("AgentRun creation was rejected: ")+512+3 {
		t.Fatalf("message not truncated: len=%d value=%q", len(got), got)
	}
}
