package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	wslListTimeout   = 4 * time.Second
	wslProbeTimeout  = 4 * time.Second
	wslMktempTimeout = 5 * time.Second
	wslWriteTimeout  = 15 * time.Second
)

// WSLStatus is the workstation's ability to run catalog CLIs inside WSL.
type WSLStatus struct {
	Available     bool     `json:"available"`
	InsideWSL     bool     `json:"insideWSL"`
	Executable    string   `json:"executable,omitempty"`
	DefaultDistro string   `json:"defaultDistro,omitempty"`
	Distros       []string `json:"distros,omitempty"`
	Message       string   `json:"message,omitempty"`
}

// WSLRequest is one wsl.exe invocation. Linux argv is never derived from the
// OIDC token or from free-form prompt text.
type WSLRequest struct {
	Distro  string
	Argv    []string
	Stdin   io.Reader
	AgyTurn bool     // keep native stream input open until the turn result
	Env     []string // extra KEY=val for /usr/bin/env on the Linux side
}

// WSLResult is stdout/stderr from a WSL command. It never includes tokens.
type WSLResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// WSLRunner looks up wsl.exe and runs commands in a distro.
type WSLRunner struct {
	LookExe func() (string, error)
	Run     func(ctx context.Context, req WSLRequest) (WSLResult, error)
	List    func(ctx context.Context) ([]string, error)
}

var (
	distroNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	binNameRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	wslTempRE    = regexp.MustCompile(`^/tmp/anvil-desktop-prompt\.[A-Za-z0-9._-]+$`)
)

func safeDistroName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && distroNameRE.MatchString(name)
}

func safeBinName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && binNameRE.MatchString(name) && !strings.Contains(name, string(os.PathSeparator))
}

func insideWSL() bool {
	if strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME")) != "" {
		return true
	}
	if strings.Contains(strings.ToLower(os.Getenv("WSL_INTEROP")), "wsl") {
		return true
	}
	raw, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	v := strings.ToLower(string(raw))
	return strings.Contains(v, "microsoft") || strings.Contains(v, "wsl")
}

func lookWSLExe() (string, error) {
	for _, name := range []string{"wsl.exe", "wsl"} {
		if path, err := exec.LookPath(name); err == nil && strings.TrimSpace(path) != "" {
			return path, nil
		}
	}
	if root := strings.TrimSpace(os.Getenv("SystemRoot")); root != "" {
		candidate := filepath.Join(root, "System32", "wsl.exe")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	for _, candidate := range []string{
		"/mnt/c/Windows/System32/wsl.exe",
		"/mnt/c/WINDOWS/System32/wsl.exe",
		"/mnt/c/Windows/system32/wsl.exe",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

func wslArgv(distro string, linuxArgv []string) ([]string, error) {
	if len(linuxArgv) == 0 {
		return nil, fmt.Errorf("WSL command is empty")
	}
	var args []string
	if d := strings.TrimSpace(distro); d != "" {
		if !safeDistroName(d) {
			return nil, fmt.Errorf("invalid WSL distro name")
		}
		args = append(args, "-d", d)
	}
	args = append(args, "--exec")
	args = append(args, linuxArgv...)
	return args, nil
}

func linuxEnvArgv(env []string, linuxArgv []string) []string {
	out := []string{"/usr/bin/env"}
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, "=\x00") {
			continue
		}
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "TOKEN") || upper == "AUTHORIZATION" || strings.HasPrefix(upper, "KUBE") {
			continue
		}
		out = append(out, key+"="+value)
	}
	return append(out, linuxArgv...)
}

