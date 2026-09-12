package runapi

import (
	"encoding/json"
	"fmt"
	"strings"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const councilDecisionPrefix = "ANVIL_COUNCIL_DECISION="

type councilHarnessDecision struct {
	Mode       string                    `json:"mode,omitempty"`
	Anvil      string                    `json:"anvil,omitempty"`
	Speaker    string                    `json:"speaker,omitempty"`
	Reply      string                    `json:"reply,omitempty"`
	Utterances []councilHarnessUtterance `json:"utterances,omitempty"`
	Messages   []councilHarnessPeerMsg   `json:"messages,omitempty"`
	Memory     []councilHarnessMemory    `json:"memory,omitempty"`
	Delegates  []councilHarnessDelegate  `json:"delegates,omitempty"`
}

type councilHarnessUtterance struct {
	Profile     string `json:"profile,omitempty"`
	Role        string `json:"role,omitempty"`
	Content     string `json:"content"`
	Kind        string `json:"kind,omitempty"`
	WaitingOn   string `json:"waitingOn,omitempty"`
	AddressedTo string `json:"addressedTo,omitempty"`
}

type councilHarnessPeerMsg struct {
	Profile string `json:"profile,omitempty"`
	To      string `json:"to,omitempty"`
	Content string `json:"content"`
}

type councilHarnessMemory struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type councilHarnessDelegate struct {
	Role    string `json:"role,omitempty"`
	Profile string `json:"profile,omitempty"`
	Harness string `json:"harness,omitempty"`
	Backend string `json:"backend,omitempty"`
	Intent  string `json:"intent,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
	Claim   string `json:"claim,omitempty"`
	Skip    bool   `json:"-"`
}

func parseCouncilHarnessDecision(output string) (councilHarnessDecision, error) {
	raw := extractCouncilDecisionJSON(output)
	if raw == "" {
		return councilHarnessDecision{}, fmt.Errorf("harness output did not include %s JSON", strings.TrimSuffix(councilDecisionPrefix, "="))
	}
	var decision councilHarnessDecision
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		return councilHarnessDecision{}, fmt.Errorf("decode harness decision: %w", err)
	}
	return decision, nil
}

func extractCouncilDecisionJSON(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return ""
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, councilDecisionPrefix); ok {
			if extracted := unwrapJSONObject(rest); extracted != "" {
				return extracted
			}
		}
	}
	return unwrapJSONObject(trimmed)
}

func unwrapJSONObject(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return ""
	}
	candidate := raw[start : end+1]
	if json.Valid([]byte(candidate)) {
		return candidate
	}
	return ""
}

func normalizeCouncilDecision(state CouncilState, addressee string, in councilHarnessDecision) (councilHarnessDecision, error) {
	addressee = canonicalCouncilProfile(addressee)
	members := councilMemberSet(state)
	if !members[addressee] {
		return councilHarnessDecision{}, fmt.Errorf("addressee %q is not in the council", addressee)
	}

	mode := strings.TrimSpace(strings.ToLower(in.Mode))
	if mode == "" {
		if addressee == anvilAgentProfileName {
			mode = "controller"
		} else {
			mode = "member"
		}
	}

	out := councilHarnessDecision{Mode: mode}
	for _, entry := range in.Memory {
		key := strings.TrimSpace(entry.Key)
		value := strings.TrimSpace(entry.Value)
		if key == "" || value == "" {
			continue
		}
		out.Memory = append(out.Memory, councilHarnessMemory{Key: key, Value: value})
	}

	if mode == "member" {
		speaker := canonicalCouncilProfile(firstNonEmpty(in.Speaker, addressee))
		if speaker != addressee {
			return councilHarnessDecision{}, fmt.Errorf("member harness must speak as %s, not %s", addressee, speaker)
		}
		out.Speaker = speaker
		reply := strings.TrimSpace(firstNonEmpty(in.Reply, in.Anvil))
		if reply == "" {
			for _, utterance := range in.Utterances {
				if canonicalCouncilProfile(utterance.Profile) == speaker && strings.TrimSpace(utterance.Content) != "" {
					reply = strings.TrimSpace(utterance.Content)
					break
				}
			}
		}
		if reply == "" {
			return councilHarnessDecision{}, fmt.Errorf("member harness produced no reply")
		}
		out.Reply = reply
		out.Utterances = append(out.Utterances, councilHarnessUtterance{
			Profile:     speaker,
			Role:        councilRoleFor(state, speaker),
			Content:     reply,
			Kind:        "utterance",
			AddressedTo: "user",
		})
		for _, message := range in.Messages {
			target := canonicalCouncilProfile(firstNonEmpty(message.To, message.Profile))
			content := strings.TrimSpace(message.Content)
			if content == "" || target == "" || target == speaker || !members[target] {
				continue
			}
			out.Messages = append(out.Messages, councilHarnessPeerMsg{Profile: target, To: target, Content: content})
			out.Utterances = append(out.Utterances, councilHarnessUtterance{
				Profile:     speaker,
				Role:        councilRoleFor(state, speaker),
				Content:     content,
				Kind:        "utterance",
				AddressedTo: target,
			})
		}
		if len(out.Utterances) == 0 {
			return councilHarnessDecision{}, fmt.Errorf("member harness produced no messages")
		}
		return out, nil
	}

	anvil := strings.TrimSpace(in.Anvil)
	if anvil == "" {
		for _, utterance := range in.Utterances {
			if canonicalCouncilProfile(utterance.Profile) == anvilAgentProfileName {
				anvil = strings.TrimSpace(utterance.Content)
				break
			}
		}
	}
	if anvil == "" {
		return councilHarnessDecision{}, fmt.Errorf("Anvil agent harness produced no controller text")
	}
	out.Anvil = anvil
	out.Utterances = append(out.Utterances, councilHarnessUtterance{
		Profile: anvilAgentProfileName,
		Role:    "controller",
		Content: anvil,
		Kind:    "utterance",
	})
	seenAnvil := true
	for _, utterance := range in.Utterances {
		profile := canonicalCouncilProfile(utterance.Profile)
		content := strings.TrimSpace(utterance.Content)
		if content == "" || !members[profile] {
			continue
		}
		if profile == anvilAgentProfileName && seenAnvil && strings.TrimSpace(utterance.Content) == anvil {
			continue
		}
		kind := strings.TrimSpace(utterance.Kind)
		if kind == "" {
			kind = "utterance"
		}
		if kind != "utterance" && kind != "interrupt" && kind != "status" {
			kind = "utterance"
		}
		addressedTo := canonicalCouncilProfile(utterance.AddressedTo)
		if addressedTo != "" && addressedTo != "user" && !members[addressedTo] {
			addressedTo = ""
		}
		waitingOn := canonicalCouncilProfile(utterance.WaitingOn)
		if waitingOn != "" && !members[waitingOn] {
			waitingOn = ""
		}
		out.Utterances = append(out.Utterances, councilHarnessUtterance{
			Profile:     profile,
			Role:        firstNonEmpty(utterance.Role, councilRoleFor(state, profile)),
			Content:     content,
			Kind:        kind,
			WaitingOn:   waitingOn,
			AddressedTo: addressedTo,
		})
	}

	claimed := map[string]string{}
	for _, entry := range out.Memory {
		if strings.HasPrefix(entry.Key, "claim:") {
			claimed[strings.TrimPrefix(entry.Key, "claim:")] = canonicalCouncilProfile(entry.Value)
		}
	}
	for _, delegate := range in.Delegates {
		profile := canonicalCouncilProfile(firstNonEmpty(delegate.Profile, profileForCouncilRole(delegate.Role)))
		if profile == "" || profile == anvilAgentProfileName || !members[profile] {
			continue
		}
		role := firstNonEmpty(delegate.Role, councilRoleFor(state, profile))
		claim := strings.TrimSpace(delegate.Claim)
		item := councilHarnessDelegate{
			Role:    role,
			Profile: profile,
			Harness: firstNonEmpty(delegate.Harness, state.memberWorkHarness(profile)),
			Backend: firstNonEmpty(delegate.Backend, councilBackendFor(profile, firstNonEmpty(delegate.Harness, state.memberWorkHarness(profile)))),
			Intent:  firstNonEmpty(delegate.Intent, councilIntentForRole(role)),
			Prompt:  strings.TrimSpace(delegate.Prompt),
			Claim:   claim,
		}
		if item.Prompt == "" {
			item.Prompt = "Council " + role + " work from Anvil agent's harness decision."
		}
		if item.Intent != string(agentsv1alpha1.AgentRunIntentObserve) &&
			item.Intent != string(agentsv1alpha1.AgentRunIntentProposeChange) &&
			item.Intent != string(agentsv1alpha1.AgentRunIntentFixTransient) &&
			item.Intent != string(agentsv1alpha1.AgentRunIntentCleanup) {
			item.Intent = councilIntentForRole(role)
		}
		if claim != "" {
			if owner, ok := claimed[claim]; ok && owner != profile {
				item.Skip = true
				markCouncilInterrupt(&out, profile)
			} else {
				claimed[claim] = profile
			}
		}
		out.Delegates = append(out.Delegates, item)
	}
	if len(out.Utterances) == 0 {
		return councilHarnessDecision{}, fmt.Errorf("Anvil agent harness produced no conferral")
	}
	return out, nil
}

func markCouncilInterrupt(decision *councilHarnessDecision, profile string) {
	for i := range decision.Utterances {
		if decision.Utterances[i].Profile == profile {
			decision.Utterances[i].Kind = "interrupt"
		}
	}
}

func councilMemberSet(state CouncilState) map[string]bool {
	out := map[string]bool{anvilAgentProfileName: true}
	for _, member := range state.Members {
		if name := strings.TrimSpace(member.ProfileName); name != "" {
			out[name] = true
		}
	}
	out[councilResearcherProfile] = true
	out[councilImplementerProfile] = true
	return out
}

func councilRoleFor(state CouncilState, profile string) string {
	for _, member := range state.Members {
		if member.ProfileName == profile && member.Role != "" {
			return member.Role
		}
	}
	switch profile {
	case anvilAgentProfileName:
		return "controller"
	case councilResearcherProfile:
		return "researcher"
	case councilImplementerProfile:
		return "implementer"
	default:
		return "member"
	}
}

func profileForCouncilRole(role string) string {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "controller", "anvil", "anvil-agent":
		return anvilAgentProfileName
	case "researcher":
		return councilResearcherProfile
	case "implementer":
		return councilImplementerProfile
	default:
		return ""
	}
}

func councilIntentForRole(role string) string {
	if strings.EqualFold(role, "implementer") {
		return string(agentsv1alpha1.AgentRunIntentProposeChange)
	}
	return string(agentsv1alpha1.AgentRunIntentObserve)
}

func councilBackendFor(profile, harness string) string {
	if profile == councilImplementerProfile || harness == councilGrokHarness {
		return "grokBuild"
	}
	if profile == anvilAgentProfileName || harness == councilLLMHarness {
		return "custom"
	}
	return "custom"
}

func canonicalCouncilProfile(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "user", "operator", "human", "you":
		return "user"
	case "anvil", "anvil agent", "anvil-agent", "controller":
		return anvilAgentProfileName
	case "researcher", "council-researcher", "council researcher":
		return councilResearcherProfile
	case "implementer", "council-implementer", "council implementer":
		return councilImplementerProfile
	default:
		return value
	}
}
