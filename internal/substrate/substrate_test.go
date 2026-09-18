package substrate

import (
	"context"
	"errors"
	"testing"
)

func TestValidateSpec(t *testing.T) {
	t.Parallel()

	if err := ValidateSpec(ActorSpec{Namespace: "agents", Name: "chat-thread-1"}); err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	for _, tc := range []struct {
		name string
		spec ActorSpec
	}{
		{"missing namespace", ActorSpec{Name: "a"}},
		{"missing name", ActorSpec{Namespace: "agents"}},
		{"whitespace class", ActorSpec{Namespace: "agents", Name: "a", ActorClass: " warm pool "}},
		{"whitespace pool", ActorSpec{Namespace: "agents", Name: "a", Pool: "warm pool"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateSpec(tc.spec); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestFakeClientLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := NewFakeClient()
	created, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "thread-1", HarnessKind: "openCode", ActorClass: "standing-chat"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.State != ActorStateActive || created.ID == "" {
		t.Fatalf("created handle = %+v, want active with an ID", created)
	}

	suspended, err := client.SuspendActor(ctx, "agents", "thread-1")
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if suspended.State != ActorStateSuspended {
		t.Fatalf("suspended state = %q, want Suspended", suspended.State)
	}

	resumed, err := client.ResumeActor(ctx, "agents", "thread-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.State != ActorStateActive || resumed.Resumes != 1 {
		t.Fatalf("resumed handle = %+v, want active with one resume", resumed)
	}

	paused, err := client.PauseActor(ctx, "agents", "thread-1")
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if paused.State != ActorStatePaused {
		t.Fatalf("paused state = %q, want Paused", paused.State)
	}

	described, err := client.DescribeActor(ctx, "agents", "thread-1")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if described.State != ActorStatePaused || described.ID != created.ID {
		t.Fatalf("described handle = %+v, want paused with stable ID", described)
	}
}

func TestFakeClientWarmReuse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := NewFakeClient()
	first, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "thread-1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := client.ResumeActor(ctx, "agents", "thread-1"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	second, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "thread-1"})
	if err != nil {
		t.Fatalf("re-create: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("re-create ID = %q, want warm reuse of %q", second.ID, first.ID)
	}
	if second.Resumes != 1 {
		t.Fatalf("re-create resumes = %d, want warm resume count preserved", second.Resumes)
	}
	if got := client.Created(); got != 1 {
		t.Fatalf("distinct actors = %d, want 1 warm actor", got)
	}
}

func TestFakeClientNamespaceIsolationAndNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := NewFakeClient()
	if _, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "thread-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := client.ResumeActor(ctx, "other", "thread-1"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("cross-namespace resume err = %v, want ErrActorNotFound", err)
	}
	if _, err := client.SuspendActor(ctx, "agents", "missing"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("missing suspend err = %v, want ErrActorNotFound", err)
	}
	if _, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents"}); err == nil {
		t.Fatal("expected validation error for missing actor name")
	}
}
