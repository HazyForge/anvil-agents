package runnercapabilities

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func structuredInlineTool(t *testing.T, name, executable, script string) Tool {
	t.Helper()
	tool := Tool{
		Name:        name,
		Description: "test tool",
		Executable:  &controlv1alpha1.AgentToolExecutable{Name: executable, Path: "bin/" + executable},
		Source: &controlv1alpha1.AgentToolSource{InlineScript: &controlv1alpha1.AgentToolInlineScript{
			Interpreter: []string{"/bin/sh"}, Script: script,
		}},
		VerifyCommand: []string{executable, "--verify"},
	}
	digest, err := ComputeToolSpecDigest(tool)
	if err != nil {
		t.Fatal(err)
	}
	tool.SpecDigest = digest
	return tool
}

func structuredHTTPTool(t *testing.T, name, executable, url string, body []byte, format controlv1alpha1.AgentToolArchiveFormat, executablePath string) Tool {
	t.Helper()
	sum := sha256.Sum256(body)
	tool := Tool{
		Name:        name,
		Description: "download test tool",
		Executable:  &controlv1alpha1.AgentToolExecutable{Name: executable, Path: "bin/" + executable},
		Source: &controlv1alpha1.AgentToolSource{HTTPArtifact: &controlv1alpha1.AgentToolHTTPArtifactSource{Artifacts: []controlv1alpha1.AgentToolHTTPArtifact{{
			Platform: controlv1alpha1.AgentToolPlatform{OS: "linux", Arch: "amd64"}, URL: url,
			SHA256: hex.EncodeToString(sum[:]), Format: format, ExecutablePath: executablePath,
		}}}},
		VerifyCommand: []string{executable, "--version"},
	}
	digest, err := ComputeToolSpecDigest(tool)
	if err != nil {
		t.Fatal(err)
	}
	tool.SpecDigest = digest
	return tool
}

func TestParseToolManifestExactResolvedShape(t *testing.T) {
	tool := structuredInlineTool(t, "query", "query", "exit 0\n")
	raw, err := json.Marshal(ToolManifest{tool})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseToolManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 1 || parsed[0].Source.InlineScript.Interpreter[0] != "/bin/sh" {
		t.Fatalf("unexpected parsed manifest: %#v", parsed)
	}

	bad := strings.Replace(string(raw), `"description":"test tool"`, `"description":"test tool","unknown":true`, 1)
	if _, err := ParseToolManifest([]byte(bad)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field error = %v", err)
	}
	if _, err := ParseToolManifest(append(raw, []byte(` {}`)...)); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}

func TestManifestRejectsDigestMismatch(t *testing.T) {
	tool := structuredInlineTool(t, "query", "query", "exit 0\n")
	tool.Source.InlineScript.Script = "exit 1\n"
	if err := (ToolManifest{tool}).Validate(); err == nil || !strings.Contains(err.Error(), "specDigest mismatch") {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestComputeToolSpecDigestMatchesAgentToolSpecJSON(t *testing.T) {
	tool := structuredInlineTool(t, "query", "query", "exit 0\n")
	spec := controlv1alpha1.AgentToolSpec{
		Description: tool.Description, Executable: *tool.Executable,
		Source: tool.Source, VerifyCommand: tool.VerifyCommand,
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if got, _ := ComputeToolSpecDigest(tool); got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
}

func TestLegacySetupFileManifestRemainsAccepted(t *testing.T) {
	manifest := ToolManifest{{Name: "legacy", SetupFile: "/payload/tool-0.sh", VerifyCommand: []string{"git", "--version"}}}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalSetupFileManifestRemainsAccepted(t *testing.T) {
	manifest := ToolManifest{{
		Name: "custom",
		Executable: &controlv1alpha1.AgentToolExecutable{
			Name: "custom",
			Path: "bin/custom",
		},
		SpecDigest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SetupFile:     "/payload/tool-0.sh",
		VerifyCommand: []string{"custom", "--version"},
	}}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
}
