package jev

import (
	"context"
	"encoding/json"
	"testing"
)

// cannedChoice returns a Backend that always answers the intent question
// with the given choice/confidence — the FakeBackend-style routing the
// spike's unit coverage is built on (no network anywhere).
func cannedChoice(t *testing.T, choice string, confidence float64) Backend {
	t.Helper()
	probs := map[string]float64{
		IntentChatReply:          0.01,
		IntentCreateAgentRequest: 0.01,
		IntentPeerHandoff:        0.01,
		IntentToolRun:            0.01,
		IntentUnclear:            0.01,
	}
	probs[choice] = 0.96
	return &FakeBackend{
		Answers: map[string]json.RawMessage{
			IntentQuestionID: MustAnswer(t, ChoiceAnswer{
				Type:          TypeChoice,
				Choice:        choice,
				Probabilities: probs,
				Confidence:    confidence,
			}),
		},
	}
}

func TestIntentRouterDecidesEachIntent(t *testing.T) {
	cases := []struct {
		name        string
		choice      string
		confidence  float64
		want        string
		wantUnclear bool
	}{
		{name: "chat reply", choice: IntentChatReply, confidence: 0.9, want: IntentChatReply},
		{name: "create agent request", choice: IntentCreateAgentRequest, confidence: 0.92, want: IntentCreateAgentRequest},
		{name: "peer handoff", choice: IntentPeerHandoff, confidence: 0.88, want: IntentPeerHandoff},
		{name: "tool run", choice: IntentToolRun, confidence: 0.85, want: IntentToolRun},
		{name: "unclear choice", choice: IntentUnclear, confidence: 0.9, want: IntentUnclear, wantUnclear: true},
		{name: "low confidence gates to unclear", choice: IntentToolRun, confidence: 0.2, want: IntentUnclear, wantUnclear: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := &Router{Backend: cannedChoice(t, tc.choice, tc.confidence)}
			decision, err := router.ClassifyIntent(context.Background(), MessageContext{Message: "please look at this"})
			if err != nil {
				t.Fatalf("ClassifyIntent: %v", err)
			}
			if decision.Intent != tc.want {
				t.Fatalf("intent = %q, want %q (raw %q conf %.2f)", decision.Intent, tc.want, decision.RawChoice, decision.Confidence)
			}
			if decision.Unclear != tc.wantUnclear {
				t.Fatalf("unclear = %v, want %v", decision.Unclear, tc.wantUnclear)
			}
			if decision.Model != DefaultModel {
				t.Fatalf("model = %q, want %q", decision.Model, DefaultModel)
			}
		})
	}
}

func TestIntentRouterRejectsUnknownChoice(t *testing.T) {
	router := &Router{Backend: cannedChoice(t, "escalate_to_sms", 0.99)}
	decision, err := router.ClassifyIntent(context.Background(), MessageContext{Message: "do something else"})
	if err != nil {
		t.Fatalf("ClassifyIntent: %v", err)
	}
	if decision.Intent != IntentUnclear || !decision.Unclear {
		t.Fatalf("unknown choice must gate to unclear, got %+v", decision)
	}
}

func TestIntentRouterThresholdOverride(t *testing.T) {
	router := &Router{Backend: cannedChoice(t, IntentChatReply, 0.8), Threshold: 0.9}
	decision, err := router.ClassifyIntent(context.Background(), MessageContext{Message: "hello"})
	if err != nil {
		t.Fatalf("ClassifyIntent: %v", err)
	}
	if decision.Intent != IntentUnclear {
		t.Fatalf("0.8 confidence under a 0.9 floor must gate to unclear, got %+v", decision)
	}
}

