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
	effective.Spec.Harness.SkillInjections = capabilities.skills
	effective.Spec.Harness.Tools = capabilities.tools
	effective.Spec.Harness.Subagents = capabilities.subagents

	status := &controlv1alpha1.AgentRunResolvedCompositionStatus{}
	if profile != nil {
		status.ProfileRef = resolvedObjectReferenceStatus(profile, digestJSON(profile.Spec))
	}
	if harnessProfile != nil {
		status.HarnessProfileRef = resolvedObjectReferenceStatus(harnessProfile, digestJSON(harnessProfile.Spec))
	}
	for _, skillSet := range resolvedSkillSets {
		status.SkillSetRefs = append(status.SkillSetRefs, *resolvedObjectReferenceStatus(skillSet, digestJSON(skillSet.Spec)))
	}
	for _, toolSet := range resolvedToolSets {
		status.ToolSetRefs = append(status.ToolSetRefs, *resolvedObjectReferenceStatus(toolSet, digestJSON(toolSet.Spec)))
	}
	status.Scope = agentRunResolvedScopeStatus(effective.Spec.Scope)
	status.EffectiveDigest = digestJSON(effective.Spec)
	effective.Status.ResolvedComposition = status.DeepCopy()
	return effective, status, "", "", "", nil
}

func agentRunResolvedScopeStatus(scope controlv1alpha1.AgentRunScopeSpec) *controlv1alpha1.AgentRunResolvedScopeStatus {
	status := &controlv1alpha1.AgentRunResolvedScopeStatus{}
	if scope.ApplicationRef != nil {
		status.Application = strings.TrimSpace(scope.ApplicationRef.Name)
	}
	if scope.ApplicationTargetRef != nil {
		status.ApplicationTarget = strings.TrimSpace(scope.ApplicationTargetRef.Name)
	}
	if status.Application == "" && status.ApplicationTarget == "" {
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

type agentRunCapabilities struct {
	skills          []controlv1alpha1.AgentRunSkillInjectionSpec
	tools           []controlv1alpha1.AgentRunToolSpec
	subagents       []controlv1alpha1.AgentRunSubagentSpec
	skillIndexes    map[string]int
	toolIndexes     map[string]int
	subagentIndexes map[string]int
}

func newAgentRunCapabilities() *agentRunCapabilities {
	return &agentRunCapabilities{
		skillIndexes:    map[string]int{},
		toolIndexes:     map[string]int{},
		subagentIndexes: map[string]int{},
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
