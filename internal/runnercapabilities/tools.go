package runnercapabilities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	DefaultMaxToolDownloadBytes = int64(512 << 20)
	DefaultMaxToolExtractBytes  = int64(1 << 30)
	DefaultMaxToolExtractFiles  = 100_000
	cacheCompletionFile         = ".anvil-agent-tool-complete.json"
	inlineScriptFile            = ".anvil-agent-inline-script"
)

type InstallOptions struct {
	CacheRoot        string
	InstallRoot      string
	BinDir           string
	Platform         Platform
	HTTPClient       *http.Client
	MaxDownloadBytes int64
	MaxExtractBytes  int64
	MaxExtractFiles  int
}

type InstalledTool struct {
	Name           string
	ExecutableName string
	ExecutablePath string
	Reused         bool
}

func (options *InstallOptions) defaults() error {
	if options.CacheRoot == "" || !filepath.IsAbs(options.CacheRoot) {
		return errors.New("tool cache root must be an absolute path")
	}
	if options.BinDir == "" || !filepath.IsAbs(options.BinDir) {
		return errors.New("tool bin directory must be an absolute path")
	}
	if options.InstallRoot == "" {
		options.InstallRoot = filepath.Join(filepath.Dir(options.BinDir), "install")
	}
	if !filepath.IsAbs(options.InstallRoot) {
		return errors.New("tool install root must be an absolute path")
	}
	if !validPlatformValue(options.Platform.OS) || !validPlatformValue(options.Platform.Arch) {
		return fmt.Errorf("tool platform %q/%q is invalid", options.Platform.OS, options.Platform.Arch)
	}
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	if options.MaxDownloadBytes <= 0 {
		options.MaxDownloadBytes = DefaultMaxToolDownloadBytes
	}
	if options.MaxExtractBytes <= 0 {
		options.MaxExtractBytes = DefaultMaxToolExtractBytes
	}
	if options.MaxExtractFiles <= 0 {
		options.MaxExtractFiles = DefaultMaxToolExtractFiles
	}
	return nil
}

// InstallTools acquires every structured tool into the per-run install root and
// atomically publishes its executable into the per-run bin directory. The
// persistent cache is currently treated as untrusted storage and is not reused:
// an extracted tree and its self-authored completion record cannot prove that it
// was derived from the pinned source digest. This safe fallback preserves the
// cache mount contract until a raw-artifact digest chain is available.
// Setup-file-only compatibility tools are intentionally skipped here.
func InstallTools(ctx context.Context, manifest ToolManifest, options InstallOptions) ([]InstalledTool, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if err := options.defaults(); err != nil {
		return nil, err
	}
	if err := ensureDirectory(options.CacheRoot, 0o755); err != nil {
		return nil, fmt.Errorf("prepare tool cache: %w", err)
	}
	if err := ensureDirectory(options.BinDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare tool bin directory: %w", err)
	}
	if err := ensureDirectory(options.InstallRoot, 0o755); err != nil {
		return nil, fmt.Errorf("prepare per-run tool install directory: %w", err)
	}

	installed := make([]InstalledTool, 0, len(manifest))
	for index := range manifest {
		tool := manifest[index]
		if tool.Source == nil {
			continue
		}
		result, err := installTool(ctx, tool, options)
		if err != nil {
			return nil, fmt.Errorf("install tool %q: %w", tool.Name, err)
		}
		installed = append(installed, result)
	}
	return installed, nil
}

