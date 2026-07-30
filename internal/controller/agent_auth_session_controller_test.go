package controller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestValidateAgentAuthSessionSpec(t *testing.T) {
	t.Parallel()
	valid := &controlv1alpha1.AgentAuthSession{
		Spec: controlv1alpha1.AgentAuthSessionSpec{
			Provider:         controlv1alpha1.AgentAuthSessionProviderCodex,
			Action:           controlv1alpha1.AgentAuthSessionActionReauth,
			DataVolumeRef:    corev1.LocalObjectReference{Name: "codex-home"},
			StagingSecretRef: &corev1.LocalObjectReference{Name: "staging"},
			SeedID:           "seed-1",
		},
	}
	if err := validateAgentAuthSessionSpec(valid); err != nil {
		t.Fatalf("valid reauth rejected: %v", err)
	}
	logout := valid.DeepCopy()
	logout.Spec.Action = controlv1alpha1.AgentAuthSessionActionLogout
	logout.Spec.StagingSecretRef = nil
	logout.Spec.SeedID = ""
	if err := validateAgentAuthSessionSpec(logout); err != nil {
		t.Fatalf("valid logout rejected: %v", err)
	}
	missingSeed := valid.DeepCopy()
	missingSeed.Spec.SeedID = ""
	if err := validateAgentAuthSessionSpec(missingSeed); err == nil {
		t.Fatal("expected missing seedID error")
	}
}

