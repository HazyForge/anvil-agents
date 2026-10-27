//go:build linux

package onechat

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A saved PID alone is insufficient after restart: another process may have
// reused it, or a reboot may have reset the PID and process-start counters.
func sessionProcessIdentity(pid int) string {
	if pid <= 1 {
		return ""
	}
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return ""
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return ""
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) <= 19 {
		return ""
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19]
}
