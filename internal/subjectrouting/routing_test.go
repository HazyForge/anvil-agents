package subjectrouting

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hazyforge/anvil-agents/internal/jev"
)

// This is the owner boundary for routing policy. Existing intent-only tests do
// not protect subject selection, new-subject creation thresholds, or ambiguity
// between otherwise authorized destinations. Fixtures supply model judgments;
// production code must decide whether any work may be handed off.
func TestJevSubjectRoutingDecisions(t *testing.T) {
	input := Input{Content: "Add a retry limit to the irrigation controller", Candidates: []Candidate{
		{ID: "irrigation", Label: "Irrigation controller", Summary: "Build the local pump controller", Kind: "project"},
		{ID: "trading", Label: "Trading assistant", Summary: "Research risk controls", Kind: "project"},
	}}
	tests := []struct {
		name    string
		change  func(*Decision)
		outcome string
		reason  string
		invalid bool
	}{
		{name: "existing subject", outcome: OutcomeRoute, reason: "matched_subject"},
		{name: "new subject", change: func(d *Decision) {
			d.SubjectChoice = NewSubject
			d.SubjectProbabilities = probabilities(NewSubject, "irrigation", "trading", NewSubject, NoSubject)
			d.Intent = IntentNewWork
			d.IntentProbabilities = intentProbabilities(IntentNewWork)
		}, outcome: OutcomeNewSubject, reason: "new_subject"},
		{name: "general conversation", change: func(d *Decision) {
			d.SubjectChoice = NoSubject
			d.SubjectProbabilities = probabilities(NoSubject, "irrigation", "trading", NewSubject, NoSubject)
		}, outcome: OutcomeGeneral, reason: "general_conversation"},
		{name: "nearby subjects", change: func(d *Decision) { d.SubjectProbabilities["irrigation"] = .54; d.SubjectProbabilities["trading"] = .46 }, outcome: OutcomeClarify, reason: "subject_ambiguous"},
		{name: "low subject confidence", change: func(d *Decision) { d.SubjectConfidence = .49 }, outcome: OutcomeClarify, reason: "subject_uncertain"},
		{name: "low intent confidence", change: func(d *Decision) { d.IntentConfidence = .49 }, outcome: OutcomeClarify, reason: "intent_uncertain"},
		{name: "unclear intent", change: func(d *Decision) {
			d.Intent = IntentUnclear
			d.IntentProbabilities = intentProbabilities(IntentUnclear)
		}, outcome: OutcomeClarify, reason: "unclear_intent"},
		{name: "new subject below creation bar", change: func(d *Decision) {
			d.SubjectChoice = NewSubject
			d.SubjectConfidence = .79
			d.SubjectProbabilities = probabilities(NewSubject, "irrigation", "trading", NewSubject, NoSubject)
		}, outcome: OutcomeClarify, reason: "new_subject_uncertain"},
		{name: "unknown subject", change: func(d *Decision) { d.SubjectChoice = "someone-elses-project" }, invalid: true},
		{name: "unknown intent", change: func(d *Decision) { d.Intent = "execute_anything" }, invalid: true},
		{name: "missing probability", change: func(d *Decision) { delete(d.SubjectProbabilities, NoSubject) }, invalid: true},
		{name: "unknown probability", change: func(d *Decision) { d.SubjectProbabilities["someone-elses-project"] = 0 }, invalid: true},
		{name: "invalid probability total", change: func(d *Decision) { d.SubjectProbabilities["trading"] = .2 }, invalid: true},
		{name: "choice disagrees with distribution", change: func(d *Decision) { d.SubjectProbabilities["irrigation"] = .1; d.SubjectProbabilities["trading"] = .9 }, invalid: true},
		{name: "negative confidence", change: func(d *Decision) { d.IntentConfidence = -.1 }, invalid: true},
		{name: "unsafe model value", change: func(d *Decision) { d.Model = "provider body with secret=unsafe" }, invalid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := validDecision()
			if test.change != nil {
				test.change(&decision)
			}
			provider := JevProvider{Backend: &jev.FakeBackend{
				Model: decision.Model,
				Answers: map[string]json.RawMessage{
					"subject": jev.MustAnswer(t, jev.ChoiceAnswer{Type: jev.TypeChoice, Choice: decision.SubjectChoice, Confidence: decision.SubjectConfidence, Probabilities: decision.SubjectProbabilities}),
					"intent":  jev.MustAnswer(t, jev.ChoiceAnswer{Type: jev.TypeChoice, Choice: decision.Intent, Confidence: decision.IntentConfidence, Probabilities: decision.IntentProbabilities}),
				},
			}}
			got, err := provider.Decide(context.Background(), input)
			if test.invalid {
				if !errors.Is(err, ErrInvalidDecision) || got.Outcome != OutcomeClarify || got.SubjectChoice != "" {
					t.Fatalf("invalid output escaped: %#v, %v", got, err)
				}
				return
			}
			if err != nil || got.Outcome != test.outcome || got.Reason != test.reason {
				t.Fatalf("decision = %#v, %v; want %s/%s", got, err, test.outcome, test.reason)
			}
			if got.SubjectChoice != decision.SubjectChoice || got.Intent != decision.Intent {
				t.Fatal("gating discarded the valid model judgments")
			}
		})
	}
}

