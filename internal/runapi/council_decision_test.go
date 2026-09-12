package runapi

import "testing"

func TestParseCouncilHarnessDecisionFromPrefixAndFences(t *testing.T) {
	t.Parallel()
	raw := "progress\nANVIL_COUNCIL_DECISION=```json\n{\"mode\":\"controller\",\"anvil\":\"I will split the work.\"}\n```\n"
	decision, err := parseCouncilHarnessDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Anvil != "I will split the work." {
		t.Fatalf("anvil = %q", decision.Anvil)
	}
}

func TestParseCouncilHarnessDecisionRejectsMissingJSON(t *testing.T) {
	t.Parallel()
	if _, err := parseCouncilHarnessDecision("the harness printed nothing useful"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestNormalizeMemberDecisionKeepsSpeakerAndPeerMessages(t *testing.T) {
	t.Parallel()
	state := CouncilState{Members: defaultCouncilMembers()}
	decision, err := normalizeCouncilDecision(state, councilResearcherProfile, councilHarnessDecision{
		Mode:    "member",
		Speaker: councilResearcherProfile,
		Reply:   "I will map the namespace.",
		Messages: []councilHarnessPeerMsg{
			{To: anvilAgentProfileName, Content: "Anvil, I claimed inventory."},
			{To: councilImplementerProfile, Content: "Wait on my map."},
			{To: "unknown-agent", Content: "should drop"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Utterances) != 3 {
		t.Fatalf("utterances = %#v", decision.Utterances)
	}
	if decision.Utterances[0].AddressedTo != "user" || decision.Utterances[1].AddressedTo != anvilAgentProfileName {
		t.Fatalf("addressing = %#v", decision.Utterances)
	}
	if decision.Utterances[1].Profile != councilResearcherProfile {
		t.Fatalf("member must keep its own voice: %#v", decision.Utterances[1])
	}
	if len(decision.Delegates) != 0 {
		t.Fatalf("member turns must not invent delegates: %#v", decision.Delegates)
	}
}

func TestNormalizeControllerDecisionInterruptsDuplicateClaims(t *testing.T) {
	t.Parallel()
	state := CouncilState{Members: defaultCouncilMembers()}
	decision, err := normalizeCouncilDecision(state, anvilAgentProfileName, councilHarnessDecision{
		Anvil: "Researcher inventories; Implementer waits.",
		Utterances: []councilHarnessUtterance{
			{Profile: councilResearcherProfile, Content: "Taking inventory.", Kind: "utterance"},
			{Profile: councilImplementerProfile, Content: "I was about to inventory too.", Kind: "utterance"},
		},
		Delegates: []councilHarnessDelegate{
			{Profile: councilResearcherProfile, Role: "researcher", Claim: "inventory", Prompt: "map it", Harness: councilResearchHarness},
			{Profile: councilImplementerProfile, Role: "implementer", Claim: "inventory", Prompt: "also map", Harness: councilGrokHarness},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Delegates) != 2 || !decision.Delegates[1].Skip {
		t.Fatalf("expected second duplicate claim skipped: %#v", decision.Delegates)
	}
	if decision.Utterances[len(decision.Utterances)-1].Kind != "interrupt" {
		t.Fatalf("implementer should be interrupted: %#v", decision.Utterances)
	}
}
