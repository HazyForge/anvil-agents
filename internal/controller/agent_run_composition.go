package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func (r *AgentRunReconciler) resolveAgentRunComposition(ctx context.Context, obj *controlv1alpha1.AgentRun) (*controlv1alpha1.AgentRun, *controlv1alpha1.AgentRunResolvedCompositionStatus, controlv1alpha1.AgentRunPhase, string, string, error) {
	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}

	profile, phase, reason, message, err := resolveAgentRunProfileObject(ctx, reader, obj)
	if err != nil || phase != "" {
		return obj.DeepCopy(), nil, phase, reason, message, err
	}
	effective := obj.DeepCopy()
	if profile != nil {
		effective = agentRunApplyProfile(obj, profile)
	}

	harnessRef := selectedAgentHarnessProfileRef(profile, obj)
	harnessProfile, phase, reason, message, err := resolveAgentHarnessProfileObject(ctx, reader, obj.Namespace, harnessRef)
	if err != nil || phase != "" {
		return effective, nil, phase, reason, message, err
	}
	effective.Spec.HarnessProfileRef = deepCopyNamespacedObjectReference(harnessRef)
	if harnessProfile != nil {
		effective.Spec.Harness = agentRunHarnessWithProfile(profile, obj, harnessProfile)
	}

	capabilities := newAgentRunCapabilities()
	resolvedSkillSets, phase, reason, message, err := r.resolveAgentSkillComposition(ctx, reader, obj, profile, effective, capabilities)
	if err != nil || phase != "" {
		return effective, nil, phase, reason, message, err
	}
	resolvedToolSets, phase, reason, message, err := r.resolveAgentToolComposition(ctx, reader, obj, profile, effective, capabilities)
	if err != nil || phase != "" {
		return effective, nil, phase, reason, message, err
	}
	canonical, phase, reason, message, err := r.resolveCanonicalCapabilities(ctx, reader, obj, profile, effective, capabilities, resolvedSkillSets, resolvedToolSets)
	if err != nil || phase != "" {
		return effective, nil, phase, reason, message, err
	}

	// Inline v1alpha1 entries remain the final compatibility overlay. Later
	// entries with the same name intentionally replace set-provided entries.
	for _, skill := range effective.Spec.Harness.SkillInjections {
		capabilities.upsertSkill(skill)
	}
	for _, tool := range effective.Spec.Harness.Tools {
		capabilities.upsertTool(tool)
	}
	for _, subagent := range effective.Spec.Harness.Subagents {
		capabilities.upsertSubagent(subagent)
	}
	for _, server := range effective.Spec.Harness.MCPServers {
		if reason, message := capabilities.overlayMCPServer(server); reason != "" {
			return effective, nil, controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
		}
	}
	effective.Spec.Harness.SkillInjections = capabilities.skills
	effective.Spec.Harness.Tools = capabilities.tools
	effective.Spec.Harness.Subagents = capabilities.subagents
	effective.Spec.Harness.MCPServers = capabilities.mcpServers

	status := &controlv1alpha1.AgentRunResolvedCompositionStatus{}
	if profile != nil {
		status.ProfileRef = resolvedObjectReferenceStatus(profile, digestJSON(profile.Spec))
	}
	if harnessProfile != nil {
		status.HarnessProfileRef = resolvedObjectReferenceStatus(harnessProfile, digestJSON(harnessProfile.Spec))
	}
	for _, skillSet := range resolvedSkillSets {
		if legacySkillSetStillContributes(skillSet, canonical) {
			status.SkillSetRefs = append(status.SkillSetRefs, *resolvedObjectReferenceStatus(skillSet, digestJSON(skillSet.Spec)))
		}
	}
	if !canonical.replacedTools {
		for _, toolSet := range resolvedToolSets {
			status.ToolSetRefs = append(status.ToolSetRefs, *resolvedObjectReferenceStatus(toolSet, digestJSON(toolSet.Spec)))
		}
	}
	for _, skillSet := range canonical.skillSets {
		status.SkillSetRefs = appendResolvedReferenceOnce(status.SkillSetRefs, skillSet, digestJSON(skillSet.Spec))
	}
	for _, toolSet := range canonical.toolSets {
		status.ToolSetRefs = appendResolvedReferenceOnce(status.ToolSetRefs, toolSet, digestJSON(toolSet.Spec))
	}
	for _, skill := range canonical.skills {
		status.SkillRefs = appendResolvedReferenceOnce(status.SkillRefs, skill, digestJSON(skill.Spec))
	}
	for _, tool := range canonical.tools {
		status.ToolRefs = appendResolvedReferenceOnce(status.ToolRefs, tool, digestJSON(tool.Spec))
	}
	for _, mcpSet := range canonical.mcpSets {
		status.MCPSetRefs = appendResolvedReferenceOnce(status.MCPSetRefs, mcpSet, digestJSON(mcpSet.Spec))
	}
	for _, server := range canonical.mcpServers {
		status.MCPServerRefs = appendResolvedReferenceOnce(status.MCPServerRefs, server, digestJSON(server.Spec))
	}
	status.Scope = agentRunResolvedScopeStatus(effective.Spec.Scope)
	status.EffectiveDigest = digestJSON(effective.Spec)
	effective.Status.ResolvedComposition = status.DeepCopy()
	return effective, status, "", "", "", nil
}

func legacySkillSetStillContributes(skillSet *controlv1alpha1.AgentSkillSet, canonical resolvedCanonicalCapabilities) bool {
	if skillSet == nil {
		return false
	}
	return (!canonical.replacedSkills && (len(skillSet.Spec.Skills) > 0 || len(skillSet.Spec.SkillRefs) > 0)) ||
		(!canonical.replacedTools && len(skillSet.Spec.Tools) > 0) ||
		len(skillSet.Spec.Subagents) > 0
}

func agentRunResolvedScopeStatus(scope controlv1alpha1.AgentRunScopeSpec) *controlv1alpha1.AgentRunResolvedScopeStatus {
	status := &controlv1alpha1.AgentRunResolvedScopeStatus{}
	if scope.ApplicationRef != nil {
		status.Application = strings.TrimSpace(scope.ApplicationRef.Name)
	}
	if scope.ApplicationTargetRef != nil {
		status.ApplicationTarget = strings.TrimSpace(scope.ApplicationTargetRef.Name)
	}
	if repo := normalizeAgentRunRepository(scope.Repository); repo != nil {
		status.Repository = repo.Name
		status.RepositoryRef = repo.Ref
		status.DestinationBranch = repo.DestinationBranch
		status.AllowedBranches = append([]string(nil), repo.AllowedBranches...)
	}
	if status.Application == "" && status.ApplicationTarget == "" && status.Repository == "" && status.RepositoryRef == "" && status.DestinationBranch == "" && len(status.AllowedBranches) == 0 {
		return nil
	}
	return status
}

