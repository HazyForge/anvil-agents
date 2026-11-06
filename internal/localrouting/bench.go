package localrouting

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
)

//go:embed scenarios.json
var scenarioJSON []byte

//go:embed fixtures.json
var fixtureJSON []byte

func Scenarios() ([]Scenario, error) {
	var scenarios []Scenario
	if json.Unmarshal(scenarioJSON, &scenarios) != nil {
		return nil, subjectrouting.ErrInvalidInput
	}
	if err := ValidateScenarios(scenarios); err != nil {
		return nil, err
	}
	return scenarios, nil
}

// ValidateScenarios bounds imported suites before any paid provider request.
func ValidateScenarios(scenarios []Scenario) error {
	if len(scenarios) == 0 || len(scenarios) > 20 {
		return subjectrouting.ErrInvalidInput
	}
	ids := map[string]bool{}
	totalTurns := 0
	for _, scenario := range scenarios {
		if !identifier.MatchString(scenario.ID) || ids[scenario.ID] || !textWithin(scenario.Name, 120) || !textWithin(scenario.Description, 1000) ||
			!validTarget(scenario.Runtime.Target, scenario.Runtime.WSLDistro) || scenario.Runtime.Now.IsZero() || len(scenario.Runtime.AvailableHarnesses) > 30 ||
			scenario.Fillers < 0 || len(scenario.Conversations)+scenario.Fillers > MaxConversations || len(scenario.Turns) == 0 || len(scenario.Turns) > 20 {
			return subjectrouting.ErrInvalidInput
		}
		ids[scenario.ID] = true
		totalTurns += len(scenario.Turns)
		if totalTurns > 60 {
			return subjectrouting.ErrInvalidInput
		}
		turnIDs := map[string]bool{}
		request := Request{Content: "scenario", Conversations: scenario.Conversations, Context: scenario.Context, DefaultHarness: scenario.DefaultHarness}
		if err := Validate(request); err != nil {
			return err
		}
		known := map[string]bool{subjectrouting.NewSubject: true, subjectrouting.NoSubject: true, "unclear": true}
		for _, conversation := range scenario.Conversations {
			known[conversation.ID] = true
		}
		for i := 0; i < scenario.Fillers; i++ {
			id := fmt.Sprintf("archive-%03d", i)
			if known[id] {
				return subjectrouting.ErrInvalidInput
			}
			known[id] = true
		}
		for _, turn := range scenario.Turns {
			if !identifier.MatchString(turn.ID) || turnIDs[turn.ID] || strings.TrimSpace(turn.Content) == "" || !textWithin(turn.Content, subjectrouting.MaxContentRunes) || turn.GapSeconds < 0 || turn.GapSeconds > 365*24*3600 ||
				(turn.TargetID != "" && (!known[turn.TargetID] || turn.TargetID == "unclear")) || !validExpectations(turn.Expect) {
				return subjectrouting.ErrInvalidInput
			}
			for _, target := range turn.Expect.Targets {
				if !known[target] || !declaredDependency(target, turn.DependsOn) {
					return subjectrouting.ErrInvalidInput
				}
			}
			for id, status := range turn.Observations {
				if !known[id] || id == subjectrouting.NewSubject || id == subjectrouting.NoSubject || id == "unclear" || !declaredDependency(id, turn.DependsOn) || !slices.Contains([]string{StatusReady, StatusRunning, StatusUnconfirmed}, status) {
					return subjectrouting.ErrInvalidInput
				}
			}
			if len(turn.DependsOn) > 20 {
				return subjectrouting.ErrInvalidInput
			}
			if !declaredDependency(turn.TargetID, turn.DependsOn) {
				return subjectrouting.ErrInvalidInput
			}
			for _, dependency := range turn.DependsOn {
				if !turnIDs[dependency] {
					return subjectrouting.ErrInvalidInput
				}
			}
			if turn.Runtime != nil && (!validTarget(turn.Runtime.Target, turn.Runtime.WSLDistro) || len(turn.Runtime.AvailableHarnesses) > 30) {
				return subjectrouting.ErrInvalidInput
			}
			turnIDs[turn.ID], known["opened:"+turn.ID] = true, true
		}
	}
	return nil
}

func declaredDependency(target string, dependencies []string) bool {
	if !strings.HasPrefix(target, "opened:") {
		return true
	}
	return slices.Contains(dependencies, strings.TrimPrefix(target, "opened:"))
}

