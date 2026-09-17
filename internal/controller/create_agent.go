package controller

import (
	"strings"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	createAgentSkillName    = "create-agent"
	createAgentRoleLabel    = "control.anvil.hazyforge.io/role"
	createAgentAllowLabel   = "control.anvil.hazyforge.io/create-agent"
	createAgentWrapperName  = "anvil-desktop-wrapper"
	createAgentSkillWhen    = "Create a named teammate (AgentRunProfile + standing-chat thread when chat is enabled). Wrapper and manager only; peers request via requestPeer."
	createAgentSkillContent = "You have the baked-in create-agent tool. Only the Desktop Wrapper or a project manager may create AgentRunProfiles.\n\nWhen the operator wants a new teammate, call create-agent with a DNS-1123 name, a description or title, and optional systemPrompt or harnessProfileName. Desktop fulfills the POST (composition write). You do not receive OIDC tokens.\n\nAfter success, tell peers the new profile name so they can address it.\n\nIf you are not Wrapper/manager, do not call create-agent. Emit requestPeer STATUS_JSON instead:\n\nANVIL_AGENT_RUN_STATUS_JSON={\"type\":\"decision\",\"action\":\"requestPeer\",\"request\":\"create-agent\",\"name\":\"<dns-label>\",\"description\":\"<why>\",\"peerProfileName\":\"desktop-manager\"}\n\nPeers who call create-agent themselves are refused."
)

var createAgentManagerNames = map[string]struct{}{
	"desktop-manager":              {},
	"manager-hazy-trade":           {},
	"anvil-primaris-agent-manager": {},
	"hazy-trade-agent-manager":     {},
	createAgentWrapperName:         {},
	"wrapper":                      {},
}

func profileMayCreateAgent(profile *controlv1alpha1.AgentRunProfile) bool {
	if profile == nil {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(profile.Name))
	if _, ok := createAgentManagerNames[name]; ok {
		return true
	}
	if strings.HasSuffix(name, "-agent-manager") {
		return true
	}
	labels := profile.GetLabels()
	if labels == nil {
		return false
	}
	role := strings.ToLower(strings.TrimSpace(labels[createAgentRoleLabel]))
	if role == "wrapper" || role == "manager" {
		return true
	}
	return strings.ToLower(strings.TrimSpace(labels[createAgentAllowLabel])) == "allow"
}

// applyCreateAgentSkill injects the baked-in create-agent skill for Wrapper and
// manager profiles. Other principals cannot keep a skill of that name: peers
// request create-agent via requestPeer instead of POSTing AgentRunProfiles.
func (c *agentRunCapabilities) applyCreateAgentSkill(profile *controlv1alpha1.AgentRunProfile) {
	if profileMayCreateAgent(profile) {
		c.upsertSkill(controlv1alpha1.AgentRunSkillInjectionSpec{
			Name:        createAgentSkillName,
			Description: createAgentSkillWhen,
			Content:     createAgentSkillContent,
		})
		return
	}
	if index, exists := c.skillIndexes[createAgentSkillName]; exists {
		c.removeSkill(index)
	}
}
