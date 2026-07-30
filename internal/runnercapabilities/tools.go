package runnercapabilities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	CachePath      string
	ExecutablePath string
	Reused         bool
}

type cacheCompletion struct {
	SpecDigest     string `json:"specDigest"`
	Platform       string `json:"platform"`
	ExecutablePath string `json:"executablePath"`
	ArtifactSHA256 string `json:"artifactSHA256"`
	TreeSHA256     string `json:"treeSHA256"`
}

func (options *InstallOptions) defaults() error {
	if options.CacheRoot == "" || !filepath.IsAbs(options.CacheRoot) {
		return errors.New("tool cache root must be an absolute path")
	}
	if options.BinDir == "" || !filepath.IsAbs(options.BinDir) {
		return errors.New("tool bin directory must be an absolute path")
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

// InstallTools acquires every structured tool into a content-addressed cache
// and atomically publishes its executable into the per-run bin directory.
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
	toolDirectory := filepath.Join(options.CacheRoot, tool.Name)
	if err := ensureDirectory(toolDirectory, 0o755); err != nil {
		return InstalledTool{}, err
	}
	parent := filepath.Join(toolDirectory, platformName)
	if err := ensureDirectory(parent, 0o755); err != nil {
		return InstalledTool{}, err
	}
	cachePath := filepath.Join(parent, strings.Replace(tool.SpecDigest, ":", "-", 1))
	expectedExecutable, expectedArtifactSHA, err := expectedCacheIdentity(tool, options.Platform)
	if err != nil {
		return InstalledTool{}, err
	}

	completion, err := validateCompletedCache(cachePath, tool.SpecDigest, platformName, expectedExecutable, expectedArtifactSHA)
	reused := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return InstalledTool{}, err
	}
	if errors.Is(err, os.ErrNotExist) {
		staging, createErr := os.MkdirTemp(parent, ".populate-")
		if createErr != nil {
			return InstalledTool{}, fmt.Errorf("create cache staging directory: %w", createErr)
		}
		keepStaging := false
		defer func() {
			if !keepStaging {
				_ = os.RemoveAll(staging)
			}
		}()

		executablePath, artifactSHA, populateErr := populateTool(ctx, tool, staging, cachePath, parent, options)
		if populateErr != nil {
			return InstalledTool{}, populateErr
		}
		if executablePath != expectedExecutable || artifactSHA != expectedArtifactSHA {
			return InstalledTool{}, errors.New("acquired tool does not match its cache identity")
		}
		if err := requireExecutable(filepath.Join(staging, filepath.FromSlash(executablePath))); err != nil {
			return InstalledTool{}, err
		}
		treeSHA, digestErr := digestCacheTree(staging)
		if digestErr != nil {
			return InstalledTool{}, digestErr
		}
		completion = cacheCompletion{
			SpecDigest: tool.SpecDigest, Platform: platformName,
			ExecutablePath: executablePath, ArtifactSHA256: artifactSHA, TreeSHA256: treeSHA,
		}
		if err := writeCompletion(staging, completion); err != nil {
			return InstalledTool{}, err
		}
		if err := os.Rename(staging, cachePath); err != nil {
			// Another concurrent process may have published the exact cache entry.
			winner, winnerErr := validateCompletedCache(cachePath, tool.SpecDigest, platformName, expectedExecutable, expectedArtifactSHA)
			if winnerErr != nil {
				return InstalledTool{}, fmt.Errorf("publish cache entry: %w", err)
			}
			completion, reused = winner, true
		} else {
			keepStaging = true // staging now names cachePath; do not remove it.
		}
	}

	executablePath := filepath.Join(cachePath, filepath.FromSlash(completion.ExecutablePath))
	if err := publishExecutable(options.BinDir, tool.Executable.Name, executablePath); err != nil {
		return InstalledTool{}, err
	}
	return InstalledTool{
		Name: tool.Name, ExecutableName: tool.Executable.Name,
		CachePath: cachePath, ExecutablePath: executablePath, Reused: reused,
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

func writeCompletion(root string, completion cacheCompletion) error {
	contents, err := json.Marshal(completion)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, cacheCompletionFile), append(contents, '\n'), 0o444)
}

func validateCompletedCache(root, specDigest, platform, executablePath, artifactSHA string) (cacheCompletion, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return cacheCompletion{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return cacheCompletion{}, errors.New("cache entry is not a real directory")
	}
	contents, err := os.ReadFile(filepath.Join(root, cacheCompletionFile))
	if err != nil {
		return cacheCompletion{}, fmt.Errorf("read cache completion: %w", err)
	}
	var completion cacheCompletion
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&completion); err != nil {
		return cacheCompletion{}, fmt.Errorf("decode cache completion: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return cacheCompletion{}, errors.New("cache completion contains trailing data")
	}
	if completion.SpecDigest != specDigest || completion.Platform != platform || completion.ExecutablePath != executablePath || completion.ArtifactSHA256 != artifactSHA || !validPrefixedSHA256(completion.TreeSHA256) {
		return cacheCompletion{}, errors.New("cache completion does not match requested tool")
	}
	if err := validateRelativePath(completion.ExecutablePath); err != nil {
		return cacheCompletion{}, fmt.Errorf("cache completion executable path: %w", err)
	}
	actual, err := digestCacheTree(root)
	if err != nil {
		return cacheCompletion{}, err
	}
	if actual != completion.TreeSHA256 {
		return cacheCompletion{}, errors.New("cache tree checksum mismatch")
	}
	if err := requireExecutable(filepath.Join(root, filepath.FromSlash(completion.ExecutablePath))); err != nil {
		return cacheCompletion{}, err
	}
	return completion, nil
}

func digestCacheTree(root string) (string, error) {
	type entry struct {
		path string
		info os.FileInfo
	}
	entries := []entry{}
	err := filepath.Walk(root, func(current string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if current == root {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == cacheCompletionFile {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("cache tree contains unsupported entry %q", relative)
		}
		entries = append(entries, entry{path: relative, info: info})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk cache tree: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	hasher := sha256.New()
	for _, item := range entries {
		writeHashField(hasher, item.path)
		writeHashField(hasher, item.info.Mode().String())
		if item.info.Mode().IsRegular() {
			file, err := os.Open(filepath.Join(root, filepath.FromSlash(item.path)))
			if err != nil {
				return "", err
			}
			_, copyErr := io.Copy(hasher, file)
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
		}
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

func writeHashField(hasher hash.Hash, value string) {
	_, _ = fmt.Fprintf(hasher, "%d:", len(value))
	_, _ = io.WriteString(hasher, value)
}

func sha256Hex(contents []byte) string {
	digest := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(digest[:])
}