func validExpectations(expected Expectations) bool {
	if len(expected.Targets) == 0 || len(expected.Targets) > 10 || len(expected.Intents) == 0 || len(expected.Intents) > 7 || len(expected.Actions) == 0 || len(expected.Actions) > 6 {
		return false
	}
	for _, intent := range expected.Intents {
		if !slices.Contains([]string{subjectrouting.IntentQuestion, subjectrouting.IntentAmendRunning, subjectrouting.IntentNewWork, subjectrouting.IntentStatus, subjectrouting.IntentDecide, subjectrouting.IntentUnclear, "unclassified"}, intent) {
			return false
		}
	}
	for _, action := range expected.Actions {
		if !slices.Contains([]string{ActionContinue, ActionReadStatus, ActionNewConversation, ActionGeneral, ActionWait, ActionClarify}, action) {
			return false
		}
	}
	return true
}

func ImportFeedback(feedback Feedback) (Scenario, error) {
	if Validate(feedback.Request) != nil || !identifier.MatchString(feedback.ID) {
		return Scenario{}, subjectrouting.ErrInvalidInput
	}
	request := SafeSnapshot(feedback.Request)
	scenario := Scenario{ID: feedback.ID, Name: "Corrected routing example", Description: "Human-corrected labels; replay calls the live provider without forcing the original manual target.", Runtime: feedback.Runtime, DefaultHarness: request.DefaultHarness, Conversations: request.Conversations, Context: request.Context,
		Turns: []ScenarioTurn{{ID: "feedback", Content: request.Content, Expect: feedback.Expected}}}
	if err := ValidateScenarios([]Scenario{scenario}); err != nil {
		return Scenario{}, err
	}
	return scenario, nil
}

