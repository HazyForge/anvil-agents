package desktop

import (
	"os/exec"
	"time"
)

func configureDelegateProcess(cmd *exec.Cmd) {
	// CommandContext terminates the native process/wsl.exe on cancellation.
	// Bound cleanup even if a descendant retains an inherited output handle.
	cmd.WaitDelay = time.Second
}
