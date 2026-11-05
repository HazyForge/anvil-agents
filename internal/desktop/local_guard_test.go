package desktop

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalRequestGuard(t *testing.T) {
	s, err := NewServer(Options{Listen: "127.0.0.1:0", ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var discoveries atomic.Int32
	s.opts.Discoverer.LookPath = func(string) (string, error) {
		discoveries.Add(1)
		return "", os.ErrNotExist
	}
	started := make(chan string, 1)
	s.opts.OnListen = func(addr string) { started <- addr }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Start(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	var addr string
	select {
	case addr = <-started:
	case err := <-done:
		t.Fatalf("start Desktop: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Desktop did not listen")
	}
	origin := "http://" + addr
	_, port, _ := net.SplitHostPort(addr)
	for _, tc := range []struct {
		name, host, origin, site string
		want                     int
	}{
		{"curl", addr, "", "", 200},
		{"same origin", addr, origin, "same-origin", 200},
		{"localhost", "localhost:" + port, "http://localhost:" + port, "none", 200},
		{"IPv6 Host", "[::1]:" + port, "", "", 200},
		{"IPv6 origin", "[::1]:" + port, "http://[::1]:" + port, "same-origin", 200},
		{"rebound host", "attacker.invalid:" + port, "", "", 403},
		{"wrong port", "127.0.0.1:0", "", "", 403},
		{"foreign origin", addr, "https://attacker.invalid", "", 403},
		{"null origin", addr, "null", "", 403},
		{"cross site", addr, "", "cross-site", 403},
		{"same site", addr, "", "same-site", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, origin+"/local/v1/snapshot", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
	// A no-cors POST can contain JSON despite its text/plain content type. The
	// allowed snapshot probes above discover harnesses; the refused POST must not.
	discoveries.Store(0)
	req, err := http.NewRequest(http.MethodPost, origin+"/local/v1/delegate", strings.NewReader(`{"harness":"codex","prompt":"run this"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://attacker.invalid")
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || discoveries.Load() != 0 {
		t.Fatalf("delegate status=%d harness discoveries=%d", resp.StatusCode, discoveries.Load())
	}
}
