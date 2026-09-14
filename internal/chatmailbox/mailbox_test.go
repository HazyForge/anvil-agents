package chatmailbox

import "testing"

func TestMayReceiveMailbox(t *testing.T) {
	t.Parallel()
	for _, purpose := range []string{"", "manual", "adverseSituation", "scheduledHealthCheck", "chained"} {
		if MayReceiveMailbox(purpose) {
			t.Fatalf("%q must not participate in the chat mailbox", purpose)
		}
	}
	if !MayReceiveMailbox(PurposeInteractive) {
		t.Fatal("interactive must participate in the chat mailbox")
	}
}

func TestInboxIsAddressedNotADump(t *testing.T) {
	t.Parallel()
	if !InInbox(AudienceAll, "", "implementer") {
		t.Fatal("audience=all must reach every summoned member")
	}
	if !InInbox(AudienceMember, "implementer", "implementer") {
		t.Fatal("direct mail to self must be in inbox")
	}
	if InInbox(AudienceMember, "implementer", "auditor") {
		t.Fatal("direct mail to another member must not appear in this inbox")
	}
	if InInbox(AudienceMember, "implementer", "") {
		t.Fatal("empty self profile has no inbox")
	}
}

func TestHumanAppendSpoofRules(t *testing.T) {
	t.Parallel()
	if got := AcceptHumanAppend(AuthorHuman, RoleUser, KindUtterance); got != "" {
		t.Fatalf("utterance: %s", got)
	}
	if got := AcceptHumanAppend(AuthorHuman, RoleUser, KindInterrupt); got != "" {
		t.Fatalf("interrupt: %s", got)
	}
	if AcceptHumanAppend(AuthorMember, RoleUser, KindUtterance) == "" {
		t.Fatal("member author must be rejected on human HTTP")
	}
	if AcceptHumanAppend(AuthorHuman, "assistant", KindUtterance) == "" {
		t.Fatal("assistant role must be rejected on human HTTP")
	}
	if AcceptHumanAppend(AuthorHuman, RoleUser, KindStatus) == "" {
		t.Fatal("status kind must be rejected on human HTTP")
	}
}

func TestParkDoesNotSpawnAndDoesNotInventStop(t *testing.T) {
	t.Parallel()
	manual := Park(Delivery{Purpose: "manual", Kind: KindUtterance})
	if manual.Action != ActionReject || manual.SpawnAgentRun {
		t.Fatalf("manual park = %#v", manual)
	}
	steer := Park(Delivery{Purpose: PurposeInteractive, Kind: KindUtterance, HasActiveSession: true})
	if steer.Action != ActionStore || steer.SpawnAgentRun || steer.InterruptGeneration {
		t.Fatalf("steer = %#v", steer)
	}
	idle := Park(Delivery{Purpose: PurposeInteractive, Kind: KindUtterance})
	if idle.Action != ActionStore || idle.SpawnAgentRun {
		t.Fatalf("idle utterance must store, not spawn: %#v", idle)
	}
	stop := Park(Delivery{Purpose: PurposeInteractive, Kind: KindInterrupt, HasActiveSession: true, AdapterCanInterrupt: false})
	if stop.InterruptGeneration || stop.Action != ActionStore {
		t.Fatalf("no adapter hook must not claim Stop: %#v", stop)
	}
	hook := Park(Delivery{Purpose: PurposeInteractive, Kind: KindInterrupt, HasActiveSession: true, AdapterCanInterrupt: true})
	if !hook.InterruptGeneration || hook.SpawnAgentRun {
		t.Fatalf("adapter hook may stop generation only: %#v", hook)
	}
}

func TestApplicationKeyIsolation(t *testing.T) {
	t.Parallel()
	if got, want := ApplicationKey("hazy-trade", "release-council"), "chat:hazy-trade/release-council"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
	if IsApplicationKey("hazy-trade") {
		t.Fatal("production key must not be classified as chat")
	}
	if !IsApplicationKey("chat:hazy-trade/release-council") {
		t.Fatal("chat key must be classified as chat")
	}
	if SameFlight("", "agents/t/p") {
		t.Fatal("empty flight key must not match")
	}
	if !SameFlight(FlightKey("agents", "t", "p"), "agents/t/p") {
		t.Fatal("same member must share a flight")
	}
}

func TestValidateRunFireAndForget(t *testing.T) {
	t.Parallel()
	if reason, _ := ValidateRun(RunView{Purpose: "manual"}); reason != "" {
		t.Fatalf("plain manual: %s", reason)
	}
	if reason, _ := ValidateRun(RunView{Purpose: "scheduledHealthCheck", Labels: map[string]string{ThreadLabel: "t"}}); reason != ReasonMailboxOnFireAndForget {
		t.Fatalf("labeled schedule: %s", reason)
	}
	if reason, _ := ValidateRun(RunView{Purpose: "chained", ApplicationName: "chat:agents/release-council"}); reason != ReasonChatApplicationReserved {
		t.Fatalf("chained chat key: %s", reason)
	}
}

func TestValidateRunInteractiveIdentity(t *testing.T) {
	t.Parallel()
	if reason, _ := ValidateRun(RunView{Purpose: PurposeInteractive, Namespace: "agents"}); reason != ReasonChatIdentityIncomplete {
		t.Fatalf("missing labels: %s", reason)
	}
	if reason, _ := ValidateRun(RunView{
		Purpose:   PurposeInteractive,
		Namespace: "agents",
		Labels:    map[string]string{ThreadLabel: "t", SessionLabel: "s", CouncilRoleLabel: "implementer"},
	}); reason != ReasonChatRoleWithoutCouncil {
		t.Fatalf("role without council: %s", reason)
	}
	if reason, _ := ValidateRun(RunView{
		Purpose:         PurposeInteractive,
		Namespace:       "agents",
		ApplicationName: "hazy-trade",
		Labels:          map[string]string{ThreadLabel: "t", SessionLabel: "s"},
	}); reason != ReasonChatApplicationRequired {
		t.Fatalf("production key: %s", reason)
	}
	if reason, _ := ValidateRun(RunView{
		Purpose:         PurposeInteractive,
		Namespace:       "agents",
		ApplicationName: "chat:agents/other",
		Labels:          map[string]string{ThreadLabel: "t", SessionLabel: "s", CouncilLabel: "release-council"},
	}); reason != ReasonChatApplicationRequired {
		t.Fatalf("wrong council key: %s", reason)
	}
	if reason, msg := ValidateRun(RunView{
		Purpose:         PurposeInteractive,
		Namespace:       "agents",
		ApplicationName: ApplicationKey("agents", "release-council"),
		Labels: map[string]string{
			ThreadLabel:      "t",
			SessionLabel:     "s",
			CouncilLabel:     "release-council",
			CouncilRoleLabel: "implementer",
		},
	}); reason != "" {
		t.Fatalf("valid interactive: %s %s", reason, msg)
	}
}

func TestValidateExtraEnv(t *testing.T) {
	t.Parallel()
	if reason, _ := ValidateExtraEnv([]string{"CUSTOM_SETTING"}); reason != "" {
		t.Fatalf("custom: %s", reason)
	}
	if reason, _ := ValidateExtraEnv([]string{EnvAgentRunUID}); reason != ReasonReservedIdentityEnv {
		t.Fatalf("uid spoof: %s", reason)
	}
	if !ReservedIdentityEnv(EnvChatThread) || ReservedIdentityEnv("CUSTOM_SETTING") {
		t.Fatal("reserved set mismatch")
	}
}
