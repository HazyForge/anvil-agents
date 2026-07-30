package agentctl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	"github.com/spf13/pflag"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

type compositionMigrateOptions struct {
	file   string
	output string
}

func writeCompositionUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Composition commands: migrate -f FILE [-o yaml]")
}

func (app App) runComposition(args []string) error {
	if len(args) == 0 {
		writeCompositionUsage(app.Err)
		return &usageError{message: "a composition command is required"}
	}
	switch args[0] {
	case "migrate":
		return app.runCompositionMigrate(args[1:])
	case "help":
		writeCompositionUsage(app.Out)
		return nil
	default:
		writeCompositionUsage(app.Err)
		return &usageError{message: fmt.Sprintf("unknown composition command %q", args[0])}
	}
}

func (app App) runCompositionMigrate(args []string) error {
	options := compositionMigrateOptions{output: "yaml"}
	flags := pflag.NewFlagSet("composition migrate", pflag.ContinueOnError)
	flags.SetOutput(app.Err)
	flags.StringVarP(&options.file, "filename", "f", "-", "Legacy multi-document YAML file, or - for stdin.")
	flags.StringVarP(&options.output, "output", "o", options.output, "Output format: yaml.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: "composition migrate does not accept positional arguments"}
	}
	if options.output != "yaml" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", options.output)}
	}
	reader := app.In
	var closer io.Closer
	if options.file != "-" {
		file, err := os.Open(options.file)
		if err != nil {
			return fmt.Errorf("open composition input: %w", err)
		}
		reader, closer = file, file
	}
	if closer != nil {
		defer closer.Close()
	}
	objects, err := decodeCompositionDocuments(reader)
	if err != nil {
		return err
	}
	migrated, err := migrateCompositionObjects(objects)
	if err != nil {
		return err
	}
	buffered := bufio.NewWriter(app.Out)
	defer buffered.Flush()
	for index, object := range migrated {
		body, err := yaml.Marshal(object)
		if err != nil {
			return fmt.Errorf("encode migrated %T: %w", object, err)
		}
		if index > 0 {
			if _, err := fmt.Fprintln(buffered, "---"); err != nil {
				return err
			}
		}
		if _, err := buffered.Write(body); err != nil {
			return err
		}
	}
	return nil
}

func decodeCompositionDocuments(reader io.Reader) ([]*unstructured.Unstructured, error) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(reader, 64*1024)
	var out []*unstructured.Unstructured
	for {
		raw := map[string]any{}
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode composition YAML: %w", err)
		}
		if len(raw) == 0 {
			continue
		}
		object := &unstructured.Unstructured{Object: raw}
		if object.IsList() {
			list, err := object.ToList()
			if err != nil {
				return nil, fmt.Errorf("decode composition List: %w", err)
			}
			for index := range list.Items {
				item := list.Items[index].DeepCopy()
				out = append(out, item)
			}
			continue
		}
		out = append(out, object)
	}
	return out, nil
}

type migrationInventory struct {
	skillSets map[string]*agentsv1alpha1.AgentSkillSet
	toolSets  map[string]*agentsv1alpha1.AgentToolSet
	generated map[string]runtime.Object
	existing  map[string]struct{}
}

