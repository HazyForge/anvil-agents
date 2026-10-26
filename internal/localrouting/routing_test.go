package localrouting

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
)

// This boundary owns actions and replay evolution. The existing subjectrouting
// tests own probability validation; desktop HTTP tests own transport and real
// discovery. These fixtures supply model evidence, never the expected action.
func TestLocalManagerScenariosReplayPolicyAndHistory(t *testing.T) {
	result, err := RunBench(context.Background(), nil, BenchOptions{Mode: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Totals.Turns == 0 || result.Totals.Passed != result.Totals.Turns || result.Totals.ProviderTurns != 0 || !strings.Contains(result.Provenance, "not model accuracy") {
		for _, scenario := range result.Scenarios {
			for _, turn := range scenario.Turns {
				if !turn.Passed {
					t.Errorf("%s/%s: target=%s intent=%s action=%s, wanted %+v", scenario.ID, turn.ID, turn.ActualTarget, turn.ActualIntent, turn.Result.Action.Kind, turn.Expected)
				}
			}
		}
		t.Fatalf("fixture replay: %+v", result.Totals)
	}
	for _, scenario := range result.Scenarios {
		for _, turn := range scenario.Turns {
			if scenario.ID == "new-and-general" && turn.ID == "followup" {
				if turn.ActualTarget != "opened:new-tool" || len(turn.Snapshot.Conversations) != 2 || !slices.ContainsFunc(turn.Snapshot.Conversations, func(c Conversation) bool { return c.ID == turn.Result.Action.TargetID }) {
					t.Fatal("new conversation was not carried into the next actual routing input")
				}
				if last := turn.Snapshot.Context[len(turn.Snapshot.Context)-1]; last.ConversationID != turn.Result.Action.TargetID {
					t.Fatal("follow-up lost its resolved manager context")
				}
			}
			if scenario.ID == "named-old" && turn.ID == "recall" {
				if len(turn.Snapshot.Conversations) <= subjectrouting.MaxCandidates || len(turn.Result.Candidates) != subjectrouting.MaxCandidates || !slices.ContainsFunc(turn.Result.Candidates, func(c subjectrouting.Candidate) bool { return c.ID == "garden" }) {
					t.Fatal("older named conversation was excluded by the recent window")
				}
			}
		}
	}
}

func TestLocalManagerManualAndRuntimeAdmission(t *testing.T) {
	base, runtime := exampleRequest()
	tests := []struct {
		name           string
		change         func(*Request, *Runtime)
		action, reason string
		invalid        bool
	}{
		{"explicit existing", func(r *Request, _ *Runtime) { r.TargetID = "garden" }, ActionContinue, "continue_saved_conversation", false},
		{"explicit new", func(r *Request, _ *Runtime) { r.TargetID = subjectrouting.NewSubject }, ActionNewConversation, "new_subject", false},
		{"explicit general", func(r *Request, _ *Runtime) { r.TargetID = subjectrouting.NoSubject }, ActionGeneral, "general_conversation", false},
		{"host running", func(r *Request, rt *Runtime) { r.TargetID = "garden"; rt.Busy = true }, ActionWait, "local_turn_unfinished", false},
		{"other unconfirmed", func(r *Request, _ *Runtime) { r.TargetID = "garden"; r.Conversations[1].Status = StatusUnconfirmed }, ActionWait, "local_turn_unfinished", false},
		{"changed target", func(r *Request, rt *Runtime) { r.TargetID = "garden"; rt.Target, rt.WSLDistro = "wsl", "Ubuntu" }, ActionClarify, "runtime_changed", false},
		{"missing harness", func(r *Request, rt *Runtime) { r.TargetID = "garden"; rt.AvailableHarnesses = []string{"opencode"} }, ActionClarify, "harness_unavailable", false},
		{"missing default", func(r *Request, _ *Runtime) { r.TargetID = subjectrouting.NewSubject; r.DefaultHarness = "missing" }, ActionClarify, "choose_available_harness", false},
		{"foreign target", func(r *Request, _ *Runtime) { r.TargetID = "not-in-inventory" }, ActionClarify, "invalid_input", true},
		{"duplicate inventory", func(r *Request, _ *Runtime) { r.TargetID = "garden"; r.Conversations[1].ID = "garden" }, ActionClarify, "invalid_input", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, current := base, runtime
			request.Conversations = append([]Conversation(nil), base.Conversations...)
			test.change(&request, &current)
			provider := providerFunc(func(context.Context, subjectrouting.Input) (subjectrouting.Decision, error) {
				t.Fatal("manual selection called provider")
				return subjectrouting.Decision{}, nil
			})
			before, _ := json.Marshal(request)
			result, err := Route(context.Background(), provider, request, current)
			if (err != nil) != test.invalid || result.Action.Kind != test.action || result.Action.Reason != test.reason {
				t.Fatalf("got %+v, %v", result.Action, err)
			}
			if !test.invalid && (result.Decision.Intent != "unclassified" || result.Decision.Model != "" || result.Decision.SubjectChoice != "") {
				t.Fatal("manual send claimed a model judgment")
			}
			after, _ := json.Marshal(request)
			if string(before) != string(after) {
				t.Fatal("planning mutated the saved conversation descriptors")
			}
		})
	}
}

func TestLocalManagerProviderReceivesOnlyBoundedRoutingContext(t *testing.T) {
	request, runtime := exampleRequest()
	request.Content = "For watering, add a timeout; api_key=sk-proj-abcdefghijklmnopqrstuvwxyz"
	request.Conversations[0].Summary = "private_key=never-share-this"
	request.Conversations[0].Aliases = []string{"watering"}
	old := runtime.Now.Add(-48 * time.Hour)
	request.Conversations[0].TouchedAt = &old
	request.Context = []Message{{Role: "user", Content: "password=never-share-context", ConversationID: "garden", At: &old}}
	called := false
	provider := providerFunc(func(_ context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
		called = true
		encoded, _ := json.Marshal(input)
		for _, secret := range []string{"sk-proj-abcdefghijklmnopqrstuvwxyz", "never-share-this", "never-share-context"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatal("provider received fixture secret")
			}
		}
		if strings.Contains(string(encoded), "wslDistro") || strings.Contains(string(encoded), "harness") || strings.Contains(string(encoded), "target") {
			t.Fatal("runtime binding was disclosed as classification state")
		}
		if len(input.Recent) != 1 || input.Recent[0].SubjectID != "garden" || input.Recent[0].AgeSeconds == nil || *input.Recent[0].AgeSeconds != 48*3600 {
			t.Fatalf("temporal context lost: %+v", input.Recent)
		}
		return canned(input, "garden", subjectrouting.IntentAmendRunning), nil
	})
	result, err := Route(context.Background(), provider, request, runtime)
	if err != nil || !called || result.Action.Kind != ActionContinue {
		t.Fatalf("%+v, %v", result, err)
	}
	provider = providerFunc(func(context.Context, subjectrouting.Input) (subjectrouting.Decision, error) {
		return subjectrouting.Decision{}, errors.New("password=provider-echo")
	})
	result, err = Route(context.Background(), provider, request, runtime)
	if err != subjectrouting.ErrUnavailable || result.Action.Kind != ActionClarify || result.Action.TargetID != "" {
		t.Fatalf("provider error dispatched: %+v, %v", result, err)
	}
}

