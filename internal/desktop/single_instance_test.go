package desktop

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

func TestIsAddrInUse(t *testing.T) {
	inUse := fmt.Errorf(`listen 127.0.0.1:1738: %w`, &net.OpError{
		Op:  "listen",
		Net: "tcp",
		Err: syscall.EADDRINUSE,
	})
	if !IsAddrInUse(inUse) {
		t.Fatalf("expected wrapped EADDRINUSE to match: %v", inUse)
	}
	if !IsAddrInUse(syscall.EADDRINUSE) {
		t.Fatal("expected bare EADDRINUSE to match")
	}
	windows := errors.New(`listen tcp 127.0.0.1:1738: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted`)
	if !IsAddrInUse(windows) {
		t.Fatalf("expected Windows Winsock message to match: %v", windows)
	}
	posix := errors.New("listen tcp 127.0.0.1:1738: bind: address already in use")
	if !IsAddrInUse(posix) {
		t.Fatalf("expected POSIX message to match: %v", posix)
	}
	if IsAddrInUse(nil) {
		t.Fatal("nil must not match")
	}
	if IsAddrInUse(errors.New("listen tcp 127.0.0.1:1738: bind: address not available")) {
		t.Fatal("unrelated bind error must not match")
	}
	if IsAddrInUse(errors.New("Anvil Agents Desktop listens on loopback only (got 0.0.0.0:1738)")) {
		t.Fatal("loopback validation error must not match")
	}
}

func TestListenURL(t *testing.T) {
	for listen, want := range map[string]string{
		"127.0.0.1:1738": "http://127.0.0.1:1738/",
		"localhost:1738": "http://localhost:1738/",
		"[::1]:1738":     "http://[::1]:1738/",
		"":               "http://127.0.0.1:1738/",
	} {
		if got := ListenURL(listen); got != want {
			t.Errorf("ListenURL(%q) = %q, want %q", listen, got, want)
		}
	}
}
