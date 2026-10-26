package onechat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const outputLimit = 1 << 20

type cappedBuffer struct {
	buf      bytes.Buffer
	overflow bool
	limit    int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buf.Len()
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return n, nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }

// childEnv mirrors standing.processChildEnv; recipes and standing behavior stay
// untouched. Provider CLI auth homes remain available, API bearer material does not.
func childEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		key = strings.ToUpper(strings.TrimSpace(key))
		if key == "" || strings.HasPrefix(key, "KUBE") || strings.Contains(key, "DATABASE_URL") {
			continue
		}
		switch key {
		case "AUTHORIZATION", "ACCESS_TOKEN", "ID_TOKEN", "REFRESH_TOKEN", "OIDC_TOKEN", "BEARER_TOKEN", "KUBECONFIG", "WSLENV":
			continue
		}
		if strings.Contains(key, "TOKEN") && (strings.Contains(key, "OIDC") || strings.Contains(key, "ANVIL") || strings.Contains(key, "ACCESS")) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func runCommand(ctx context.Context, dir, binary string, args []string, prompt string, timeout time.Duration) (string, *int, error) {
	return runCommandEnv(ctx, dir, binary, args, prompt, timeout, childEnv())
}

func runCommandEnv(ctx context.Context, dir, binary string, args []string, prompt string, timeout time.Duration, env []string) (string, *int, error) {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...) // #nosec G204 -- binary is the claude or codex CLI, or the operator's own configured tokenCommand; argv is a separate slice and no shell runs
	cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(prompt)
	cmd.WaitDelay = time.Second
	out, stderr := &cappedBuffer{limit: outputLimit}, &cappedBuffer{limit: 32 << 10}
	cmd.Stdout, cmd.Stderr = out, stderr
	err := cmd.Run()
	var code *int
	if cmd.ProcessState != nil {
		n := cmd.ProcessState.ExitCode()
		code = &n
	}
	if ctx.Err() != nil {
		return out.String(), code, ctx.Err()
	}
	if out.overflow {
		return "", code, fmt.Errorf("harness output exceeds %d bytes", outputLimit)
	}
	if err != nil {
		// Diagnostic streams can contain credentials or tool output. Preserve the
		// real exit code but do not propagate either stderr or arbitrary argv.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out.String(), code, fmt.Errorf("harness exited with code %d", exit.ExitCode())
		}
		return "", code, fmt.Errorf("could not execute configured binary")
	}
	return out.String(), code, nil
}
