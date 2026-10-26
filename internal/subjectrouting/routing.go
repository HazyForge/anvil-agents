// Package subjectrouting selects a subject and intent before a chat harness runs.
// Providers supply typed judgments; application policy owns routing thresholds,
// persistence, authorization and execution.
package subjectrouting

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxCandidates   = 15
	MaxContentRunes = 12000
	MaxSummaryRunes = 1000
	MaxLabelRunes   = 120

	NewSubject        = "new_subject"
	NoSubject         = "none"
	OutcomeRoute      = "route"
	OutcomeNewSubject = "new_subject"
	OutcomeGeneral    = "general"
	OutcomeClarify    = "clarify"

	IntentQuestion     = "question"
	IntentAmendRunning = "amend_running"
	IntentNewWork      = "new_work"
	IntentStatus       = "status"
	IntentDecide       = "decide"
	IntentUnclear      = "unclear"

	ConfidenceFloor      = 0.5
	NewSubjectConfidence = 0.8
	AmbiguityMargin      = 0.15
)

var (
	ErrInvalidInput    = errors.New("invalid subject routing input")
	ErrUnavailable     = errors.New("subject decision provider unavailable")
	ErrInvalidDecision = errors.New("subject decision provider returned an invalid decision")
)

type Candidate struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Summary string   `json:"summary"`
	Kind    string   `json:"kind"`
	Aliases []string `json:"aliases,omitempty"`
}

type Input struct {
	Content string `json:"content"`
	// Candidates must already be authorized for the current owner and namespace,
	// ordered most recently touched first. The provider cannot discover subjects.
	Candidates []Candidate `json:"candidates"`
}

// Decision keeps the validated model judgments separate from the action allowed
// by policy. An outcome is routing information, never execution authorization.
type Decision struct {
	SubjectChoice        string             `json:"subjectChoice"`
	SubjectConfidence    float64            `json:"subjectConfidence"`
	SubjectProbabilities map[string]float64 `json:"subjectProbabilities"`
	Intent               string             `json:"intent"`
	IntentConfidence     float64            `json:"intentConfidence"`
	IntentProbabilities  map[string]float64 `json:"intentProbabilities"`
	Model                string             `json:"model"`
	Outcome              string             `json:"outcome"`
	Reason               string             `json:"reason"`
}

type Provider interface {
	Decide(context.Context, Input) (Decision, error)
}

var safeIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

// Prepare bounds and redacts the only state providers may see. It does not
// reorder candidates: recency and owner checks belong to the store/API.
func Prepare(input Input) (Input, error) {
	if !utf8.ValidString(input.Content) || strings.TrimSpace(input.Content) == "" {
		return Input{}, ErrInvalidInput
	}
	prepared := Input{Content: bounded(Redact(input.Content), MaxContentRunes), Candidates: []Candidate{}}
	seen := map[string]bool{NewSubject: true, NoSubject: true}
	for _, candidate := range input.Candidates {
		if len(prepared.Candidates) == MaxCandidates {
			break
		}
		if !safeIdentifier.MatchString(candidate.ID) || seen[candidate.ID] || !utf8.ValidString(candidate.Label) || !utf8.ValidString(candidate.Summary) || !utf8.ValidString(candidate.Kind) {
			return Input{}, ErrInvalidInput
		}
		seen[candidate.ID] = true
		candidate.Label = Label(candidate.Label)
		candidate.Summary = Summary(candidate.Summary)
		candidate.Kind = bounded(Redact(candidate.Kind), 40)
		aliases := make([]string, 0, min(len(candidate.Aliases), 8))
		for _, alias := range candidate.Aliases {
			if len(aliases) == 8 {
				break
			}
			if !utf8.ValidString(alias) {
				return Input{}, ErrInvalidInput
			}
			if alias = Label(alias); alias != "" {
				aliases = append(aliases, alias)
			}
		}
		candidate.Aliases = aliases
		prepared.Candidates = append(prepared.Candidates, candidate)
	}
	return prepared, nil
}