// RunBench uses the same Route as the live manager. It simulates inventory
// evolution only; no native processes, filesystem work or remote runs occur.
// Fixture results measure plumbing/policy, never model classification accuracy.
func RunBench(ctx context.Context, provider subjectrouting.Provider, options BenchOptions) (result BenchResult, err error) {
	result = BenchResult{Mode: options.Mode, StartedAt: time.Now().UTC(), Scenarios: []ScenarioResult{}}
	defer func() {
		if err != nil {
			switch err {
			case context.Canceled:
				result.ErrorReason = "cancelled"
			case context.DeadlineExceeded:
				result.ErrorReason = "timeout"
			case subjectrouting.ErrUnavailable:
				result.ErrorReason = "provider_unavailable"
			default:
				result.ErrorReason = "invalid_input"
			}
		}
	}()
	if options.Mode != "fixture" && options.Mode != "live" {
		return result, subjectrouting.ErrInvalidInput
	}
	result.Provenance = "Live provider judgments; inventory transitions simulated; no harness execution"
	if options.Mode == "fixture" {
		result.Provenance = "Scripted provider fixtures; policy/bookkeeping replay only; not model accuracy or harness execution"
	}
	if options.Mode == "live" && provider == nil {
		return result, subjectrouting.ErrUnavailable
	}
	scenarios, err := Scenarios()
	if err != nil {
		return result, err
	}
	if options.Scenarios != nil {
		if options.Mode != "live" {
			return result, subjectrouting.ErrInvalidInput
		}
		scenarios = options.Scenarios
	}
	if err = ValidateScenarios(scenarios); err != nil {
		return result, err
	}
	selected := map[string]bool{}
	for _, id := range options.ScenarioIDs {
		if selected[id] {
			return result, subjectrouting.ErrInvalidInput
		}
		selected[id] = true
		if !slices.ContainsFunc(scenarios, func(s Scenario) bool { return s.ID == id }) {
			return result, subjectrouting.ErrInvalidInput
		}
	}
	var fixtures map[string]fixtureJudgment
	if options.Mode == "fixture" && json.Unmarshal(fixtureJSON, &fixtures) != nil {
		return result, subjectrouting.ErrInvalidInput
	}
	for _, scenario := range scenarios {
		if len(selected) > 0 && !selected[scenario.ID] {
			continue
		}
		if scenario.FixtureOnly && options.Mode == "live" {
			if selected[scenario.ID] {
				return result, subjectrouting.ErrInvalidInput
			}
			continue
		}
		result.Scenarios = append(result.Scenarios, ScenarioResult{ID: scenario.ID, Name: scenario.Name, Turns: []TurnResult{}})
		scenarioResult := &result.Scenarios[len(result.Scenarios)-1]
		inventory := append([]Conversation(nil), scenario.Conversations...)
		for i := 0; i < scenario.Fillers; i++ {
			touched := scenario.Runtime.Now.Add(-time.Duration(i+1) * time.Hour)
			inventory = append(inventory, Conversation{ID: fmt.Sprintf("archive-%03d", i), Label: fmt.Sprintf("Archive project %03d", i), Summary: "An unrelated archived documentation experiment", Harness: scenario.DefaultHarness, Target: scenario.Runtime.Target, WSLDistro: scenario.Runtime.WSLDistro, Status: StatusReady, TouchedAt: &touched})
		}
		recent := append([]Message(nil), scenario.Context...)
		runtime := scenario.Runtime
		opened := map[string]string{}
		general := map[string]string{}
		for _, turn := range scenario.Turns {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			runtime.Now = runtime.Now.Add(time.Duration(turn.GapSeconds) * time.Second)
			if turn.Runtime != nil {
				now := runtime.Now
				runtime = *turn.Runtime
				runtime.Now = now
			}
			for i := range inventory {
				if status, ok := turn.Observations[stableTarget(inventory[i].ID, opened)]; ok {
					inventory[i].Status = status
				}
			}
			request := Request{Content: turn.Content, Conversations: inventory, Context: recent, TargetID: resolveTarget(turn.TargetID, opened), DefaultHarness: scenario.DefaultHarness}
			turnProvider := provider
			if options.Mode == "fixture" {
				judgment, exists := fixtures[scenario.ID+"/"+turn.ID]
				if !exists {
					return result, subjectrouting.ErrInvalidInput
				}
				judgment.Subject = resolveTarget(judgment.Subject, opened)
				turnProvider = fixtureProvider{judgment}
			}
			missingDependency := false
			for _, dependency := range turn.DependsOn {
				if opened["opened:"+dependency] == "" {
					missingDependency = true
				}
			}
			var outcome Result
			var routeErr error
			if missingDependency {
				outcome = Result{Decision: subjectrouting.Decision{Outcome: subjectrouting.OutcomeClarify, Reason: "missing_opened_dependency"}, Action: Action{Kind: ActionClarify, Reason: "missing_opened_dependency"}, Candidates: []subjectrouting.Candidate{}}
				routeErr = subjectrouting.ErrInvalidInput
			} else {
				outcome, routeErr = Route(ctx, turnProvider, request, runtime)
			}
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			actualTarget := outcome.Action.TargetID
			if outcome.Action.Kind == ActionClarify {
				actualTarget = "unclear"
			} else {
				actualTarget = stableTarget(actualTarget, opened)
			}
			actualIntent := outcome.Decision.Intent
			if actualIntent == "" {
				actualIntent = "unclassified"
			}
			turnResult := TurnResult{ID: turn.ID, Content: subjectrouting.Summary(turn.Content), Expected: turn.Expect, ActualTarget: actualTarget, ActualIntent: actualIntent, Result: outcome, Snapshot: SafeSnapshot(request), Runtime: runtime,
				TargetPass: slices.Contains(turn.Expect.Targets, actualTarget), IntentPass: slices.Contains(turn.Expect.Intents, actualIntent), ActionPass: slices.Contains(turn.Expect.Actions, outcome.Action.Kind)}
			if routeErr != nil {
				turnResult.Error = outcome.Action.Reason
			}
			turnResult.JudgmentSource = "unavailable"
			if missingDependency {
				turnResult.Skipped = true
				turnResult.TargetPass, turnResult.IntentPass, turnResult.ActionPass = false, false, false
			} else if request.TargetID != "" {
				turnResult.JudgmentSource = "manual"
				result.Totals.ManualTurns++
			} else if options.Mode == "fixture" {
				turnResult.JudgmentSource = "fixture"
			} else if outcome.Decision.Model != "" {
				turnResult.JudgmentSource = "provider"
				result.Totals.ProviderTurns++
			}
			turnResult.Passed = turnResult.TargetPass && turnResult.IntentPass && turnResult.ActionPass
			result.Totals.Turns++
			if turnResult.Passed {
				result.Totals.Passed++
			}
			if turnResult.TargetPass {
				result.Totals.TargetPassed++
			}
			if turnResult.IntentPass {
				result.Totals.IntentPassed++
			}
			if turnResult.ActionPass {
				result.Totals.ActionPassed++
			}
			scenarioResult.Turns = append(scenarioResult.Turns, turnResult)
			if missingDependency {
				continue
			}
			resolved := outcome.Action.TargetID
			if outcome.Action.Kind == ActionNewConversation || outcome.Action.Kind == ActionGeneral {
				generalKey := scenario.DefaultHarness + "/" + runtime.Target + "/" + runtime.WSLDistro
				if outcome.Action.Kind == ActionGeneral && general[generalKey] != "" {
					resolved = general[generalKey]
				} else {
					resolved = openedConversationID(turn.ID, inventory)
					opened["opened:"+turn.ID] = resolved
					touched := runtime.Now
					inventory = append(inventory, Conversation{ID: resolved, Label: subjectrouting.Label(turn.Content), Summary: subjectrouting.Summary(turn.Content), Harness: scenario.DefaultHarness, Target: runtime.Target, WSLDistro: runtime.WSLDistro, Status: StatusReady, TouchedAt: &touched})
					if outcome.Action.Kind == ActionGeneral {
						general[generalKey] = resolved
					}
				}
			}
			if outcome.Action.Kind == ActionContinue || outcome.Action.Kind == ActionReadStatus || outcome.Action.Kind == ActionGeneral {
				for i := range inventory {
					if inventory[i].ID == resolved {
						touched := runtime.Now
						inventory[i].TouchedAt = &touched
						if outcome.Action.Kind == ActionContinue || outcome.Action.Kind == ActionGeneral {
							inventory[i].Summary = subjectrouting.SummaryUpdate(inventory[i].Summary, turn.Content, outcome.Decision.Intent)
						}
					}
				}
			}
			if outcome.Action.Kind == ActionClarify {
				resolved = ""
			}
			at := runtime.Now
			recent = append(recent, Message{Role: "user", Content: subjectrouting.Summary(turn.Content), ConversationID: resolved, At: &at})
			if len(recent) > MaxContext {
				recent = recent[len(recent)-MaxContext:]
			}
		}
	}
	result.Complete = true
	return result, nil
}