func TestAgentAuthSessionScriptNeverEchoesCredentials(t *testing.T) {
	t.Parallel()
	codex, err := agentAuthLayout(controlv1alpha1.AgentAuthSessionProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	script := agentAuthSessionScript(controlv1alpha1.AgentAuthSessionActionReauth, "/codex-home", codex)
	for _, banned := range []string{"echo \"$CODEX_AUTH_JSON\"", "cat \"$auth\"", "printenv"} {
		if strings.Contains(script, banned) {
			t.Fatalf("script contains banned credential leak pattern %q", banned)
		}
	}
	for _, required := range []string{"auth.json", ".anvil-codex-auth-seed-id", ".anvil-codex-auth-logged-out", "mktemp", "mv ", "CODEX_AUTH_JSON"} {
		if !strings.Contains(script, required) {
			t.Fatalf("reauth script missing %q", required)
		}
	}
	logout := agentAuthSessionScript(controlv1alpha1.AgentAuthSessionActionLogout, "/codex-home", codex)
	if !strings.Contains(logout, "logged-out") {
		t.Fatal("logout script missing tombstone write")
	}

	grok, err := agentAuthLayout(controlv1alpha1.AgentAuthSessionProviderGrokBuild)
	if err != nil {
		t.Fatal(err)
	}
	grokScript := agentAuthSessionScript(controlv1alpha1.AgentAuthSessionActionReauth, "/opt/anvil/grok-build", grok)
	for _, required := range []string{".grok/auth.json", "GROK_AUTH_JSON", "ANVIL_GROK_AUTH_SEED_ID", ".anvil-grok-auth-seed-id"} {
		if !strings.Contains(grokScript, required) {
			t.Fatalf("grok reauth script missing %q\n%s", required, grokScript)
		}
	}
}

func TestValidateAgentAuthSessionSpecAcceptsGrokBuild(t *testing.T) {
	t.Parallel()
	session := &controlv1alpha1.AgentAuthSession{
		Spec: controlv1alpha1.AgentAuthSessionSpec{
			Provider:         controlv1alpha1.AgentAuthSessionProviderGrokBuild,
			Action:           controlv1alpha1.AgentAuthSessionActionReauth,
			DataVolumeRef:    corev1.LocalObjectReference{Name: "grok-home"},
			StagingSecretRef: &corev1.LocalObjectReference{Name: "staging"},
			SeedID:           "seed-xai",
		},
	}
	if err := validateAgentAuthSessionSpec(session); err != nil {
		t.Fatalf("grokBuild reauth rejected: %v", err)
	}
}

func TestValidateAgentAuthSessionSpecOpenClaw(t *testing.T) {
	t.Parallel()
	valid := &controlv1alpha1.AgentAuthSession{
		Spec: controlv1alpha1.AgentAuthSessionSpec{
			Provider:         controlv1alpha1.AgentAuthSessionProviderOpenClaw,
			Action:           controlv1alpha1.AgentAuthSessionActionReauth,
			AuthMode:         controlv1alpha1.AgentRunProviderAuthModeOAuth,
			AgentID:          "anvil",
			ModelProvider:    "xai",
			DataVolumeRef:    corev1.LocalObjectReference{Name: "openclaw-home"},
			StagingSecretRef: &corev1.LocalObjectReference{Name: "staging"},
			SeedID:           "seed-oc",
		},
	}
	if err := validateAgentAuthSessionSpec(valid); err != nil {
		t.Fatalf("openClaw reauth rejected: %v", err)
	}
	verify := valid.DeepCopy()
	verify.Spec.Action = controlv1alpha1.AgentAuthSessionActionVerify
	verify.Spec.StagingSecretRef = nil
	verify.Spec.SeedID = ""
	if err := validateAgentAuthSessionSpec(verify); err != nil {
		t.Fatalf("openClaw verify rejected: %v", err)
	}
	missingAgent := valid.DeepCopy()
	missingAgent.Spec.AgentID = ""
	if err := validateAgentAuthSessionSpec(missingAgent); err == nil {
		t.Fatal("expected missing agentID error")
	}
	missingModelProvider := valid.DeepCopy()
	missingModelProvider.Spec.ModelProvider = ""
	if err := validateAgentAuthSessionSpec(missingModelProvider); err == nil {
		t.Fatal("expected missing modelProvider error")
	}
	codexWithAgent := &controlv1alpha1.AgentAuthSession{
		Spec: controlv1alpha1.AgentAuthSessionSpec{
			Provider:         controlv1alpha1.AgentAuthSessionProviderCodex,
			Action:           controlv1alpha1.AgentAuthSessionActionReauth,
			AgentID:          "nope",
			DataVolumeRef:    corev1.LocalObjectReference{Name: "codex-home"},
			StagingSecretRef: &corev1.LocalObjectReference{Name: "staging"},
			SeedID:           "seed",
		},
	}
	if err := validateAgentAuthSessionSpec(codexWithAgent); err == nil {
		t.Fatal("expected agentID forbidden for codex")
	}
	verifyWithSeed := verify.DeepCopy()
	verifyWithSeed.Spec.SeedID = "seed"
	if err := validateAgentAuthSessionSpec(verifyWithSeed); err == nil {
		t.Fatal("expected seedID forbidden for verify")
	}
}

func TestValidateOpenClawAuthProfilesJSON(t *testing.T) {
	t.Parallel()
	oauth := []byte(`{"version":1,"profiles":{"xai:default":{"type":"oauth","provider":"xai","access":"a","refresh":"r","expires":4102444800}}}`)
	if err := validateOpenClawAuthProfilesJSON(oauth, controlv1alpha1.AgentRunProviderAuthModeOAuth, "xai"); err != nil {
		t.Fatalf("oauth store rejected: %v", err)
	}
	apiKey := []byte(`{"version":1,"profiles":{"xai:default":{"type":"api_key","provider":"xai","key":"k"}}}`)
	if err := validateOpenClawAuthProfilesJSON(apiKey, controlv1alpha1.AgentRunProviderAuthModeAPIKey, "xai"); err != nil {
		t.Fatalf("api_key store rejected: %v", err)
	}
	keyRef := []byte(`{"version":1,"profiles":{"xai:default":{"type":"api_key","provider":"xai","keyRef":{"source":"env","provider":"default","id":"XAI_API_KEY"}}}}`)
	if err := validateOpenClawAuthProfilesJSON(keyRef, controlv1alpha1.AgentRunProviderAuthModeAPIKey, "xai"); err != nil {
		t.Fatalf("api_key keyRef store rejected: %v", err)
	}
	if err := validateOpenClawAuthProfilesJSON(oauth, controlv1alpha1.AgentRunProviderAuthModeOAuth, "openai"); err == nil {
		t.Fatal("expected model provider mismatch")
	}
	if err := validateOpenClawAuthProfilesJSON(oauth, controlv1alpha1.AgentRunProviderAuthModeAPIKey, "xai"); err == nil {
		t.Fatal("expected mode mismatch")
	}
	mixed := []byte(`{"version":1,"profiles":{"a":{"type":"oauth","provider":"p","access":"a","refresh":"r","expires":1},"b":{"type":"api_key","provider":"p","key":"k"}}}`)
	if err := validateOpenClawAuthProfilesJSON(mixed, controlv1alpha1.AgentRunProviderAuthModeOAuth, "p"); err == nil {
		t.Fatal("expected mixed type rejection")
	}
}

func TestAgentAuthOpenClawScriptAndImage(t *testing.T) {
	t.Parallel()
	layout, err := agentAuthLayout(controlv1alpha1.AgentAuthSessionProviderOpenClaw)
	if err != nil {
		t.Fatal(err)
	}
	script := agentAuthSessionScript(controlv1alpha1.AgentAuthSessionActionReauth, "/opt/anvil/openclaw", layout)
	for _, required := range []string{
		"OPENCLAW_AUTH_PROFILES_JSON",
		"OPENCLAW_AUTH_SEED_ID",
		"saveAuthProfileStore",
		"ANVIL_OPENCLAW_AGENT_ID",
		"ANVIL_OPENCLAW_MODEL_PROVIDER",
		"ANVIL_AUTH_SESSION_COMPLETE",
		"openclaw.json",
		"agent-config-unsafe",
		"agent-dir-outside-volume",
		"loadAuthProfileStoreWithoutExternalProfiles",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("openClaw reauth script missing %q", required)
		}
	}
	for _, banned := range []string{`echo "$OPENCLAW_AUTH_PROFILES_JSON"`, "printenv", "console.log(profilesRaw)", "spawnSync", "openclaw agents", "updateOpenclawConfigAuth", "models', 'status"} {
		if strings.Contains(script, banned) {
			t.Fatalf("script leaks credentials via %q", banned)
		}
	}
	verify := agentAuthSessionScript(controlv1alpha1.AgentAuthSessionActionVerify, "/opt/anvil/openclaw", layout)
	if !strings.Contains(verify, "action=verify") {
		t.Fatal("verify script missing completion marker")
	}
	uid, gid := agentAuthSessionIDs(controlv1alpha1.AgentAuthSessionProviderOpenClaw)
	if uid != 1000 || gid != 1000 {
		t.Fatalf("openClaw ids = %d/%d", uid, gid)
	}
	cuid, cgid := agentAuthSessionIDs(controlv1alpha1.AgentAuthSessionProviderCodex)
	if cuid != 10001 || cgid != 10001 {
		t.Fatalf("codex ids = %d/%d", cuid, cgid)
	}
	r := &AgentAuthSessionReconciler{CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{OpenClawRunnerImage: "openclaw:test"}}}
	if got := r.authSessionImage(controlv1alpha1.AgentAuthSessionProviderOpenClaw); got != "openclaw:test" {
		t.Fatalf("image = %s", got)
	}
}