// A non-Jev adapter cannot bypass the policy by returning an execution outcome.
// This also guards against logging provider errors that echo secrets or state.
func TestRouteEnforcesPolicyForEveryProvider(t *testing.T) {
	input := Input{Content: "Check the irrigation project", Candidates: []Candidate{{ID: "irrigation"}, {ID: "trading"}}}
	got, err := Route(context.Background(), decisionFunc(func(context.Context, Input) (Decision, error) {
		d := validDecision()
		d.Outcome = OutcomeRoute
		d.SubjectConfidence = math.NaN()
		return d, nil
	}), input)
	if !errors.Is(err, ErrInvalidDecision) || got.Outcome != OutcomeClarify {
		t.Fatalf("NaN bypassed routing policy: %#v, %v", got, err)
	}

	got, err = Route(context.Background(), decisionFunc(func(context.Context, Input) (Decision, error) {
		return validDecision(), errors.New("request failed: password=do-not-return")
	}), input)
	if err != ErrUnavailable || got.Outcome != OutcomeClarify || got.SubjectChoice != "" {
		t.Fatalf("provider failure produced usable decision: %#v, %v", got, err)
	}
	if strings.Contains(err.Error(), "do-not-return") {
		t.Fatal("provider error leaked")
	}
	got, err = Route(context.Background(), nil, input)
	if err != ErrUnavailable || got.Outcome != OutcomeClarify {
		t.Fatalf("missing provider: %#v, %v", got, err)
	}
}

// An HTTP server witnesses the actual serialized provider request, independently
// of request-building internals. This protects the external disclosure boundary
// and the batching/bounds contract; it does not claim classifier accuracy.
func TestJevRequestBatchesBoundedRedactedSubjectState(t *testing.T) {
	const fakeKey = "sk-proj-abcdefghijklmnopqrstuvwxyz"
	const fakeJWT = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJmaXh0dXJlIn0.signature"
	const fakePassword = "secret database pass"
	input := Input{Content: "Please check irrigation. api_key=" + fakeKey + "\nAuthorization: Basic ZmFrZTpwYXNzd29yZA==\n" + fakeJWT + "\n" + strings.Repeat("茄", MaxContentRunes+100)}
	ageSeconds := 35.0
	for i := 0; i < 8; i++ {
		input.Recent = append(input.Recent, RecentMessage{Role: "user", Content: "Continue this work, password=private-context " + strings.Repeat("c", 1200), SubjectID: "a", AgeSeconds: &ageSeconds})
	}
	for i := 0; i < MaxCandidates+2; i++ {
		input.Candidates = append(input.Candidates, Candidate{ID: string(rune('a' + i)), Label: "Database password=private-value", Summary: "password=\"" + fakePassword + "\" " + strings.Repeat("a", MaxSummaryRunes+50), Kind: "project"})
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			State struct {
				Message  string          `json:"message"`
				Subjects []Candidate     `json:"subjects"`
				Recent   []RecentMessage `json:"recent"`
			} `json:"state"`
			Model     string `json:"model"`
			Questions map[string]struct {
				Type     string            `json:"type"`
				Criteria map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		encoded, _ := json.Marshal(request)
		for _, secret := range []string{fakeKey, fakeJWT, fakePassword, "private-value", "private-context", "ZmFrZTpwYXNzd29yZA=="} {
			if strings.Contains(string(encoded), secret) {
				t.Errorf("request contains unredacted fixture credential %q", secret)
			}
		}
		if len(request.Questions) != 2 || request.Questions["subject"].Type != "choice" || request.Questions["intent"].Type != "choice" {
			t.Errorf("expected two batched choice questions, got %#v", request.Questions)
		}
		if len(request.State.Subjects) != MaxCandidates || utf8.RuneCountInString(request.State.Message) > MaxContentRunes {
			t.Error("state is not bounded")
		}
		if len(request.State.Recent) != 6 {
			t.Error("recent context is not bounded")
		}
		for _, message := range request.State.Recent {
			if message.SubjectID != "a" || message.AgeSeconds == nil || *message.AgeSeconds != 35 || utf8.RuneCountInString(message.Content) > 1000 {
				t.Error("temporal context contract lost")
			}
		}
		for _, candidate := range request.State.Subjects {
			if utf8.RuneCountInString(candidate.Summary) > MaxSummaryRunes {
				t.Error("summary not bounded")
			}
		}
		if len(request.Questions["subject"].Criteria) != MaxCandidates+2 || len(request.Questions["intent"].Criteria) != 6 {
			t.Error("closed choices missing")
		}
		if request.Model != "jev-test-pin" {
			t.Errorf("model = %s", request.Model)
		}
		subjectProbabilities := map[string]float64{}
		for id := range request.Questions["subject"].Criteria {
			subjectProbabilities[id] = 0
		}
		subjectProbabilities[NoSubject] = 1
		_ = json.NewEncoder(w).Encode(jev.Response{Model: "jev-test-pin", Answers: map[string]json.RawMessage{
			"subject": jev.MustAnswer(t, jev.ChoiceAnswer{Type: jev.TypeChoice, Choice: NoSubject, Confidence: .99, Probabilities: subjectProbabilities}),
			"intent":  jev.MustAnswer(t, jev.ChoiceAnswer{Type: jev.TypeChoice, Choice: IntentQuestion, Confidence: .99, Probabilities: intentProbabilities(IntentQuestion)}),
		}})
	}))
	defer server.Close()
	client := jev.NewHTTPClient("fake-test-credential")
	client.Endpoint = server.URL
	got, err := Route(context.Background(), JevProvider{Backend: client, Model: "jev-test-pin"}, input)
	if err != nil || got.Outcome != OutcomeGeneral || calls != 1 {
		t.Fatalf("route = %#v, %v, calls=%d", got, err, calls)
	}
}