func resolveAgentRunProfileObject(ctx context.Context, reader client.Reader, obj *controlv1alpha1.AgentRun) (*controlv1alpha1.AgentRunProfile, controlv1alpha1.AgentRunPhase, string, string, error) {
	if obj.Spec.ProfileRef == nil {
		return nil, "", "", "", nil
	}
	name := strings.TrimSpace(obj.Spec.ProfileRef.Name)
	if name == "" {
		return nil, controlv1alpha1.AgentRunPhaseFailed, "InvalidProfileRef", "spec.profileRef.name is required when profileRef is set.", nil
	}
	namespace := firstNonEmpty(strings.TrimSpace(obj.Spec.ProfileRef.Namespace), obj.Namespace)
	if namespace != obj.Namespace {
		return nil, controlv1alpha1.AgentRunPhaseFailed, "CrossNamespaceProfileRef", "AgentRun profileRef must reference an AgentRunProfile in the AgentRun namespace.", nil
	}
	profile := &controlv1alpha1.AgentRunProfile{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, profile); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, controlv1alpha1.AgentRunPhaseNeedsHuman, "ProfileNotFound", fmt.Sprintf("AgentRunProfile %s/%s was not found.", namespace, name), nil
		}
		return nil, "", "", "", err
	}
	return profile, "", "", "", nil
}

func selectedAgentHarnessProfileRef(profile *controlv1alpha1.AgentRunProfile, obj *controlv1alpha1.AgentRun) *controlv1alpha1.NamespacedObjectReference {
	if obj.Spec.HarnessProfileRef != nil {
		return obj.Spec.HarnessProfileRef
	}
	if profile != nil {
		return profile.Spec.HarnessProfileRef
	}
	return nil
}

func resolveAgentHarnessProfileObject(ctx context.Context, reader client.Reader, namespace string, ref *controlv1alpha1.NamespacedObjectReference) (*controlv1alpha1.AgentHarnessProfile, controlv1alpha1.AgentRunPhase, string, string, error) {
	if ref == nil {
		return nil, "", "", "", nil
	}
	name := strings.TrimSpace(ref.Name)
	if name == "" {
		return nil, controlv1alpha1.AgentRunPhaseFailed, "InvalidHarnessProfileRef", "spec.harnessProfileRef.name is required when harnessProfileRef is set.", nil
	}
	refNamespace := firstNonEmpty(strings.TrimSpace(ref.Namespace), namespace)
	if refNamespace != namespace {
		return nil, controlv1alpha1.AgentRunPhaseFailed, "CrossNamespaceHarnessProfileRef", "AgentRun harnessProfileRef must reference an AgentHarnessProfile in the AgentRun namespace.", nil
	}
	profile := &controlv1alpha1.AgentHarnessProfile{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, profile); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, controlv1alpha1.AgentRunPhaseNeedsHuman, "HarnessProfileNotFound", fmt.Sprintf("AgentHarnessProfile %s/%s was not found.", namespace, name), nil
		}
		return nil, "", "", "", err
	}
	return profile, "", "", "", nil
}

func agentRunHarnessWithProfile(profile *controlv1alpha1.AgentRunProfile, obj *controlv1alpha1.AgentRun, harnessProfile *controlv1alpha1.AgentHarnessProfile) controlv1alpha1.AgentRunHarnessSpec {
	profileHarness := controlv1alpha1.AgentRunHarnessSpec{}
	if profile != nil {
		profileHarness = profile.Spec.Harness
	}
	out := agentRunMergeHarness(profileHarness, obj.Spec.Harness)
	backend := *harnessProfile.Spec.Backend.DeepCopy()
	execution := *harnessProfile.Spec.Execution.DeepCopy()
	// A run-local ref is an atomic runtime swap. Profile inline runtime fields
	// belong to the profile-selected harness and must not leak into the new one.
	if obj.Spec.HarnessProfileRef == nil {
		backend = agentRunMergeBackend(backend, profileHarness.Backend)
		execution = agentRunMergeExecution(execution, profileHarness.Execution)
	}
	out.Backend = agentRunMergeBackend(backend, obj.Spec.Harness.Backend)
	out.Execution = agentRunMergeExecution(execution, obj.Spec.Harness.Execution)
	return out
}

func agentRunMergeSkillComposition(profile, run *controlv1alpha1.AgentSkillCompositionSpec) *controlv1alpha1.AgentSkillCompositionSpec {
	if profile == nil && run == nil {
		return nil
	}
	if run != nil && run.Mode == controlv1alpha1.AgentSkillCompositionReplace {
		return run.DeepCopy()
	}
	out := &controlv1alpha1.AgentSkillCompositionSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run != nil {
		out.Refs = append(out.Refs, run.Refs...)
		out.Overrides = append(out.Overrides, run.Overrides...)
		if run.Mode != "" {
			out.Mode = run.Mode
		}
	}
	return out
}

func agentRunMergeToolComposition(profile, run *controlv1alpha1.AgentToolCompositionSpec) *controlv1alpha1.AgentToolCompositionSpec {
	if profile == nil && run == nil {
		return nil
	}
	if run != nil && run.Mode == controlv1alpha1.AgentToolCompositionReplace {
		return run.DeepCopy()
	}
	out := &controlv1alpha1.AgentToolCompositionSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run != nil {
		out.Refs = append(out.Refs, run.Refs...)
		if run.Mode != "" {
			out.Mode = run.Mode
		}
	}
	return out
}

func agentRunMergeCapabilities(profile, run *controlv1alpha1.AgentCapabilitiesSpec) *controlv1alpha1.AgentCapabilitiesSpec {
	if profile == nil && run == nil {
		return nil
	}
	out := &controlv1alpha1.AgentCapabilitiesSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run == nil {
		return out
	}
	out.Skills = mergeCanonicalSkillComposition(out.Skills, run.Skills)
	out.Tools = mergeCanonicalToolComposition(out.Tools, run.Tools)
	out.MCPServers = mergeCanonicalMCPComposition(out.MCPServers, run.MCPServers)
	return out
}