func TestAgentAuthSessionEnvProjectsOnlyRequiredStagingKey(t *testing.T) {
	t.Parallel()
	obj := &controlv1alpha1.AgentAuthSession{Spec: controlv1alpha1.AgentAuthSessionSpec{
		Provider:         controlv1alpha1.AgentAuthSessionProviderOpenClaw,
		Action:           controlv1alpha1.AgentAuthSessionActionReauth,
		AuthMode:         controlv1alpha1.AgentRunProviderAuthModeOAuth,
		AgentID:          "anvil",
		ModelProvider:    "xai",
		StagingSecretRef: &corev1.LocalObjectReference{Name: "staging"},
		SeedID:           "seed-1",
	}}
	env := agentAuthSessionEnv(obj)
	var credential *corev1.EnvVar
	for i := range env {
		if env[i].ValueFrom != nil {
			if credential != nil {
				t.Fatal("more than one staging Secret key was projected")
			}
			credential = &env[i]
		}
	}
	if credential == nil || credential.Name != "OPENCLAW_AUTH_PROFILES_JSON" {
		t.Fatalf("credential env = %#v", credential)
	}
	ref := credential.ValueFrom.SecretKeyRef
	if ref == nil || ref.Name != "staging" || ref.Key != "OPENCLAW_AUTH_PROFILES_JSON" {
		t.Fatalf("credential SecretKeyRef = %#v", ref)
	}
}

