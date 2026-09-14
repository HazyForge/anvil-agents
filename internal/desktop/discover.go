package desktop

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	versionTimeout  = 2 * time.Second
	maxVersionBytes = 200
)

// LookPathFunc resolves a binary name to an absolute path.
type LookPathFunc func(file string) (string, error)

// VersionFunc runs a version command for a resolved binary.
type VersionFunc func(ctx context.Context, bin string, args []string) (string, error)

// Discoverer finds catalog CLIs on the local machine or inside WSL.
type Discoverer struct {
	Path      string
	LookPath  LookPathFunc
	Version   VersionFunc
	Target    string // native | wsl; empty means native
	Distro    string // WSL distro when Target is wsl
	WSLPath   string // extra PATH prefix inside WSL (tests / fixtures)
	WSL       WSLRunner
	InsideWSL func() bool // optional platform probe override for deterministic tests
}

// Discovered is one catalog entry after PATH lookup.
type Discovered struct {
	ID           string   `json:"id"`
	DisplayName  string   `json:"displayName"`
	Kind         string   `json:"kind"`
	Backend      string   `json:"backend,omitempty"`
	Binaries     []string `json:"binaries"`
	Present      bool     `json:"present"`
	Path         string   `json:"path,omitempty"`
	Version      string   `json:"version,omitempty"`
	VersionError string   `json:"versionError,omitempty"`
	AuthFileHint string   `json:"authFileHint,omitempty"`
	Delegatable  bool     `json:"delegatable"`
	Notes        string   `json:"notes,omitempty"`
	Source       string   `json:"source,omitempty"`
	Distro       string   `json:"wslDistro,omitempty"`
}

func defaultLookPath(pathEnv string) LookPathFunc {
	return func(file string) (string, error) {
		if pathEnv == "" {
			return exec.LookPath(file)
		}
		return lookPathOn(pathEnv, file)
	}
}

func lookPathOn(pathEnv, file string) (string, error) {
	if strings.Contains(file, string(os.PathSeparator)) {
		return exec.LookPath(file)
	}
	names := []string{file}
	if runtime.GOOS == "windows" && filepath.Ext(file) == "" {
		names = append(names, file+".exe", file+".bat", file+".cmd")
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() {
				continue
			}
			if !fileRunnable(candidate, info) {
				continue
			}
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

func fileRunnable(path string, info os.FileInfo) bool {
	if info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".exe", ".bat", ".cmd", ".com":
			return true
		default:
			return false
		}
	}
	return info.Mode()&0o111 != 0
}

func defaultVersion() VersionFunc {
	return func(ctx context.Context, bin string, args []string) (string, error) {
		runCtx, cancel := context.WithTimeout(ctx, versionTimeout)
		defer cancel()
		cmd := exec.CommandContext(runCtx, bin, args...) // #nosec G204 -- bin is LookPath of a catalog CLI; args are catalog constants
		cmd.Stdin = nil
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			if runCtx.Err() != nil {
				return "", runCtx.Err()
			}
			text := sanitizeVersion(out.String())
			if text != "" {
				return text, nil
			}
			return "", err
		}
		return sanitizeVersion(out.String()), nil
	}
}

func extraSearchDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{"/usr/local/bin", "/opt/homebrew/bin"}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, "bin"),
		)
	}
	return dirs
}

