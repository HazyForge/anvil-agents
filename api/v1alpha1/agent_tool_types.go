package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type AgentToolArchiveFormat string

const (
	AgentToolArchiveBinary AgentToolArchiveFormat = "binary"
	AgentToolArchiveTarGZ  AgentToolArchiveFormat = "tar.gz"
	AgentToolArchiveZip    AgentToolArchiveFormat = "zip"
)

// AgentToolPlatform selects one runner operating system and architecture.
type AgentToolPlatform struct {
	// OS is the Go-style operating system name.
	// +kubebuilder:validation:Enum=linux;darwin
	OS string `json:"os"`
	// Arch is the Go-style CPU architecture name.
	// +kubebuilder:validation:Enum=amd64;arm64
	Arch string `json:"arch"`
}

// AgentToolHTTPArtifact is one integrity-pinned HTTP artifact variant.
// +kubebuilder:validation:XValidation:rule="(self.format == 'binary' && !has(self.executablePath)) || (self.format != 'binary' && has(self.executablePath) && self.executablePath != ” && !self.executablePath.startsWith('/') && self.executablePath.split('/').all(p, p != ” && p != '.' && p != '..'))",message="executablePath must be empty for binary artifacts and a safe relative path for archives"
type AgentToolHTTPArtifact struct {
	Platform AgentToolPlatform `json:"platform"`
	// URL must use HTTPS.
	// +kubebuilder:validation:Pattern=`^https://[^[:space:]]+$`
	URL string `json:"url"`
	// SHA256 is the expected lowercase artifact digest.
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	SHA256 string `json:"sha256"`
	// Format controls safe extraction.
	// +kubebuilder:validation:Enum=binary;tar.gz;zip
	Format AgentToolArchiveFormat `json:"format"`
	// ExecutablePath is the safe relative file path inside an archive. It is
	// empty only for format=binary.
	// +optional
	ExecutablePath string `json:"executablePath,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.artifacts.all(a, self.artifacts.filter(b, b.platform.os == a.platform.os && b.platform.arch == a.platform.arch).size() == 1)",message="artifacts must not contain duplicate platforms"
type AgentToolHTTPArtifactSource struct {
	// Artifacts must contain at most one entry for each platform.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Artifacts []AgentToolHTTPArtifact `json:"artifacts"`
}

// AgentToolOCIArtifact is one digest-pinned OCI artifact variant.
// +kubebuilder:validation:XValidation:rule="!self.executablePath.startsWith('/') && self.executablePath.split('/').all(p, p != ” && p != '.' && p != '..')",message="executablePath must be a safe relative path"
type AgentToolOCIArtifact struct {
	Platform AgentToolPlatform `json:"platform"`
	// Reference must include a sha256 digest.
	// +kubebuilder:validation:Pattern=`^[^[:space:]@]+@sha256:[a-f0-9]{64}$`
	Reference string `json:"reference"`
	// ExecutablePath is the safe relative executable path in the artifact.
	// +kubebuilder:validation:MinLength=1
	ExecutablePath string `json:"executablePath"`
}

// +kubebuilder:validation:XValidation:rule="self.artifacts.all(a, self.artifacts.filter(b, b.platform.os == a.platform.os && b.platform.arch == a.platform.arch).size() == 1)",message="artifacts must not contain duplicate platforms"
type AgentToolOCIArtifactSource struct {
	// Artifacts must contain at most one entry for each platform.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Artifacts []AgentToolOCIArtifact `json:"artifacts"`
}

// AgentToolInlineScript is a complete executable script installed as the
// declared executable. It is distinct from setupScript, which is an
// unrestricted environment-mutating compatibility escape hatch.
// +kubebuilder:validation:XValidation:rule="self.interpreter.all(a, a != ”)",message="interpreter arguments must not be empty"
type AgentToolInlineScript struct {
	// Interpreter is an argv-form interpreter prefix, for example
	// ["/usr/bin/env", "bash"].
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	Interpreter []string `json:"interpreter"`
	// Script is the executable script body.
	// +kubebuilder:validation:MinLength=1
	Script string `json:"script"`
}

// AgentToolSource is an exclusive structured acquisition union.
// +kubebuilder:validation:XValidation:rule="(has(self.httpArtifact) ? 1 : 0) + (has(self.ociArtifact) ? 1 : 0) + (has(self.inlineScript) ? 1 : 0) == 1",message="exactly one tool source must be set"
type AgentToolSource struct {
	// +optional
	HTTPArtifact *AgentToolHTTPArtifactSource `json:"httpArtifact,omitempty"`
	// +optional
	OCIArtifact *AgentToolOCIArtifactSource `json:"ociArtifact,omitempty"`
	// +optional
	InlineScript *AgentToolInlineScript `json:"inlineScript,omitempty"`
}

// AgentToolExecutable identifies the command published into the per-run bin
// directory and its relative path inside the content-addressed install root.
// +kubebuilder:validation:XValidation:rule="!self.path.startsWith('/') && self.path.split('/').all(p, p != ” && p != '.' && p != '..')",message="path must be a safe relative path"
type AgentToolExecutable struct {
	// Name is the command name exposed on PATH.
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9._-]*$`
	Name string `json:"name"`
	// Path is a safe relative executable path.
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`
}

// AgentToolSpec defines one executable acquisition contract.
// +kubebuilder:validation:XValidation:rule="has(self.source) != (has(self.setupScript) && self.setupScript != ”)",message="exactly one of source or setupScript must be set"
// +kubebuilder:validation:XValidation:rule="self.verifyCommand.all(a, a != ”)",message="verifyCommand arguments must not be empty"
type AgentToolSpec struct {
	// Description explains what the executable does.
	// +optional
	Description string              `json:"description,omitempty"`
	Executable  AgentToolExecutable `json:"executable"`
	// Source is the preferred structured acquisition contract.
	// +optional
	Source *AgentToolSource `json:"source,omitempty"`
	// SetupScript is the unrestricted compatibility escape hatch. It runs with
	// the same authority as the consuming AgentRun and is composition-write
	// code-execution authority.
	// +optional
	SetupScript string `json:"setupScript,omitempty"`
	// VerifyCommand is an argv-form post-install check.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	VerifyCommand []string `json:"verifyCommand"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=agenttools,scope=Namespaced,shortName=agtool
// +kubebuilder:printcolumn:name="Executable",type="string",JSONPath=".spec.executable.name"
// +kubebuilder:printcolumn:name="Description",type="string",JSONPath=".spec.description"
type AgentTool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AgentToolSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type AgentToolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTool `json:"items"`
}
