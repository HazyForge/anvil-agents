package agentctl

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCompositionMigrateEmitsAtomicsSetsAndCanonicalProfileWithoutKubernetes(t *testing.T) {
	t.Parallel()
	input := `apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentSkillSet
metadata:
  name: reviewer
  namespace: agents
spec:
  skills:
    - name: evidence
      description: Review evidence.
      content: Read concrete files.
  tools:
    - name: evidence-check
      setupScript: install-evidence-check
      verifyCommand: [evidence-check, --version]
  subagents:
    - name: checker
      systemPrompt: Check the result.
---
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentToolSet
metadata:
  name: query-tools
  namespace: agents
spec:
  tools:
    - name: query
      setupScript: |
        export PATH="/tools:$PATH"
      verifyCommand: [query, --help]
---
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentRunProfile
metadata:
  name: review
  namespace: agents
spec:
  skillSets:
    refs: [{name: reviewer}]
  toolSets:
    refs: [{name: query-tools}]
  harness:
    systemPrompt: Preserve this inline overlay.
`
	var out, errOut bytes.Buffer
	factoryCalled := false
	app := App{
		In:  strings.NewReader(input),
		Out: &out,
		Err: &errOut,
		Factory: func(KubeOptions) (Backend, error) {
			factoryCalled = true
			return nil, nil
		},
	}
	if err := app.Run(context.Background(), []string{"composition", "migrate", "-f", "-"}); err != nil {
		t.Fatalf("migrate composition: %v stderr=%s", err, errOut.String())
	}
	if factoryCalled {
		t.Fatal("composition migration contacted the Kubernetes backend")
	}
	body := out.String()
	for _, want := range []string{
		"kind: AgentSkill\n",
		"name: evidence",
		"name: evidence-check",
		"name: reviewer-canonical",
		"name: query-tools-canonical",
		"skillRefs:",
		"kind: AgentTool\n",
		"name: query",
		"toolRefs:",
		"skillSets:",
		"toolSets:",
		"systemPrompt: Preserve this inline overlay.",
		"subagents:",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("migrated YAML missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "capabilities:") {
		t.Fatalf("migrator must not silently move profile selectors into a later precedence layer:\n%s", body)
	}
	for _, unwanted := range []string{"backend: {}", "resources: {}", "spiffeWorkloadAPI: {}", "scope: {}"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("targeted migration added zero-value field %q:\n%s", unwanted, body)
		}
	}
	if firstSkill, set := strings.Index(body, "kind: AgentSkill\n"), strings.Index(body, "kind: AgentSkillSet\n"); firstSkill < 0 || set < 0 || firstSkill > set {
		t.Fatalf("atomic skill must precede migrated set:\n%s", body)
	}
	if strings.Count(body, "name: evidence-check") < 1 || !strings.Contains(body, "setupScript: install-evidence-check") {
		t.Fatalf("embedded AgentSkillSet tools must remain as compatibility inputs:\n%s", body)
	}
}

func TestCompositionMigrateLeavesCanonicalInputCanonical(t *testing.T) {
	t.Parallel()
	input := `apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentRunProfile
metadata: {name: canonical, namespace: agents}
spec:
  capabilities:
    tools:
      mode: Replace
      selections:
        - toolRef: {name: query}
`
	var out bytes.Buffer
	app := App{In: strings.NewReader(input), Out: &out, Err: &bytes.Buffer{}}
	if err := app.Run(context.Background(), []string{"composition", "migrate", "-f", "-"}); err != nil {
		t.Fatalf("migrate canonical composition: %v", err)
	}
	body := out.String()
	if strings.Count(body, "toolRef:") != 1 || !strings.Contains(body, "mode: Replace") {
		t.Fatalf("canonical composition changed unexpectedly:\n%s", body)
	}
}

