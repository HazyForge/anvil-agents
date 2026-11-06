package subjectrouting

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/hazyforge/anvil-agents/internal/jev"
)

type answerProviderFunc func(context.Context, AnswerInput) (AnswerResolution, error)

func (f answerProviderFunc) ResolveAnswer(ctx context.Context, input AnswerInput) (AnswerResolution, error) {
	return f(ctx, input)
}

// The answer policy owns the boundary between chat text and a specific saved
// action. Subject-routing tests cannot protect negation, unknown-process
// attestations or model answers outside the pending decision's choices.
func TestPendingDecisionAnswers(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		choices       []string
		want          string
	}{
		{"explicit retry", "Retry it!", []string{"retry", "pause", "cancel"}, "retry"},
		{"hold", "leave it", []string{"retry", "pause"}, "pause"},
		{"cancel", "cancel it", []string{"cancel", "retry"}, "cancel"},
		{"negation", "don't retry it", []string{"retry", "pause"}, ""},
		{"conditional", "retry if it stopped", []string{"retry", "pause"}, ""},
		{"unknown cannot retry", "retry it", []string{"checked_stopped", "pause"}, ""},
		{"explicit checked", "I checked that this work has stopped.", []string{"checked_stopped", "pause"}, "checked_stopped"},
		{"unverified claim", "it probably stopped", []string{"checked_stopped", "pause"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveAnswer(context.Background(), nil, AnswerInput{Content: tc.content, Question: "What should happen?", Choices: tc.choices})
			if err != nil || got.Choice != tc.want || got.Confidence != nil {
				t.Fatalf("answer=%#v, %v; want %q without invented confidence", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		alter   func(*AnswerResolution)
		want    string
		invalid bool
	}{
		{name: "confident action", want: "retry"},
		{name: "uncertain", alter: func(r *AnswerResolution) { *r.Confidence = .84 }},
		{name: "nearby choices", alter: func(r *AnswerResolution) {
			r.Probabilities = map[string]float64{"retry": .51, "pause": .49, "unclear": 0}
		}},
		{name: "unknown action", alter: func(r *AnswerResolution) { r.Choice = "checked_stopped" }, invalid: true},
		{name: "unknown distribution", alter: func(r *AnswerResolution) { r.Probabilities["arbitrary_command"] = 0 }, invalid: true},
		{name: "invalid total", alter: func(r *AnswerResolution) { r.Probabilities["pause"] = .5 }, invalid: true},
		{name: "nan", alter: func(r *AnswerResolution) { *r.Confidence = math.NaN() }, invalid: true},
		{name: "missing confidence", alter: func(r *AnswerResolution) { r.Confidence = nil }, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			confidence := .97
			r := AnswerResolution{Choice: "retry", Confidence: &confidence, Probabilities: map[string]float64{"retry": .96, "pause": .03, "unclear": .01}, Model: "fixture"}
			if tc.alter != nil {
				tc.alter(&r)
			}
			got, err := ResolveAnswer(context.Background(), answerProviderFunc(func(_ context.Context, input AnswerInput) (AnswerResolution, error) {
				if strings.Contains(strings.Join(input.Choices, ","), "checked_stopped") || strings.Contains(input.Content, "fixture-secret") || strings.Contains(input.Question, "question-secret") {
					t.Fatal("unsafe provider input")
				}
				return r, nil
			}), AnswerInput{Content: "Please attempt that task once more. password=fixture-secret", Question: "A failed turn. api_key=question-secret", Choices: []string{"retry", "pause", "checked_stopped"}})
			if got.Choice != tc.want || (errors.Is(err, ErrInvalidDecision) != tc.invalid) {
				t.Fatalf("answer=%#v, %v", got, err)
			}
		})
	}
}

// Exercises the actual Jev answer adapter and production policy together;
// fixtures provide probabilities only, never the admitted action or receipt.
func TestJevPendingAnswerAdapter(t *testing.T) {
	provider := JevProvider{Backend: &jev.FakeBackend{Model: "fixture", Answers: map[string]json.RawMessage{
		"answer": jev.MustAnswer(t, jev.ChoiceAnswer{Type: jev.TypeChoice, Choice: "pause", Confidence: .98, Probabilities: map[string]float64{"pause": .97, "retry": .01, "unclear": .02}}),
	}}}
	got, err := ResolveAnswer(context.Background(), provider, AnswerInput{Content: "Keep that task on hold for the moment", Question: "Attempt failed. What next?", Choices: []string{"pause", "retry"}})
	if err != nil || got.Choice != "pause" || got.Source != "classifier" || got.Model != "fixture" {
		t.Fatalf("answer=%#v, %v", got, err)
	}
}