func mergeCanonicalSkillComposition(profile, run *controlv1alpha1.AgentSkillCapabilityComposition) *controlv1alpha1.AgentSkillCapabilityComposition {
	if profile == nil && run == nil {
		return nil
	}
	if run != nil && run.Mode == controlv1alpha1.AgentCapabilityCompositionReplace {
		return run.DeepCopy()
	}
	out := &controlv1alpha1.AgentSkillCapabilityComposition{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run != nil {
		out.Selections = append(out.Selections, run.Selections...)
		out.Overrides = append(out.Overrides, run.Overrides...)
		if run.Mode != "" {
			out.Mode = run.Mode
		}
	}
	return out
}

func mergeCanonicalToolComposition(profile, run *controlv1alpha1.AgentToolCapabilityComposition) *controlv1alpha1.AgentToolCapabilityComposition {
	if profile == nil && run == nil {
		return nil
	}
	if run != nil && run.Mode == controlv1alpha1.AgentCapabilityCompositionReplace {
		return run.DeepCopy()
	}
	out := &controlv1alpha1.AgentToolCapabilityComposition{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run != nil {
		out.Selections = append(out.Selections, run.Selections...)
		if run.Mode != "" {
			out.Mode = run.Mode
		}
	}
	return out
}

func mergeCanonicalMCPComposition(profile, run *controlv1alpha1.AgentMCPCapabilityComposition) *controlv1alpha1.AgentMCPCapabilityComposition {
	if profile == nil && run == nil {
		return nil
	}
	if run != nil && run.Mode == controlv1alpha1.AgentCapabilityCompositionReplace {
		return run.DeepCopy()
	}
	out := &controlv1alpha1.AgentMCPCapabilityComposition{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run != nil {
		out.Selections = append(out.Selections, run.Selections...)
		if run.Mode != "" {
			out.Mode = run.Mode
		}
	}
	return out
}

func (r *AgentRunReconciler) resolveAgentSkillComposition(ctx context.Context, reader client.Reader, obj *controlv1alpha1.AgentRun, profile *controlv1alpha1.AgentRunProfile, effective *controlv1alpha1.AgentRun, capabilities *agentRunCapabilities) ([]*controlv1alpha1.AgentSkillSet, controlv1alpha1.AgentRunPhase, string, string, error) {
	var profileComposition *controlv1alpha1.AgentSkillCompositionSpec
	if profile != nil {
		profileComposition = profile.Spec.SkillSets
	}
	runComposition := obj.Spec.SkillSets
	effective.Spec.SkillSets = agentRunMergeSkillComposition(profileComposition, runComposition)
	if profileComposition == nil && runComposition == nil {
		return nil, "", "", "", nil
	}

	resolved := []*controlv1alpha1.AgentSkillSet{}
	seenRefs := map[string]struct{}{}
	applyComposition := func(composition *controlv1alpha1.AgentSkillCompositionSpec) (controlv1alpha1.AgentRunPhase, string, string, error) {
		if composition == nil {
			return "", "", "", nil
		}
		for _, ref := range composition.Refs {
			name := strings.TrimSpace(ref.Name)
			if name == "" {
				return controlv1alpha1.AgentRunPhaseFailed, "InvalidSkillSetRef", "skillSets.refs[].name must be set.", nil
			}
			namespace := firstNonEmpty(strings.TrimSpace(ref.Namespace), obj.Namespace)
			if namespace != obj.Namespace {
				return controlv1alpha1.AgentRunPhaseFailed, "CrossNamespaceSkillSetRef", "AgentSkillSet refs must stay in the AgentRun namespace.", nil
			}
			key := namespace + "/" + name
			if _, exists := seenRefs[key]; exists {
				return controlv1alpha1.AgentRunPhaseFailed, "DuplicateSkillSetRef", fmt.Sprintf("AgentSkillSet %s is selected more than once.", key), nil
			}
			seenRefs[key] = struct{}{}
			skillSet := &controlv1alpha1.AgentSkillSet{}
			if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, skillSet); err != nil {
				if apierrors.IsNotFound(err) {
					return controlv1alpha1.AgentRunPhaseNeedsHuman, "SkillSetNotFound", fmt.Sprintf("AgentSkillSet %s was not found.", key), nil
				}
				return "", "", "", err
			}
			if reason, message := capabilities.applySkillSet(skillSet); reason != "" {
				return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
			}
			resolved = append(resolved, skillSet)
		}
		if reason, message := capabilities.applyOverrides(composition.Overrides); reason != "" {
			return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
		}
		return "", "", "", nil
	}

	if runComposition == nil || runComposition.Mode != controlv1alpha1.AgentSkillCompositionReplace {
		if phase, reason, message, err := applyComposition(profileComposition); err != nil || phase != "" {
			return nil, phase, reason, message, err
		}
	}
	if phase, reason, message, err := applyComposition(runComposition); err != nil || phase != "" {
		return nil, phase, reason, message, err
	}

	return resolved, "", "", "", nil
}

func (r *AgentRunReconciler) resolveAgentToolComposition(ctx context.Context, reader client.Reader, obj *controlv1alpha1.AgentRun, profile *controlv1alpha1.AgentRunProfile, effective *controlv1alpha1.AgentRun, capabilities *agentRunCapabilities) ([]*controlv1alpha1.AgentToolSet, controlv1alpha1.AgentRunPhase, string, string, error) {
	var profileComposition *controlv1alpha1.AgentToolCompositionSpec
	if profile != nil {
		profileComposition = profile.Spec.ToolSets
	}
	runComposition := obj.Spec.ToolSets
	effective.Spec.ToolSets = agentRunMergeToolComposition(profileComposition, runComposition)
	if profileComposition == nil && runComposition == nil {
		return nil, "", "", "", nil
	}

	resolved := []*controlv1alpha1.AgentToolSet{}
	seenRefs := map[string]struct{}{}
	applyComposition := func(composition *controlv1alpha1.AgentToolCompositionSpec) (controlv1alpha1.AgentRunPhase, string, string, error) {
		if composition == nil {
			return "", "", "", nil
		}
		for _, ref := range composition.Refs {
			name := strings.TrimSpace(ref.Name)
			if name == "" {
				return controlv1alpha1.AgentRunPhaseFailed, "InvalidToolSetRef", "toolSets.refs[].name must be set.", nil
			}
			namespace := firstNonEmpty(strings.TrimSpace(ref.Namespace), obj.Namespace)
			if namespace != obj.Namespace {
				return controlv1alpha1.AgentRunPhaseFailed, "CrossNamespaceToolSetRef", "AgentToolSet refs must stay in the AgentRun namespace.", nil
			}
			key := namespace + "/" + name
			if _, exists := seenRefs[key]; exists {
				return controlv1alpha1.AgentRunPhaseFailed, "DuplicateToolSetRef", fmt.Sprintf("AgentToolSet %s is selected more than once.", key), nil
			}
			seenRefs[key] = struct{}{}
			toolSet := &controlv1alpha1.AgentToolSet{}
			if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, toolSet); err != nil {
				if apierrors.IsNotFound(err) {
					return controlv1alpha1.AgentRunPhaseNeedsHuman, "ToolSetNotFound", fmt.Sprintf("AgentToolSet %s was not found.", key), nil
				}
				return "", "", "", err
			}
			if reason, message := capabilities.applyToolSet(toolSet); reason != "" {
				return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
			}
			resolved = append(resolved, toolSet)
		}
		return "", "", "", nil
	}

	if runComposition == nil || runComposition.Mode != controlv1alpha1.AgentToolCompositionReplace {
		if phase, reason, message, err := applyComposition(profileComposition); err != nil || phase != "" {
			return nil, phase, reason, message, err
		}
	}
	if phase, reason, message, err := applyComposition(runComposition); err != nil || phase != "" {
		return nil, phase, reason, message, err
	}
	return resolved, "", "", "", nil
}

type resolvedCanonicalCapabilities struct {
	skillSets      []*controlv1alpha1.AgentSkillSet
	toolSets       []*controlv1alpha1.AgentToolSet
	skills         []*controlv1alpha1.AgentSkill
	tools          []*controlv1alpha1.AgentTool
	mcpSets        []*controlv1alpha1.AgentMCPSet
	mcpServers     []*controlv1alpha1.AgentMCPServer
	replacedSkills bool
	replacedTools  bool
	replacedMCP    bool
}

func (r *AgentRunReconciler) resolveCanonicalCapabilities(
	ctx context.Context,
	reader client.Reader,
	obj *controlv1alpha1.AgentRun,
	profile *controlv1alpha1.AgentRunProfile,
	effective *controlv1alpha1.AgentRun,
	capabilities *agentRunCapabilities,
	legacySkillSets []*controlv1alpha1.AgentSkillSet,
	legacyToolSets []*controlv1alpha1.AgentToolSet,
) (resolvedCanonicalCapabilities, controlv1alpha1.AgentRunPhase, string, string, error) {
	resolved := resolvedCanonicalCapabilities{}
	var profileCapabilities *controlv1alpha1.AgentCapabilitiesSpec
	if profile != nil {
		profileCapabilities = profile.Spec.Capabilities
	}
	runCapabilities := obj.Spec.Capabilities
	effective.Spec.Capabilities = agentRunMergeCapabilities(profileCapabilities, runCapabilities)

	seenSkillSets := objectKeys(legacySkillSets)
	seenToolSets := objectKeys(legacyToolSets)
	seenSkills := map[string]struct{}{}
	seenTools := map[string]struct{}{}
	seenMCPsets := map[string]struct{}{}
	seenMCPServers := map[string]struct{}{}

	// A set selected through the deprecated profile/run fields may already use
	// its canonical atomic refs. Expand those refs before canonical profile/run
	// layers so migration can update sets independently without changing old
	// consumers, while still preserving the documented legacy-first order.
	for _, set := range legacySkillSets {
		for index := range set.Spec.SkillRefs {
			skill, phase, reason, message, err := resolveAtomicSkill(ctx, reader, obj.Namespace, &set.Spec.SkillRefs[index], seenSkills)
			if err != nil || phase != "" {
				return resolved, phase, reason, message, err
			}
			if reason, message := capabilities.applyAtomicSkill(skill); reason != "" {
				return resolved, controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
			}
			resolved.skills = append(resolved.skills, skill)
		}
	}
	for _, set := range legacyToolSets {
		for index := range set.Spec.ToolRefs {
			tool, phase, reason, message, err := resolveAtomicTool(ctx, reader, obj.Namespace, &set.Spec.ToolRefs[index], seenTools)
			if err != nil || phase != "" {
				return resolved, phase, reason, message, err
			}
			if reason, message := capabilities.applyAtomicTool(tool); reason != "" {
				return resolved, controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
			}
			resolved.tools = append(resolved.tools, tool)
		}
	}

	applySkills := func(layer *controlv1alpha1.AgentSkillCapabilityComposition) (controlv1alpha1.AgentRunPhase, string, string, error) {
		if layer == nil {
			return "", "", "", nil
		}
		if layer.Mode == controlv1alpha1.AgentCapabilityCompositionReplace {
			capabilities.resetSkills()
			seenSkills = map[string]struct{}{}
			seenSkillSets = map[string]struct{}{}
			resolved.skills = nil
			resolved.skillSets = nil
			resolved.replacedSkills = true
		}
		for _, selection := range layer.Selections {
			switch {
			case selection.SkillRef != nil && selection.SkillSetRef == nil:
				skill, phase, reason, message, err := resolveAtomicSkill(ctx, reader, obj.Namespace, selection.SkillRef, seenSkills)
				if err != nil || phase != "" {
					return phase, reason, message, err
				}
				if reason, message := capabilities.applyAtomicSkill(skill); reason != "" {
					return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
				}
				resolved.skills = append(resolved.skills, skill)
			case selection.SkillSetRef != nil && selection.SkillRef == nil:
				set, phase, reason, message, err := resolveSkillSet(ctx, reader, obj.Namespace, selection.SkillSetRef, seenSkillSets)
				if err != nil || phase != "" {
					return phase, reason, message, err
				}
				if reason, message := capabilities.applySkillSet(set); reason != "" {
					return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
				}
				resolved.skillSets = append(resolved.skillSets, set)
				for index := range set.Spec.SkillRefs {
					skill, phase, reason, message, err := resolveAtomicSkill(ctx, reader, obj.Namespace, &set.Spec.SkillRefs[index], seenSkills)
					if err != nil || phase != "" {
						return phase, reason, message, err
					}
					if reason, message := capabilities.applyAtomicSkill(skill); reason != "" {
						return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
					}
					resolved.skills = append(resolved.skills, skill)
				}
			default:
				return controlv1alpha1.AgentRunPhaseFailed, "InvalidSkillSelection", "capabilities.skills.selections entries must set exactly one of skillRef or skillSetRef.", nil
			}
		}
		if reason, message := capabilities.applyOverrides(layer.Overrides); reason != "" {
			return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
		}
		return "", "", "", nil
	}

	applyTools := func(layer *controlv1alpha1.AgentToolCapabilityComposition) (controlv1alpha1.AgentRunPhase, string, string, error) {
		if layer == nil {
			return "", "", "", nil
		}
		if layer.Mode == controlv1alpha1.AgentCapabilityCompositionReplace {
			capabilities.resetTools()
			seenTools = map[string]struct{}{}
			seenToolSets = map[string]struct{}{}
			resolved.tools = nil
			resolved.toolSets = nil
			resolved.replacedTools = true
		}
		for _, selection := range layer.Selections {
			switch {
			case selection.ToolRef != nil && selection.ToolSetRef == nil:
				tool, phase, reason, message, err := resolveAtomicTool(ctx, reader, obj.Namespace, selection.ToolRef, seenTools)
				if err != nil || phase != "" {
					return phase, reason, message, err
				}
				if reason, message := capabilities.applyAtomicTool(tool); reason != "" {
					return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
				}
				resolved.tools = append(resolved.tools, tool)
			case selection.ToolSetRef != nil && selection.ToolRef == nil:
				set, phase, reason, message, err := resolveToolSet(ctx, reader, obj.Namespace, selection.ToolSetRef, seenToolSets)
				if err != nil || phase != "" {
					return phase, reason, message, err
				}
				if reason, message := capabilities.applyToolSet(set); reason != "" {
					return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
				}
				resolved.toolSets = append(resolved.toolSets, set)
				for index := range set.Spec.ToolRefs {
					tool, phase, reason, message, err := resolveAtomicTool(ctx, reader, obj.Namespace, &set.Spec.ToolRefs[index], seenTools)
					if err != nil || phase != "" {
						return phase, reason, message, err
					}
					if reason, message := capabilities.applyAtomicTool(tool); reason != "" {
						return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
					}
					resolved.tools = append(resolved.tools, tool)
				}
			default:
				return controlv1alpha1.AgentRunPhaseFailed, "InvalidToolSelection", "capabilities.tools.selections entries must set exactly one of toolRef or toolSetRef.", nil
			}
		}
		return "", "", "", nil
	}

	applyMCP := func(layer *controlv1alpha1.AgentMCPCapabilityComposition) (controlv1alpha1.AgentRunPhase, string, string, error) {
		if layer == nil {
			return "", "", "", nil
		}
		if layer.Mode == controlv1alpha1.AgentCapabilityCompositionReplace {
			capabilities.resetMCPServers()
			seenMCPsets = map[string]struct{}{}
			seenMCPServers = map[string]struct{}{}
			resolved.mcpSets = nil
			resolved.mcpServers = nil
			resolved.replacedMCP = true
		}
		for _, selection := range layer.Selections {
			switch {
			case selection.ServerRef != nil && selection.MCPSetRef == nil:
				server, phase, reason, message, err := resolveMCPServer(ctx, reader, obj.Namespace, selection.ServerRef, seenMCPServers)
				if err != nil || phase != "" {
					return phase, reason, message, err
				}
				if reason, message := capabilities.applyMCPServer(server); reason != "" {
					return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
				}
				resolved.mcpServers = append(resolved.mcpServers, server)
			case selection.MCPSetRef != nil && selection.ServerRef == nil:
				set, phase, reason, message, err := resolveMCPSet(ctx, reader, obj.Namespace, selection.MCPSetRef, seenMCPsets)
				if err != nil || phase != "" {
					return phase, reason, message, err
				}
				resolved.mcpSets = append(resolved.mcpSets, set)
				for index := range set.Spec.ServerRefs {
					server, phase, reason, message, err := resolveMCPServer(ctx, reader, obj.Namespace, &set.Spec.ServerRefs[index], seenMCPServers)
					if err != nil || phase != "" {
						return phase, reason, message, err
					}
					if reason, message := capabilities.applyMCPServer(server); reason != "" {
						return controlv1alpha1.AgentRunPhaseFailed, reason, message, nil
					}
					resolved.mcpServers = append(resolved.mcpServers, server)
				}
			default:
				return controlv1alpha1.AgentRunPhaseFailed, "InvalidMCPSelection", "capabilities.mcpServers.selections entries must set exactly one of serverRef or mcpSetRef.", nil
			}
		}
		return "", "", "", nil
	}

	for _, layer := range []*controlv1alpha1.AgentCapabilitiesSpec{profileCapabilities, runCapabilities} {
		if layer == nil {
			continue
		}
		if phase, reason, message, err := applySkills(layer.Skills); err != nil || phase != "" {
			return resolved, phase, reason, message, err
		}
		if phase, reason, message, err := applyTools(layer.Tools); err != nil || phase != "" {
			return resolved, phase, reason, message, err
		}
		if phase, reason, message, err := applyMCP(layer.MCPServers); err != nil || phase != "" {
			return resolved, phase, reason, message, err
		}
	}
	return resolved, "", "", "", nil
}

func objectKeys[T client.Object](objects []T) map[string]struct{} {
	out := map[string]struct{}{}
	for _, obj := range objects {
		out[obj.GetNamespace()+"/"+obj.GetName()] = struct{}{}
	}
	return out
}

func resolveReferenceIdentity(namespace string, ref *controlv1alpha1.NamespacedObjectReference, kind string, seen map[string]struct{}) (client.ObjectKey, controlv1alpha1.AgentRunPhase, string, string) {
	if ref == nil || strings.TrimSpace(ref.Name) == "" {
		return client.ObjectKey{}, controlv1alpha1.AgentRunPhaseFailed, "Invalid" + kind + "Ref", kind + " ref name must be set."
	}
	refNamespace := firstNonEmpty(strings.TrimSpace(ref.Namespace), namespace)
	if refNamespace != namespace {
		return client.ObjectKey{}, controlv1alpha1.AgentRunPhaseFailed, "CrossNamespace" + kind + "Ref", kind + " refs must stay in the AgentRun namespace."
	}
	key := refNamespace + "/" + strings.TrimSpace(ref.Name)
	if _, exists := seen[key]; exists {
		return client.ObjectKey{}, controlv1alpha1.AgentRunPhaseFailed, "Duplicate" + kind + "Ref", kind + " " + key + " is selected more than once."
	}
	seen[key] = struct{}{}
	return client.ObjectKey{Namespace: refNamespace, Name: strings.TrimSpace(ref.Name)}, "", "", ""
}

func resolveAtomicSkill(ctx context.Context, reader client.Reader, namespace string, ref *controlv1alpha1.NamespacedObjectReference, seen map[string]struct{}) (*controlv1alpha1.AgentSkill, controlv1alpha1.AgentRunPhase, string, string, error) {
	key, phase, reason, message := resolveReferenceIdentity(namespace, ref, "Skill", seen)
	if phase != "" {
		return nil, phase, reason, message, nil
	}
	obj := &controlv1alpha1.AgentSkill{}
	if err := reader.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, controlv1alpha1.AgentRunPhaseNeedsHuman, "SkillNotFound", fmt.Sprintf("AgentSkill %s was not found.", key.String()), nil
		}
		return nil, "", "", "", err
	}
	return obj, "", "", "", nil
}