// Route enforces the same output policy for any provider. Provider errors are
// replaced with a fixed safe error: transport bodies can contain request data.
func Route(ctx context.Context, provider Provider, input Input) (Decision, error) {
	prepared, err := Prepare(input)
	if err != nil {
		return Decision{Outcome: OutcomeClarify, Reason: "invalid_input"}, err
	}
	if provider == nil || ctx.Err() != nil {
		return Decision{Outcome: OutcomeClarify, Reason: "provider_unavailable"}, ErrUnavailable
	}
	decision, err := provider.Decide(ctx, prepared)
	if ctx.Err() != nil {
		return Decision{Outcome: OutcomeClarify, Reason: "provider_unavailable"}, ErrUnavailable
	}
	if err != nil {
		if errors.Is(err, ErrInvalidDecision) {
			return Decision{Outcome: OutcomeClarify, Reason: "invalid_decision"}, ErrInvalidDecision
		}
		return Decision{Outcome: OutcomeClarify, Reason: "provider_unavailable"}, ErrUnavailable
	}
	return ApplyPolicy(prepared, decision)
}

// ApplyPolicy validates closed-set judgments before exposing them to a caller.
// These are conservative initial thresholds, not a claim of model accuracy.
func ApplyPolicy(input Input, decision Decision) (Decision, error) {
	prepared, err := Prepare(input)
	if err != nil {
		return Decision{Outcome: OutcomeClarify, Reason: "invalid_input"}, err
	}
	subjects := map[string]bool{NewSubject: true, NoSubject: true}
	for _, candidate := range prepared.Candidates {
		subjects[candidate.ID] = true
	}
	intents := map[string]bool{}
	for key := range intentCriteria() {
		intents[key] = true
	}
	if !validChoice(decision.SubjectChoice, decision.SubjectConfidence, decision.SubjectProbabilities, subjects) ||
		!validChoice(decision.Intent, decision.IntentConfidence, decision.IntentProbabilities, intents) ||
		!safeIdentifier.MatchString(decision.Model) || Redact(decision.Model) != decision.Model {
		return Decision{Outcome: OutcomeClarify, Reason: "invalid_decision"}, ErrInvalidDecision
	}
	decision.SubjectProbabilities = cloneProbabilities(decision.SubjectProbabilities)
	decision.IntentProbabilities = cloneProbabilities(decision.IntentProbabilities)
	decision.Outcome = OutcomeClarify
	switch {
	case decision.SubjectConfidence < ConfidenceFloor || decision.SubjectProbabilities[decision.SubjectChoice] < ConfidenceFloor:
		decision.Reason = "subject_uncertain"
	case margin(decision.SubjectChoice, decision.SubjectProbabilities) < AmbiguityMargin:
		decision.Reason = "subject_ambiguous"
	case decision.Intent == IntentUnclear:
		decision.Reason = "unclear_intent"
	case decision.IntentConfidence < ConfidenceFloor || decision.IntentProbabilities[decision.Intent] < ConfidenceFloor || margin(decision.Intent, decision.IntentProbabilities) < AmbiguityMargin:
		decision.Reason = "intent_uncertain"
	case decision.SubjectChoice == NewSubject && (decision.SubjectConfidence < NewSubjectConfidence || decision.SubjectProbabilities[NewSubject] < NewSubjectConfidence):
		decision.Reason = "new_subject_uncertain"
	case decision.SubjectChoice == NewSubject:
		decision.Outcome, decision.Reason = OutcomeNewSubject, "new_subject"
	case decision.SubjectChoice == NoSubject:
		decision.Outcome, decision.Reason = OutcomeGeneral, "general_conversation"
	default:
		decision.Outcome, decision.Reason = OutcomeRoute, "matched_subject"
	}
	return decision, nil
}

func validChoice(choice string, confidence float64, probabilities map[string]float64, options map[string]bool) bool {
	if !options[choice] || !unitInterval(confidence) || len(probabilities) != len(options) {
		return false
	}
	total := 0.0
	selected, exists := probabilities[choice]
	if !exists {
		return false
	}
	for option, probability := range probabilities {
		if !options[option] || !unitInterval(probability) || probability > selected+1e-9 {
			return false
		}
		total += probability
	}
	return math.Abs(total-1) <= 0.01
}