func defaultWSLRun(look func() (string, error)) func(ctx context.Context, req WSLRequest) (WSLResult, error) {
	return func(ctx context.Context, req WSLRequest) (WSLResult, error) {
		exe, err := look()
		if err != nil {
			return WSLResult{}, fmt.Errorf("wsl.exe not found")
		}
		linuxArgv := req.Argv
		if len(req.Env) > 0 {
			linuxArgv = linuxEnvArgv(req.Env, linuxArgv)
		}
		args, err := wslArgv(req.Distro, linuxArgv)
		if err != nil {
			return WSLResult{}, err
		}
		cmd := exec.CommandContext(ctx, exe, args...) // #nosec G204 -- exe is LookPath of wsl.exe; argv is catalog constants plus a validated distro/temp path
		cmd.Stdin = req.Stdin
		cmd.Env = delegateEnv(os.Environ())
		var stdout, stderr cappedBuffer
		stdout.limit = maxDelegateCapture
		stderr.limit = maxDelegateCapture
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if req.AgyTurn {
			err = runAgyTurn(cmd, req.Stdin)
		} else {
			err = cmd.Run()
		}
		result := WSLResult{Stdout: stdout.String(), Stderr: stderr.String()}
		if ctx.Err() != nil {
			result.TimedOut = true
			result.ExitCode = -1
			return result, nil
		}
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				result.ExitCode = exitErr.ExitCode()
				return result, nil
			}
			return result, err
		}
		if cmd.ProcessState != nil {
			result.ExitCode = cmd.ProcessState.ExitCode()
		}
		return result, nil
	}
}

func defaultWSLList(look func() (string, error)) func(ctx context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		exe, err := look()
		if err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, exe, "-l", "-q") // #nosec G204 -- exe is LookPath of wsl.exe; argv is constant
		cmd.Env = delegateEnv(os.Environ())
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return nil, err
		}
		return parseWSLDistroList(decodeWSLOutput(out.Bytes())), nil
	}
}

func (d Discoverer) wslRunner() WSLRunner {
	look := d.WSL.LookExe
	if look == nil {
		look = lookWSLExe
	}
	run := d.WSL.Run
	if run == nil {
		run = defaultWSLRun(look)
	}
	list := d.WSL.List
	if list == nil {
		list = defaultWSLList(look)
	}
	return WSLRunner{LookExe: look, Run: run, List: list}
}

func probeWSL(ctx context.Context, d Discoverer) WSLStatus {
	status := WSLStatus{InsideWSL: d.isInsideWSL()}
	if status.InsideWSL {
		status.Available = true
		status.DefaultDistro = strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME"))
		if status.DefaultDistro != "" {
			status.Distros = []string{status.DefaultDistro}
		}
		status.Message = "This process is already inside WSL. Operate on WSL uses this distro PATH."
		return status
	}
	runner := d.wslRunner()
	exe, err := runner.LookExe()
	if err != nil || strings.TrimSpace(exe) == "" {
		status.Message = "wsl.exe was not found. Install WSL or switch to native PATH."
		return status
	}
	status.Executable = exe

	listCtx, cancel := context.WithTimeout(ctx, wslListTimeout)
	defer cancel()
	distros, listErr := runner.List(listCtx)
	if listErr == nil {
		status.Distros = distros
	}

	defCtx, cancelDef := context.WithTimeout(ctx, wslProbeTimeout)
	defer cancelDef()
	def, defErr := runner.Run(defCtx, WSLRequest{
		Argv: []string{"/bin/sh", "-c", `printf %s "$WSL_DISTRO_NAME"`},
	})
	if defErr == nil {
		name := strings.TrimSpace(strings.ReplaceAll(def.Stdout, "\x00", ""))
		if safeDistroName(name) {
			status.DefaultDistro = name
		}
	}
	if status.DefaultDistro == "" && len(status.Distros) > 0 {
		status.DefaultDistro = status.Distros[0]
	}
	if status.DefaultDistro == "" && len(status.Distros) == 0 {
		status.Message = "wsl.exe is present but no distro responded. Install a WSL distro or switch to native PATH."
		return status
	}
	status.Available = true
	if status.DefaultDistro != "" {
		status.Message = "WSL default distro " + status.DefaultDistro + ". Catalog CLIs are resolved with that distro PATH via wsl.exe."
	} else {
		status.Message = "wsl.exe is available. Catalog CLIs are resolved with the default distro PATH."
	}
	return status
}

func resolveHarnessTarget(prefs Prefs, wsl WSLStatus) string {
	switch strings.ToLower(strings.TrimSpace(prefs.HarnessTarget)) {
	case HarnessTargetNative:
		return HarnessTargetNative
	case HarnessTargetWSL:
		return HarnessTargetWSL
	}
	if wsl.Available || wsl.InsideWSL {
		return HarnessTargetWSL
	}
	return HarnessTargetNative
}

func resolveWSLDistro(prefs Prefs, wsl WSLStatus) string {
	if d := strings.TrimSpace(prefs.WSLDistro); d != "" {
		if safeDistroName(d) {
			return d
		}
		return ""
	}
	return strings.TrimSpace(wsl.DefaultDistro)
}

