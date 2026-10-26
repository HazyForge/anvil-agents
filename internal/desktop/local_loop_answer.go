package desktop

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
)

// Answers target an explicit durable interaction. They never enter the general
// message router or authorize a newly inferred conversation/action.
func (s *Server) handleLocalLoopAnswer(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		RequestID string `json:"requestId"`
		Version   int    `json:"version"`
		Content   string `json:"content"`
	}
	if !decodeLocalManagerRequest(writer, request, &body, 12<<10) {
		return
	}
	if _, err := uuid.Parse(body.RequestID); err != nil || body.Version < 1 || !utf8.ValidString(body.Content) || strings.TrimSpace(body.Content) == "" || utf8.RuneCountInString(body.Content) > 2000 {
		writeError(writer, http.StatusBadRequest, "invalid_answer", "answer requires a request ID, decision version and at most 2,000 characters")
		return
	}
	loop, err := s.getLoop()
	if err != nil {
		writeLoopError(writer, err)
		return
	}
	decisionID := request.PathValue("id")
	if choice, found, replayErr := loop.decisionReplay(decisionID, body.RequestID); replayErr != nil {
		writeLoopError(writer, replayErr)
		return
	} else if found {
		writeJSON(writer, http.StatusOK, map[string]any{"snapshot": loop.snapshot(), "resolution": subjectrouting.AnswerResolution{Choice: choice, Source: "recorded", Reason: "replayed_answer"}})
		return
	}
	snapshot := loop.snapshot()
	var decision *loopDecision
	for i := range snapshot.Decisions {
		if snapshot.Decisions[i].ID == decisionID {
			decision = &snapshot.Decisions[i]
			break
		}
	}
	if decision == nil {
		writeError(writer, http.StatusNotFound, "decision_not_found", "this pending question is no longer available")
		return
	}
	if decision.State != "pending" || decision.Version != body.Version {
		writeError(writer, http.StatusConflict, "decision_changed", "this decision changed; read its current state before answering")
		return
	}
	input := subjectrouting.AnswerInput{Content: body.Content, Question: decision.Message}
	for _, choice := range decision.Choices {
		input.Choices = append(input.Choices, choice.ID)
	}
	provider, _ := s.localManagerProvider().(subjectrouting.AnswerProvider)
	resolution, _ := subjectrouting.ResolveAnswer(request.Context(), provider, input)
	if resolution.Choice == "" {
		writeJSON(writer, http.StatusOK, map[string]any{"snapshot": loop.snapshot(), "resolution": resolution})
		return
	}
	// The loop checks the same version again inside its durable transaction;
	// another tab answering while classification ran cannot be overwritten.
	updated, err := s.loopDecisionChoice(request.Context(), decisionID, body.RequestID, resolution.Choice, body.Version)
	if err != nil {
		writeLoopError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"snapshot": updated, "resolution": resolution})
}
