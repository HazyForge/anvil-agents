package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxPromptBytes     = 64 << 10
	maxDelegateCapture = 256 << 10
	defaultDelegateTO  = 2 * time.Minute
	maxDelegateTO      = 5 * time.Minute
	minDelegateTO      = 5 * time.Second
)

// DelegateRequest is POST /local/v1/delegate.
type DelegateRequest struct {
	Harness        string `json:"harness"`
	Prompt         string `json:"prompt"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

// DelegateResult is the local-harness tool result. It never includes OIDC tokens.
type DelegateResult struct {
	Harness  string `json:"harness"`
	Target   string `json:"target,omitempty"`
	Distro   string `json:"wslDistro,omitempty"`
	Path     string `json:"path,omitempty"`
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timedOut"`
}

func (s *Server) resolveDelegate(ctx context.Context, harness string) (Tool, invokeTarget, error) {
	tool, ok := catalogTool(harness)
	if !ok || !tool.Delegatable() {
		return Tool{}, invokeTarget{}, fmt.Errorf("harness %q is not a delegatable catalog CLI", strings.TrimSpace(harness))
	}
	prefs := s.currentPrefs()
	d := s.discovererFor(prefs)
	wsl := probeWSL(ctx, d)
	targetName := resolveHarnessTarget(prefs, wsl)
	if targetName == HarnessTargetWSL && d.useWSL() {
		distro := resolveWSLDistro(prefs, wsl)
		for _, name := range tool.Binaries {
			path, err := d.wslLookPath(ctx, distro, name)
			if err != nil || strings.TrimSpace(path) == "" {
				continue
			}
			return tool, invokeTarget{Mode: HarnessTargetWSL, Bin: path, Distro: distro, Name: name}, nil
		}
		return Tool{}, invokeTarget{}, fmt.Errorf("harness %q is not on the WSL distro PATH", tool.ID)
	}
	look := d.LookPath
	if look == nil {
		look = defaultLookPath(searchPATH(d.Path))
	}
	var resolved string
	for _, name := range tool.Binaries {
		path, err := look(name)
		if err != nil || strings.TrimSpace(path) == "" {
			continue
		}
		resolved = path
		break
	}
	if resolved == "" {
		if targetName == HarnessTargetWSL {
			return Tool{}, invokeTarget{}, fmt.Errorf("harness %q is not on the WSL distro PATH", tool.ID)
		}
		return Tool{}, invokeTarget{}, fmt.Errorf("harness %q is not on PATH", tool.ID)
	}
	mode := HarnessTargetNative
	if targetName == HarnessTargetWSL {
		mode = HarnessTargetWSL
	}
	return tool, invokeTarget{Mode: mode, Bin: resolved}, nil
}

type invokeTarget struct {
	Mode   string
	Bin    string
	Distro string
	Name   string
}

func delegateTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultDelegateTO
	}
	d := time.Duration(seconds) * time.Second
	if d < minDelegateTO {
		return minDelegateTO
	}
	if d > maxDelegateTO {
		return maxDelegateTO
	}
	return d
}

func runDelegate(ctx context.Context, d Discoverer, tool Tool, target invokeTarget, prompt string, timeout time.Duration) (DelegateResult, error) {
	if target.Mode == HarnessTargetWSL && d.useWSL() {
		return d.runDelegateWSL(ctx, tool, target.Distro, prompt, timeout)
	}
	result := DelegateResult{Harness: tool.ID, Path: target.Bin, Target: target.Mode}
	if result.Target == "" {
		result.Target = HarnessTargetNative
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string(nil), tool.Invoke.Args...)
	var stdin io.Reader
	switch tool.Invoke.Mode {
	case PromptStdin:
		stdin = strings.NewReader(prompt)
	case PromptFile:
		file, err := os.CreateTemp("", "anvil-desktop-prompt-*.txt")
		if err != nil {
			return result, fmt.Errorf("create prompt file: %w", err)
		}
		path := file.Name()
		defer func() {
			_ = os.Remove(path)
		}()
		if err := file.Chmod(0o600); err != nil {
			_ = file.Close()
			return result, fmt.Errorf("prompt file permissions: %w", err)
		}
		if _, err := io.WriteString(file, prompt); err != nil {
			_ = file.Close()
			return result, fmt.Errorf("write prompt file: %w", err)
		}
		if err := file.Close(); err != nil {
			return result, fmt.Errorf("close prompt file: %w", err)
		}
		flag := strings.TrimSpace(tool.Invoke.FileFlag)
		if flag == "" {
			return result, fmt.Errorf("harness %q is missing a prompt file flag", tool.ID)
		}
		args = append(args, flag, path)
	default:
		return result, fmt.Errorf("harness %q cannot accept a prompt", tool.ID)
	}

	cmd := exec.CommandContext(runCtx, target.Bin, args...) // #nosec G204 -- bin is LookPath of a catalog CLI; args are catalog constants plus a temp file path
	cmd.Stdin = stdin
	cmd.Env = delegateEnv(os.Environ())
	var stdout, stderr cappedBuffer
	stdout.limit = maxDelegateCapture
	stderr.limit = maxDelegateCapture
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	if runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
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

func delegateEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(strings.TrimSpace(key))
		if upper == "" {
			continue
		}
		if strings.HasPrefix(upper, "KUBE") || strings.HasPrefix(upper, "KUBERNETES_") {
			continue
		}
		switch upper {
		case "AUTHORIZATION", "ACCESS_TOKEN", "ID_TOKEN", "REFRESH_TOKEN", "OIDC_TOKEN", "BEARER_TOKEN", "KUBECONFIG", "WSLENV":
			continue
		}
		if strings.Contains(upper, "TOKEN") && (strings.Contains(upper, "OIDC") || strings.Contains(upper, "ANVIL") || strings.Contains(upper, "ACCESS")) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	remain := c.limit - c.buf.Len()
	if remain <= 0 {
		return len(p), nil
	}
	if len(p) > remain {
		_, _ = c.buf.Write(p[:remain])
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) String() string {
	text := c.buf.String()
	if utf8.ValidString(text) {
		return text
	}
	return strings.ToValidUTF8(text, "")
}