func decodeWSLOutput(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE {
		return utf16LEToString(raw[2:])
	}
	nuls := 0
	limit := len(raw)
	if limit > 64 {
		limit = 64
	}
	for i := 0; i < limit; i++ {
		if raw[i] == 0 {
			nuls++
		}
	}
	if nuls > limit/4 {
		return utf16LEToString(raw)
	}
	return string(raw)
}

func utf16LEToString(raw []byte) string {
	if len(raw)%2 == 1 {
		raw = raw[:len(raw)-1]
	}
	u16 := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	return string(utf16.Decode(u16))
}

func parseWSLDistroList(text string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "\r"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		if line == "" || strings.EqualFold(line, "NAME") {
			continue
		}
		name := line
		if !safeDistroName(name) {
			fields := strings.Fields(line)
			if len(fields) < 2 || !safeDistroName(fields[0]) {
				continue
			}
			state := strings.ToLower(fields[1])
			if state != "running" && state != "stopped" && state != "installing" && state != "converting" {
				continue
			}
			name = fields[0]
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func wslPathScript() string {
	return strings.Join([]string{
		`export PATH="${ANVIL_DESKTOP_PATH_PREFIX:+$ANVIL_DESKTOP_PATH_PREFIX:}$HOME/.local/bin:$HOME/.cargo/bin:$HOME/bin:$HOME/.npm-global/bin:/usr/local/bin:$PATH"`,
		`bin=$(command -v -- "$ANVIL_DESKTOP_BIN") || exit 127`,
		`exec "$bin" "$@"`,
	}, "\n")
}

func wslWhichScript() string {
	return strings.Join([]string{
		`export PATH="${ANVIL_DESKTOP_PATH_PREFIX:+$ANVIL_DESKTOP_PATH_PREFIX:}$HOME/.local/bin:$HOME/.cargo/bin:$HOME/bin:$HOME/.npm-global/bin:/usr/local/bin:$PATH"`,
		`command -v -- "$ANVIL_DESKTOP_BIN"`,
	}, "\n")
}

func (d Discoverer) wslEnv(bin string) []string {
	env := []string{"ANVIL_DESKTOP_BIN=" + bin}
	if prefix := strings.TrimSpace(d.WSLPath); prefix != "" {
		env = append(env, "ANVIL_DESKTOP_PATH_PREFIX="+prefix)
	}
	return env
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.IndexAny(text, "\r\n"); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}
	return text
}

func (d Discoverer) wslLookPath(ctx context.Context, distro, name string) (string, error) {
	if !safeBinName(name) {
		return "", fmt.Errorf("invalid catalog binary name")
	}
	runner := d.wslRunner()
	runCtx, cancel := context.WithTimeout(ctx, wslProbeTimeout)
	defer cancel()
	result, err := runner.Run(runCtx, WSLRequest{
		Distro: distro,
		Argv:   []string{"/bin/bash", "-c", wslWhichScript()},
		Env:    d.wslEnv(name),
	})
	if err != nil {
		return "", err
	}
	if result.TimedOut || result.ExitCode != 0 {
		return "", os.ErrNotExist
	}
	path := firstLine(result.Stdout)
	if path == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, "\x00") {
		return "", os.ErrNotExist
	}
	return path, nil
}

func (d Discoverer) wslVersion(ctx context.Context, distro, name string, args []string) (string, error) {
	if !safeBinName(name) {
		return "", fmt.Errorf("invalid catalog binary name")
	}
	linuxArgv := []string{"/bin/bash", "-c", wslPathScript(), "anvil-desktop"}
	linuxArgv = append(linuxArgv, args...)
	runner := d.wslRunner()
	runCtx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	result, err := runner.Run(runCtx, WSLRequest{
		Distro: distro,
		Argv:   linuxArgv,
		Env:    d.wslEnv(name),
	})
	if err != nil {
		return "", err
	}
	text := sanitizeVersion(result.Stdout)
	if text == "" {
		text = sanitizeVersion(result.Stderr)
	}
	if text != "" {
		return text, nil
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("wsl version probe exit %d", result.ExitCode)
	}
	return "", nil
}