func migrateCompositionObjects(objects []*unstructured.Unstructured) ([]runtime.Object, error) {
	inventory := migrationInventory{
		skillSets: map[string]*agentsv1alpha1.AgentSkillSet{},
		toolSets:  map[string]*agentsv1alpha1.AgentToolSet{},
		generated: map[string]runtime.Object{},
		existing:  map[string]struct{}{},
	}
	for _, object := range objects {
		inventory.existing[compositionGeneratedKey(object.GetKind(), object.GetNamespace(), object.GetName())] = struct{}{}
		key := compositionObjectKey(object.GetNamespace(), object.GetName())
		switch object.GetKind() {
		case "AgentSkillSet":
			value := &agentsv1alpha1.AgentSkillSet{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, value); err != nil {
				return nil, fmt.Errorf("decode AgentSkillSet %s: %w", key, err)
			}
			inventory.skillSets[key] = value
		case "AgentToolSet":
			value := &agentsv1alpha1.AgentToolSet{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, value); err != nil {
				return nil, fmt.Errorf("decode AgentToolSet %s: %w", key, err)
			}
			inventory.toolSets[key] = value
		}
	}
	var converted []runtime.Object
	for _, object := range objects {
		switch object.GetKind() {
		case "AgentSkillSet":
			items, err := inventory.migrateSkillSet(object, inventory.skillSets[compositionObjectKey(object.GetNamespace(), object.GetName())])
			if err != nil {
				return nil, err
			}
			converted = append(converted, items...)
		case "AgentToolSet":
			items, err := inventory.migrateToolSet(object, inventory.toolSets[compositionObjectKey(object.GetNamespace(), object.GetName())])
			if err != nil {
				return nil, err
			}
			converted = append(converted, items...)
		case "AgentRunProfile":
			value := &agentsv1alpha1.AgentRunProfile{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, value); err != nil {
				return nil, fmt.Errorf("decode AgentRunProfile %s/%s: %w", object.GetNamespace(), object.GetName(), err)
			}
			if inventory.migrateRunProfileSpec(&value.Spec) {
				patched, patchErr := patchProfileComposition(object, &value.Spec)
				if patchErr != nil {
					return nil, patchErr
				}
				converted = append(converted, patched)
			} else {
				converted = append(converted, object.DeepCopy())
			}
		case "AgentRun":
			// AgentRun is append-only. The migration command may be run over a
			// mixed export, but it never rewrites an existing execution record.
			converted = append(converted, object.DeepCopy())
		default:
			converted = append(converted, object.DeepCopy())
		}
	}
	return converted, nil
}

func (inventory *migrationInventory) migrateSkillSet(original *unstructured.Unstructured, set *agentsv1alpha1.AgentSkillSet) ([]runtime.Object, error) {
	if set == nil {
		return nil, nil
	}
	out := set.DeepCopy()
	remainingSkills := make([]agentsv1alpha1.AgentRunSkillInjectionSpec, 0, len(set.Spec.Skills))
	generatedRefs := make([]agentsv1alpha1.NamespacedObjectReference, 0, len(set.Spec.Skills))
	var objects []runtime.Object
	for _, skill := range set.Spec.Skills {
		atomic, ok := migratableLegacySkill(set.ObjectMeta, skill)
		if !ok {
			remainingSkills = append(remainingSkills, skill)
			continue
		}
		if !inventory.reserveGenerated(atomic) || referenceNameExists(out.Spec.SkillRefs, atomic.Name) {
			remainingSkills = append(remainingSkills, skill)
			continue
		}
		objects = append(objects, atomic)
		generatedRefs = append(generatedRefs, agentsv1alpha1.NamespacedObjectReference{Name: atomic.Name})
	}
	out.Spec.Skills = remainingSkills
	out.Spec.SkillRefs = append(generatedRefs, out.Spec.SkillRefs...)
	patched, err := patchSetComposition(original, "skills", out.Spec.Skills, "skillRefs", out.Spec.SkillRefs)
	if err != nil {
		return nil, err
	}
	objects = append(objects, patched)
	return objects, nil
}