func TestJevRejectsMissingAnswersAndProviderFailure(t *testing.T) {
	for _, test := range []struct {
		name    string
		backend jev.Backend
		want    error
	}{
		{"missing answers", &jev.FakeBackend{Answers: map[string]json.RawMessage{}}, ErrInvalidDecision},
		{"error contains data", &jev.FakeBackend{Err: errors.New("Authorization: secret")}, ErrUnavailable},
		{"no backend", nil, ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := (JevProvider{Backend: test.backend}).Decide(context.Background(), Input{Content: "hi"})
			if err != test.want || got.Outcome != OutcomeClarify || got.SubjectChoice != "" {
				t.Fatalf("got %#v, %v", got, err)
			}
		})
	}
}

func TestRoutingInputAndSummaryBoundary(t *testing.T) {
	for _, input := range []Input{
		{Content: "  "},
		{Content: "hello", Candidates: []Candidate{{ID: NoSubject}}},
		{Content: "hello", Candidates: []Candidate{{ID: "same"}, {ID: "same"}}},
		{Content: "hello", Candidates: []Candidate{{ID: "unknown secret=123"}}},
	} {
		_, err := Prepare(input)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid input accepted: %#v, %v", input, err)
		}
	}
	content := "Remember this\n-----BEGIN OPENSSH PRIVATE KEY-----\nfixture-private-material\n-----END OPENSSH PRIVATE KEY-----\npostgres://user:password@db.example/app"
	for _, value := range []string{Summary(content), Label(content)} {
		if strings.Contains(value, "fixture-private-material") || strings.Contains(value, "user:password") {
			t.Fatalf("summary contains credentials: %s", value)
		}
		if strings.Contains(value, "\n") {
			t.Fatalf("summary is not a single compact paragraph: %s", value)
		}
	}
}

// The classifier cannot select a named older subject unless retrieval includes
// it. Its latest shorthand must also leave the subject's identity recognizable.
func TestNamedOlderSubjectAndSummaryStayAvailable(t *testing.T) {
	var candidates []Candidate
	for i := 0; i < 20; i++ {
		candidates = append(candidates, Candidate{ID: string(rune('a' + i)), Label: "Recent project"})
	}
	candidates = append(candidates, Candidate{ID: "irrigation", Label: "Garden irrigation controller", Aliases: []string{"garden pump"}})
	selected := SelectCandidates("What happened with the Garden Pump?", candidates)
	if len(selected) != MaxCandidates || selected[0].ID != "irrigation" || selected[1].ID != "a" {
		t.Fatalf("named subject lost outside recent window: %#v", selected)
	}
	selected = SelectCandidates("the garden pumping station", candidates)
	if selected[0].ID != "a" {
		t.Fatal("partial word incorrectly promoted an older subject")
	}
	summary := SummaryUpdate("Garden irrigation controller with a dry-run guard", "Add a retry limit", IntentAmendRunning)
	summary = SummaryUpdate(summary, "yes", IntentDecide)
	if !strings.Contains(summary, "dry-run guard") || !strings.Contains(summary, "Latest request (decide): yes") || strings.Contains(summary, "Add a retry limit") {
		t.Fatalf("identity lost or latest request accumulated: %q", summary)
	}
	if utf8.RuneCountInString(summary) > MaxSummaryRunes {
		t.Fatal("updated summary exceeded provider limit")
	}
}

type decisionFunc func(context.Context, Input) (Decision, error)

func (fn decisionFunc) Decide(ctx context.Context, input Input) (Decision, error) {
	return fn(ctx, input)
}

func validDecision() Decision {
	return Decision{SubjectChoice: "irrigation", SubjectConfidence: .95, SubjectProbabilities: probabilities("irrigation", "irrigation", "trading", NewSubject, NoSubject), Intent: IntentAmendRunning, IntentConfidence: .95, IntentProbabilities: intentProbabilities(IntentAmendRunning), Model: "test-jev"}
}

func probabilities(choice string, options ...string) map[string]float64 {
	out := map[string]float64{}
	for _, option := range options {
		out[option] = 0
	}
	out[choice] = 1
	return out
}

func intentProbabilities(choice string) map[string]float64 {
	return probabilities(choice, IntentQuestion, IntentAmendRunning, IntentNewWork, IntentStatus, IntentDecide, IntentUnclear)
}