func resolveAtomicTool(ctx context.Context, reader client.Reader, namespace string, ref *controlv1alpha1.NamespacedObjectReference, seen map[string]struct{}) (*controlv1alpha1.AgentTool, controlv1alpha1.AgentRunPhase, string, string, error) {
	key, phase, reason, message := resolveReferenceIdentity(namespace, ref, "Tool", seen)
	if phase != "" {
		return nil, phase, reason, message, nil
	}
	obj := &controlv1alpha1.AgentTool{}
	if err := reader.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, controlv1alpha1.AgentRunPhaseNeedsHuman, "ToolNotFound", fmt.Sprintf("AgentTool %s was not found.", key.String()), nil
		}
		return nil, "", "", "", err
	}
	return obj, "", "", "", nil
}

func resolveMCPServer(ctx context.Context, reader client.Reader, namespace string, ref *controlv1alpha1.NamespacedObjectReference, seen map[string]struct{}) (*controlv1alpha1.AgentMCPServer, controlv1alpha1.AgentRunPhase, string, string, error) {
	key, phase, reason, message := resolveReferenceIdentity(namespace, ref, "MCPServer", seen)
	if phase != "" {
		return nil, phase, reason, message, nil
	}
	obj := &controlv1alpha1.AgentMCPServer{}
	if err := reader.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, controlv1alpha1.AgentRunPhaseNeedsHuman, "MCPServerNotFound", fmt.Sprintf("AgentMCPServer %s was not found.", key.String()), nil
		}
		return nil, "", "", "", err
	}
	return obj, "", "", "", nil
}

