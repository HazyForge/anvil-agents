package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hazyforge/anvil-agents/internal/onechat/wire"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

const onechatProtocol = "anvil.onechat.v1"

type onechatStatus struct {
	Mode       string `json:"mode"`
	Distro     string `json:"distro"`
	Installed  bool   `json:"installed"`
	Running    bool   `json:"running"`
	Configured bool   `json:"configured"`
	HostID     string `json:"hostId"`
	Version    string `json:"version"`
	ManagedBy  string `json:"managedBy"`
	Message    string `json:"message"`
}

type onechatBinary struct {
	mode   string
	distro string
	path   string
	wslExe string
}

type onechatBinaryCache struct {
	key    string
	binary onechatBinary
	at     time.Time
}

func onechatWSL() bool { return runtime.GOOS == "windows" && !insideWSL() }

func resolveOnechatPath(path string) (string, error) {
	// Windows must not resolve a Linux home against its own filesystem.
	if onechatWSL() || path == "" {
		return path, nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve one chat path: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/")), nil
	}
	return path, nil
}

func resolveOnechatStateDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = strings.TrimSpace(os.Getenv("ANVIL_ONECHAT_STATE_DIR"))
	}
	// With no override the CLI chooses its configured state directory in the
	// environment where the daemon runs, including the WSL home directory.
	return resolveOnechatPath(dir)
}

func (s *Server) onechatBinary(ctx context.Context) (onechatBinary, error) {
	prefs := s.currentPrefs()
	key := prefs.WSLDistro + "\x00" + s.opts.OnechatBin
	s.mu.Lock()
	cached := s.onechat
	s.mu.Unlock()
	if cached.key == key && time.Since(cached.at) < 30*time.Second {
		return cached.binary, nil
	}
	binary := onechatBinary{mode: "native"}
	name := s.opts.OnechatBin
	if name == "" {
		name = "onechat"
	}
	if onechatWSL() {
		binary.mode = "wsl"
		d := s.opts.Discoverer
		wsl := WSLStatus{}
		if prefs.WSLDistro == "" {
			wsl = probeWSL(ctx, d)
		}
		binary.distro = resolveWSLDistro(prefs, wsl)
		var err error
		binary.wslExe, err = d.wslRunner().LookExe()
		if err != nil {
			return binary, fmt.Errorf("install WSL and put onechat on the distro's PATH, then retry")
		}
		if strings.HasPrefix(name, "/") {
			// An explicit Linux path still needs discovery: wsl.exe does not
			// consistently use exit 127 when --exec cannot open the program.
			checkCtx, cancelCheck := context.WithTimeout(ctx, wslProbeTimeout)
			check, err := d.wslRunner().Run(checkCtx, WSLRequest{
				Distro: binary.distro,
				Argv:   []string{"/usr/bin/test", "-f", name, "-a", "-x", name},
			})
			cancelCheck()
			if err != nil || check.TimedOut || check.ExitCode != 0 {
				return binary, fmt.Errorf("--onechat-bin must name an executable file in the %s WSL distro; install onechat there or correct the Linux path", binary.distro)
			}
			binary.path = name
		} else {
			binary.path, err = d.wslLookPath(ctx, binary.distro, name)
			if err != nil {
				return binary, fmt.Errorf("put onechat on the %s WSL distro's PATH (for example ~/.local/bin), or set --onechat-bin to its absolute Linux path", binary.distro)
			}
		}
	} else {
		path, err := resolveOnechatPath(name)
		if err != nil {
			return binary, err
		}
		binary.path, err = exec.LookPath(path)
		if err != nil {
			return binary, fmt.Errorf("put onechat on PATH, or set --onechat-bin to its executable path")
		}
	}
	s.mu.Lock()
	s.onechat = onechatBinaryCache{key: key, binary: binary, at: time.Now()}
	s.mu.Unlock()
	return binary, nil
}

func (s *Server) onechatCommand(ctx context.Context, binary onechatBinary, op string, jsonOutput bool) (*exec.Cmd, error) {
	var args []string
	if s.opts.OnechatConfig != "" {
		args = append(args, "--config", s.opts.OnechatConfig)
	}
	args = append(args, "daemon", op)
	if s.opts.OnechatStateDir != "" {
		args = append(args, "--state-dir", s.opts.OnechatStateDir)
	}
	if jsonOutput {
		args = append(args, "--json")
	}
	path := binary.path
	if binary.mode == "wsl" {
		var err error
		args, err = wslArgv(binary.distro, append([]string{path}, args...))
		if err != nil {
			return nil, err
		}
		path = binary.wslExe
	}
	cmd := exec.CommandContext(ctx, path, args...) // #nosec G204 -- configured/resolved CLI path; fixed daemon commands and separately passed local options
	cmd.Env = delegateEnv(os.Environ())
	configureDelegateProcess(cmd)
	return cmd, nil
}

