package standing

import (
	"strings"
	"testing"
	"time"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestClaimRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	claim := Claim{TurnID: "turn-1", Owner: "api-a/123", AtUnix: now.Unix()}
	raw, err := EncodeClaim(claim)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "api-a") == false {
		t.Fatalf("encoded claim = %q, want the owner carried", raw)
	}
	parsed, err := ParseClaim(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed != claim {
		t.Fatalf("parsed = %+v, want %+v", parsed, claim)
	}
}

func TestClaimEncodeRejectsBlankFields(t *testing.T) {
	t.Parallel()

	for name, claim := range map[string]Claim{
		"blank turn":  {Owner: "api-a/1", AtUnix: time.Now().Unix()},
		"blank owner": {TurnID: "turn-1", AtUnix: time.Now().Unix()},
		"zero time":   {TurnID: "turn-1", Owner: "api-a/1"},
	} {
		if _, err := EncodeClaim(claim); err == nil {
			t.Fatalf("%s: EncodeClaim succeeded, want an error", name)
		}
	}
}

func TestParseClaimRejectsMalformed(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"empty":        "",
		"not json":     "turn-1",
		"wrong shape":  `{"foo":"bar"}`,
		"blank turn":   `{"turn":"  ","owner":"api-a/1","at":1700000000}`,
		"blank owner":  `{"turn":"turn-1","owner":"  ","at":1700000000}`,
		"zero time":    `{"turn":"turn-1","owner":"api-a/1","at":0}`,
		"missing time": `{"turn":"turn-1","owner":"api-a/1"}`,
	} {
		if _, err := ParseClaim(raw); err == nil {
			t.Fatalf("%s: ParseClaim(%q) succeeded, want an error", name, raw)
		}
	}
}

func TestClaimQuotingSurvivesFraming(t *testing.T) {
	t.Parallel()

	claim := Claim{TurnID: "turn-1", Owner: `api-"quoted"/1`, AtUnix: 1700000000}
	raw, err := EncodeClaim(claim)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseClaim(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Owner != claim.Owner {
		t.Fatalf("owner = %q, want %q", parsed.Owner, claim.Owner)
	}
}

func TestClaimForTurn(t *testing.T) {
	t.Parallel()

	now := time.Now()
	live, err := EncodeClaim(Claim{TurnID: "turn-1", Owner: "api-a/1", AtUnix: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := EncodeClaim(Claim{TurnID: "turn-1", Owner: "api-a/1", AtUnix: now.Add(-10 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	other, err := EncodeClaim(Claim{TurnID: "turn-2", Owner: "api-a/1", AtUnix: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}

	key := agentsv1alpha1.AgentRunStandingClaimAnnotation
	for name, annotations := range map[string]map[string]string{
		"nil":              nil,
		"empty":            {},
		"blank value":      {key: "  "},
		"malformed":        {key: "not-json"},
		"other turn":       {key: other},
		"stale":            {key: stale},
		"unrelated":        {"example.com/other": "value"},
		"live with extras": {key: live, "example.com/other": "value"},
	} {
		_, ok := ClaimForTurn(annotations, "turn-1", now, ClaimTTL)
		want := name == "live with extras"
		if ok != want {
			t.Fatalf("%s: live = %v, want %v", name, ok, want)
		}
	}
	if _, ok := ClaimForTurn(map[string]string{key: live}, "  ", now, ClaimTTL); ok {
		t.Fatal("blank turn ID matched, want no match")
	}
	if _, ok := ClaimForTurn(map[string]string{key: live}, "turn-1", now, 0); ok {
		t.Fatal("non-positive TTL matched, want fail-closed")
	}
	if _, ok := ClaimForTurn(map[string]string{key: live}, "turn-1", time.Time{}, ClaimTTL); ok {
		t.Fatal("zero now matched, want fail-closed")
	}
}

func TestClaimFreshBounds(t *testing.T) {
	t.Parallel()

	now := time.Now()
	fresh := Claim{TurnID: "turn-1", Owner: "api-a/1", AtUnix: now.Unix()}
	if !fresh.Fresh(now, ClaimTTL) {
		t.Fatal("fresh claim reads expired")
	}
	aged := Claim{TurnID: "turn-1", Owner: "api-a/1", AtUnix: now.Add(-ClaimTTL - time.Second).Unix()}
	if aged.Fresh(now, ClaimTTL) {
		t.Fatal("claim past TTL reads fresh")
	}
	skewed := Claim{TurnID: "turn-1", Owner: "api-a/1", AtUnix: now.Add(time.Minute).Unix()}
	if !skewed.Fresh(now, ClaimTTL) {
		t.Fatal("small future skew reads expired")
	}
}

func TestNewOwnerIDDistinguishesProcesses(t *testing.T) {
	t.Parallel()

	owner := NewOwnerID()
	if strings.TrimSpace(owner) == "" || !strings.Contains(owner, "/") {
		t.Fatalf("owner = %q, want hostname/pid", owner)
	}
	if _, err := EncodeClaim(Claim{TurnID: "turn-1", Owner: owner, AtUnix: time.Now().Unix()}); err != nil {
		t.Fatalf("owner is not claim-safe: %v", err)
	}
}
