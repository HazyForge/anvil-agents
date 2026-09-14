package desktop

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var errWSLCleanupUnconfirmed = errors.New("WSL process cleanup was not confirmed")
var wslRunDirRE = regexp.MustCompile(`^/tmp/anvil-desktop-run\.[A-Za-z0-9]+$`)

// The lock serializes publishing a Linux process-group ID with cancellation.
// A cancel before startup leaves no opportunity for a late WSL launch to run.
const wslManagedRunScript = `exec 9>"$ANVIL_DESKTOP_RUN_DIR/lock" || exit 125
flock -x 9 || exit 125
[ -d "$ANVIL_DESKTOP_RUN_DIR" ] && [ ! -e "$ANVIL_DESKTOP_RUN_DIR/cancel" ] || exit 125
printf '%s\n' "$$" > "$ANVIL_DESKTOP_RUN_DIR/pid" || exit 125
flock -u 9
exec 9>&-
run_limit="$ANVIL_DESKTOP_RUN_TIMEOUT"
unset ANVIL_DESKTOP_RUN_DIR ANVIL_DESKTOP_RUN_TIMEOUT
exec timeout --signal=TERM --kill-after=2s "$run_limit" "$@"`

const wslManagedCleanupScript = `[ -d "$ANVIL_DESKTOP_RUN_DIR" ] || exit 0
exec 9>"$ANVIL_DESKTOP_RUN_DIR/lock" || exit 125
flock -x 9 || exit 125
: > "$ANVIL_DESKTOP_RUN_DIR/cancel" || exit 125
if [ -f "$ANVIL_DESKTOP_RUN_DIR/pid" ]; then
  read -r pid < "$ANVIL_DESKTOP_RUN_DIR/pid" || exit 125
  case "$pid" in ''|*[!0-9]*) exit 125 ;; esac
  [ "$pid" -gt 1 ] || exit 125
  if ! kill -KILL -- "-$pid" 2>/dev/null; then
    if kill -0 -- "-$pid" 2>/dev/null; then exit 125; fi
  fi
fi
rm -rf -- "$ANVIL_DESKTOP_RUN_DIR" || exit 125`

// Killing wsl.exe alone does not kill its Linux workload. Always launch actual
// delegated commands in a Linux-owned group and explicitly clean that group
// through a separate WSL invocation, independent of the canceled request.
func managedWSLRun(ctx context.Context, look func() (string, error), req WSLRequest) (WSLResult, error) {
	run := defaultWSLRun(look)
	setupCtx, cancelSetup := context.WithTimeout(ctx, wslMktempTimeout)
	setup, err := run(setupCtx, WSLRequest{Distro: req.Distro, Argv: []string{"/bin/bash", "-c", `command -v setsid flock timeout >/dev/null || exit 125
umask 077
mktemp -d /tmp/anvil-desktop-run.XXXXXXXXXX`}})
	cancelSetup()
	control := strings.TrimSpace(setup.Stdout)
	if err != nil || setup.ExitCode != 0 || setup.TimedOut || !wslRunDirRE.MatchString(control) {
		return WSLResult{}, fmt.Errorf("could not prepare WSL process control")
	}
	limit := maxDelegateTO
	if deadline, ok := ctx.Deadline(); ok {
		limit = time.Until(deadline)
	}
	if limit < time.Millisecond {
		limit = time.Millisecond
	}
	timeout := strconv.FormatFloat(limit.Seconds(), 'f', 3, 64) + "s"
	req.ManagedProcess = false
	req.Argv = append([]string{"setsid", "--wait", "/bin/bash", "-c", wslManagedRunScript, "anvil-desktop-run"}, req.Argv...)
	req.Env = append(append([]string(nil), req.Env...), "ANVIL_DESKTOP_RUN_DIR="+control, "ANVIL_DESKTOP_RUN_TIMEOUT="+timeout)
	result, runErr := run(ctx, req)
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCleanup()
	cleanup, cleanupErr := run(cleanupCtx, WSLRequest{Distro: req.Distro, Argv: []string{"/bin/bash", "-c", wslManagedCleanupScript}, Env: []string{"ANVIL_DESKTOP_RUN_DIR=" + control}})
	if cleanupErr != nil || cleanup.ExitCode != 0 || cleanup.TimedOut {
		return result, errWSLCleanupUnconfirmed
	}
	if result.ExitCode == 124 {
		result.TimedOut = true
		result.ExitCode = -1
	}
	return result, runErr
}