func resolveSkillSet(ctx context.Context, reader client.Reader, namespace string, ref *controlv1alpha1.NamespacedObjectReference, seen map[string]struct{}) (*controlv1alpha1.AgentSkillSet, controlv1alpha1.AgentRunPhase, string, string, error) {
	key, phase, reason, message := resolveReferenceIdentity(namespace, ref, "SkillSet", seen)
	if phase != "" {
		return nil, phase, reason, message, nil
	}
	obj := &controlv1alpha1.AgentSkillSet{}
	if err := reader.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, controlv1alpha1.AgentRunPhaseNeedsHuman, "SkillSetNotFound", fmt.Sprintf("AgentSkillSet %s was not found.", key.String()), nil
		}
		return nil, "", "", "", err
	}
	return obj, "", "", "", nil
}

func resolveToolSet(ctx context.Context, reader client.Reader, namespace string, ref *controlv1alpha1.NamespacedObjectReference, seen map[string]struct{}) (*controlv1alpha1.AgentToolSet, controlv1alpha1.AgentRunPhase, string, string, error) {
	key, phase, reason, message := resolveReferenceIdentity(namespace, ref, "ToolSet", seen)
	if phase != "" {
		return nil, phase, reason, message, nil
	}
	obj := &controlv1alpha1.AgentToolSet{}
	if err := reader.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, controlv1alpha1.AgentRunPhaseNeedsHuman, "ToolSetNotFound", fmt.Sprintf("AgentToolSet %s was not found.", key.String()), nil
		}
		return nil, "", "", "", err
	}
	return obj, "", "", "", nil
}