func TestCompositionMigratePreservesGeneratedNameCollision(t *testing.T) {
	t.Parallel()
	input := `apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentSkillSet
metadata: {name: review, namespace: agents}
spec:
  skills:
    - {name: "A B", content: one}
    - {name: "a-b", content: two}
`
	app := App{In: strings.NewReader(input), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	if err := app.Run(context.Background(), []string{"composition", "migrate", "-f", "-"}); err != nil {
		t.Fatalf("migrate colliding names: %v", err)
	}
	body := app.Out.(*bytes.Buffer).String()
	if strings.Contains(body, "kind: AgentSkill\n") || !strings.Contains(body, "name: A B") || !strings.Contains(body, "name: a-b") {
		t.Fatalf("a partially convertible set must remain entirely unchanged:\n%s", body)
	}
}

func TestCompositionMigratePreservesMixedLegacyAndCanonicalPrecedence(t *testing.T) {
	t.Parallel()
	input := `apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentRunProfile
metadata: {name: mixed, namespace: agents}
spec:
  skillSets:
    refs: [{name: legacy}]
  capabilities:
    skills:
      mode: Replace
      selections:
        - skillRef: {name: canonical}
`
	var out bytes.Buffer
	app := App{In: strings.NewReader(input), Out: &out, Err: &bytes.Buffer{}}
	if err := app.Run(context.Background(), []string{"composition", "migrate", "-f", "-"}); err != nil {
		t.Fatalf("migrate mixed composition: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "skillSets:") || strings.Count(body, "skillRef:") != 1 || !strings.Contains(body, "mode: Replace") {
		t.Fatalf("mixed precedence was rewritten lossily:\n%s", body)
	}
}

func TestCompositionMigratePreservesIdentityOrderingAndAppendOnlyRuns(t *testing.T) {
	t.Parallel()
	input := `apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentSkillSet
metadata: {name: review, namespace: agents}
spec:
  skillRefs: [{name: already-atomic, namespace: agents}]
  skills:
    - {name: evidence, content: "  keep whitespace  "}
---
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentToolSet
metadata: {name: tools, namespace: agents}
spec:
  toolRefs: [{name: already-atomic, namespace: agents}]
  tools:
    - name: query
      setupScript: install-query
      verifyCommand: [query, --version]
---
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentRun
metadata: {name: immutable-run, namespace: agents}
spec:
  prompt: keep
  skillSets:
    refs: [{name: review, namespace: agents}]
status:
  phase: Pending
`
	var out bytes.Buffer
	app := App{In: strings.NewReader(input), Out: &out, Err: &bytes.Buffer{}}
	if err := app.Run(context.Background(), []string{"composition", "migrate", "-f", "-"}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	setStart := strings.Index(body, "kind: AgentSkillSet\n")
	setEnd := strings.Index(body[setStart:], "---\n")
	skillSet := body[setStart : setStart+setEnd]
	if migrated, existing := strings.Index(skillSet, "name: evidence"), strings.Index(skillSet, "name: already-atomic"); migrated < 0 || existing < 0 || migrated > existing {
		t.Fatalf("formerly embedded skill must remain first and keep its identity:\n%s", skillSet)
	}
	toolSetStart := strings.Index(body, "kind: AgentToolSet\n")
	toolSetEnd := strings.Index(body[toolSetStart:], "---\n")
	toolSet := body[toolSetStart : toolSetStart+toolSetEnd]
	if migrated, existing := strings.Index(toolSet, "name: query"), strings.Index(toolSet, "name: already-atomic"); migrated < 0 || existing < 0 || migrated > existing {
		t.Fatalf("formerly embedded tool must remain first and keep its identity:\n%s", toolSet)
	}
	runStart := strings.Index(body, "kind: AgentRun\n")
	run := body[runStart:]
	if !strings.Contains(run, "skillSets:") || !strings.Contains(run, "status:\n  phase: Pending") || strings.Contains(run, "capabilities:") || strings.Contains(run, "backend: {}") {
		t.Fatalf("append-only AgentRun was rewritten:\n%s", run)
	}
}

func TestCompositionMigrateLeavesAdmissionInvalidLegacyEntriesEmbedded(t *testing.T) {
	t.Parallel()
	input := `apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentSkillSet
metadata: {name: review, namespace: agents}
spec:
  skills:
    - name: unsafe
      sourceRefs:
        - github:
            repository: not-a-repository
            ref: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
            path: ../SKILL.md
---
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentToolSet
metadata: {name: tools, namespace: agents}
spec:
  tools:
    - name: "not a dns name"
      setupScript: install
      verifyCommand: [""]
`
	var out bytes.Buffer
	app := App{In: strings.NewReader(input), Out: &out, Err: &bytes.Buffer{}}
	if err := app.Run(context.Background(), []string{"composition", "migrate", "-f", "-"}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if strings.Contains(body, "kind: AgentSkill\n") || strings.Contains(body, "kind: AgentTool\n") || !strings.Contains(body, "../SKILL.md") || !strings.Contains(body, "not a dns name") {
		t.Fatalf("invalid entries should remain compatibility inputs:\n%s", body)
	}
}

func TestCompositionMigrateLeavesDuplicateAtomicNamesEmbedded(t *testing.T) {
	t.Parallel()
	input := `apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentSkillSet
metadata: {name: review, namespace: agents}
spec:
  skills:
    - {name: same, content: one}
    - {name: same, content: two}
---
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentToolSet
metadata: {name: tools, namespace: agents}
spec:
  tools:
    - {name: same, setupScript: one, verifyCommand: [same]}
    - {name: same, setupScript: two, verifyCommand: [same]}
`
	var out bytes.Buffer
	app := App{In: strings.NewReader(input), Out: &out, Err: &bytes.Buffer{}}
	if err := app.Run(context.Background(), []string{"composition", "migrate", "-f", "-"}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if strings.Contains(body, "kind: AgentSkill\n") || strings.Contains(body, "kind: AgentTool\n") || strings.Contains(body, "-canonical") {
		t.Fatalf("sets with duplicate atomic names must remain entirely unchanged:\n%s", body)
	}
}

func TestCompositionMigratePassesThroughForeignVersionBeforeTypedDecode(t *testing.T) {
	t.Parallel()
	input := `apiVersion: control.anvil.hazyforge.io/v2
kind: AgentSkillSet
metadata: {name: future, namespace: agents}
spec:
  skills: not-the-v1alpha1-shape
  futureField: keep
`
	var out bytes.Buffer
	app := App{In: strings.NewReader(input), Out: &out, Err: &bytes.Buffer{}}
	if err := app.Run(context.Background(), []string{"composition", "migrate", "-f", "-"}); err != nil {
		t.Fatalf("foreign-version object should pass through: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "apiVersion: control.anvil.hazyforge.io/v2") || !strings.Contains(body, "skills: not-the-v1alpha1-shape") || !strings.Contains(body, "futureField: keep") {
		t.Fatalf("foreign-version object was not preserved:\n%s", body)
	}
}
