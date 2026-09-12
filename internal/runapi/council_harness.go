package runapi

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	councilHarnessWait      = 3 * time.Minute
	councilHarnessPoll      = time.Second
	councilDecisionLogLimit = 32 * 1024
)

func buildCouncilHarnessPrompt(state CouncilState, addressee, content string) string {
	addressee = canonicalCouncilProfile(addressee)
	var b strings.Builder
	if addressee == anvilAgentProfileName {
		b.WriteString("You are Anvil agent's council harness. You are non-deterministic: decide who confers, who waits, who is interrupted, and which members to delegate based on this operator message. Do not follow a fixed script.\n\n")
		b.WriteString("Return ONLY a JSON object with this shape:\n")
		b.WriteString(`{"mode":"controller","anvil":"your own words as Anvil agent","utterances":[{"profile":"council-researcher","role":"researcher","content":"their own words","kind":"utterance|interrupt","waitingOn":"optional profile","addressedTo":"optional profile or user"}],"memory":[{"key":"claim:inventory","value":"council-researcher"}],"delegates":[{"role":"researcher","profile":"council-researcher","harness":"council-research","intent":"observe","prompt":"work for this member","claim":"inventory"}]}`)
		b.WriteString("\nRules:\n")
		b.WriteString("- anvil is required and is Anvil agent speaking as itself.\n")
		b.WriteString("- utterances are distinct member voices. Members post as themselves.\n")
		b.WriteString("- If two members would claim the same work, mark the duplicate kind=interrupt and do not emit two delegates with that claim.\n")
		b.WriteString("- delegates are optional. Only create them when work should run. Mix harnesses when two members work.\n")
		b.WriteString("- Researcher work harness is council-research (custom). Implementer work harness is council-grok (grokBuild).\n")
		b.WriteString("- Do not speak as the human operator.\n\n")
	} else {
		b.WriteString("You are ")
		b.WriteString(councilDisplayName(addressee))
		b.WriteString(" in the Anvil council. This is a real conversation with you, not a canned script. Reply as yourself. You may send messages to Anvil agent and to other council members.\n\n")
		b.WriteString("Return ONLY a JSON object with this shape:\n")
		b.WriteString(`{"mode":"member","speaker":"`)
		b.WriteString(addressee)
		b.WriteString(`","reply":"your reply to the operator","messages":[{"to":"anvil-agent","content":"message you send to Anvil agent"},{"to":"council-implementer","content":"message you send to a peer"}]}`)
		b.WriteString("\nRules:\n")
		b.WriteString("- speaker must be ")
		b.WriteString(addressee)
		b.WriteString(". You only post as yourself.\n")
		b.WriteString("- reply is required and addresses the operator.\n")
		b.WriteString("- messages are optional outgoing lines you send to Anvil agent or peers. Use their profile names.\n")
		b.WriteString("- Do not invent work delegates. Anvil agent decides delegation when the operator talks to Anvil.\n")
		b.WriteString("- Do not speak as another member or as the human.\n\n")
	}
	b.WriteString("ADDRESSEE: ")
	b.WriteString(addressee)
	b.WriteString("\nNAMESPACE: ")
	b.WriteString(state.Namespace)
	b.WriteString("\n\nMEMBERS:\n")
	for _, member := range state.Members {
		fmt.Fprintf(&b, "- %s role=%s harness=%s backend=%s\n", member.ProfileName, member.Role, member.Harness, member.Backend)
	}
	if len(state.Knowledge) > 0 {
		b.WriteString("\nKNOWLEDGE:\n")
		for _, entry := range state.Knowledge {
			fmt.Fprintf(&b, "- %s: %s\n", entry.Title, trimRunes(entry.Body, 600))
		}
	}
	if len(state.Memory) > 0 {
		b.WriteString("\nSHARED MEMORY:\n")
		for _, entry := range state.Memory {
			fmt.Fprintf(&b, "- %s=%s\n", entry.Key, entry.Value)
		}
	}
	if recent := recentCouncilTranscript(state.Messages, 12); recent != "" {
		b.WriteString("\nRECENT ROOM:\n")
		b.WriteString(recent)
	}
	b.WriteString("\nUSER_MESSAGE:\n<<<\n")
	b.WriteString(strings.TrimSpace(content))
	b.WriteString("\n>>>\n")
	return b.String()
}