func resolveMCPSet(ctx context.Context, reader client.Reader, namespace string, ref *controlv1alpha1.NamespacedObjectReference, seen map[string]struct{}) (*controlv1alpha1.AgentMCPSet, controlv1alpha1.AgentRunPhase, string, string, error) {
	key, phase, reason, message := resolveReferenceIdentity(namespace, ref, "MCPSet", seen)
	if phase != "" {
		return nil, phase, reason, message, nil
	}
	obj := &controlv1alpha1.AgentMCPSet{}
	if err := reader.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, controlv1alpha1.AgentRunPhaseNeedsHuman, "MCPSetNotFound", fmt.Sprintf("AgentMCPSet %s was not found.", key.String()), nil
		}
		return nil, "", "", "", err
	}
	return obj, "", "", "", nil
}

func appendResolvedReferenceOnce[T client.Object](refs []controlv1alpha1.AgentRunResolvedObjectReferenceStatus, obj T, digest string) []controlv1alpha1.AgentRunResolvedObjectReferenceStatus {
	for _, ref := range refs {
		if ref.Namespace == obj.GetNamespace() && ref.Name == obj.GetName() {
			return refs
		}
	}
	return append(refs, *resolvedObjectReferenceStatus(obj, digest))
}

type agentRunCapabilities struct {
	skills          []controlv1alpha1.AgentRunSkillInjectionSpec
	tools           []controlv1alpha1.AgentRunToolSpec
	subagents       []controlv1alpha1.AgentRunSubagentSpec
	mcpServers      []controlv1alpha1.AgentRunMCPServerSpec
	skillIndexes    map[string]int
	toolIndexes     map[string]int
	subagentIndexes map[string]int
	mcpIndexes      map[string]int
}

func newAgentRunCapabilities() *agentRunCapabilities {
	return &agentRunCapabilities{
		skillIndexes:    map[string]int{},
		toolIndexes:     map[string]int{},
		subagentIndexes: map[string]int{},
		mcpIndexes:      map[string]int{},
	}
}

func (c *agentRunCapabilities) applySkillSet(skillSet *controlv1alpha1.AgentSkillSet) (string, string) {
	seenSkills := map[string]struct{}{}
	for _, skill := range skillSet.Spec.Skills {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			return "InvalidSkillName", fmt.Sprintf("AgentSkillSet %s/%s contains an empty skill name.", skillSet.Namespace, skillSet.Name)
		}
		if _, exists := seenSkills[name]; exists {
			return "DuplicateSkillName", fmt.Sprintf("AgentSkillSet %s/%s contains duplicate skill %q.", skillSet.Namespace, skillSet.Name, name)
		}
		seenSkills[name] = struct{}{}
		c.upsertSkill(skill)
	}
	seenTools := map[string]struct{}{}
	for _, tool := range skillSet.Spec.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return "InvalidToolName", fmt.Sprintf("AgentSkillSet %s/%s contains an empty tool name.", skillSet.Namespace, skillSet.Name)
		}
		if _, exists := seenTools[name]; exists {
			return "DuplicateToolName", fmt.Sprintf("AgentSkillSet %s/%s contains duplicate tool %q.", skillSet.Namespace, skillSet.Name, name)
		}
		seenTools[name] = struct{}{}
		if index, exists := c.toolIndexes[name]; exists && !reflect.DeepEqual(c.tools[index], tool) {
			return "ConflictingToolName", fmt.Sprintf("Selected AgentSkillSets define conflicting tool %q contracts.", name)
		}
		c.upsertTool(tool)
	}
	seenSubagents := map[string]struct{}{}
	for _, subagent := range skillSet.Spec.Subagents {
		name := strings.TrimSpace(subagent.Name)
		if name == "" {
			return "InvalidSubagentName", fmt.Sprintf("AgentSkillSet %s/%s contains an empty subagent name.", skillSet.Namespace, skillSet.Name)
		}
		if _, exists := seenSubagents[name]; exists {
			return "DuplicateSubagentName", fmt.Sprintf("AgentSkillSet %s/%s contains duplicate subagent %q.", skillSet.Namespace, skillSet.Name, name)
		}
		seenSubagents[name] = struct{}{}
		if index, exists := c.subagentIndexes[name]; exists && !reflect.DeepEqual(c.subagents[index], subagent) {
			return "ConflictingSubagentName", fmt.Sprintf("Selected AgentSkillSets define conflicting subagent %q personas.", name)
		}
		c.upsertSubagent(subagent)
	}
	return "", ""
}