// Label corrections must be capable of exposing a wrong model choice. A
// benchmark that reads its expected labels to fabricate the provider response
// would incorrectly pass this example. Manual input is intentionally not forced.
func TestFeedbackReplayDoesNotForceCorrectedLabels(t *testing.T) {
	request, runtime := exampleRequest()
	request.TargetID = "garden"
	scenario, err := ImportFeedback(Feedback{ID: "wrong-destination", Request: request, Runtime: runtime, Expected: Expectations{Targets: []string{"garden"}, Intents: []string{subjectrouting.IntentAmendRunning}, Actions: []string{ActionContinue}}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	provider := providerFunc(func(_ context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
		calls++
		return canned(input, "film", subjectrouting.IntentAmendRunning), nil
	})
	result, err := RunBench(context.Background(), provider, BenchOptions{Mode: "live", Scenarios: []Scenario{scenario}})
	if err != nil || calls != 1 {
		t.Fatalf("live replay provider calls=%d, err=%v", calls, err)
	}
	turn := result.Scenarios[0].Turns[0]
	if turn.Passed || turn.TargetPass || turn.ActualTarget != "film" || !turn.ActionPass || turn.JudgmentSource != "provider" || result.Totals.ManualTurns != 0 {
		t.Fatalf("corrected label masked actual wrong choice: %+v", turn)
	}
	if _, err := RunBench(context.Background(), provider, BenchOptions{Mode: "fixture", Scenarios: []Scenario{scenario}}); err != subjectrouting.ErrInvalidInput {
		t.Fatal("imported labels acquired fake fixture output")
	}
}

func TestLiveReplayDoesNotInventOpenedDependencies(t *testing.T) {
	scenarios, err := Scenarios()
	if err != nil {
		t.Fatal(err)
	}
	var scenario Scenario
	for _, value := range scenarios {
		if value.ID == "new-and-general" {
			scenario = value
		}
	}
	calls := 0
	provider := providerFunc(func(_ context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
		calls++
		return canned(input, subjectrouting.NoSubject, subjectrouting.IntentQuestion), nil
	})
	result, err := RunBench(context.Background(), provider, BenchOptions{Mode: "live", Scenarios: []Scenario{scenario}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("dependent turns called provider after failed creation: %d", calls)
	}
	for _, turn := range result.Scenarios[0].Turns[2:] {
		if !turn.Skipped || turn.Passed || turn.Error != "missing_opened_dependency" || len(turn.Snapshot.Conversations) != 1 || turn.Snapshot.Conversations[0].ID != "opened-greeting" {
			t.Fatalf("missing dependency invented work: %+v", turn)
		}
	}
}

func TestReplayRetainsCompletedEvidenceWhenCancelled(t *testing.T) {
	request, runtime := exampleRequest()
	scenario, err := ImportFeedback(Feedback{ID: "interrupted", Request: request, Runtime: runtime, Expected: Expectations{Targets: []string{"garden"}, Intents: []string{subjectrouting.IntentAmendRunning}, Actions: []string{ActionContinue}}})
	if err != nil {
		t.Fatal(err)
	}
	scenario.Turns = append(scenario.Turns, ScenarioTurn{ID: "second", Content: "Then add retries", Expect: scenario.Turns[0].Expect})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	provider := providerFunc(func(_ context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
		calls++
		if calls == 2 {
			cancel()
		}
		return canned(input, "garden", subjectrouting.IntentAmendRunning), nil
	})
	result, err := RunBench(ctx, provider, BenchOptions{Mode: "live", Scenarios: []Scenario{scenario}})
	if err != context.Canceled || result.Complete || result.ErrorReason != "cancelled" || result.Totals.Turns != 1 || len(result.Scenarios) != 1 || len(result.Scenarios[0].Turns) != 1 || !result.Scenarios[0].Turns[0].Passed {
		t.Fatalf("lost partial evidence: %+v, %v", result, err)
	}
}

func TestFullInventoryCannotCreateAnUnrouteableConversation(t *testing.T) {
	scenarios, err := Scenarios()
	if err != nil {
		t.Fatal(err)
	}
	var scenario Scenario
	for _, value := range scenarios {
		if value.ID == "named-old" {
			scenario = value
		}
	}
	scenario.Fillers = MaxConversations - len(scenario.Conversations)
	expect := Expectations{Targets: []string{"unclear"}, Intents: []string{subjectrouting.IntentNewWork}, Actions: []string{ActionClarify}}
	scenario.Turns = []ScenarioTurn{{ID: "one", Content: "Build a new timer app", Expect: expect}, {ID: "two", Content: "Build another new camera app", Expect: expect}}
	calls := 0
	provider := providerFunc(func(_ context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
		calls++
		return canned(input, subjectrouting.NewSubject, subjectrouting.IntentNewWork), nil
	})
	result, err := RunBench(context.Background(), provider, BenchOptions{Mode: "live", Scenarios: []Scenario{scenario}})
	if err != nil || !result.Complete || calls != 2 || result.Totals.Passed != 2 {
		t.Fatalf("capacity replay: %+v, %v, calls=%d", result.Totals, err, calls)
	}
	for _, turn := range result.Scenarios[0].Turns {
		if turn.Result.Action.Reason != "inventory_full" || len(turn.Snapshot.Conversations) != MaxConversations {
			t.Fatal("capacity overflow or silent inventory truncation")
		}
	}
	request := result.Scenarios[0].Turns[0].Snapshot
	request.TargetID = subjectrouting.NoSubject
	manual, err := Route(context.Background(), nil, request, scenario.Runtime)
	if err != nil || manual.Action.Kind != ActionClarify || manual.Action.Reason != "inventory_full" {
		t.Fatalf("manual general bypassed capacity: %+v, %v", manual, err)
	}
	request.TargetID = "garden"
	manual, err = Route(context.Background(), nil, request, scenario.Runtime)
	if err != nil || manual.Action.Kind != ActionContinue {
		t.Fatalf("full inventory blocked existing work: %+v, %v", manual, err)
	}
}

func TestMalformedScenarioDependenciesFailBeforeProvider(t *testing.T) {
	scenarios, err := Scenarios()
	if err != nil {
		t.Fatal(err)
	}
	var base Scenario
	for _, scenario := range scenarios {
		if scenario.ID == "new-and-general" {
			base = scenario
		}
	}
	for _, change := range []func(*Scenario){
		func(s *Scenario) { s.Turns[2].DependsOn = nil },
		func(s *Scenario) { s.Turns[2].DependsOn = []string{"does-not-exist"} },
		func(s *Scenario) { s.Turns[2].TargetID = "opened:does-not-exist" },
		func(s *Scenario) {
			s.Conversations = []Conversation{{ID: "archive-000", Label: "Existing archive", Harness: "codex", Target: "native", Status: StatusReady}}
			s.Fillers = 1
		},
	} {
		scenario := base
		scenario.Turns = append([]ScenarioTurn(nil), base.Turns...)
		change(&scenario)
		provider := providerFunc(func(context.Context, subjectrouting.Input) (subjectrouting.Decision, error) {
			t.Fatal("invalid scenario reached paid provider")
			return subjectrouting.Decision{}, nil
		})
		if _, err := RunBench(context.Background(), provider, BenchOptions{Mode: "live", Scenarios: []Scenario{scenario}}); err != subjectrouting.ErrInvalidInput {
			t.Fatalf("invalid dependency accepted: %v", err)
		}
	}
}

// Imported IDs are valid caller data; they must not collide with IDs generated
// by the replay or make the next otherwise-valid routing request fail.
func TestReplayGeneratedConversationIDsRemainRouteable(t *testing.T) {
	for _, test := range []struct{ name, turnID, seedID string }{
		{"existing generated name", "create", "opened-create"},
		{"maximum turn length", strings.Repeat("a", 128), "existing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, runtime := exampleRequest()
			scenario := Scenario{ID: "generated-identity", Name: "Imported identities", Runtime: runtime, DefaultHarness: "codex", Conversations: []Conversation{{ID: test.seedID, Label: "Existing unrelated work", Harness: "codex", Target: "native", Status: StatusReady}}, Turns: []ScenarioTurn{
				{ID: test.turnID, Content: "Build a portable timer", Expect: Expectations{Targets: []string{subjectrouting.NewSubject}, Intents: []string{subjectrouting.IntentNewWork}, Actions: []string{ActionNewConversation}}},
				{ID: "followup", Content: "Add a large display to it", DependsOn: []string{test.turnID}, Expect: Expectations{Targets: []string{"opened:" + test.turnID}, Intents: []string{subjectrouting.IntentAmendRunning}, Actions: []string{ActionContinue}}},
			}}
			calls := 0
			provider := providerFunc(func(_ context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
				calls++
				if calls == 1 {
					return canned(input, subjectrouting.NewSubject, subjectrouting.IntentNewWork), nil
				}
				for _, candidate := range input.Candidates {
					if candidate.Label == "Build a portable timer" {
						return canned(input, candidate.ID, subjectrouting.IntentAmendRunning), nil
					}
				}
				t.Fatal("created conversation absent from follow-up inventory")
				return subjectrouting.Decision{}, nil
			})
			result, err := RunBench(context.Background(), provider, BenchOptions{Mode: "live", Scenarios: []Scenario{scenario}})
			if err != nil || calls != 2 || result.Totals.Passed != 2 {
				t.Fatalf("generated identity broke follow-up: calls=%d totals=%+v err=%v", calls, result.Totals, err)
			}
			next := result.Scenarios[0].Turns[1]
			if next.Result.Action.TargetID == test.seedID || len(next.Snapshot.Conversations) != 2 {
				t.Fatal("created conversation replaced or reused unrelated seed")
			}
		})
	}
}

func exampleRequest() (Request, Runtime) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	return Request{Content: "Add a timeout to the garden controller", DefaultHarness: "codex", Conversations: []Conversation{
		{ID: "garden", Label: "Garden", Summary: "Irrigation controller", Harness: "codex", Target: "native", Status: StatusReady},
		{ID: "film", Label: "Launch film", Summary: "Edit the launch movie", Harness: "opencode", Target: "native", Status: StatusReady},
	}}, Runtime{Target: "native", AvailableHarnesses: []string{"codex", "opencode"}, Now: now}
}

type providerFunc func(context.Context, subjectrouting.Input) (subjectrouting.Decision, error)

func (f providerFunc) Decide(ctx context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
	return f(ctx, input)
}
func canned(input subjectrouting.Input, subject, intent string) subjectrouting.Decision {
	decision, _ := (fixtureProvider{fixtureJudgment{Subject: subject, Intent: intent}}).Decide(context.Background(), input)
	decision.Model = "test-provider"
	return decision
}
