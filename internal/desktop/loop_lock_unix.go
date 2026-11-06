//go:build !windows

package desktop

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockLoopFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func unlockLoopFile(f *os.File)     { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }

func syncLoopDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
