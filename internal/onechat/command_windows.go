package onechat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func configureCommandProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		binary := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
		kill := exec.CommandContext(ctx, binary, "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if err := kill.Run(); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}

// Daemon workers currently run on Linux; no unverified persisted Windows PID
// is considered safe to recover or signal.
func sessionProcessAlive(int) bool      { return false }
func interruptSessionProcess(int) error { return os.ErrProcessDone }
