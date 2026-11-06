//go:build linux

package main

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type nativeProcess struct {
	pid, ppid, pgrp int
	state           string
	start           string
}

func nativeProcesses() map[int]nativeProcess {
	result := map[int]nativeProcess{}
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		end := strings.LastIndex(string(raw), ") ")
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(raw)[end+2:])
		if len(fields) < 20 {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		group, _ := strconv.Atoi(fields[2])
		result[pid] = nativeProcess{pid: pid, ppid: ppid, pgrp: group, state: fields[0], start: fields[19]}
	}
	return result
}

// The actor is a dedicated supervisor with one native harness. Subreaping
// keeps detached/double-forked tool children owned after their parent exits.
// Existing children are excluded; PID start times fence reuse during teardown.
type processFence struct {
	cmd      *exec.Cmd
	baseline map[int]string
	known    map[int]string
	mu       sync.Mutex
}

func newProcessFence(cmd *exec.Cmd) (*processFence, error) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return nil, fmt.Errorf("native process ownership unavailable")
	}
	baseline := map[int]string{}
	for pid, p := range nativeProcesses() {
		if p.ppid == os.Getpid() {
			baseline[pid] = p.start
		}
	}
	fence := &processFence{cmd: cmd, baseline: baseline, known: map[int]string{}}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = fence.kill
	return fence, nil
}
func (f *processFence) owned(processes map[int]nativeProcess) []nativeProcess {
	root := 0
	if f.cmd.Process != nil {
		root = f.cmd.Process.Pid
	}
	owned := map[int]bool{}
	for pid, p := range processes {
		if (pid == root && (f.known[pid] == "" || f.known[pid] == p.start)) || (f.known[pid] != "" && f.known[pid] == p.start) || (p.ppid == os.Getpid() && f.baseline[pid] != p.start) {
			owned[pid] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for pid, p := range processes {
			if !owned[pid] && owned[p.ppid] {
				owned[pid] = true
				changed = true
			}
		}
	}
	result := []nativeProcess{}
	for pid := range owned {
		p := processes[pid]
		f.known[pid] = p.start
		result = append(result, p)
	}
	return result
}
func (f *processFence) kill() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cmd.Process == nil {
		return nil
	}
	processes := nativeProcesses()
	owned := f.owned(processes)
	// Stop deepest tools before their parent can exit. This also handles
	// descendants that deliberately create a separate session/group.
	byPID := map[int]nativeProcess{}
	for _, p := range owned {
		byPID[p.pid] = p
	}
	depth := func(p nativeProcess) int {
		n := 0
		for n < len(owned) {
			parent, ok := byPID[p.ppid]
			if !ok {
				break
			}
			n++
			p = parent
		}
		return n
	}
	sort.Slice(owned, func(i, j int) bool { return depth(owned[i]) > depth(owned[j]) })
	for _, p := range owned {
		if p.pid != f.cmd.Process.Pid {
			_ = unix.Kill(p.pid, unix.SIGKILL)
		}
	}
	// The group is private to the harness. Never signal the actor's group.
	groupOwned := false
	for _, p := range owned {
		if p.pgrp == f.cmd.Process.Pid {
			groupOwned = true
			break
		}
	}
	if groupOwned {
		_ = unix.Kill(-f.cmd.Process.Pid, unix.SIGKILL)
	}
	for _, p := range owned {
		if p.pid == f.cmd.Process.Pid {
			_ = unix.Kill(p.pid, unix.SIGKILL)
		}
	}
	return nil
}
func (f *processFence) waitDescendants() {
	for {
		f.mu.Lock()
		owned := f.owned(nativeProcesses())
		live := false
		for _, p := range owned {
			if p.state != "Z" && p.state != "X" {
				live = true
				_ = unix.Kill(p.pid, unix.SIGKILL)
			}
			if p.ppid == os.Getpid() && p.pid != f.cmd.Process.Pid && (p.state == "Z" || p.state == "X") {
				_, _ = unix.Wait4(p.pid, nil, unix.WNOHANG, nil)
			}
		}
		f.mu.Unlock()
		if !live {
			return
		}
		// Retain ownership if kernel teardown is delayed; returning early could
		// release a shared home while an owned tool process can still write.
		time.Sleep(5 * time.Millisecond)
	}
}
