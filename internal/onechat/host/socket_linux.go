//go:build linux

package host

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// The caller owns the state lock before removing a previous process's socket.
func listenControl(dir string) (net.Listener, error) {
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("make onechat state directory private: %w", err)
	}
	path := filepath.Join(dir, "onechat.sock")
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s is not a socket; move it before starting onechat", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale onechat socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect onechat socket: %w", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen on onechat control socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("make onechat socket private: %w", err)
	}
	// UnixListener removes its path when closed, before releasing the state lock.
	return listener, nil
}

func checkPeer(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("onechat control requires a Unix socket")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return err
	}
	var credentials *unix.Ucred
	var peerErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if peerErr != nil {
		return peerErr
	}
	if credentials == nil || credentials.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("onechat control socket requires the daemon's user")
	}
	return nil
}
