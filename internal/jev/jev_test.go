package jev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestQuestionValidation(t *testing.T) {
	if err := NoulQuestion("Does `message` ask for money back?", "", "").Validate("refund"); err != nil {
		t.Fatalf("noul without criteria should validate: %v", err)
	}
	if err := NoulQuestion("Does `message` convey urgency?", "time-sensitive", "no urgency").Validate("urgent"); err != nil {
		t.Fatalf("noul with criteria should validate: %v", err)
	}
	if err := ChoiceQuestion("Which team?", map[string]*string{"a": Desc("A team")}).Validate("dept"); err != nil {
		t.Fatalf("choice should validate: %v", err)
	}
	if err := ChoiceQuestion("Which team?", map[string]*string{}).Validate("dept"); err == nil {
		t.Fatal("choice with no options should fail validation")
	}
	many := map[string]*string{}
	for i := 0; i < MaxChoiceOptions+1; i++ {
		key := strings.Repeat("o", 3) + string(rune('a'+i%26)) + string(rune('0'+i/26))
		many[key] = Desc("option")
	}
	if err := ChoiceQuestion("Which team?", many).Validate("dept"); err == nil {
		t.Fatal("choice over 255 options should fail validation")
	}
	if err := ScoreQuestion("How severe?", []string{"cosmetic", "blocking"}).Validate("sev"); err != nil {
		t.Fatalf("score with 2 levels should validate: %v", err)
	}
	if err := ScoreQuestion("How severe?", []string{"only"}).Validate("sev"); err == nil {
		t.Fatal("score with 1 level should fail validation")
	}
	if err := ScoreQuestion("How severe?", []string{"", "blocking"}).Validate("sev"); err == nil {
		t.Fatal("score with an empty level should fail validation")
	}
	if err := (Question{Type: TypeNoul}).Validate("q"); err == nil {
		t.Fatal("missing instructions should fail validation")
	}
	if err := (Question{Type: "generate", Instructions: "write prose"}).Validate("q"); err == nil {
		t.Fatal("unknown question type should fail validation")
	}
}

func TestRequestValidation(t *testing.T) {
	question := NoulQuestion("Does `message` ask for a refund?", "", "")
	base := Request{Model: DefaultModel, Questions: map[string]Question{"refund": question}}
	for name, state := range map[string]any{
		"string": "my card was charged twice",
		"object": map[string]any{"message": "charged twice"},
		"array":  []any{"first", "second"},
	} {
		req := base
		req.State = state
		if err := req.Validate(); err != nil {
			t.Fatalf("state %s should validate: %v", name, err)
		}
	}
	for name, state := range map[string]any{
		"nil":    nil,
		"number": 42.0,
		"bool":   true,
	} {
		req := base
		req.State = state
		if err := req.Validate(); err == nil {
			t.Fatalf("state %s should fail validation", name)
		}
	}
	empty := Request{Model: DefaultModel, State: "hi"}
	if err := empty.Validate(); err == nil {
		t.Fatal("request with no questions should fail validation")
	}
}

func TestHTTPClientRoundTrip(t *testing.T) {
	var gotAuth, gotContentType, gotModel string
	var gotQuestions int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("cannot decode request: %v", err)
		}
		gotModel = req.Model
		gotQuestions = len(req.Questions)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.92}},"usage":{"input_tokens":10,"output_tokens":2}}`))
	}))
	defer server.Close()

	client := NewHTTPClient("test-key")
	client.Endpoint = server.URL + "/v1/systemone"
	resp, err := client.Evaluate(context.Background(), Request{
		State:     "Help! My payouts have been failing for 3 days.",
		Questions: map[string]Question{"urgent": NoulQuestion("Does this convey urgency?", "", "")},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer key", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	if gotModel != DefaultModel {
		t.Fatalf("model = %q, want default %q", gotModel, DefaultModel)
	}
	if gotQuestions != 1 {
		t.Fatalf("questions = %d, want 1", gotQuestions)
	}
	answer, err := resp.Noul("urgent")
	if err != nil {
		t.Fatalf("Noul: %v", err)
	}
	if answer.Noul != 0.92 {
		t.Fatalf("noul = %v, want 0.92", answer.Noul)
	}
	if resp.Model != "jev-1.13.0" {
		t.Fatalf("response model = %q", resp.Model)
	}
	if resp.Usage.InputTokens != 10 {
		t.Fatalf("input tokens = %d", resp.Usage.InputTokens)
	}
}

func TestHTTPClientRetriesOverload(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"detail":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{},"usage":{"input_tokens":1,"output_tokens":0}}`))
	}))
	defer server.Close()

	client := NewHTTPClient("test-key")
	client.Endpoint = server.URL
	client.RetryWait = time.Millisecond
	if _, err := client.Evaluate(context.Background(), Request{
		Model:     DefaultModel,
		State:     "hi",
		Questions: map[string]Question{"q": NoulQuestion("Is this a greeting?", "", "")},
	}); err != nil {
		t.Fatalf("Evaluate with retry: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (one retry after 429)", calls)
	}
}

func TestHTTPClientSurfacesAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"bad key"}`))
	}))
	defer server.Close()

	client := NewHTTPClient("wrong-key")
	client.Endpoint = server.URL
	_, err := client.Evaluate(context.Background(), Request{
		Model:     DefaultModel,
		State:     "hi",
		Questions: map[string]Question{"q": NoulQuestion("Is this a greeting?", "", "")},
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want *APIError", err, err)
	}
	if apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", apiErr.Status)
	}
	if apiErr.Retryable() {
		t.Fatal("401 must not be retryable")
	}
	if (&APIError{Status: 529}).Retryable() != true {
		t.Fatal("529 must be retryable")
	}
}

func TestHTTPClientRequiresKey(t *testing.T) {
	client := NewHTTPClient("")
	if _, err := client.Evaluate(context.Background(), Request{
		Model:     DefaultModel,
		State:     "hi",
		Questions: map[string]Question{"q": NoulQuestion("Is this a greeting?", "", "")},
	}); err == nil || !strings.Contains(err.Error(), EnvAPIKey) {
		t.Fatalf("missing key should name %s, got %v", EnvAPIKey, err)
	}
}

func TestClientFromEnv(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	if _, ok := ClientFromEnv(); ok {
		t.Fatal("ClientFromEnv without a key should report ok=false")
	}
	t.Setenv(EnvAPIKey, "env-key")
	client, ok := ClientFromEnv()
	if !ok {
		t.Fatal("ClientFromEnv with a key should report ok=true")
	}
	if client.APIKey != "env-key" {
		t.Fatalf("APIKey = %q", client.APIKey)
	}
}

func TestAnswerAccessorsRejectMismatch(t *testing.T) {
	resp := Response{Model: "jev-1.13.0", Answers: map[string]json.RawMessage{
		"dept": json.RawMessage(`{"type":"choice","choice":"billing","probabilities":{"billing":1},"confidence":0.9}`),
	}}
	if _, err := resp.Noul("dept"); err == nil {
		t.Fatal("decoding a choice answer as noul should fail")
	}
	if _, err := resp.Choice("missing"); err == nil {
		t.Fatal("missing answer should fail")
	}
	choice, err := resp.Choice("dept")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if choice.Choice != "billing" || choice.Confidence != 0.9 {
		t.Fatalf("unexpected choice answer: %+v", choice)
	}
}

func TestFakeBackendServesCannedAnswers(t *testing.T) {
	fake := &FakeBackend{
		Answers: map[string]json.RawMessage{
			"sev": MustAnswer(t, ScoreAnswer{
				Type:          TypeScore,
				Score:         1.6,
				Legend:        map[string]string{"0": "Calm", "1": "Frustrated", "2": "Very angry"},
				Probabilities: map[string]float64{"0": 0.05, "1": 0.3, "2": 0.65},
				Confidence:    0.78,
			}),
		},
	}
	resp, err := fake.Evaluate(context.Background(), Request{
		Model:     DefaultModel,
		State:     "my export broke",
		Questions: map[string]Question{"sev": ScoreQuestion("How frustrated?", []string{"Calm", "Frustrated", "Very angry"})},
	})
	if err != nil {
		t.Fatalf("FakeBackend: %v", err)
	}
	score, err := resp.Score("sev")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score.Score != 1.6 || score.Legend["2"] != "Very angry" {
		t.Fatalf("unexpected score answer: %+v", score)
	}
}