func (s *Server) onechatDaemonStatus(ctx context.Context, op string) onechatStatus {
	binary, err := s.onechatBinary(ctx)
	status := onechatStatus{Mode: binary.mode, Distro: binary.distro, ManagedBy: "none"}
	if err != nil {
		status.Message = err.Error()
		return status
	}
	status.Installed = true
	cmd, err := s.onechatCommand(ctx, binary, op, true)
	if err != nil {
		status.Message = err.Error()
		return status
	}
	var stdout cappedBuffer
	stdout.limit = 64 << 10
	var stderr onechatTail
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var result struct {
		Running    bool   `json:"running"`
		Configured bool   `json:"configured"`
		HostID     string `json:"hostId"`
		Version    string `json:"version"`
		ManagedBy  string `json:"managedBy"`
		Message    string `json:"message"`
	}
	if !stdout.truncated && json.Unmarshal([]byte(stdout.String()), &result) == nil {
		status.Running, status.Configured = result.Running, result.Configured
		status.HostID, status.Version, status.ManagedBy = result.HostID, result.Version, result.ManagedBy
		status.Message = result.Message
		return status
	}
	var exitErr *exec.ExitError
	if errors.Is(runErr, os.ErrNotExist) || (binary.mode == "wsl" && errors.As(runErr, &exitErr) && exitErr.ExitCode() == 127) {
		status.Installed = false
		status.Message = "put onechat on PATH in the environment where the daemon runs, or correct --onechat-bin"
	} else if ctx.Err() != nil {
		status.Message = "onechat daemon " + op + " timed out; check the daemon in its terminal"
	} else if message := stderr.String(); message != "" {
		status.Message = message
	} else if runErr != nil {
		status.Message = fmt.Sprintf("onechat daemon %s failed: %v", op, runErr)
	} else {
		status.Message = "onechat returned invalid status; update the onechat binary and retry"
	}
	return status
}

func (s *Server) handleOnechatStatus(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 6*time.Second)
	defer cancel()
	writeJSON(writer, http.StatusOK, s.onechatDaemonStatus(ctx, "status"))
}

func (s *Server) handleOnechatStart(writer http.ResponseWriter, request *http.Request) {
	// The CLI waits up to ten seconds for readiness after starting the daemon.
	ctx, cancel := context.WithTimeout(request.Context(), 16*time.Second)
	defer cancel()
	writeJSON(writer, http.StatusOK, s.onechatDaemonStatus(ctx, "start"))
}

func (s *Server) handleOnechatConnect(writer http.ResponseWriter, request *http.Request) {
	found := false
	for _, protocol := range strings.Split(strings.Join(request.Header.Values("Sec-WebSocket-Protocol"), ","), ",") {
		if strings.TrimSpace(protocol) == onechatProtocol {
			found = true
		}
	}
	if !found {
		writeError(writer, http.StatusBadRequest, "invalid", "request WebSocket subprotocol "+onechatProtocol)
		return
	}
	lookupCtx, stopLookup := context.WithTimeout(request.Context(), 6*time.Second)
	binary, err := s.onechatBinary(lookupCtx)
	stopLookup()
	if err != nil {
		writeError(writer, http.StatusBadGateway, "onechat_unavailable", err.Error())
		return
	}
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	cmd, err := s.onechatCommand(ctx, binary, "proxy", false)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "onechat_unavailable", err.Error())
		return
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		writeError(writer, http.StatusBadGateway, "onechat_unavailable", err.Error())
		return
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeError(writer, http.StatusBadGateway, "onechat_unavailable", err.Error())
		return
	}
	defer stdout.Close()
	var stderr onechatTail
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		writeError(writer, http.StatusBadGateway, "onechat_unavailable", "could not start onechat daemon proxy: "+err.Error())
		return
	}
	// A WebSocket outlives the HTTP server's ordinary request deadlines.
	controller := http.NewResponseController(writer)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	conn, err := standing.Upgrade(writer, request, onechatProtocol)
	if err != nil {
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
		writeError(writer, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	defer conn.Abort()
	ended := make(chan error, 2)
	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		ended <- onechatBrowserToProxy(conn, stdin)
	}()
	go func() {
		defer copies.Done()
		ended <- onechatProxyToBrowser(stdout, conn)
	}()
	select {
	case err = <-ended:
	case <-ctx.Done():
		err = ctx.Err()
	}
	// Closing stdin is also the Linux proxy's shutdown signal: killing wsl.exe
	// alone does not reliably terminate a process inside the distro.
	_ = stdin.Close()
	_ = stdout.Close()
	cancel()
	_ = cmd.Wait()
	reason := stderr.String()
	if reason == "" && err != nil && err != io.EOF {
		reason = err.Error()
	}
	closed := make(chan struct{})
	go func() {
		code := uint16(1000)
		if reason != "" {
			code = 1011
		}
		_ = conn.CloseWithReason(code, reason)
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(250 * time.Millisecond):
		_ = conn.Abort()
		<-closed
	}
	copies.Wait()
}

func onechatBrowserToProxy(conn *standing.Conn, stdin io.Writer) error {
	for {
		op, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if op != 1 {
			return fmt.Errorf("onechat expects JSON text frames")
		}
		// A formatted JSON object is still one request. Compact it before line
		// framing so embedded formatting cannot turn one frame into two requests.
		var compact bytes.Buffer
		if json.Compact(&compact, raw) == nil {
			raw = compact.Bytes()
		}
		// Malformed single-line requests still reach the host's invalid reply.
		// WriteLine rejects raw newlines that cannot be safely compacted.
		if err := wire.WriteLine(stdin, raw); err != nil {
			return err
		}
	}
}

func onechatProxyToBrowser(stdout io.Reader, conn *standing.Conn) error {
	reader := bufio.NewReader(stdout)
	for {
		raw, err := wire.ReadLine(reader)
		if err != nil {
			return err
		}
		if err := conn.WriteText(raw); err != nil {
			return err
		}
	}
}

// A proxy may run indefinitely; keep only its last diagnostic bytes, including
// when stderr is being drained concurrently with connection cleanup.
type onechatTail struct {
	mu   sync.Mutex
	data []byte
}

func (b *onechatTail) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const limit = 8 << 10
	count := len(p)
	if len(p) >= limit {
		b.data = append(b.data[:0], p[len(p)-limit:]...)
	} else {
		if len(b.data)+len(p) > limit {
			b.data = b.data[len(b.data)+len(p)-limit:]
		}
		b.data = append(b.data, p...)
	}
	return count, nil
}

func (b *onechatTail) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(strings.ToValidUTF8(string(b.data), ""))
}
