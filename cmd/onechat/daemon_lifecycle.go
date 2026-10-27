package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func startDaemon(ctx context.Context, paths daemonPaths, override bool) (daemonStatus, error) {
	status := inspectDaemon(ctx, paths)
	if status.Running {
		return status, nil
	}
	_, err := loadCLIConfig(paths.config)
	if err != nil {
		status.Message = err.Error()
		return status, err
	}
	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if paths.systemd() {
		err = serviceCommand(startCtx, "systemctl", "--user", "start", "onechat.service")
	} else {
		err = detachDaemon(paths, override)
	}
	if err != nil {
		status.Message = err.Error()
		return status, err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		status = inspectDaemon(startCtx, paths)
		if status.Running {
			return status, nil
		}
		select {
		case <-startCtx.Done():
			status.Message = "onechat did not become ready within 10 seconds; run onechat daemon logs: " + status.Message
			return status, errors.New(status.Message)
		case <-ticker.C:
		}
	}
}

func detachDaemon(paths daemonPaths, override bool) error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.state, 0700); err != nil {
		return err
	}
	if err := os.Chmod(paths.state, 0700); err != nil {
		return err
	}
	output, err := os.OpenFile(filepath.Join(paths.state, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer output.Close()
	if err := output.Chmod(0600); err != nil {
		return err
	}
	input, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer input.Close()
	args := []string{"--config", paths.config, "daemon", "run"}
	if override {
		args = append(args, "--state-dir", paths.state)
	}
	cmd := exec.Command(binary, args...)
	if err := detachProcess(cmd); err != nil {
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, output
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start onechat daemon: %w", err)
	}
	return cmd.Process.Release()
}

func stopDaemon(ctx context.Context, paths daemonPaths) error {
	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	info, err := readDaemonMetadata(paths.state)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	alive := err == nil && info.StoppedAt == nil && daemonProcessAlive(info.PID)
	managed := paths.systemd()
	if alive {
		// An installed unit does not own a daemon launched manually with run.
		managed = daemonInService(info.PID)
	}
	if managed {
		// Stop the unit even during RestartSec or startup failure, before host.json
		// exists. Otherwise it would keep restarting after this command returned.
		if err := serviceCommand(stopCtx, "systemctl", "--user", "stop", "onechat.service"); err != nil {
			return err
		}
	} else if alive {
		if err := terminateDaemon(info.PID); err != nil {
			return err
		}
	}
	if !alive {
		return nil
	}
	return waitDaemonStopped(stopCtx, paths, info.HostID)
}

func waitDaemonStopped(ctx context.Context, paths daemonPaths, hostID string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := readDaemonMetadata(paths.state)
		if err != nil {
			return err
		}
		if info.HostID != hostID {
			return fmt.Errorf("host identity changed while stopping; inspect onechat daemon status before retrying")
		}
		if info.StoppedAt != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("onechat did not record shutdown within 10 seconds; check onechat daemon logs: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func serviceCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func daemonLogs(ctx context.Context, paths daemonPaths, follow bool) error {
	managed := paths.systemd()
	if info, err := readDaemonMetadata(paths.state); err == nil && daemonProcessAlive(info.PID) {
		managed = daemonInService(info.PID)
	}
	if managed {
		args := []string{"--user", "-u", "onechat.service", "-n", "200", "--no-pager"}
		if follow {
			args = append(args, "-f")
		}
		cmd := exec.CommandContext(ctx, "journalctl", args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	return tailDaemonLog(ctx, filepath.Join(paths.state, "daemon.log"), follow)
}

func tailDaemonLog(ctx context.Context, path string, follow bool) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open daemon log: %w; run onechat daemon start", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	// Read backwards until the newest 200 lines are present, keeping memory
	// bounded even when a child writes a very long diagnostic without newlines.
	offset := info.Size()
	tail := []byte{}
	for offset > 0 && len(tail) < 1<<20 && strings.Count(string(tail), "\n") <= 200 {
		size := int64(4096)
		if offset < size {
			size = offset
		}
		offset -= size
		chunk := make([]byte, size)
		if _, err := file.ReadAt(chunk, offset); err != nil {
			return err
		}
		tail = append(chunk, tail...)
	}
	lines := strings.SplitAfter(string(tail), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	if _, err := io.WriteString(os.Stdout, strings.Join(lines, "")); err != nil {
		return err
	}
	if !follow {
		return nil
	}
	position, err := file.Seek(info.Size(), io.SeekStart)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			info, err := file.Stat()
			if err != nil {
				return err
			}
			if info.Size() < position {
				if _, err := file.Seek(0, io.SeekStart); err != nil {
					return err
				}
				position = 0
			}
			n, err := io.Copy(os.Stdout, file)
			position += n
			if err != nil {
				return err
			}
		}
	}
}

func bootstrapDaemon(ctx context.Context, paths daemonPaths) error {
	if err := requireSystemdPlatform(); err != nil {
		return err
	}
	if _, err := loadCLIConfig(paths.config); err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	// A process started before the unit existed must release state ownership
	// before systemd starts its replacement.
	info, readErr := readDaemonMetadata(paths.state)
	if readErr == nil && info.StoppedAt == nil && daemonProcessAlive(info.PID) && !daemonInService(info.PID) {
		if err := terminateDaemon(info.PID); err != nil {
			return err
		}
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := waitDaemonStopped(stopCtx, paths, info.HostID)
		cancel()
		if err != nil {
			return err
		}
	} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	unit := "[Unit]\nDescription=One chat daemon\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=simple\nEnvironment=HOME=%h\nUMask=0077\nWorkingDirectory=%h\nExecStart=" + unitArgument(binary) + " --config " + unitArgument(paths.config) + " daemon run\nRestart=on-failure\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n"
	if err := writeUnit(paths.unit, []byte(unit)); err != nil {
		return err
	}
	if err := serviceCommand(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if err := serviceCommand(ctx, "systemctl", "--user", "enable", "--now", "onechat.service"); err != nil {
		return err
	}
	fmt.Println("Installed:", paths.unit)
	user := os.Getenv("USER")
	if user == "" {
		user = strconv.Itoa(os.Getuid())
	}
	cmd := exec.CommandContext(ctx, "loginctl", "show-user", user, "-p", "Linger")
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Linger: unknown (loginctl could not read it; unchanged)")
	} else {
		fmt.Print(string(output))
	}
	return nil
}

func unitArgument(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, "$", "$$")
	return strconv.Quote(value)
}

func writeUnit(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".onechat-unit-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
