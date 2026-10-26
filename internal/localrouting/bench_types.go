package localrouting

import "time"

// Scenario expectations are human labels. Fixture model outputs are stored in
// a separate file, and are never read by the live-provider path.
type Scenario struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	FixtureOnly    bool           `json:"fixtureOnly,omitempty"`
	Runtime        Runtime        `json:"runtime"`
	DefaultHarness string         `json:"defaultHarness"`
	Conversations  []Conversation `json:"conversations"`
	Fillers        int            `json:"fillers,omitempty"`
	Context        []Message      `json:"context,omitempty"`
	Turns          []ScenarioTurn `json:"turns"`
}

type ScenarioTurn struct {
	ID         string   `json:"id"`
	Content    string   `json:"content"`
	TargetID   string   `json:"targetId,omitempty"`
	GapSeconds int      `json:"gapSeconds,omitempty"`
	DependsOn  []string `json:"dependsOn,omitempty"`
	// Observations simulate the browser's next inventory snapshot; they are not
	// delivery acknowledgements or evidence that native work executed.
	Observations map[string]string `json:"observations,omitempty"`
	Runtime      *Runtime          `json:"runtime,omitempty"`
	Expect       Expectations      `json:"expect"`
}

type Expectations struct {
	Targets []string `json:"targets"`
	Intents []string `json:"intents"`
	Actions []string `json:"actions"`
}

type BenchOptions struct {
	Mode        string   `json:"mode"`
	ScenarioIDs []string `json:"scenarioIds,omitempty"`
	// Optional imported, labelled scenarios are supported only in live mode:
	// they have no independent canned provider output for fixture replay.
	Scenarios []Scenario `json:"scenarios,omitempty"`
}

type TurnResult struct {
	ID             string       `json:"id"`
	Content        string       `json:"content"`
	Expected       Expectations `json:"expected"`
	ActualTarget   string       `json:"actualTarget"`
	ActualIntent   string       `json:"actualIntent"`
	TargetPass     bool         `json:"targetPass"`
	IntentPass     bool         `json:"intentPass"`
	ActionPass     bool         `json:"actionPass"`
	Passed         bool         `json:"passed"`
	Error          string       `json:"error,omitempty"`
	Skipped        bool         `json:"skipped,omitempty"`
	JudgmentSource string       `json:"judgmentSource"`
	Result         Result       `json:"result"`
	// Snapshot is bounded/redacted input for an exact feedback replay.
	Snapshot Request `json:"snapshot"`
	Runtime  Runtime `json:"runtime"`
}

type ScenarioResult struct {
	ID    string       `json:"id"`
	Name  string       `json:"name"`
	Turns []TurnResult `json:"turns"`
}

type BenchTotals struct {
	Turns         int `json:"turns"`
	Passed        int `json:"passed"`
	TargetPassed  int `json:"targetPassed"`
	IntentPassed  int `json:"intentPassed"`
	ActionPassed  int `json:"actionPassed"`
	ManualTurns   int `json:"manualTurns"`
	ProviderTurns int `json:"providerTurns"`
}

type BenchResult struct {
	Mode        string           `json:"mode"`
	Complete    bool             `json:"complete"`
	ErrorReason string           `json:"errorReason,omitempty"`
	Provenance  string           `json:"provenance"`
	StartedAt   time.Time        `json:"startedAt"`
	Scenarios   []ScenarioResult `json:"scenarios"`
	Totals      BenchTotals      `json:"totals"`
}

// Feedback records a correction to labels, not a provider output. Importing it
// produces a fresh live-provider scenario without assuming the correction was
// what the model originally said.
type Feedback struct {
	ID       string       `json:"id"`
	Request  Request      `json:"request"`
	Runtime  Runtime      `json:"runtime"`
	Expected Expectations `json:"expected"`
}