var immutableGitObjectID = regexp.MustCompile(`^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

func migratableLegacySkill(meta metav1.ObjectMeta, skill agentsv1alpha1.AgentRunSkillInjectionSpec) (*agentsv1alpha1.AgentSkill, bool) {
	name := strings.TrimSpace(skill.Name)
	if len(validation.IsDNS1123Label(name)) != 0 {
		return nil, false
	}
	atomic := &agentsv1alpha1.AgentSkill{
		TypeMeta:   metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentSkill"},
		ObjectMeta: migratedObjectMeta(meta, name),
		Spec:       agentsv1alpha1.AgentSkillSpec{Description: skill.Description},
	}
	if strings.TrimSpace(skill.Content) != "" && len(skill.SourceRefs) == 0 && len(skill.Paths) == 0 {
		atomic.Spec.Inline = &agentsv1alpha1.AgentSkillInlineSource{SkillMD: skill.Content}
		return atomic, true
	}
	if len(skill.SourceRefs) == 1 && skill.SourceRefs[0].GitHub != nil && strings.TrimSpace(skill.Content) == "" && len(skill.Paths) == 0 {
		source := skill.SourceRefs[0].GitHub
		path := strings.TrimSpace(source.Path)
		if immutableGitObjectID.MatchString(strings.TrimSpace(source.Ref)) && validGitHubRepository(source.Repository) && safeRelativePath(path) && (path == "SKILL.md" || strings.HasSuffix(path, "/SKILL.md")) && validGitHubAPIBaseURL(source.APIBaseURL) {
			atomic.Spec.GitHub = &agentsv1alpha1.AgentSkillGitHubSource{Repository: source.Repository, Ref: source.Ref, Path: source.Path, APIBaseURL: source.APIBaseURL}
			return atomic, true
		}
	}
	return nil, false
}

func (inventory *migrationInventory) migrateToolSet(original *unstructured.Unstructured, set *agentsv1alpha1.AgentToolSet) ([]runtime.Object, error) {
	if set == nil {
		return nil, nil
	}
	out := set.DeepCopy()
	remaining := make([]agentsv1alpha1.AgentRunToolSpec, 0, len(set.Spec.Tools))
	generatedRefs := make([]agentsv1alpha1.NamespacedObjectReference, 0, len(set.Spec.Tools))
	var objects []runtime.Object
	for _, tool := range set.Spec.Tools {
		atomic, ok := migratableLegacyTool(set.ObjectMeta, tool)
		if !ok {
			remaining = append(remaining, tool)
			continue
		}
		if !inventory.reserveGenerated(atomic) || referenceNameExists(out.Spec.ToolRefs, atomic.Name) {
			remaining = append(remaining, tool)
			continue
		}
		objects = append(objects, atomic)
		generatedRefs = append(generatedRefs, agentsv1alpha1.NamespacedObjectReference{Name: atomic.Name})
	}
	out.Spec.Tools = remaining
	out.Spec.ToolRefs = append(generatedRefs, out.Spec.ToolRefs...)
	patched, err := patchSetComposition(original, "tools", out.Spec.Tools, "toolRefs", out.Spec.ToolRefs)
	if err != nil {
		return nil, err
	}
	objects = append(objects, patched)
	return objects, nil
}

func migratableLegacyTool(meta metav1.ObjectMeta, tool agentsv1alpha1.AgentRunToolSpec) (*agentsv1alpha1.AgentTool, bool) {
	name := strings.TrimSpace(tool.Name)
	if len(validation.IsDNS1123Label(name)) != 0 || strings.TrimSpace(tool.SetupScript) == "" || len(tool.VerifyCommand) == 0 || len(tool.VerifyCommand) > 32 {
		return nil, false
	}
	for _, argument := range tool.VerifyCommand {
		if argument == "" {
			return nil, false
		}
	}
	return &agentsv1alpha1.AgentTool{
		TypeMeta:   metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentTool"},
		ObjectMeta: migratedObjectMeta(meta, name),
		Spec: agentsv1alpha1.AgentToolSpec{
			Description:   tool.Description,
			Executable:    agentsv1alpha1.AgentToolExecutable{Name: tool.Name, Path: tool.Name},
			SetupScript:   tool.SetupScript,
			VerifyCommand: append([]string(nil), tool.VerifyCommand...),
		},
	}, true
}

func (inventory *migrationInventory) migrateRunProfileSpec(spec *agentsv1alpha1.AgentRunProfileSpec) bool {
	hadCanonicalSkills := spec.Capabilities != nil && spec.Capabilities.Skills != nil
	hadCanonicalTools := spec.Capabilities != nil && spec.Capabilities.Tools != nil
	changed := false
	if spec.Capabilities == nil {
		spec.Capabilities = &agentsv1alpha1.AgentCapabilitiesSpec{}
	}
	if spec.SkillSets != nil && !hadCanonicalSkills {
		spec.Capabilities.Skills = &agentsv1alpha1.AgentSkillCapabilityComposition{Mode: agentsv1alpha1.AgentCapabilityCompositionMode(spec.SkillSets.Mode), Overrides: append([]agentsv1alpha1.AgentSkillOverrideSpec(nil), spec.SkillSets.Overrides...)}
		for index := range spec.SkillSets.Refs {
			ref := spec.SkillSets.Refs[index]
			spec.Capabilities.Skills.Selections = append(spec.Capabilities.Skills.Selections, agentsv1alpha1.AgentSkillSelection{SkillSetRef: ref.DeepCopy()})
		}
		spec.SkillSets = nil
		changed = true
	}
	if spec.ToolSets != nil && !hadCanonicalTools {
		legacyTools := canonicalToolsFromLegacy(spec.ToolSets)
		if spec.Capabilities.Tools == nil {
			spec.Capabilities.Tools = legacyTools
		} else {
			spec.Capabilities.Tools.Selections = append(spec.Capabilities.Tools.Selections, legacyTools.Selections...)
			if legacyTools.Mode == agentsv1alpha1.AgentCapabilityCompositionReplace {
				spec.Capabilities.Tools.Mode = legacyTools.Mode
			}
		}
		spec.ToolSets = nil
		changed = true
	}
	if !changed && spec.Capabilities != nil && spec.Capabilities.Skills == nil && spec.Capabilities.Tools == nil && spec.Capabilities.MCPServers == nil {
		spec.Capabilities = nil
	}
	return changed
}

func canonicalToolsFromLegacy(legacy *agentsv1alpha1.AgentToolCompositionSpec) *agentsv1alpha1.AgentToolCapabilityComposition {
	out := &agentsv1alpha1.AgentToolCapabilityComposition{Mode: agentsv1alpha1.AgentCapabilityCompositionMode(legacy.Mode)}
	for index := range legacy.Refs {
		out.Selections = append(out.Selections, agentsv1alpha1.AgentToolSelection{ToolSetRef: legacy.Refs[index].DeepCopy()})
	}
	return out
}

func (inventory *migrationInventory) reserveGenerated(object metav1.Object) bool {
	runtimeObject := object.(runtime.Object)
	kind := runtimeObject.GetObjectKind().GroupVersionKind().Kind
	key := compositionGeneratedKey(kind, object.GetNamespace(), object.GetName())
	if _, ok := inventory.existing[key]; ok {
		return false
	}
	if _, ok := inventory.generated[key]; ok {
		return false
	}
	inventory.generated[key] = runtimeObject
	return true
}

func compositionGeneratedKey(kind, namespace, name string) string {
	return strings.TrimSpace(kind) + ":" + compositionObjectKey(namespace, name)
}

func referenceNameExists(refs []agentsv1alpha1.NamespacedObjectReference, name string) bool {
	for _, ref := range refs {
		if strings.TrimSpace(ref.Name) == strings.TrimSpace(name) {
			return true
		}
	}
	return false
}

func migratedObjectMeta(source metav1.ObjectMeta, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: source.Namespace, Labels: copyStringMapLocal(source.Labels), Annotations: copyStringMapLocal(source.Annotations)}
}

func copyStringMapLocal(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func compositionObjectKey(namespace, name string) string {
	return strings.TrimSpace(namespace) + "/" + strings.TrimSpace(name)
}

var githubRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func validGitHubRepository(repository string) bool {
	return githubRepositoryPattern.MatchString(strings.TrimSpace(repository))
}

func validGitHubAPIBaseURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func safeRelativePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func patchSetComposition(original *unstructured.Unstructured, legacyKey string, legacy any, refsKey string, refs any) (*unstructured.Unstructured, error) {
	out := original.DeepCopy()
	if err := setSpecField(out, legacyKey, legacy); err != nil {
		return nil, err
	}
	if err := setSpecField(out, refsKey, refs); err != nil {
		return nil, err
	}
	return out, nil
}

func patchProfileComposition(original *unstructured.Unstructured, spec *agentsv1alpha1.AgentRunProfileSpec) (*unstructured.Unstructured, error) {
	out := original.DeepCopy()
	for _, field := range []struct {
		name  string
		value any
	}{
		{name: "skillSets", value: spec.SkillSets},
		{name: "toolSets", value: spec.ToolSets},
		{name: "capabilities", value: spec.Capabilities},
	} {
		if err := setSpecField(out, field.name, field.value); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func setSpecField(object *unstructured.Unstructured, name string, value any) error {
	if value == nil {
		unstructured.RemoveNestedField(object.Object, "spec", name)
		return nil
	}
	reflected := reflect.ValueOf(value)
	if (reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Slice) && reflected.IsNil() || reflected.Kind() == reflect.Slice && reflected.Len() == 0 {
		unstructured.RemoveNestedField(object.Object, "spec", name)
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode migrated spec.%s: %w", name, err)
	}
	var normalized any
	if err := json.Unmarshal(body, &normalized); err != nil {
		return fmt.Errorf("normalize migrated spec.%s: %w", name, err)
	}
	return unstructured.SetNestedField(object.Object, normalized, "spec", name)
}