func recentCouncilTranscript(messages []CouncilMessageView, limit int) string {
	if limit <= 0 || len(messages) == 0 {
		return ""
	}
	start := 0
	if len(messages) > limit {
		start = len(messages) - limit
	}
	var b strings.Builder
	for _, message := range messages[start:] {
		name := firstNonEmpty(message.DisplayName, message.AuthorProfile, message.Role)
		if message.AddressedTo != "" {
			fmt.Fprintf(&b, "%s -> %s: %s\n", name, councilDisplayName(message.AddressedTo), trimRunes(message.Content, 240))
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", name, trimRunes(message.Content, 240))
	}
	return b.String()
}

func trimRunes(value string, max int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if max <= 0 || len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}

func (server *Server) startCouncilHarnessRun(ctx context.Context, namespace, addressee, prompt string) (CouncilDelegatedRun, error) {
	role := "controller"
	profile := anvilAgentProfileName
	harness := councilLLMHarness
	backend := "custom"
	intent := string(agentsv1alpha1.AgentRunIntentObserve)
	if addressee != anvilAgentProfileName {
		role = strings.TrimPrefix(addressee, "council-")
		if role == addressee {
			role = "member"
		}
		profile = addressee
	}
	name := newCouncilRunName(role)
	run, err := buildAgentRunFromCreateRequest(namespace, CreateAgentRunRequest{
		Name:               name,
		Prompt:             prompt,
		ProfileName:        profile,
		HarnessProfileName: harness,
		Intent:             intent,
		Purpose:            string(agentsv1alpha1.AgentRunPurposeManual),
		SourceKind:         "AgentCouncil",
		SourceName:         anvilCouncilName,
	})
	if err != nil {
		return CouncilDelegatedRun{}, err
	}
	run.Spec.Scope.ApplicationRef = &agentsv1alpha1.ApplicationReferenceSpec{Name: councilApplicationKey(namespace, role)}
	run.Spec.CouncilRef = &agentsv1alpha1.NamespacedObjectReference{Name: anvilCouncilName}
	if run.Labels == nil {
		run.Labels = map[string]string{}
	}
	run.Labels["control.anvil.hazyforge.io/council"] = anvilCouncilName
	run.Labels["control.anvil.hazyforge.io/council-role"] = role
	run.Labels["control.anvil.hazyforge.io/council-kind"] = "conversation"
	if err := server.writes.Create(ctx, run); err != nil {
		return CouncilDelegatedRun{}, err
	}
	return CouncilDelegatedRun{
		Name:               run.Name,
		Namespace:          run.Namespace,
		ProfileName:        profile,
		HarnessProfileName: harness,
		Backend:            backend,
		Role:               role,
		Application:        councilApplicationKey(namespace, role),
		Kind:               "conversation",
	}, nil
}

func (server *Server) waitCouncilHarnessOutput(ctx context.Context, namespace, name string) (string, error) {
	if server.councilHarnessWaiter != nil {
		return server.councilHarnessWaiter(ctx, namespace, name)
	}
	ticker := time.NewTicker(councilHarnessPoll)
	defer ticker.Stop()
	for {
		run := &agentsv1alpha1.AgentRun{}
		if err := server.runs.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, run); err != nil {
			return "", err
		}
		switch run.Status.Phase {
		case agentsv1alpha1.AgentRunPhaseSucceeded:
			output := strings.TrimSpace(run.Status.Output)
			if output == "" {
				output = strings.TrimSpace(server.readCouncilRunLogs(ctx, run))
			}
			if output == "" {
				return "", fmt.Errorf("harness run %s succeeded without output", name)
			}
			return output, nil
		case agentsv1alpha1.AgentRunPhaseFailed:
			message := strings.TrimSpace(run.Status.Error)
			if message == "" {
				message = "failed"
			}
			if extra := strings.TrimSpace(run.Status.Output); extra != "" {
				return "", fmt.Errorf("harness run %s failed: %s: %s", name, message, trimRunes(extra, 400))
			}
			return "", fmt.Errorf("harness run %s failed: %s", name, message)
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timed out waiting for harness run %s", name)
		case <-ticker.C:
		}
	}
}

func (server *Server) readCouncilRunLogs(ctx context.Context, run *agentsv1alpha1.AgentRun) string {
	if server.logs == nil || run == nil || run.Status.RunnerPodRef == nil || strings.TrimSpace(run.Status.RunnerPodRef.Name) == "" {
		return ""
	}
	stream, _, err := server.logs.Open(ctx, run, corev1.PodLogOptions{})
	if err != nil {
		return ""
	}
	defer stream.Close()
	raw, err := io.ReadAll(io.LimitReader(stream, councilDecisionLogLimit))
	if err != nil {
		return ""
	}
	return string(raw)
}

func newCouncilRunName(role string) string {
	role = strings.TrimSpace(strings.ToLower(role))
	if role == "" {
		role = "turn"
	}
	role = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, role)
	return fmt.Sprintf("council-%s-%d", strings.Trim(role, "-"), time.Now().UnixNano()%1_000_000_000)
}