func searchPATH(explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit)
	}
	pathEnv := os.Getenv("PATH")
	seen := map[string]struct{}{}
	var parts []string
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		parts = append(parts, dir)
	}
	for _, dir := range extraSearchDirs() {
		add(dir)
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		add(dir)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

func (d Discoverer) isInsideWSL() bool {
	if d.InsideWSL != nil {
		return d.InsideWSL()
	}
	return insideWSL()
}

func (d Discoverer) useWSL() bool {
	// WSL-native Linux process: catalog PATH already is the distro PATH.
	// Windows-hosted anvil-desktop.exe must go through wsl.exe --exec.
	return strings.EqualFold(strings.TrimSpace(d.Target), HarnessTargetWSL) && !d.isInsideWSL()
}

// Discover walks the catalog and reports which CLIs are present.
func (d Discoverer) Discover(ctx context.Context) []Discovered {
	if d.useWSL() {
		return d.discoverWSL(ctx)
	}
	look := d.LookPath
	if look == nil {
		look = defaultLookPath(searchPATH(d.Path))
	}
	version := d.Version
	if version == nil {
		version = defaultVersion()
	}
	source := HarnessTargetNative
	if strings.EqualFold(strings.TrimSpace(d.Target), HarnessTargetWSL) && d.isInsideWSL() {
		source = HarnessTargetWSL
	}
	catalog := Catalog()
	out := make([]Discovered, 0, len(catalog))
	for _, tool := range catalog {
		item := Discovered{
			ID:           tool.ID,
			DisplayName:  tool.DisplayName,
			Kind:         tool.Kind,
			Backend:      tool.Backend,
			Binaries:     append([]string(nil), tool.Binaries...),
			AuthFileHint: tool.AuthFileHint,
			Delegatable:  tool.Delegatable(),
			Notes:        tool.Notes,
			Source:       source,
		}
		if source == HarnessTargetWSL {
			item.Distro = strings.TrimSpace(d.Distro)
			if item.Distro == "" {
				item.Distro = strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME"))
			}
		}
		for _, name := range tool.Binaries {
			resolved, err := look(name)
			if err != nil || strings.TrimSpace(resolved) == "" {
				continue
			}
			item.Present = true
			item.Path = resolved
			item.Version, item.VersionError = firstVersion(ctx, version, resolved, tool.VersionArgs)
			break
		}
		out = append(out, item)
	}
	return out
}

func (d Discoverer) discoverWSL(ctx context.Context) []Discovered {
	distro := strings.TrimSpace(d.Distro)
	catalog := Catalog()
	out := make([]Discovered, 0, len(catalog))
	for _, tool := range catalog {
		item := Discovered{
			ID:           tool.ID,
			DisplayName:  tool.DisplayName,
			Kind:         tool.Kind,
			Backend:      tool.Backend,
			Binaries:     append([]string(nil), tool.Binaries...),
			AuthFileHint: tool.AuthFileHint,
			Delegatable:  tool.Delegatable(),
			Notes:        tool.Notes,
			Source:       HarnessTargetWSL,
			Distro:       distro,
		}
		for _, name := range tool.Binaries {
			resolved, err := d.wslLookPath(ctx, distro, name)
			if err != nil || strings.TrimSpace(resolved) == "" {
				continue
			}
			item.Present = true
			item.Path = resolved
			item.Version, item.VersionError = firstWSLVersion(ctx, d, distro, name, tool.VersionArgs)
			break
		}
		out = append(out, item)
	}
	return out
}

func firstWSLVersion(ctx context.Context, d Discoverer, distro, name string, argSets [][]string) (string, string) {
	if len(argSets) == 0 {
		return "", ""
	}
	var lastErr string
	for _, args := range argSets {
		if ctx.Err() != nil {
			return "", ctx.Err().Error()
		}
		text, err := d.wslVersion(ctx, distro, name, args)
		if text != "" {
			return text, ""
		}
		if err != nil {
			lastErr = err.Error()
		}
	}
	return "", lastErr
}

func firstVersion(ctx context.Context, run VersionFunc, bin string, argSets [][]string) (string, string) {
	if len(argSets) == 0 {
		return "", ""
	}
	var lastErr string
	for _, args := range argSets {
		if ctx.Err() != nil {
			return "", ctx.Err().Error()
		}
		text, err := run(ctx, bin, args)
		if text != "" {
			return text, ""
		}
		if err != nil {
			lastErr = err.Error()
		}
	}
	return "", lastErr
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func sanitizeVersion(raw string) string {
	text := ansiEscape.ReplaceAllString(strings.TrimSpace(raw), "")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if idx := strings.IndexAny(text, "\r\n"); idx >= 0 {
		text = text[:idx]
	}
	text = strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > maxVersionBytes {
		runes := []rune(text)
		text = string(runes[:maxVersionBytes])
	}
	return text
}