func TestIntentRouterRequiresMessageAndBackend(t *testing.T) {
	router := &Router{Backend: cannedChoice(t, IntentChatReply, 0.9)}
	if _, err := router.ClassifyIntent(context.Background(), MessageContext{}); err == nil {
		t.Fatal("empty message should fail")
	}
	bare := &Router{}
	if _, err := bare.ClassifyIntent(context.Background(), MessageContext{Message: "hi"}); err == nil {
		t.Fatal("missing backend should fail")
	}
}

func TestIntentRequestShape(t *testing.T) {
	req, err := IntentRequest("", MessageContext{Message: "hello", Recent: []string{"prior turn"}})
	if err != nil {
		t.Fatalf("IntentRequest: %v", err)
	}
	if req.Model != DefaultModel {
		t.Fatalf("model = %q, want default", req.Model)
	}
	question, ok := req.Questions[IntentQuestionID]
	if !ok {
		t.Fatal("request must carry the intent question")
	}
	if question.Type != TypeChoice {
		t.Fatalf("intent question type = %q, want choice", question.Type)
	}
	criteria, ok := question.Criteria.(map[string]*string)
	if !ok {
		t.Fatalf("intent criteria type = %T, want map", question.Criteria)
	}
	for _, want := range []string{IntentChatReply, IntentCreateAgentRequest, IntentPeerHandoff, IntentToolRun, IntentUnclear} {
		if _, ok := criteria[want]; !ok {
			t.Fatalf("criteria missing option %q", want)
		}
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		State     map[string]any `json:"state"`
		Questions map[string]any `json:"questions"`
		Model     string         `json:"model"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.State["message"] != "hello" {
		t.Fatalf("state.message = %v", decoded.State["message"])
	}
}

func TestKeywordFakeBackendRoutes(t *testing.T) {
	router := &Router{Backend: KeywordFakeBackend()}
	cases := []struct{ message, want string }{
		{"hello, how are you?", IntentChatReply},
		{"please create a helper agent for triage", IntentCreateAgentRequest},
		{"delegate this to the reviewer peer", IntentPeerHandoff},
		{"run the deploy tool now", IntentToolRun},
		{"", IntentUnclear},
	}
	for _, tc := range cases {
		decision, err := router.ClassifyIntent(context.Background(), MessageContext{Message: tc.message})
		if tc.message == "" {
			if err == nil {
				t.Fatal("empty message should fail even on the fake")
			}
			continue
		}
		if err != nil {
			t.Fatalf("message %q: %v", tc.message, err)
		}
		if decision.Intent != tc.want {
			t.Fatalf("message %q: intent = %q, want %q", tc.message, decision.Intent, tc.want)
		}
	}
}

func TestDestructiveBarIsStricterThanClassifyFloor(t *testing.T) {
	if DestructiveActionConfidenceBar <= DefaultConfidenceThreshold {
		t.Fatalf("DestructiveActionConfidenceBar = %v, want > DefaultConfidenceThreshold %v", DestructiveActionConfidenceBar, DefaultConfidenceThreshold)
	}
}

func TestDecisionMeetsDestructiveActionBar(t *testing.T) {
	cases := []struct {
		confidence float64
		want       bool
	}{
		{confidence: 0.0, want: false},
		{confidence: DefaultConfidenceThreshold, want: false},
		{confidence: 0.79, want: false},
		{confidence: DestructiveActionConfidenceBar, want: true},
		{confidence: 0.92, want: true},
		{confidence: 1.0, want: true},
	}
	for _, tc := range cases {
		got := Decision{Confidence: tc.confidence}.MeetsDestructiveActionBar()
		if got != tc.want {
			t.Fatalf("confidence %v: MeetsDestructiveActionBar = %v, want %v", tc.confidence, got, tc.want)
		}
	}
}

// TestIntentConfidenceFloorLabeledFixtures pins classify-floor behavior
// against labeled (message, canned choice, confidence) traffic. FakeBackend
// only — no network. High-confidence rows keep the class; near-floor and
// ambiguous/truncated rows gate to unclear while RawChoice stays on the
// labeled model output for observability.
func TestIntentConfidenceFloorLabeledFixtures(t *testing.T) {
	type fixture struct {
		name        string
		message     string
		recent      []string
		choice      string
		confidence  float64
		wantIntent  string
		wantUnclear bool
	}
	fixtures := []fixture{
		{
			name:       "high-confidence keep-class: create scout",
			message:    "create an agent named Scout for research",
			choice:     IntentCreateAgentRequest,
			confidence: 1.0,
			wantIntent: IntentCreateAgentRequest,
		},
		{
			name:       "high-confidence keep-class: chat reply",
			message:    "hello, how are you?",
			choice:     IntentChatReply,
			confidence: 0.9,
			wantIntent: IntentChatReply,
		},
		{
			name:       "high-confidence keep-class: peer handoff",
			message:    "delegate this to the reviewer peer",
			choice:     IntentPeerHandoff,
			confidence: 0.88,
			wantIntent: IntentPeerHandoff,
		},
		{
			name:       "high-confidence keep-class: tool run",
			message:    "look up the kb article on refunds",
			choice:     IntentToolRun,
			confidence: 0.85,
			wantIntent: IntentToolRun,
		},
		{
			name:       "at classify floor keeps class",
			message:    "please create a helper agent for triage",
			choice:     IntentCreateAgentRequest,
			confidence: DefaultConfidenceThreshold,
			wantIntent: IntentCreateAgentRequest,
		},
		{
			name:        "near-floor gate to unclear",
			message:     "please create a helper agent for triage",
			choice:      IntentCreateAgentRequest,
			confidence:  0.49,
			wantIntent:  IntentUnclear,
			wantUnclear: true,
		},
		{
			name:        "truncated create gates to unclear",
			message:     "create",
			choice:      IntentCreateAgentRequest,
			confidence:  0.42,
			wantIntent:  IntentUnclear,
			wantUnclear: true,
		},
		{
			name:        "truncated handoff gates to unclear",
			message:     "hand off",
			choice:      IntentPeerHandoff,
			confidence:  0.42,
			wantIntent:  IntentUnclear,
			wantUnclear: true,
		},
		{
			name:        "truncated tool run gates to unclear",
			message:     "run it",
			choice:      IntentToolRun,
			confidence:  0.3,
			wantIntent:  IntentUnclear,
			wantUnclear: true,
		},
		{
			name:        "ambiguous unclear choice stays unclear",
			message:     "do it",
			recent:      []string{"should we create a scout or hand this to the reviewer?"},
			choice:      IntentUnclear,
			confidence:  0.7,
			wantIntent:  IntentUnclear,
			wantUnclear: true,
		},
		{
			name:        "ambiguous mid-sentence truncated gates to unclear",
			message:     "maybe create something?",
			choice:      IntentCreateAgentRequest,
			confidence:  0.48,
			wantIntent:  IntentUnclear,
			wantUnclear: true,
		},
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			router := &Router{Backend: cannedChoice(t, fx.choice, fx.confidence)}
			decision, err := router.ClassifyIntent(context.Background(), MessageContext{Message: fx.message, Recent: fx.recent})
			if err != nil {
				t.Fatalf("ClassifyIntent: %v", err)
			}
			if decision.RawChoice != fx.choice {
				t.Fatalf("rawChoice = %q, want labeled %q", decision.RawChoice, fx.choice)
			}
			if decision.Confidence != fx.confidence {
				t.Fatalf("confidence = %v, want labeled %v", decision.Confidence, fx.confidence)
			}
			if decision.Intent != fx.wantIntent {
				t.Fatalf("intent = %q, want %q (raw %q conf %.2f)", decision.Intent, fx.wantIntent, decision.RawChoice, decision.Confidence)
			}
			if decision.Unclear != fx.wantUnclear {
				t.Fatalf("unclear = %v, want %v", decision.Unclear, fx.wantUnclear)
			}
		})
	}
}