func (c *agentRunCapabilities) applyToolSet(toolSet *controlv1alpha1.AgentToolSet) (string, string) {
	seenTools := map[string]struct{}{}
	for _, tool := range toolSet.Spec.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return "InvalidToolName", fmt.Sprintf("AgentToolSet %s/%s contains an empty tool name.", toolSet.Namespace, toolSet.Name)
		}
		if _, exists := seenTools[name]; exists {
			return "DuplicateToolName", fmt.Sprintf("AgentToolSet %s/%s contains duplicate tool %q.", toolSet.Namespace, toolSet.Name, name)
		}
		seenTools[name] = struct{}{}
		if index, exists := c.toolIndexes[name]; exists && !reflect.DeepEqual(c.tools[index], tool) {
			return "ConflictingToolName", fmt.Sprintf("Selected AgentSkillSets and AgentToolSets define conflicting tool %q contracts.", name)
		}
		c.upsertTool(tool)
	}
	return "", ""
}

func (c *agentRunCapabilities) applyAtomicSkill(skill *controlv1alpha1.AgentSkill) (string, string) {
	if skill == nil || strings.TrimSpace(skill.Name) == "" {
		return "InvalidSkillName", "AgentSkill name must not be empty."
	}
	injection, reason, message := agentRunSkillFromAtomic(skill)
	if reason != "" {
		return reason, message
	}
	c.upsertSkill(injection)
	return "", ""
}

func agentRunSkillFromAtomic(skill *controlv1alpha1.AgentSkill) (controlv1alpha1.AgentRunSkillInjectionSpec, string, string) {
	out := controlv1alpha1.AgentRunSkillInjectionSpec{Name: strings.TrimSpace(skill.Name), Description: skill.Spec.Description}
	switch {
	case skill.Spec.Inline != nil && skill.Spec.GitHub == nil:
		parts := []string{strings.TrimSpace(skill.Spec.Inline.SkillMD)}
		seenPaths := map[string]struct{}{}
		for _, reference := range skill.Spec.Inline.References {
			path := strings.TrimSpace(reference.Path)
			if !safeMarkdownPath(path) {
				return out, "InvalidSkillReferencePath", fmt.Sprintf("AgentSkill %s/%s contains unsafe non-Markdown reference path %q.", skill.Namespace, skill.Name, path)
			}
			if _, exists := seenPaths[path]; exists {
				return out, "DuplicateSkillReferencePath", fmt.Sprintf("AgentSkill %s/%s contains duplicate reference path %q.", skill.Namespace, skill.Name, path)
			}
			seenPaths[path] = struct{}{}
			parts = append(parts, "## Reference: "+path, strings.TrimSpace(reference.Content))
		}
		out.Content = strings.Join(parts, "\n\n")
	case skill.Spec.GitHub != nil && skill.Spec.Inline == nil:
		source := skill.Spec.GitHub
		if !safeSkillMDPath(source.Path) {
			return out, "InvalidSkillSourcePath", fmt.Sprintf("AgentSkill %s/%s github.path must safely name SKILL.md.", skill.Namespace, skill.Name)
		}
		paths := append([]string{source.Path}, source.ReferencePaths...)
		seenPaths := map[string]struct{}{}
		for _, path := range paths {
			path = strings.TrimSpace(path)
			if !safeMarkdownPath(path) {
				return out, "InvalidSkillReferencePath", fmt.Sprintf("AgentSkill %s/%s contains unsafe non-Markdown reference path %q.", skill.Namespace, skill.Name, path)
			}
			if _, exists := seenPaths[path]; exists {
				return out, "DuplicateSkillReferencePath", fmt.Sprintf("AgentSkill %s/%s contains duplicate reference path %q.", skill.Namespace, skill.Name, path)
			}
			seenPaths[path] = struct{}{}
			out.SourceRefs = append(out.SourceRefs, controlv1alpha1.AgentRunSkillSourceRef{GitHub: &controlv1alpha1.AgentRunGitHubSkillSourceSpec{
				Repository: source.Repository,
				Ref:        source.Ref,
				Path:       path,
				APIBaseURL: source.APIBaseURL,
			}})
		}
	default:
		return out, "InvalidSkillSource", fmt.Sprintf("AgentSkill %s/%s must set exactly one of inline or github.", skill.Namespace, skill.Name)
	}
	return out, "", ""
}

func safeMarkdownPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "/") || !strings.HasSuffix(strings.ToLower(path), ".md") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func safeSkillMDPath(path string) bool {
	return safeMarkdownPath(path) && (path == "SKILL.md" || strings.HasSuffix(path, "/SKILL.md"))
}

func (c *agentRunCapabilities) applyAtomicTool(tool *controlv1alpha1.AgentTool) (string, string) {
	if tool == nil || strings.TrimSpace(tool.Name) == "" {
		return "InvalidToolName", "AgentTool name must not be empty."
	}
	resolved := controlv1alpha1.AgentRunToolSpec{
		Name:          strings.TrimSpace(tool.Name),
		Description:   tool.Spec.Description,
		Executable:    tool.Spec.Executable.DeepCopy(),
		Source:        tool.Spec.Source.DeepCopy(),
		SetupScript:   tool.Spec.SetupScript,
		VerifyCommand: append([]string(nil), tool.Spec.VerifyCommand...),
		SpecDigest:    digestJSON(tool.Spec),
	}
	if index, exists := c.toolIndexes[resolved.Name]; exists && !reflect.DeepEqual(c.tools[index], resolved) {
		return "ConflictingToolName", fmt.Sprintf("Selected capability resources define conflicting tool %q contracts.", resolved.Name)
	}
	c.upsertTool(resolved)
	return "", ""
}

func (c *agentRunCapabilities) applyMCPServer(server *controlv1alpha1.AgentMCPServer) (string, string) {
	if server == nil || strings.TrimSpace(server.Name) == "" {
		return "InvalidMCPServerName", "AgentMCPServer name must not be empty."
	}
	return c.upsertMCPServer(controlv1alpha1.AgentRunMCPServerSpec{
		Name:          strings.TrimSpace(server.Name),
		Description:   server.Spec.Description,
		Transport:     *server.Spec.Transport.DeepCopy(),
		ToolAllowlist: append([]string(nil), server.Spec.ToolAllowlist...),
		SpecDigest:    digestJSON(server.Spec),
	})
}

