package substrate

import (
	"testing"
)

func TestGenerateEnabledRequiresGateAndExactClass(t *testing.T) {
	t.Parallel()

	if GenerateEnabled(false, []string{DefaultGenerateActorClass}, DefaultGenerateActorClass) {
		t.Fatal("generate gate off must not generate")
	}
	if GenerateEnabled(true, nil, DefaultGenerateActorClass) {
		t.Fatal("empty allowlist must not generate")
	}
	if GenerateEnabled(true, []string{DefaultGenerateActorClass}, "standing-chat") {
		t.Fatal("standing-chat must not generate when only acp-spike is allowed")
	}
	if GenerateEnabled(true, []string{"standing-chat", DefaultGenerateActorClass}, "standing-chat") {
		t.Fatal("standing-chat is never generate-on-actor even if listed")
	}
	if GenerateEnabled(true, []string{DefaultGenerateActorClass}, "") {
		t.Fatal("empty actorClass must not generate")
	}
	if !GenerateEnabled(true, []string{"acp-spike", "other"}, DefaultGenerateActorClass) {
		t.Fatal("matching actorClass must generate")
	}
}

func TestParseActorClassListDedupes(t *testing.T) {
	t.Parallel()

	got := ParseActorClassList(" acp-spike, standing-chat,acp-spike, ")
	if len(got) != 2 || got[0] != "acp-spike" || got[1] != "standing-chat" {
		t.Fatalf("classes = %#v", got)
	}
}