func resolveTarget(target string, opened map[string]string) string {
	if id, ok := opened[target]; ok {
		return id
	}
	return target
}

func openedConversationID(turnID string, inventory []Conversation) string {
	// Validated turn IDs are ASCII. Leave room for a collision suffix within
	// the routing identifier limit, including the longest imported turn ID.
	base := "opened-" + turnID
	if len(base) > 120 {
		base = base[:120]
	}
	id := base
	for suffix := 1; slices.ContainsFunc(inventory, func(c Conversation) bool { return c.ID == id }); suffix++ {
		id = fmt.Sprintf("%s-%d", base, suffix)
	}
	return id
}
func stableTarget(target string, opened map[string]string) string {
	for key, id := range opened {
		if target == id {
			return key
		}
	}
	return target
}

// SafeSnapshot copies a validated request and redacts its content fields for
// trace/feedback export. It neither reads nor includes native transcripts.
func SafeSnapshot(request Request) Request {
	request.Content = subjectrouting.Redact(request.Content)
	request.Conversations = append([]Conversation{}, request.Conversations...)
	for i := range request.Conversations {
		c := &request.Conversations[i]
		c.Label, c.Summary = subjectrouting.Label(c.Label), subjectrouting.Summary(c.Summary)
		c.Aliases = append([]string(nil), c.Aliases...)
		for j := range c.Aliases {
			c.Aliases[j] = subjectrouting.Label(c.Aliases[j])
		}
	}
	request.Context = append([]Message(nil), request.Context...)
	for i := range request.Context {
		request.Context[i].Content = subjectrouting.Redact(request.Context[i].Content)
	}
	return request
}

type fixtureJudgment struct {
	Subject              string             `json:"subject"`
	Intent               string             `json:"intent"`
	Confidence           *float64           `json:"confidence,omitempty"`
	SubjectProbabilities map[string]float64 `json:"subjectProbabilities,omitempty"`
	Unavailable          bool               `json:"unavailable,omitempty"`
}
type fixtureProvider struct{ judgment fixtureJudgment }

func (provider fixtureProvider) Decide(_ context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
	fixture := provider.judgment
	if fixture.Unavailable {
		return subjectrouting.Decision{}, subjectrouting.ErrUnavailable
	}
	subjects := map[string]float64{subjectrouting.NewSubject: 0, subjectrouting.NoSubject: 0}
	for _, candidate := range input.Candidates {
		subjects[candidate.ID] = 0
	}
	if len(fixture.SubjectProbabilities) > 0 {
		for id, value := range fixture.SubjectProbabilities {
			subjects[id] = value
		}
	} else {
		subjects[fixture.Subject] = 1
	}
	intents := map[string]float64{subjectrouting.IntentQuestion: 0, subjectrouting.IntentAmendRunning: 0, subjectrouting.IntentNewWork: 0, subjectrouting.IntentStatus: 0, subjectrouting.IntentDecide: 0, subjectrouting.IntentUnclear: 0}
	intents[fixture.Intent] = 1
	confidence := .99
	if fixture.Confidence != nil {
		confidence = *fixture.Confidence
	}
	return subjectrouting.Decision{SubjectChoice: fixture.Subject, SubjectConfidence: confidence, SubjectProbabilities: subjects, Intent: fixture.Intent, IntentConfidence: .99, IntentProbabilities: intents, Model: "scripted-fixture"}, nil
}
