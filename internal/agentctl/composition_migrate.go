package agentctl

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
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
		if object.GetAPIVersion() != agentsv1alpha1.GroupVersion.String() {
			converted = append(converted, object.DeepCopy())
			continue
		}
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
			// Moving profile legacy selectors into the later canonical layer can
			// reverse precedence against append-only runs that still use legacy
			// run selectors. Emit canonical set candidates, but keep profiles
			// unchanged for an explicit producer-aware cutover.
			converted = append(converted, object.DeepCopy())
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
	if len(set.Spec.Skills) == 0 {
		return []runtime.Object{original.DeepCopy()}, nil
	}
	atomics := make([]*agentsv1alpha1.AgentSkill, 0, len(set.Spec.Skills))
	for _, skill := range set.Spec.Skills {
		atomic, ok := migratableLegacySkill(set.ObjectMeta, skill)
		if !ok || referenceNameExists(set.Spec.SkillRefs, atomic.Name) || !inventory.canReserveGenerated(atomic) {
			return []runtime.Object{original.DeepCopy()}, nil
		}
		atomics = append(atomics, atomic)
	}
	canonical := set.DeepCopy()
	canonical.Name = canonicalMigrationName(set.Name)
	canonical.ResourceVersion, canonical.UID, canonical.Generation = "", "", 0
	canonical.Spec.Skills = nil
	canonical.Spec.SkillRefs = nil
	if !inventory.canReserveGenerated(canonical) {
		return []runtime.Object{original.DeepCopy()}, nil
	}
	objects := make([]runtime.Object, 0, len(atomics)+2)
	for _, atomic := range atomics {
		inventory.reserveGenerated(atomic)
		objects = append(objects, atomic)
		canonical.Spec.SkillRefs = append(canonical.Spec.SkillRefs, agentsv1alpha1.NamespacedObjectReference{Name: atomic.Name})
	}
	canonical.Spec.SkillRefs = append(canonical.Spec.SkillRefs, set.Spec.SkillRefs...)
	inventory.reserveGenerated(canonical)
	objects = append(objects, canonical, original.DeepCopy())
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
	if len(set.Spec.Tools) == 0 {
		return []runtime.Object{original.DeepCopy()}, nil
	}
	atomics := make([]*agentsv1alpha1.AgentTool, 0, len(set.Spec.Tools))
	for _, tool := range set.Spec.Tools {
		atomic, ok := migratableLegacyTool(set.ObjectMeta, tool)
		if !ok || referenceNameExists(set.Spec.ToolRefs, atomic.Name) || !inventory.canReserveGenerated(atomic) {
			return []runtime.Object{original.DeepCopy()}, nil
		}
		atomics = append(atomics, atomic)
	}
	canonical := set.DeepCopy()
	canonical.Name = canonicalMigrationName(set.Name)
	canonical.ResourceVersion, canonical.UID, canonical.Generation = "", "", 0
	canonical.Spec.Tools = nil
	canonical.Spec.ToolRefs = nil
	if !inventory.canReserveGenerated(canonical) {
		return []runtime.Object{original.DeepCopy()}, nil
	}
	objects := make([]runtime.Object, 0, len(atomics)+2)
	for _, atomic := range atomics {
		inventory.reserveGenerated(atomic)
		objects = append(objects, atomic)
		canonical.Spec.ToolRefs = append(canonical.Spec.ToolRefs, agentsv1alpha1.NamespacedObjectReference{Name: atomic.Name})
	}
	canonical.Spec.ToolRefs = append(canonical.Spec.ToolRefs, set.Spec.ToolRefs...)
	inventory.reserveGenerated(canonical)
	objects = append(objects, canonical, original.DeepCopy())
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
			Executable:    agentsv1alpha1.AgentToolExecutable{Name: name, Path: name},
			SetupScript:   tool.SetupScript,
			VerifyCommand: append([]string(nil), tool.VerifyCommand...),
		},
	}, true
}

func (inventory *migrationInventory) reserveGenerated(object metav1.Object) bool {
	if !inventory.canReserveGenerated(object) {
		return false
	}
	runtimeObject := object.(runtime.Object)
	kind := runtimeObject.GetObjectKind().GroupVersionKind().Kind
	key := compositionGeneratedKey(kind, object.GetNamespace(), object.GetName())
	inventory.generated[key] = runtimeObject
	return true
}

func (inventory *migrationInventory) canReserveGenerated(object metav1.Object) bool {
	runtimeObject := object.(runtime.Object)
	kind := runtimeObject.GetObjectKind().GroupVersionKind().Kind
	key := compositionGeneratedKey(kind, object.GetNamespace(), object.GetName())
	_, inputExists := inventory.existing[key]
	_, generatedExists := inventory.generated[key]
	return !inputExists && !generatedExists
}

func canonicalMigrationName(name string) string {
	const suffix = "-canonical"
	name = strings.TrimSpace(name)
	if len(name)+len(suffix) <= 63 {
		return name + suffix
	}
	return strings.TrimRight(name[:63-len(suffix)], "-") + suffix
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
