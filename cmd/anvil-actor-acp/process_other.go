//go:build !linux

package main

import (
	"fmt"
	"os/exec"
)

type processFence struct{}

func newProcessFence(*exec.Cmd) (*processFence, error) {
	return nil, fmt.Errorf("native ACP process-tree ownership currently requires Linux")
}
func (*processFence) kill() error      { return nil }
func (*processFence) waitDescendants() {}
