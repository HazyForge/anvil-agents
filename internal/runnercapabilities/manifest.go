// Package runnercapabilities installs and verifies the normalized capability
// payload prepared by the AgentRun controller.
package runnercapabilities

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	DefaultMaxToolManifestSize = int64(4 << 20)
	maxTools                   = 128
	maxArtifactsPerTool        = 16
	maxVerifyArguments         = 32
	maxInterpreterArguments    = 8
	maxStringLength            = 4096
	maxInlineScriptSize        = 1 << 20
)

var safeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ToolManifest is the exact resolved AgentRun tool array, with setupScript
// replaced by the path of its separately mounted compatibility file.
type ToolManifest []Tool

type Tool struct {
	Name          string                               `json:"name"`
	Description   string                               `json:"description,omitempty"`
	Executable    *controlv1alpha1.AgentToolExecutable `json:"executable,omitempty"`
	Source        *controlv1alpha1.AgentToolSource     `json:"source,omitempty"`
	SpecDigest    string                               `json:"specDigest,omitempty"`
	SetupFile     string                               `json:"setupFile,omitempty"`
	VerifyCommand []string                             `json:"verifyCommand,omitempty"`
}

type Platform struct {
	OS   string
	Arch string
}

func ParseToolManifest(contents []byte) (ToolManifest, error) {
	return DecodeToolManifest(bytes.NewReader(contents), DefaultMaxToolManifestSize)
}

func DecodeToolManifest(reader io.Reader, maxBytes int64) (ToolManifest, error) {
	if maxBytes <= 0 {
		return nil, errors.New("tool manifest size limit must be positive")
	}
	contents, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read tool manifest: %w", err)
	}
	if int64(len(contents)) > maxBytes {
		return nil, fmt.Errorf("tool manifest exceeds %d bytes", maxBytes)
	}

	var manifest ToolManifest
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode tool manifest: %w", err)
	}
	if manifest == nil {
		return nil, errors.New("decode tool manifest: expected a JSON array")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decode tool manifest: multiple JSON values are not allowed")
		}
		return nil, fmt.Errorf("decode tool manifest trailing data: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return manifest, nil
}

func (manifest ToolManifest) Validate() error {
	if len(manifest) > maxTools {
		return fmt.Errorf("tool manifest contains %d tools; maximum is %d", len(manifest), maxTools)
	}
	toolNames := make(map[string]struct{}, len(manifest))
	executableNames := make(map[string]struct{}, len(manifest))
	for index := range manifest {
		tool := &manifest[index]
		if err := tool.validate(); err != nil {
			return fmt.Errorf("tools[%d]: %w", index, err)
		}
		if _, found := toolNames[tool.Name]; found {
			return fmt.Errorf("tools[%d]: duplicate tool name %q", index, tool.Name)
		}
		toolNames[tool.Name] = struct{}{}
		if tool.Executable != nil {
			if _, found := executableNames[tool.Executable.Name]; found {
				return fmt.Errorf("tools[%d]: duplicate executable name %q", index, tool.Executable.Name)
			}
			executableNames[tool.Executable.Name] = struct{}{}
		}
	}
	return nil
}