func installTool(ctx context.Context, tool Tool, options InstallOptions) (InstalledTool, error) {
	platformName := options.Platform.OS + "-" + options.Platform.Arch
	toolDirectory := filepath.Join(options.InstallRoot, tool.Name)
	if err := ensureDirectory(toolDirectory, 0o755); err != nil {
		return InstalledTool{}, err
	}
	parent := filepath.Join(toolDirectory, platformName)
	if err := ensureDirectory(parent, 0o755); err != nil {
		return InstalledTool{}, err
	}
	installPath := filepath.Join(parent, strings.Replace(tool.SpecDigest, ":", "-", 1))
	expectedExecutable, expectedArtifactSHA, err := expectedCacheIdentity(tool, options.Platform)
	if err != nil {
		return InstalledTool{}, err
	}
	staging, err := os.MkdirTemp(parent, ".acquire-")
	if err != nil {
		return InstalledTool{}, fmt.Errorf("create per-run tool staging directory: %w", err)
	}
	keepStaging := false
	defer func() {
		if !keepStaging {
			_ = os.RemoveAll(staging)
		}
	}()
	executableRelative, artifactSHA, err := populateTool(ctx, tool, staging, installPath, staging, options)
	if err != nil {
		return InstalledTool{}, err
	}
	if executableRelative != expectedExecutable || artifactSHA != expectedArtifactSHA {
		return InstalledTool{}, errors.New("acquired tool does not match its pinned source identity")
	}
	if err := requireExecutable(filepath.Join(staging, filepath.FromSlash(executableRelative))); err != nil {
		return InstalledTool{}, err
	}
	if err := os.Rename(staging, installPath); err != nil {
		return InstalledTool{}, fmt.Errorf("publish per-run tool install: %w", err)
	}
	keepStaging = true
	executablePath := filepath.Join(installPath, filepath.FromSlash(executableRelative))
	if err := publishExecutable(options.BinDir, tool.Executable.Name, executablePath); err != nil {
		return InstalledTool{}, err
	}
	return InstalledTool{
		Name: tool.Name, ExecutableName: tool.Executable.Name,
		ExecutablePath: executablePath, Reused: false,
	}, nil
}

func expectedCacheIdentity(tool Tool, platform Platform) (string, string, error) {
	if inline := tool.Source.InlineScript; inline != nil {
		return tool.Executable.Path, sha256Hex([]byte(inline.Script)), nil
	}
	if tool.Source.HTTPArtifact != nil {
		artifact, err := SelectHTTPArtifact(tool, platform)
		if err != nil {
			return "", "", err
		}
		executable := artifact.ExecutablePath
		if artifact.Format == controlv1alpha1.AgentToolArchiveBinary {
			executable = tool.Executable.Path
		}
		return executable, "sha256:" + artifact.SHA256, nil
	}
	artifact, err := SelectOCIArtifact(tool, platform)
	if err != nil {
		return "", "", err
	}
	reference, err := parseOCIReference(artifact.Reference)
	if err != nil {
		return "", "", err
	}
	return artifact.ExecutablePath, reference.digest, nil
}

func populateTool(ctx context.Context, tool Tool, staging, finalCachePath, downloadDir string, options InstallOptions) (string, string, error) {
	if inline := tool.Source.InlineScript; inline != nil {
		scriptTarget := filepath.Join(staging, inlineScriptFile)
		if err := writeRegularFile(scriptTarget, strings.NewReader(inline.Script), int64(len(inline.Script)), 0o444); err != nil {
			return "", "", fmt.Errorf("write inline script: %w", err)
		}
		wrapper := inlineScriptWrapper(inline.Interpreter, filepath.Join(finalCachePath, inlineScriptFile))
		target := filepath.Join(staging, filepath.FromSlash(tool.Executable.Path))
		if err := writeRegularFile(target, strings.NewReader(wrapper), int64(len(wrapper)), 0o755); err != nil {
			return "", "", fmt.Errorf("write inline script wrapper: %w", err)
		}
		return tool.Executable.Path, sha256Hex([]byte(inline.Script)), nil
	}
	if tool.Source.OCIArtifact != nil {
		artifact, err := SelectOCIArtifact(tool, options.Platform)
		if err != nil {
			return "", "", err
		}
		return acquireOCIArtifact(ctx, artifact, staging, downloadDir, options)
	}
	artifact, err := SelectHTTPArtifact(tool, options.Platform)
	if err != nil {
		return "", "", err
	}
	downloadPath, err := downloadArtifact(ctx, artifact, downloadDir, options)
	if err != nil {
		return "", "", err
	}
	defer os.Remove(downloadPath)

	limits := extractionLimits{maxBytes: options.MaxExtractBytes, maxFiles: options.MaxExtractFiles}
	executableRelative := artifact.ExecutablePath
	switch artifact.Format {
	case controlv1alpha1.AgentToolArchiveBinary:
		executableRelative = tool.Executable.Path
		input, err := os.Open(downloadPath)
		if err != nil {
			return "", "", fmt.Errorf("open downloaded binary: %w", err)
		}
		err = writeRegularFile(filepath.Join(staging, filepath.FromSlash(executableRelative)), input, -1, 0o755)
		closeErr := input.Close()
		if err != nil {
			return "", "", err
		}
		if closeErr != nil {
			return "", "", fmt.Errorf("close downloaded binary: %w", closeErr)
		}
	case controlv1alpha1.AgentToolArchiveTarGZ:
		if err := extractTarGz(downloadPath, staging, limits); err != nil {
			return "", "", err
		}
	case controlv1alpha1.AgentToolArchiveZip:
		if err := extractZip(downloadPath, staging, limits); err != nil {
			return "", "", err
		}
	default:
		return "", "", fmt.Errorf("unsupported artifact format %q", artifact.Format)
	}
	// #nosec G302 -- this is the selected tool executable, not sensitive data;
	// execution by the container user requires executable mode bits.
	if err := os.Chmod(filepath.Join(staging, filepath.FromSlash(executableRelative)), 0o755); err != nil {
		return "", "", fmt.Errorf("mark executable: %w", err)
	}
	return executableRelative, "sha256:" + artifact.SHA256, nil
}

