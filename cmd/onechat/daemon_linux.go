//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func daemonProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func detachProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}

func terminateDaemon(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to signal invalid daemon pid %d", pid)
	}
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return fmt.Errorf("verify onechat pid %d: %w", pid, err)
	}
	argv := bytes.Split(bytes.TrimSuffix(raw, []byte{0}), []byte{0})
	binary := ""
	if len(argv) > 0 {
		binary = filepath.Base(string(argv[0]))
	}
	sameExecutable := false
	if executable, err := os.Executable(); err == nil {
		current, currentErr := os.Stat(executable)
		running, runningErr := os.Stat(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
		sameExecutable = currentErr == nil && runningErr == nil && os.SameFile(current, running)
	}
	if binary != "onechat" && !sameExecutable {
		return fmt.Errorf("pid %d is not onechat; refusing to signal it; inspect host.json", pid)
	}
	daemon := false
	for i, arg := range argv[1:] {
		if string(arg) == "serve" || (string(arg) == "daemon" && i+2 < len(argv) && string(argv[i+2]) == "run") {
			daemon = true
			break
		}
	}
	if !daemon {
		return fmt.Errorf("pid %d is not onechat daemon run; refusing to signal it", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop onechat pid %d: %w", pid, err)
	}
	return nil
}

func daemonInService(pid int) bool {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasSuffix(line, "/onechat.service") {
			return true
		}
	}
	return false
}

func requireSystemdPlatform() error { return nil }

// useLoginPath gives a daemon started outside a login shell (wsl.exe --exec
// from Windows Desktop, a systemd unit) the PATH the person's own shell has,
// so claude and codex resolve the same way they do in a terminal.
func useLoginPath() {
	var dirs []string
	if shell := loginShell(); shell != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// The marker keeps anything a profile prints out of the PATH.
		out, err := exec.CommandContext(ctx, shell, "-lc", `printf '\n__ONECHAT_PATH__%s' "$PATH"`).Output()
		if err == nil {
			if i := bytes.LastIndex(out, []byte("__ONECHAT_PATH__")); i >= 0 {
				dirs = append(dirs, filepath.SplitList(strings.TrimSpace(string(out[i+len("__ONECHAT_PATH__"):])))...)
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range []string{".local/bin", "bin", ".cargo/bin", ".npm-global/bin"} {
			dirs = append(dirs, filepath.Join(home, dir))
		}
	}
	dirs = append(dirs, "/usr/local/bin")
	dirs = append(dirs, filepath.SplitList(os.Getenv("PATH"))...)
	seen := map[string]bool{}
	path := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		path = append(path, dir)
	}
	_ = os.Setenv("PATH", strings.Join(path, string(os.PathListSeparator)))
}

// loginShell is $SHELL, or the account's shell from /etc/passwd when the
// daemon was started without one.
func loginShell() string {
	if shell := os.Getenv("SHELL"); filepath.IsAbs(shell) {
		return shell
	}
	raw, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	uid := strconv.Itoa(os.Getuid())
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 7 && fields[2] == uid && filepath.IsAbs(fields[6]) {
			return fields[6]
		}
	}
	return ""
}