func (tool Tool) validate() error {
	if !safeNamePattern.MatchString(tool.Name) {
		return fmt.Errorf("name %q is not a safe tool name", tool.Name)
	}
	if len(tool.Description) > maxStringLength || strings.IndexByte(tool.Description, 0) >= 0 {
		return errors.New("description is invalid")
	}
	if tool.SetupFile != "" && (len(tool.SetupFile) > maxStringLength || strings.IndexByte(tool.SetupFile, 0) >= 0 || !path.IsAbs(tool.SetupFile) || path.Clean(tool.SetupFile) != tool.SetupFile) {
		return errors.New("setupFile must be a clean absolute path")
	}
	if len(tool.VerifyCommand) > maxVerifyArguments {
		return fmt.Errorf("verifyCommand contains %d arguments; maximum is %d", len(tool.VerifyCommand), maxVerifyArguments)
	}
	for index, argument := range tool.VerifyCommand {
		if argument == "" || len(argument) > maxStringLength || strings.IndexByte(argument, 0) >= 0 {
			return fmt.Errorf("verifyCommand[%d] is invalid", index)
		}
	}

	// Setup-file-only entries are compatibility inputs. Canonical AgentTools
	// retain executable and specDigest metadata, while legacy embedded tools do
	// not. The wrapper file differs from the original setupScript, so its
	// controller-owned spec digest cannot be recomputed here.
	if tool.Source == nil {
		if tool.Executable == nil && tool.SpecDigest == "" {
			return nil
		}
		if tool.SetupFile == "" || tool.Executable == nil || !validPrefixedSHA256(tool.SpecDigest) {
			return errors.New("canonical setupFile tools require executable and specDigest")
		}
		if !safeNamePattern.MatchString(tool.Executable.Name) {
			return fmt.Errorf("executable.name %q is not a safe file name", tool.Executable.Name)
		}
		if err := validateRelativePath(tool.Executable.Path); err != nil {
			return fmt.Errorf("executable.path: %w", err)
		}
		return nil
	}
	if tool.SetupFile != "" {
		return errors.New("structured source and setupFile are mutually exclusive")
	}
	if tool.Executable == nil {
		return errors.New("structured source requires executable")
	}
	if !safeNamePattern.MatchString(tool.Executable.Name) {
		return fmt.Errorf("executable.name %q is not a safe file name", tool.Executable.Name)
	}
	if err := validateRelativePath(tool.Executable.Path); err != nil {
		return fmt.Errorf("executable.path: %w", err)
	}
	if len(tool.VerifyCommand) == 0 {
		return errors.New("structured source requires verifyCommand")
	}
	if !validPrefixedSHA256(tool.SpecDigest) {
		return errors.New("specDigest must be a lowercase sha256 digest")
	}
	if err := validateSource(tool.Source); err != nil {
		return err
	}
	want, err := ComputeToolSpecDigest(tool)
	if err != nil {
		return fmt.Errorf("compute spec digest: %w", err)
	}
	if tool.SpecDigest != want {
		return fmt.Errorf("specDigest mismatch: got %q, want %q", tool.SpecDigest, want)
	}
	return nil
}

func validateSource(source *controlv1alpha1.AgentToolSource) error {
	count := 0
	if source.HTTPArtifact != nil {
		count++
	}
	if source.OCIArtifact != nil {
		count++
	}
	if source.InlineScript != nil {
		count++
	}
	if count != 1 {
		return errors.New("source must configure exactly one of httpArtifact, ociArtifact, or inlineScript")
	}
	if inline := source.InlineScript; inline != nil {
		if len(inline.Interpreter) == 0 || len(inline.Interpreter) > maxInterpreterArguments {
			return fmt.Errorf("source.inlineScript.interpreter must contain between 1 and %d arguments", maxInterpreterArguments)
		}
		for index, argument := range inline.Interpreter {
			if argument == "" || len(argument) > maxStringLength || strings.IndexByte(argument, 0) >= 0 {
				return fmt.Errorf("source.inlineScript.interpreter[%d] is invalid", index)
			}
		}
		if inline.Script == "" || len(inline.Script) > maxInlineScriptSize || strings.IndexByte(inline.Script, 0) >= 0 {
			return fmt.Errorf("source.inlineScript.script must contain between 1 and %d bytes", maxInlineScriptSize)
		}
		return nil
	}
	if source.OCIArtifact != nil {
		artifacts := source.OCIArtifact.Artifacts
		if len(artifacts) == 0 || len(artifacts) > maxArtifactsPerTool {
			return fmt.Errorf("source.ociArtifact.artifacts must contain between 1 and %d entries", maxArtifactsPerTool)
		}
		selectors := make(map[string]struct{}, len(artifacts))
		for index := range artifacts {
			artifact := &artifacts[index]
			selector := artifact.Platform.OS + "/" + artifact.Platform.Arch
			if _, found := selectors[selector]; found {
				return fmt.Errorf("source.ociArtifact.artifacts[%d] duplicates platform %q", index, selector)
			}
			selectors[selector] = struct{}{}
			if !validPlatformValue(artifact.Platform.OS) || !validPlatformValue(artifact.Platform.Arch) {
				return fmt.Errorf("source.ociArtifact.artifacts[%d] has invalid platform %q", index, selector)
			}
			if err := validateOCIReference(artifact.Reference); err != nil {
				return fmt.Errorf("source.ociArtifact.artifacts[%d].reference: %w", index, err)
			}
			if err := validateRelativePath(artifact.ExecutablePath); err != nil {
				return fmt.Errorf("source.ociArtifact.artifacts[%d].executablePath: %w", index, err)
			}
		}
		return nil
	}
	artifacts := source.HTTPArtifact.Artifacts
	if len(artifacts) == 0 || len(artifacts) > maxArtifactsPerTool {
		return fmt.Errorf("source.httpArtifact.artifacts must contain between 1 and %d entries", maxArtifactsPerTool)
	}
	selectors := make(map[string]struct{}, len(artifacts))
	for index := range artifacts {
		artifact := &artifacts[index]
		selector := artifact.Platform.OS + "/" + artifact.Platform.Arch
		if _, found := selectors[selector]; found {
			return fmt.Errorf("source.httpArtifact.artifacts[%d] duplicates platform %q", index, selector)
		}
		selectors[selector] = struct{}{}
		if !validPlatformValue(artifact.Platform.OS) || !validPlatformValue(artifact.Platform.Arch) {
			return fmt.Errorf("source.httpArtifact.artifacts[%d] has invalid platform %q", index, selector)
		}
		parsed, err := url.Parse(artifact.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return fmt.Errorf("source.httpArtifact.artifacts[%d].url must be HTTPS without credentials or a fragment", index)
		}
		if len(artifact.URL) > maxStringLength || !validBareSHA256(artifact.SHA256) {
			return fmt.Errorf("source.httpArtifact.artifacts[%d] has invalid URL or sha256", index)
		}
		switch artifact.Format {
		case controlv1alpha1.AgentToolArchiveBinary:
			if artifact.ExecutablePath != "" {
				return fmt.Errorf("source.httpArtifact.artifacts[%d].executablePath must be empty for binary format", index)
			}
		case controlv1alpha1.AgentToolArchiveTarGZ, controlv1alpha1.AgentToolArchiveZip:
			if err := validateRelativePath(artifact.ExecutablePath); err != nil {
				return fmt.Errorf("source.httpArtifact.artifacts[%d].executablePath: %w", index, err)
			}
		default:
			return fmt.Errorf("source.httpArtifact.artifacts[%d].format %q is unsupported", index, artifact.Format)
		}
	}
	return nil
}