func inlineScriptWrapper(interpreter []string, finalScriptPath string) string {
	arguments := make([]string, 0, len(interpreter)+1)
	for _, argument := range interpreter {
		arguments = append(arguments, shellQuote(argument))
	}
	arguments = append(arguments, shellQuote(finalScriptPath))
	return "#!/bin/sh\nexec " + strings.Join(arguments, " ") + " \"$@\"\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func downloadArtifact(ctx context.Context, artifact *controlv1alpha1.AgentToolHTTPArtifact, directory string, options InstallOptions) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return "", fmt.Errorf("create artifact request: %w", err)
	}
	response, err := options.HTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("download artifact: %w", err)
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL == nil || response.Request.URL.Scheme != "https" {
		return "", errors.New("artifact request was redirected away from HTTPS")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("download artifact: HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > options.MaxDownloadBytes {
		return "", fmt.Errorf("artifact exceeds download limit of %d bytes", options.MaxDownloadBytes)
	}
	file, err := os.CreateTemp(directory, ".download-")
	if err != nil {
		return "", fmt.Errorf("create artifact download file: %w", err)
	}
	path := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(response.Body, options.MaxDownloadBytes+1))
	if copyErr != nil {
		return "", fmt.Errorf("download artifact body: %w", copyErr)
	}
	if written > options.MaxDownloadBytes {
		return "", fmt.Errorf("artifact exceeds download limit of %d bytes", options.MaxDownloadBytes)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close artifact download: %w", err)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != artifact.SHA256 {
		return "", fmt.Errorf("artifact checksum mismatch: got sha256:%s", actual)
	}
	keep = true
	return path, nil
}

func writeRegularFile(target string, reader io.Reader, expectedSize int64, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if expectedSize >= 0 && written != expectedSize {
		return fmt.Errorf("file size mismatch: wrote %d, expected %d", written, expectedSize)
	}
	return nil
}

func ensureDirectory(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("path %q is not a real directory", path)
	}
	return nil
}

func requireExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("declared executable is unavailable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("declared executable is not a regular file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return errors.New("declared executable does not have an execute bit")
	}
	return nil
}

func publishExecutable(binDir, name, target string) error {
	if err := requireExecutable(target); err != nil {
		return err
	}
	link := filepath.Join(binDir, name)
	if err := os.Symlink(target, link); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("publish executable %q: %w", name, err)
		}
		existing, readErr := os.Readlink(link)
		if readErr != nil || existing != target {
			return fmt.Errorf("publish executable %q: bin path already exists", name)
		}
	}
	return nil
}

func sha256Hex(contents []byte) string {
	digest := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(digest[:])
}
