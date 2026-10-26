package desktop

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockLoopFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func unlockLoopFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}

func syncLoopDirectory(path string) error { return nil } // File.Sync precedes atomic rename; Windows cannot fsync a directory.
