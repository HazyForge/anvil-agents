package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func configureDelegateProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Native CLI children must stop as well. WSL additionally needs explicit
		// Linux-group cleanup: taskkill cannot reach across the VM boundary.
		taskkill := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
		cleanup := exec.CommandContext(cleanupCtx, taskkill, "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)) // #nosec G204 -- OS taskkill path and PID-only arguments
		cleanup.Env = delegateEnv(os.Environ())
		if err := cleanup.Run(); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = time.Second
}
