package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// AgentSkillMarkdownReference is one optional Markdown reference shipped with
// an inline AgentSkill. Paths are relative to the skill package root.
// +kubebuilder:validation:XValidation:rule="!self.path.startsWith('/') && self.path.split('/').all(p, p != ” && p != '.' && p != '..') && self.path.endsWith('.md')",message="path must be a safe relative Markdown path"
type AgentSkillMarkdownReference struct {
	// Path is the package-relative Markdown path.
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`
	// Content is the Markdown document body.
	Content string `json:"content"`
}

// AgentSkillInlineSource contains exactly one SKILL.md document and optional
// Markdown-only references. Executable scripts and binary assets belong in an
// AgentTool instead.
// +kubebuilder:validation:XValidation:rule="!has(self.references) || self.references.all(r, r.path != 'SKILL.md' && !r.path.endsWith('/SKILL.md'))",message="references must not contain another SKILL.md"
// +kubebuilder:validation:XValidation:rule="!has(self.references) || self.references.all(r, self.references.filter(q, q.path == r.path).size() == 1)",message="references must not contain duplicate paths"
type AgentSkillInlineSource struct {
	// SkillMD is the complete SKILL.md document.
	// +kubebuilder:validation:MinLength=1
	SkillMD string `json:"skillMD"`
	// References are optional Markdown documents in package-relative order.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	References []AgentSkillMarkdownReference `json:"references,omitempty"`
}

// AgentSkillGitHubSource points at an immutable Markdown-only package in a
// GitHub repository. Path must name SKILL.md; ReferencePaths may name only
// additional Markdown files below the same repository commit.
// +kubebuilder:validation:XValidation:rule="self.path == 'SKILL.md' || self.path.endsWith('/SKILL.md')",message="path must name SKILL.md"
// +kubebuilder:validation:XValidation:rule="!self.path.startsWith('/') && self.path.split('/').all(p, p != ” && p != '.' && p != '..')",message="path must be a safe relative path"
// +kubebuilder:validation:XValidation:rule="!has(self.referencePaths) || self.referencePaths.all(p, p != ” && !p.startsWith('/') && p.split('/').all(s, s != ” && s != '.' && s != '..') && p.endsWith('.md') && p != 'SKILL.md' && !p.endsWith('/SKILL.md'))",message="referencePaths must contain only safe relative non-SKILL Markdown paths"
// +kubebuilder:validation:XValidation:rule="!has(self.referencePaths) || self.referencePaths.all(p, self.referencePaths.filter(q, q == p).size() == 1)",message="referencePaths must not contain duplicates"
type AgentSkillGitHubSource struct {
	// Repository is owner/name.
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`
	Repository string `json:"repository"`
	// Ref is an immutable Git commit object ID.
	// +kubebuilder:validation:Pattern=`^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`
	Ref string `json:"ref"`
	// Path is the repository-relative path to SKILL.md.
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`
	// ReferencePaths are additional Markdown-only package documents.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	ReferencePaths []string `json:"referencePaths,omitempty"`
	// APIBaseURL overrides the GitHub API base for an allowlisted GitHub
	// Enterprise host. Empty uses https://api.github.com.
	// +kubebuilder:validation:Pattern=`^https://[^/?#]+(?:/[^?#]*)?$`
	// +optional
	APIBaseURL string `json:"apiBaseURL,omitempty"`
}

// AgentSkillSpec defines one Markdown-only instruction package.
// +kubebuilder:validation:XValidation:rule="has(self.inline) != has(self.github)",message="exactly one of inline or github must be set"
type AgentSkillSpec struct {
	// Description explains when the skill should be selected.
	// +optional
	Description string `json:"description,omitempty"`
	// Inline embeds the Markdown package in the CR.
	// +optional
	Inline *AgentSkillInlineSource `json:"inline,omitempty"`
	// GitHub selects an immutable Markdown package by commit.
	// +optional
	GitHub *AgentSkillGitHubSource `json:"github,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=agentskills,scope=Namespaced,shortName=agskill
// +kubebuilder:printcolumn:name="Description",type="string",JSONPath=".spec.description"
// +kubebuilder:printcolumn:name="Source",type="string",JSONPath=".spec.github.repository"
type AgentSkill struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AgentSkillSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type AgentSkillList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentSkill `json:"items"`
}