func (c *agentRunCapabilities) applyOverrides(overrides []controlv1alpha1.AgentSkillOverrideSpec) (string, string) {
	seen := map[string]struct{}{}
	for _, override := range overrides {
		name := strings.TrimSpace(override.Name)
		if name == "" {
			return "InvalidSkillOverride", "skillSets.overrides[].name must be set."
		}
		if _, exists := seen[name]; exists {
			return "DuplicateSkillOverride", fmt.Sprintf("Skill override %q appears more than once in the same composition layer.", name)
		}
		seen[name] = struct{}{}
		index, exists := c.skillIndexes[name]
		switch override.Operation {
		case controlv1alpha1.AgentSkillOverrideAdd:
			if exists {
				return "InvalidSkillOverride", fmt.Sprintf("Add override for skill %q requires the skill not to exist.", name)
			}
			c.upsertSkill(skillFromOverride(override))
		case controlv1alpha1.AgentSkillOverrideAugment:
			if !exists {
				return "UnknownSkillOverride", fmt.Sprintf("Augment override targets unknown skill %q.", name)
			}
			skill := c.skills[index].DeepCopy()
			if strings.TrimSpace(override.Description) != "" {
				skill.Description = override.Description
			}
			if strings.TrimSpace(override.Content) != "" {
				skill.Content = mergePromptText(skill.Content, "Local override:\n"+strings.TrimSpace(override.Content))
			}
			skill.SourceRefs = append(skill.SourceRefs, override.SourceRefs...)
			skill.Paths = appendUniqueStrings(skill.Paths, override.Paths...)
			c.skills[index] = *skill
		case controlv1alpha1.AgentSkillOverrideReplace:
			if !exists {
				return "UnknownSkillOverride", fmt.Sprintf("Replace override targets unknown skill %q.", name)
			}
			c.skills[index] = skillFromOverride(override)
		case controlv1alpha1.AgentSkillOverrideDisable:
			if !exists {
				return "UnknownSkillOverride", fmt.Sprintf("Disable override targets unknown skill %q.", name)
			}
			if strings.TrimSpace(override.Description) != "" || strings.TrimSpace(override.Content) != "" || len(override.SourceRefs) > 0 || len(override.Paths) > 0 {
				return "InvalidSkillOverride", fmt.Sprintf("Disable override for skill %q cannot include content fields.", name)
			}
			c.removeSkill(index)
		default:
			return "InvalidSkillOverride", fmt.Sprintf("Skill override %q has unsupported operation %q.", name, override.Operation)
		}
	}
	return "", ""
}

func skillFromOverride(override controlv1alpha1.AgentSkillOverrideSpec) controlv1alpha1.AgentRunSkillInjectionSpec {
	return controlv1alpha1.AgentRunSkillInjectionSpec{
		Name:        strings.TrimSpace(override.Name),
		Description: override.Description,
		Content:     override.Content,
		SourceRefs:  append([]controlv1alpha1.AgentRunSkillSourceRef(nil), override.SourceRefs...),
		Paths:       append([]string(nil), override.Paths...),
	}
}

func (c *agentRunCapabilities) upsertSkill(skill controlv1alpha1.AgentRunSkillInjectionSpec) {
	name := strings.TrimSpace(skill.Name)
	if index, exists := c.skillIndexes[name]; exists {
		c.skills[index] = *skill.DeepCopy()
		return
	}
	c.skillIndexes[name] = len(c.skills)
	c.skills = append(c.skills, *skill.DeepCopy())
}

func (c *agentRunCapabilities) removeSkill(index int) {
	c.skills = append(c.skills[:index], c.skills[index+1:]...)
	c.skillIndexes = map[string]int{}
	for skillIndex := range c.skills {
		c.skillIndexes[strings.TrimSpace(c.skills[skillIndex].Name)] = skillIndex
	}
}

func (c *agentRunCapabilities) upsertTool(tool controlv1alpha1.AgentRunToolSpec) {
	name := strings.TrimSpace(tool.Name)
	if index, exists := c.toolIndexes[name]; exists {
		c.tools[index] = *tool.DeepCopy()
		return
	}
	c.toolIndexes[name] = len(c.tools)
	c.tools = append(c.tools, *tool.DeepCopy())
}

func (c *agentRunCapabilities) upsertSubagent(subagent controlv1alpha1.AgentRunSubagentSpec) {
	name := strings.TrimSpace(subagent.Name)
	if index, exists := c.subagentIndexes[name]; exists {
		c.subagents[index] = *subagent.DeepCopy()
		return
	}
	c.subagentIndexes[name] = len(c.subagents)
	c.subagents = append(c.subagents, *subagent.DeepCopy())
}

func (c *agentRunCapabilities) upsertMCPServer(server controlv1alpha1.AgentRunMCPServerSpec) (string, string) {
	name := strings.TrimSpace(server.Name)
	if name == "" {
		return "InvalidMCPServerName", "MCP server name must not be empty."
	}
	if index, exists := c.mcpIndexes[name]; exists {
		if !reflect.DeepEqual(c.mcpServers[index], server) {
			return "ConflictingMCPServerName", fmt.Sprintf("Selected capability resources define conflicting MCP server %q contracts.", name)
		}
		return "", ""
	}
	c.mcpIndexes[name] = len(c.mcpServers)
	c.mcpServers = append(c.mcpServers, *server.DeepCopy())
	return "", ""
}

func (c *agentRunCapabilities) overlayMCPServer(server controlv1alpha1.AgentRunMCPServerSpec) (string, string) {
	name := strings.TrimSpace(server.Name)
	if name == "" {
		return "InvalidMCPServerName", "MCP server name must not be empty."
	}
	if index, exists := c.mcpIndexes[name]; exists {
		c.mcpServers[index] = *server.DeepCopy()
		return "", ""
	}
	c.mcpIndexes[name] = len(c.mcpServers)
	c.mcpServers = append(c.mcpServers, *server.DeepCopy())
	return "", ""
}

func (c *agentRunCapabilities) resetSkills() {
	c.skills = nil
	c.skillIndexes = map[string]int{}
}

func (c *agentRunCapabilities) resetTools() {
	c.tools = nil
	c.toolIndexes = map[string]int{}
}

func (c *agentRunCapabilities) resetMCPServers() {
	c.mcpServers = nil
	c.mcpIndexes = map[string]int{}
}

func deepCopyNamespacedObjectReference(ref *controlv1alpha1.NamespacedObjectReference) *controlv1alpha1.NamespacedObjectReference {
	if ref == nil {
		return nil
	}
	return ref.DeepCopy()
}

func resolvedObjectReferenceStatus(obj client.Object, digest string) *controlv1alpha1.AgentRunResolvedObjectReferenceStatus {
	if obj == nil {
		return nil
	}
	return &controlv1alpha1.AgentRunResolvedObjectReferenceStatus{
		Name:            obj.GetName(),
		Namespace:       obj.GetNamespace(),
		UID:             string(obj.GetUID()),
		Generation:      obj.GetGeneration(),
		ResourceVersion: obj.GetResourceVersion(),
		Digest:          digest,
	}
}

func digestJSON(value any) string {
	contents, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(digest[:])
}