func validPlatformValue(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func validateRelativePath(value string) error {
	if value == "" || len(value) > maxStringLength || strings.Contains(value, `\`) || strings.IndexByte(value, 0) >= 0 {
		return errors.New("must be a non-empty safe relative slash path")
	}
	if strings.HasPrefix(value, "/") || path.Clean(value) != value || value == "." || strings.HasPrefix(value, "../") {
		return errors.New("must be a clean relative slash path without traversal")
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return errors.New("must not contain control characters")
		}
	}
	return nil
}

func validBareSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validPrefixedSHA256(value string) bool {
	return strings.HasPrefix(value, "sha256:") && validBareSHA256(strings.TrimPrefix(value, "sha256:"))
}

// ComputeToolSpecDigest exactly mirrors the controller digest over
// AgentToolSpec. setupScript is intentionally absent for structured tools.
func ComputeToolSpecDigest(tool Tool) (string, error) {
	if tool.Executable == nil || tool.Source == nil {
		return "", errors.New("structured tool executable and source are required")
	}
	spec := controlv1alpha1.AgentToolSpec{
		Description:   tool.Description,
		Executable:    *tool.Executable.DeepCopy(),
		Source:        tool.Source.DeepCopy(),
		VerifyCommand: append([]string(nil), tool.VerifyCommand...),
	}
	contents, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func SelectHTTPArtifact(tool Tool, target Platform) (*controlv1alpha1.AgentToolHTTPArtifact, error) {
	if tool.Source == nil || tool.Source.HTTPArtifact == nil {
		return nil, fmt.Errorf("tool %q does not use an HTTP artifact", tool.Name)
	}
	for index := range tool.Source.HTTPArtifact.Artifacts {
		candidate := &tool.Source.HTTPArtifact.Artifacts[index]
		if candidate.Platform.OS == target.OS && candidate.Platform.Arch == target.Arch {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("tool %q has no artifact for platform %s/%s", tool.Name, target.OS, target.Arch)
}

func SelectOCIArtifact(tool Tool, target Platform) (*controlv1alpha1.AgentToolOCIArtifact, error) {
	if tool.Source == nil || tool.Source.OCIArtifact == nil {
		return nil, fmt.Errorf("tool %q does not use an OCI artifact", tool.Name)
	}
	for index := range tool.Source.OCIArtifact.Artifacts {
		candidate := &tool.Source.OCIArtifact.Artifacts[index]
		if candidate.Platform.OS == target.OS && candidate.Platform.Arch == target.Arch {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("tool %q has no OCI artifact for platform %s/%s", tool.Name, target.OS, target.Arch)
}
