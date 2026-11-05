//go:build !linux

package main

import (
	"fmt"
	"os/exec"
)

func daemonProcessAlive(int) bool   { return false }
func daemonInService(int) bool      { return false }
func detachProcess(*exec.Cmd) error { return requireSystemdPlatform() }
func terminateDaemon(int) error     { return requireSystemdPlatform() }
func requireSystemdPlatform() error {
	return fmt.Errorf("onechat daemon requires Linux; run it inside WSL on Windows")
}

func useLoginPath() {}