func unitInterval(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func margin(choice string, probabilities map[string]float64) float64 {
	second := 0.0
	for option, probability := range probabilities {
		if option != choice && probability > second {
			second = probability
		}
	}
	return probabilities[choice] - second
}

func cloneProbabilities(probabilities map[string]float64) map[string]float64 {
	copy := make(map[string]float64, len(probabilities))
	for option, probability := range probabilities {
		copy[option] = probability
	}
	return copy
}

func intentCriteria() map[string]string {
	return map[string]string{
		IntentQuestion:     "A question, explanation, or ordinary conversation; no request to change existing work or start a new task.",
		IntentAmendRunning: "Change, add instructions to, or correct work that is already running or planned for an existing subject.",
		IntentNewWork:      "Start a new task, project, investigation, or piece of work, including new work within an existing subject.",
		IntentStatus:       "Ask for progress, state, results, blockers, or where we left off on a subject.",
		IntentDecide:       "Express a selection, approval, rejection, or decision about a previously discussed choice. This describes intent only and grants no permissions.",
		IntentUnclear:      "The message is too ambiguous to identify one intent, or contains multiple incompatible requests that need clarification.",
	}
}

// Summary is a bounded, redacted latest-message summary suitable for a subject
// rubric. It deliberately makes no claim that work has been executed.
func Summary(content string) string {
	return bounded(strings.Join(strings.Fields(Redact(content)), " "), MaxSummaryRunes)
}

func Label(content string) string {
	return bounded(strings.Join(strings.Fields(Redact(content)), " "), MaxLabelRunes)
}

// SelectCandidates retains explicitly named older subjects within the bounded
// classifier window. Inputs must already be authorized and in recency order.
// Exact label/alias mentions only affect candidate coverage, never the decision.
func SelectCandidates(content string, authorized []Candidate) []Candidate {
	if len(authorized) > 200 {
		authorized = authorized[:200]
	}
	content = strings.ToLower(Redact(content))
	mentioned := make([]Candidate, 0, MaxCandidates)
	recent := make([]Candidate, 0, MaxCandidates)
	for _, candidate := range authorized {
		match := mentions(content, candidate.Label)
		for _, alias := range candidate.Aliases {
			if match {
				break
			}
			match = mentions(content, alias)
		}
		if match {
			mentioned = append(mentioned, candidate)
		} else {
			recent = append(recent, candidate)
		}
	}
	selected := append(mentioned, recent...)
	if len(selected) > MaxCandidates {
		selected = selected[:MaxCandidates]
	}
	return selected
}

func mentions(content, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if utf8.RuneCountInString(name) < 3 {
		return false
	}
	start := 0
	for start < len(content) {
		relative := strings.Index(content[start:], name)
		if relative < 0 {
			return false
		}
		index := start + relative
		end := index + len(name)
		before, _ := utf8.DecodeLastRuneInString(content[:index])
		after, _ := utf8.DecodeRuneInString(content[end:])
		if !unicode.IsLetter(before) && !unicode.IsNumber(before) && !unicode.IsLetter(after) && !unicode.IsNumber(after) {
			return true
		}
		start = end
	}
	return false
}

// SummaryUpdate keeps the original subject description while replacing the
// latest-request slot. A short follow-up such as "yes" must not erase identity.
// These summaries describe requests, not unverified execution outcomes.
func SummaryUpdate(existing, content, intent string) string {
	identity, _, _ := strings.Cut(existing, "\nLatest request (")
	identity = bounded(Summary(identity), 600)
	if identity == "" {
		identity = bounded(Summary(content), 600)
	}
	if _, known := intentCriteria()[intent]; !known {
		intent = "message"
	}
	return identity + "\nLatest request (" + intent + "): " + bounded(Summary(content), 320)
}

func bounded(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return value
}