func (d Discoverer) wslMktemp(ctx context.Context, distro string) (string, error) {
	runner := d.wslRunner()
	runCtx, cancel := context.WithTimeout(ctx, wslMktempTimeout)
	defer cancel()
	result, err := runner.Run(runCtx, WSLRequest{
		Distro: distro,
		Argv:   []string{"/bin/mktemp", "/tmp/anvil-desktop-prompt.XXXXXX"},
	})
	if err != nil {
		return "", err
	}
	path := firstLine(result.Stdout)
	if result.ExitCode != 0 || !wslTempRE.MatchString(path) {
		return "", fmt.Errorf("wsl mktemp failed")
	}
	return path, nil
}

func (d Discoverer) wslWriteFile(ctx context.Context, distro, path, prompt string) error {
	if !wslTempRE.MatchString(path) {
		return fmt.Errorf("invalid WSL prompt path")
	}
	runner := d.wslRunner()
	runCtx, cancel := context.WithTimeout(ctx, wslWriteTimeout)
	defer cancel()
	result, err := runner.Run(runCtx, WSLRequest{
		Distro: distro,
		Argv:   []string{"/usr/bin/tee", path},
		Stdin:  strings.NewReader(prompt),
	})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wsl tee exit %d", result.ExitCode)
	}
	chmodCtx, cancelChmod := context.WithTimeout(ctx, wslMktempTimeout)
	defer cancelChmod()
	chmod, err := runner.Run(chmodCtx, WSLRequest{
		Distro: distro,
		Argv:   []string{"/bin/chmod", "600", path},
	})
	if err != nil {
		return err
	}
	if chmod.ExitCode != 0 {
		return fmt.Errorf("wsl chmod exit %d", chmod.ExitCode)
	}
	return nil
}

func (d Discoverer) wslRemove(ctx context.Context, distro, path string) {
	if !wslTempRE.MatchString(path) {
		return
	}
	runner := d.wslRunner()
	runCtx, cancel := context.WithTimeout(ctx, wslMktempTimeout)
	defer cancel()
	_, _ = runner.Run(runCtx, WSLRequest{
		Distro: distro,
		Argv:   []string{"/bin/rm", "-f", path},
	})
}

func (d Discoverer) runDelegateWSL(ctx context.Context, tool Tool, distro, prompt string, timeout time.Duration) (DelegateResult, error) {
	result := DelegateResult{Harness: tool.ID, Target: HarnessTargetWSL, Distro: distro}
	var binName string
	for _, name := range tool.Binaries {
		if !safeBinName(name) {
			continue
		}
		path, err := d.wslLookPath(ctx, distro, name)
		if err != nil || path == "" {
			continue
		}
		binName = name
		result.Path = path
		break
	}
	if binName == "" {
		return result, fmt.Errorf("harness %q is not on the WSL distro PATH", tool.ID)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	linuxArgv := []string{"/bin/bash", "-c", wslPathScript(), "anvil-desktop"}
	linuxArgv = append(linuxArgv, tool.Invoke.Args...)
	var stdin io.Reader
	switch tool.Invoke.Mode {
	case PromptStdin:
		stdin = strings.NewReader(prompt)
	case PromptAgyStream:
		stdin = agyPromptInput(prompt)
	case PromptFile:
		tmp, err := d.wslMktemp(runCtx, distro)
		if err != nil {
			return result, fmt.Errorf("create WSL prompt file: %w", err)
		}
		defer d.wslRemove(context.Background(), distro, tmp)
		if err := d.wslWriteFile(runCtx, distro, tmp, prompt); err != nil {
			return result, fmt.Errorf("write WSL prompt file: %w", err)
		}
		flag := strings.TrimSpace(tool.Invoke.FileFlag)
		if flag == "" {
			return result, fmt.Errorf("harness %q is missing a prompt file flag", tool.ID)
		}
		linuxArgv = append(linuxArgv, flag, tmp)
	default:
		return result, fmt.Errorf("harness %q cannot accept a prompt", tool.ID)
	}

	runner := d.wslRunner()
	wslResult, err := runner.Run(runCtx, WSLRequest{
		Distro:  distro,
		Argv:    linuxArgv,
		Stdin:   stdin,
		AgyTurn: tool.Invoke.Mode == PromptAgyStream,
		Env:     d.wslEnv(binName),
	})
	result.Stdout = wslResult.Stdout
	result.Stderr = wslResult.Stderr
	result.ExitCode = wslResult.ExitCode
	result.TimedOut = wslResult.TimedOut
	if err != nil {
		return result, err
	}
	return result, nil
}