func TestAgentAuthVolumeBackendMustBeExplicit(t *testing.T) {
	t.Parallel()
	volume := &controlv1alpha1.AgentDataVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "openclaw-home", Namespace: "agents"},
		Spec: controlv1alpha1.AgentDataVolumeSpec{
			ProfileRef: &corev1.LocalObjectReference{Name: "shared-home-shape"},
		},
	}
	reason, _ := agentAuthVolumeBackendValidation(volume, controlv1alpha1.AgentAuthSessionProviderOpenClaw)
	if reason != "DataVolumeBackendRequired" {
		t.Fatalf("reason = %q, want explicit backend requirement", reason)
	}
	volume.Spec.Backend = controlv1alpha1.AgentRunHarnessBackendOpenClaw
	if reason, message := agentAuthVolumeBackendValidation(volume, controlv1alpha1.AgentAuthSessionProviderOpenClaw); reason != "" {
		t.Fatalf("explicit matching backend rejected: %s: %s", reason, message)
	}
	if reason, _ := agentAuthVolumeBackendValidation(volume, controlv1alpha1.AgentAuthSessionProviderCodex); reason != "ProviderVolumeMismatch" {
		t.Fatalf("reason = %q, want provider mismatch", reason)
	}
}

func TestAgentAuthOpenClawPinnedImageLifecycle(t *testing.T) {
	if os.Getenv("ANVIL_RUN_OPENCLAW_IMAGE_TEST") != "true" {
		t.Skip("set ANVIL_RUN_OPENCLAW_IMAGE_TEST=true to run the pinned-image contract")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("docker is required: %v", err)
	}
	const image = "ghcr.io/hazyforge/anvil-agent-run-openclaw@sha256:7bd2164cc2b21fd2c2f2f20342ff2f2b45c90add452532e64c68c8823a4efc7e"
	root := t.TempDir()
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")
	agentDir := filepath.Join(stateDir, "anvil")
	for _, dir := range []string{stateDir, agentDir, filepath.Join(root, "workspace")} {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	config := `{"agents":{"list":[{"id":"anvil","agentDir":"/opt/anvil/openclaw/state/anvil","workspace":"/opt/anvil/openclaw/workspace"}]}}`
	if err := os.WriteFile(filepath.Join(stateDir, "openclaw.json"), []byte(config), 0o666); err != nil {
		t.Fatal(err)
	}
	layout, err := agentAuthLayout(controlv1alpha1.AgentAuthSessionProviderOpenClaw)
	if err != nil {
		t.Fatal(err)
	}
	writeScript := func(name string, action controlv1alpha1.AgentAuthSessionAction) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(agentAuthSessionScript(action, "/opt/anvil/openclaw", layout)), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	reauthScript := writeScript("reauth.sh", controlv1alpha1.AgentAuthSessionActionReauth)
	verifyScript := writeScript("verify.sh", controlv1alpha1.AgentAuthSessionActionVerify)
	logoutScript := writeScript("logout.sh", controlv1alpha1.AgentAuthSessionActionLogout)
	const access = "anvil-fixture-access-do-not-log"
	const refresh = "anvil-fixture-refresh-do-not-log"
	profiles := `{"version":1,"profiles":{"xai:default":{"type":"oauth","provider":"xai","access":"` + access + `","refresh":"` + refresh + `","expires":4102444800}}}`
	run := func(script, provider string, withProfiles bool) ([]byte, error) {
		args := []string{"run", "--rm", "--entrypoint", "/bin/bash",
			"-e", "ANVIL_OPENCLAW_AGENT_ID=anvil",
			"-e", "ANVIL_OPENCLAW_MODEL_PROVIDER=" + provider,
			"-e", "ANVIL_AUTH_MODE=oauth",
			"-v", root + ":/opt/anvil/openclaw",
			"-v", script + ":/anvil-auth-test.sh:ro"}
		if withProfiles {
			args = append(args, "-e", "OPENCLAW_AUTH_PROFILES_JSON", "-e", "OPENCLAW_AUTH_SEED_ID=seed-test")
		}
		args = append(args, image, "/anvil-auth-test.sh")
		cmd := exec.Command("docker", args...)
		cmd.Env = append(os.Environ(), "OPENCLAW_AUTH_PROFILES_JSON="+profiles)
		return cmd.CombinedOutput()
	}
	assertNoCredentials := func(output []byte) {
		t.Helper()
		if strings.Contains(string(output), access) || strings.Contains(string(output), refresh) {
			t.Fatalf("runtime output leaked fixture credentials: %s", output)
		}
	}
	output, err := run(reauthScript, "xai", true)
	assertNoCredentials(output)
	if err != nil {
		t.Fatalf("reauth failed: %v\n%s", err, output)
	}
	dbPath := filepath.Join(agentDir, "openclaw-agent.sqlite")
	python := exec.Command("python3", "-c", `import sqlite3,sys; db=sqlite3.connect(sys.argv[1]); db.execute("create table anvil_sentinel(value text)"); db.execute("insert into anvil_sentinel values ('preserve-me')"); db.commit()`, dbPath)
	if output, err := python.CombinedOutput(); err != nil {
		t.Fatalf("create SQLite sentinel: %v\n%s", err, output)
	}
	output, err = run(verifyScript, "xai", false)
	assertNoCredentials(output)
	if err != nil {
		t.Fatalf("verify failed: %v\n%s", err, output)
	}
	output, err = run(verifyScript, "openai", false)
	assertNoCredentials(output)
	if err == nil || !strings.Contains(string(output), "no-usable-profile") {
		t.Fatalf("wrong-provider verify should fail closed: err=%v output=%s", err, output)
	}
	output, err = run(logoutScript, "xai", false)
	assertNoCredentials(output)
	if err != nil {
		t.Fatalf("logout failed: %v\n%s", err, output)
	}
	python = exec.Command("python3", "-c", `import sqlite3,sys; db=sqlite3.connect(sys.argv[1]); assert db.execute("select value from anvil_sentinel").fetchone()[0] == 'preserve-me'`, dbPath)
	if output, err := python.CombinedOutput(); err != nil {
		t.Fatalf("SQLite sentinel was not preserved: %v\n%s", err, output)
	}
	if err := os.RemoveAll(agentDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp", agentDir); err != nil {
		t.Fatal(err)
	}
	output, err = run(verifyScript, "xai", false)
	assertNoCredentials(output)
	if err == nil || !strings.Contains(string(output), "agent-dir-unsafe") {
		t.Fatalf("symlink agentDir should fail closed: err=%v output=%s", err, output)
	}
}

func TestSecretManagedByExternalSecret(t *testing.T) {
	t.Parallel()
	plain := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "seed"}}
	if secretManagedByExternalSecret(plain) {
		t.Fatal("plain secret should be writable")
	}
	eso := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: "seed",
		Labels: map[string]string{
			"reconcile.external-secrets.io/managed": "true",
		},
	}}
	if !secretManagedByExternalSecret(eso) {
		t.Fatal("eso-labeled secret should be detected")
	}
}

func TestAgentRunIsTerminalForAuthIdle(t *testing.T) {
	t.Parallel()
	if !agentRunIsTerminal(controlv1alpha1.AgentRunPhaseSucceeded) || !agentRunIsTerminal(controlv1alpha1.AgentRunPhaseFailed) {
		t.Fatal("expected terminal phases")
	}
	if agentRunIsTerminal(controlv1alpha1.AgentRunPhaseRunning) || agentRunIsTerminal(controlv1alpha1.AgentRunPhasePending) {
		t.Fatal("expected non-terminal phases")
	}
}
