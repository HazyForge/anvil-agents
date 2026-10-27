// Package host owns a manager and its state for loopback protocol v1 clients.
package host

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/onechat"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

const Protocol = "anvil.onechat.v1"
const DefaultListen = "127.0.0.1:1740"
const queueSize = 256

type Options struct {
	Manager *onechat.Manager
	// OpenManager opens state and credentials only after acquiring ownership.
	// Use it instead of Manager when starting from a saved configuration.
	OpenManager func(context.Context) (*onechat.Manager, error)
	StateDir    string
	Listen      string
	Version     string
	Logger      *log.Logger
}

type Host struct {
	manager     *onechat.Manager
	dir         string
	info        Metadata
	token       string
	name        string
	ctx         context.Context
	done        chan struct{}
	err         error
	mu          sync.Mutex
	clients     map[*client]struct{}
	turn        *runningTurn
	finished    []completedTurn
	refreshDone chan struct{}
	closing     bool
	workers     sync.WaitGroup
}

// Start holds the state lock until shutdown and binds before publishing
// discovery metadata. The CLI uses the same lock for standalone commands.
func Start(ctx context.Context, opts Options) (*Host, error) {
	if opts.Listen == "" {
		opts.Listen = DefaultListen
	}
	if err := loopbackAddress(opts.Listen); err != nil {
		return nil, err
	}
	if (opts.Manager == nil) == (opts.OpenManager == nil) {
		return nil, fmt.Errorf("onechat host requires either a manager or a manager opener")
	}
	if opts.StateDir == "" && opts.Manager != nil {
		opts.StateDir = opts.Manager.Config.StateDir
	}
	if opts.StateDir == "" {
		return nil, fmt.Errorf("onechat host requires a state directory")
	}
	dir, err := filepath.Abs(opts.StateDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lock, err := LockState(dir)
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			_ = lock.Close()
		}
	}()
	info, err := readMetadata(dir)
	if err != nil {
		return nil, err
	}
	if liveHost(info) {
		return nil, fmt.Errorf("onechat host already serves this state directory at %s; connect to that host", info.Listen)
	}
	if opts.OpenManager != nil {
		opts.Manager, err = opts.OpenManager(ctx)
		if err != nil {
			return nil, err
		}
		if opts.Manager == nil {
			return nil, fmt.Errorf("manager opener returned no manager")
		}
	}
	managerDir, err := filepath.Abs(opts.Manager.Config.StateDir)
	if err != nil || managerDir != dir {
		return nil, fmt.Errorf("host state directory must match the manager's state directory")
	}
	listener, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return nil, fmt.Errorf("listen for onechat: %w", err)
	}
	token, err := hostToken(dir)
	if err != nil {
		listener.Close()
		return nil, err
	}
	if info.HostID == "" {
		info.HostID = uuid.NewString()
	}
	info.Listen, info.PID, info.Version, info.StartedAt, info.StoppedAt = listener.Addr().String(), os.Getpid(), opts.Version, time.Now().UTC(), nil
	if err := writeMetadata(dir, info); err != nil {
		listener.Close()
		return nil, err
	}
	hostCtx, cancel := context.WithCancel(ctx)
	name, _ := os.Hostname()
	h := &Host{manager: opts.Manager, dir: dir, info: info, token: token, name: name, ctx: hostCtx, done: make(chan struct{}), clients: make(map[*client]struct{})}
	server := &http.Server{Handler: http.HandlerFunc(h.handleHTTP), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return hostCtx }}
	go func() {
		<-hostCtx.Done()
		h.mu.Lock()
		h.closing = true
		for c := range h.clients {
			c.close()
		}
		h.mu.Unlock()
		_ = server.Close()
	}()
	started = true
	go func() {
		err := server.Serve(listener)
		cancel()
		h.mu.Lock()
		h.closing = true
		for c := range h.clients {
			c.close()
		}
		h.mu.Unlock()
		h.workers.Wait()
		if !errors.Is(err, http.ErrServerClosed) {
			h.err = err
		}
		stopped := time.Now().UTC()
		stoppedInfo := info
		stoppedInfo.StoppedAt = &stopped
		if err := writeMetadata(dir, stoppedInfo); err != nil {
			h.err = errors.Join(h.err, err)
		}
		_ = lock.Close()
		close(h.done)
	}()
	if opts.Logger != nil {
		opts.Logger.Printf("onechat listening at %s, host %s", info.Listen, info.HostID)
	}
	return h, nil
}

func Serve(ctx context.Context, opts Options) error {
	h, err := Start(ctx, opts)
	if err != nil {
		return err
	}
	return h.Wait()
}

func (h *Host) Info() Metadata { return h.info }
func (h *Host) Wait() error    { <-h.done; return h.err }

func (h *Host) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		writeHTTP(w, http.StatusOK, map[string]any{"ok": true, "hostId": h.info.HostID})
		return
	}
	if r.URL.Path != "/v1/connect" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if _, present := r.Header["Origin"]; present {
		httpError(w, 403, "unauthorized", "browser origins cannot connect directly; use Anvil Agents Desktop")
		return
	}
	_, port, _ := net.SplitHostPort(h.info.Listen)
	if r.Host != "127.0.0.1:"+port && r.Host != "localhost:"+port && r.Host != "[::1]:"+port {
		httpError(w, 403, "unauthorized", "Host must name this loopback listener")
		return
	}
	// No operation uses query parameters, so a query can never transport a token.
	if r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+h.token)) != 1 {
		httpError(w, 401, "unauthorized", "provide the host-token in Authorization: Bearer")
		return
	}
	protocols := strings.Split(strings.Join(r.Header.Values("Sec-WebSocket-Protocol"), ","), ",")
	found := false
	for _, protocol := range protocols {
		if strings.TrimSpace(protocol) == Protocol {
			found = true
		}
	}
	if !found {
		httpError(w, 400, "invalid", "request WebSocket subprotocol "+Protocol)
		return
	}
	conn, err := standing.Upgrade(w, r, Protocol)
	if err != nil {
		httpError(w, 400, "invalid", err.Error())
		return
	}
	c := &client{host: h, conn: conn, out: make(chan []byte, queueSize), done: make(chan struct{})}
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		c.close()
		return
	}
	h.clients[c] = struct{}{}
	h.workers.Add(2)
	h.mu.Unlock()
	go c.writeLoop()
	go c.readLoop()
}

func writeHTTP(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func httpError(w http.ResponseWriter, status int, code, message string) {
	writeHTTP(w, status, map[string]string{"error": code, "message": message})
}
