package localrouting

import (
	"context"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Validate rejects an oversized or malformed inventory before calling a model.
// Descriptors are observations from the owning browser, not execution authority.
func Validate(request Request) error {
	invalid := subjectrouting.ErrInvalidInput
	if strings.TrimSpace(request.Content) == "" || !textWithin(request.Content, subjectrouting.MaxContentRunes) || len(request.Conversations) > MaxConversations || len(request.Context) > MaxContext {
		return invalid
	}
	ids := map[string]bool{}
	for _, conversation := range request.Conversations {
		if !identifier.MatchString(conversation.ID) || conversation.ID == subjectrouting.NewSubject || conversation.ID == subjectrouting.NoSubject || ids[conversation.ID] ||
			strings.TrimSpace(conversation.Label) == "" || !textWithin(conversation.Label, subjectrouting.MaxLabelRunes) || !textWithin(conversation.Summary, subjectrouting.MaxSummaryRunes) ||
			!identifier.MatchString(conversation.Harness) || !validTarget(conversation.Target, conversation.WSLDistro) || len(conversation.Aliases) > 8 ||
			!slices.Contains([]string{StatusReady, StatusRunning, StatusUnconfirmed}, conversation.Status) {
			return invalid
		}
		for _, alias := range conversation.Aliases {
			if !textWithin(alias, subjectrouting.MaxLabelRunes) {
				return invalid
			}
		}
		ids[conversation.ID] = true
	}
	for _, message := range request.Context {
		if (message.Role != "user" && message.Role != "assistant") || !textWithin(message.Content, 1000) || (message.ConversationID != "" && !identifier.MatchString(message.ConversationID)) {
			return invalid
		}
	}
	if request.DefaultHarness != "" && !identifier.MatchString(request.DefaultHarness) {
		return invalid
	}
	if request.TargetID != "" && request.TargetID != subjectrouting.NewSubject && request.TargetID != subjectrouting.NoSubject && !ids[request.TargetID] {
		return invalid
	}
	return nil
}

func textWithin(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit
}
func validTarget(target, distro string) bool {
	return (target == "native" && distro == "") || (target == "wsl" && strings.TrimSpace(distro) != "" && textWithin(distro, 128) && !strings.ContainsAny(distro, "\x00\r\n"))
}

// Route runs the shared provider boundary, then chooses an action using actual
// runtime observations. A plan is not a delivery receipt. The UI must recheck
// state and bindings immediately before invoking its existing send path.
func Route(ctx context.Context, provider subjectrouting.Provider, request Request, runtime Runtime) (result Result, err error) {
	started := time.Now()
	defer func() { result.LatencyMS = time.Since(started).Milliseconds() }()
	result.Action = Action{Kind: ActionClarify, Reason: "invalid_input"}
	result.Candidates = []subjectrouting.Candidate{}
	if err = Validate(request); err != nil {
		return result, err
	}
	if !validTarget(runtime.Target, runtime.WSLDistro) {
		return result, subjectrouting.ErrInvalidInput
	}
	if runtime.Now.IsZero() {
		runtime.Now = time.Now().UTC()
	}
	input, err := routingInput(request, runtime.Now)
	if err != nil {
		return result, err
	}
	result.Candidates = input.Candidates
	if request.TargetID != "" && ctx.Err() == nil {
		// Explicit selection is the same user-directed send as choosing a
		// conversation in Chat, and does not call a classifier at all.
		result.Decision = subjectrouting.Decision{Intent: "unclassified", Outcome: "manual", Reason: "user_selected"}
	} else {
		result.Decision, err = subjectrouting.Route(ctx, provider, input)
		if err != nil {
			result.Action.Reason = result.Decision.Reason
			return result, err
		}
	}
	target := result.Decision.SubjectChoice
	if request.TargetID != "" {
		// The explicit choice is a direct-send instruction, not a probability.
		target = request.TargetID
	} else if result.Decision.Outcome == subjectrouting.OutcomeClarify {
		result.Action.Reason = result.Decision.Reason
		return result, nil
	}
	if request.TargetID == "" && !intentCertain(result.Decision) {
		result.Action.Reason = "intent_uncertain"
		return result, nil
	}
	result.Action.TargetID = target
	if target == subjectrouting.NoSubject || target == subjectrouting.NewSubject {
		if result.Decision.Intent == subjectrouting.IntentStatus && intentCertain(result.Decision) {
			result.Action.Reason = "status_target_required"
			return result, nil
		}
		if len(request.Conversations) >= MaxConversations {
			result.Action.Reason = "inventory_full"
			return result, nil
		}
		if !slices.Contains(runtime.AvailableHarnesses, request.DefaultHarness) || request.DefaultHarness == "" {
			result.Action.Reason = "choose_available_harness"
			return result, nil
		}
		if blocked(request, runtime) {
			result.Action.Kind, result.Action.Reason = ActionWait, "local_turn_unfinished"
			return result, nil
		}
		if target == subjectrouting.NewSubject {
			result.Action.Kind, result.Action.Reason = ActionNewConversation, "new_subject"
		} else {
			result.Action.Kind, result.Action.Reason = ActionGeneral, "general_conversation"
		}
		return result, nil
	}
	for _, conversation := range request.Conversations {
		if conversation.ID != target {
			continue
		}
		if result.Decision.Intent == subjectrouting.IntentStatus && intentCertain(result.Decision) {
			result.Action.Kind, result.Action.Reason = ActionReadStatus, "read_saved_state"
			return result, nil
		}
		if conversation.Target != runtime.Target || conversation.WSLDistro != runtime.WSLDistro {
			result.Action.Reason = "runtime_changed"
			return result, nil
		}
		if !slices.Contains(runtime.AvailableHarnesses, conversation.Harness) {
			result.Action.Reason = "harness_unavailable"
			return result, nil
		}
		if blocked(request, runtime) {
			result.Action.Kind, result.Action.Reason = ActionWait, "local_turn_unfinished"
			return result, nil
		}
		result.Action.Kind, result.Action.Reason = ActionContinue, "continue_saved_conversation"
		return result, nil
	}
	result.Action.Reason = "target_unavailable"
	return result, nil
}

func blocked(request Request, runtime Runtime) bool {
	if runtime.Busy {
		return true
	}
	for _, conversation := range request.Conversations {
		if conversation.Status != StatusReady {
			return true
		}
	}
	return false
}

func intentCertain(decision subjectrouting.Decision) bool {
	if decision.Intent == subjectrouting.IntentUnclear || decision.IntentConfidence < subjectrouting.ConfidenceFloor {
		return false
	}
	chosen := decision.IntentProbabilities[decision.Intent]
	if chosen < subjectrouting.ConfidenceFloor {
		return false
	}
	for intent, probability := range decision.IntentProbabilities {
		if intent != decision.Intent && chosen-probability < subjectrouting.AmbiguityMargin {
			return false
		}
	}
	return true
}

func routingInput(request Request, now time.Time) (subjectrouting.Input, error) {
	conversations := append([]Conversation(nil), request.Conversations...)
	sort.SliceStable(conversations, func(i, j int) bool {
		if conversations[i].TouchedAt == nil {
			return false
		}
		return conversations[j].TouchedAt == nil || conversations[i].TouchedAt.After(*conversations[j].TouchedAt)
	})
	// Keep a recent resolved pronoun target in the window. An explicit current
	// name still takes precedence when SelectCandidates promotes named matches.
	priority := request.TargetID
	if priority == "" {
		for i := len(request.Context) - 1; i >= 0; i-- {
			if request.Context[i].ConversationID != "" {
				priority = request.Context[i].ConversationID
				break
			}
		}
	}
	candidates := make([]subjectrouting.Candidate, 0, len(conversations))
	for _, conversation := range conversations {
		candidate := subjectrouting.Candidate{ID: conversation.ID, Label: conversation.Label, Summary: conversation.Summary, Aliases: conversation.Aliases, Kind: "local_conversation", AgeSeconds: age(now, conversation.TouchedAt)}
		if candidate.ID == priority {
			candidates = append([]subjectrouting.Candidate{candidate}, candidates...)
		} else {
			candidates = append(candidates, candidate)
		}
	}
	input := subjectrouting.Input{Content: request.Content, Candidates: subjectrouting.SelectCandidates(request.Content, candidates)}
	for _, message := range request.Context {
		input.Recent = append(input.Recent, subjectrouting.RecentMessage{Role: message.Role, Content: message.Content, SubjectID: message.ConversationID, AgeSeconds: age(now, message.At)})
	}
	return subjectrouting.Prepare(input)
}

func age(now time.Time, at *time.Time) *float64 {
	if at == nil {
		return nil
	}
	seconds := max(0, now.Sub(*at).Seconds())
	return &seconds
}
