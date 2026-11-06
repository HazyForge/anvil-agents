package desktop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"
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

func TestServerStartPreservesSingleInstanceWithManagerStore(t *testing.T) {
	configDir := t.TempDir()
	startHost := func(listen string) (string, func()) {
		t.Helper()
		started := make(chan string, 1)
		server, err := NewServer(Options{
			Listen:    listen,
			ConfigDir: configDir,
			OnListen:  func(addr string) { started <- addr },
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- server.Start(ctx) }()
		stopped := false
		stop := func() {
			if stopped {
				return
			}
			stopped = true
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("stop Desktop: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Desktop did not stop")
			}
		}
		t.Cleanup(stop)
		select {
		case addr := <-started:
			return addr, stop
		case err := <-done:
			stopped = true
			cancel()
			t.Fatalf("start Desktop: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("Desktop did not listen")
		}
		return "", nil
	}

	addr, stopOwner := startHost("127.0.0.1:0")
	second, err := NewServer(Options{Listen: addr, ConfigDir: configDir})
	if err != nil {
		t.Fatal(err)
	}
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 3*time.Second)
	err = second.Start(secondCtx)
	cancelSecond()
	if !IsAddrInUse(err) {
		t.Fatalf("second launch error = %v; want address-in-use for opening the existing Desktop", err)
	}

	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	otherAddr := reservation.Addr().String()
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := NewServer(Options{Listen: otherAddr, ConfigDir: configDir})
	if err != nil {
		t.Fatal(err)
	}
	otherCtx, cancelOther := context.WithTimeout(context.Background(), 3*time.Second)
	err = other.Start(otherCtx)
	cancelOther()
	if err == nil || IsAddrInUse(err) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second manager-store owner error = %v; want a prompt ownership failure", err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("existing Desktop stopped responding: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("existing Desktop health = %d, want %d", response.StatusCode, http.StatusOK)
	}

	// The failed second owner must release its listener, and stopping the
	// original owner must release the manager store for a new host.
	stopOwner()
	restartedAddr, _ := startHost(otherAddr)
	if restartedAddr != otherAddr {
		t.Fatalf("restarted Desktop listened at %q, want %q", restartedAddr, otherAddr)
	}
}
