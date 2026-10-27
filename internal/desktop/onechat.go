package desktop

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type onechatHost struct {
	Listen    string     `json:"listen"`
	HostID    string     `json:"hostId"`
	Version   string     `json:"version"`
	StoppedAt *time.Time `json:"stoppedAt,omitempty"`
}

type onechatStatus struct {
	Configured bool   `json:"configured"`
	Running    bool   `json:"running"`
	Listen     string `json:"listen"`
	HostID     string `json:"hostId"`
	Version    string `json:"version"`
	Message    string `json:"message"`
}

func resolveOnechatStateDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = strings.TrimSpace(os.Getenv("ANVIL_ONECHAT_STATE_DIR"))
	}
	if dir == "" {
		dir = "~/.config/onechat"
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve one chat state directory: %w", err)
		}
		if dir == "~" {
			dir = home
		} else {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
		}
	}
	return dir, nil
}

func (s *Server) readOnechatHost() (onechatHost, error) {
	var host onechatHost
	file, err := os.Open(filepath.Join(s.opts.OnechatStateDir, "host.json"))
	if err != nil {
		return host, fmt.Errorf("read one chat host.json; run onechat serve: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 16<<10+1))
	if err != nil || len(raw) > 16<<10 || json.Unmarshal(raw, &host) != nil {
		return host, fmt.Errorf("one chat host.json is invalid; restart onechat serve")
	}
	if err := onechatLoopback(host.Listen); err != nil {
		return host, err
	}
	return host, nil
}

func onechatLoopback(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	n, portErr := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return fmt.Errorf("one chat host.json must name a loopback IP and port; restart onechat serve on loopback")
	}
	return nil
}

func (s *Server) handleOnechatStatus(writer http.ResponseWriter, request *http.Request) {
	host, err := s.readOnechatHost()
	status := onechatStatus{}
	if err != nil {
		status.Message = err.Error()
		writeJSON(writer, http.StatusOK, status)
		return
	}
	if _, err := os.Stat(filepath.Join(s.opts.OnechatStateDir, "host-token")); err != nil {
		status.Message = "one chat host-token is unavailable; run onechat serve"
		writeJSON(writer, http.StatusOK, status)
		return
	}
	status.Configured = true
	status.Listen, status.HostID, status.Version = host.Listen, host.HostID, host.Version
	if err := verifyOnechatHost(request.Context(), host); err != nil {
		status.Message = err.Error()
	} else {
		status.Running = true
	}
	writeJSON(writer, http.StatusOK, status)
}

func verifyOnechatHost(ctx context.Context, host onechatHost) error {
	if host.StoppedAt != nil {
		return fmt.Errorf("one chat host is stopped; run onechat serve")
	}
	if host.HostID == "" {
		return fmt.Errorf("one chat host.json has no host identity; restart onechat serve")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	probe, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host.Listen+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("one chat host.json is invalid; restart onechat serve")
	}
	// Probe without credentials before trusting a port that another process may
	// now own. Neither redirects nor environment proxies may leave loopback.
	client := &http.Client{Transport: onechatTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(probe)
	if err != nil {
		return fmt.Errorf("one chat host is unreachable; run onechat serve")
	}
	defer response.Body.Close()
	var health struct {
		OK     bool   `json:"ok"`
		HostID string `json:"hostId"`
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil || len(raw) > 16<<10 || response.StatusCode != http.StatusOK || json.Unmarshal(raw, &health) != nil || !health.OK || health.HostID != host.HostID {
		return fmt.Errorf("one chat host identity could not be verified; restart onechat serve")
	}
	return nil
}

// This transport never honors proxy environment variables for local credentials.
var onechatTransport = &http.Transport{
	DialContext:           (&net.Dialer{Timeout: time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ResponseHeaderTimeout: time.Second,
	IdleConnTimeout:       30 * time.Second,
}

func (s *Server) readOnechatToken() (string, error) {
	file, err := os.Open(filepath.Join(s.opts.OnechatStateDir, "host-token"))
	if err != nil {
		return "", fmt.Errorf("read one chat host-token; run onechat serve: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat one chat host-token: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() & ^os.FileMode(0600) != 0 {
		return "", fmt.Errorf("one chat host-token must be a regular file with mode 0600 or stricter; fix its permissions")
	}
	if err := onechatTokenOwner(file, info); err != nil {
		return "", err
	}
	raw, err := io.ReadAll(io.LimitReader(file, 1025))
	if err != nil || len(raw) > 1024 {
		return "", fmt.Errorf("unable to read one chat host-token; restart onechat serve")
	}
	token := strings.TrimSpace(string(raw))
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("one chat host-token is invalid; restore the token created by onechat serve")
	}
	return token, nil
}

func (s *Server) handleOnechatConnect(writer http.ResponseWriter, request *http.Request) {
	host, err := s.readOnechatHost()
	if err != nil {
		writeError(writer, http.StatusBadGateway, "onechat_unavailable", err.Error())
		return
	}
	if err := verifyOnechatHost(request.Context(), host); err != nil {
		writeError(writer, http.StatusBadGateway, "onechat_unavailable", err.Error())
		return
	}
	token, err := s.readOnechatToken()
	if err != nil {
		writeError(writer, http.StatusBadGateway, "onechat_unavailable", err.Error())
		return
	}
	// A WebSocket outlives the HTTP server's ordinary request deadlines.
	controller := http.NewResponseController(writer)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	proxy := &httputil.ReverseProxy{
		Transport: onechatTransport,
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			req := proxyRequest.Out
			req.URL.Scheme = "http"
			req.URL.Host = host.Listen
			req.URL.Path = "/v1/connect"
			req.URL.RawPath = ""
			req.Host = host.Listen
			req.Header.Set("Authorization", "Bearer "+token)
			for _, name := range []string{"Origin", "Referer", "Cookie", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				req.Header.Del(name)
			}
		},
		ModifyResponse: func(response *http.Response) error {
			response.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeError(w, http.StatusBadGateway, "onechat_unavailable", "one chat host is unreachable; run onechat serve")
		},
	}
	proxy.ServeHTTP(writer, request)
}
