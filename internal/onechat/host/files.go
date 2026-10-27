package host

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Metadata is the private discovery document shared with Desktop.
type Metadata struct {
	Listen    string     `json:"listen"`
	PID       int        `json:"pid"`
	HostID    string     `json:"hostId"`
	Version   string     `json:"version"`
	StartedAt time.Time  `json:"startedAt"`
	StoppedAt *time.Time `json:"stoppedAt,omitempty"`
}

func loopbackAddress(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen must be a loopback IP and port: %w", err)
	}
	ip := net.ParseIP(host)
	n, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("onechat listens only on a loopback IP (127.0.0.0/8 or ::1); use 127.0.0.1:1740")
	}
	return nil
}

func readMetadata(dir string) (Metadata, error) {
	var info Metadata
	raw, err := os.ReadFile(filepath.Join(dir, "host.json"))
	if errors.Is(err, os.ErrNotExist) {
		return info, nil
	}
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return info, fmt.Errorf("read host.json: %w", err)
	}
	if !validUUID(info.HostID) {
		return info, fmt.Errorf("host.json has an invalid hostId; restore its original UUID before starting")
	}
	return info, nil
}

func liveHost(info Metadata) bool {
	if info.StoppedAt != nil || info.PID <= 0 || !processAlive(info.PID) || loopbackAddress(info.Listen) != nil {
		return false
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://" + info.Listen + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var health struct {
		OK bool `json:"ok"`
	}
	return resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&health) == nil && health.OK
}

func hostToken(dir string) (string, error) {
	path := filepath.Join(dir, "host-token")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		raw := make([]byte, 32)
		if _, err = rand.Read(raw); err == nil {
			_, err = f.WriteString(base64.RawURLEncoding.EncodeToString(raw) + "\n")
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	} else if !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o177 != 0 {
		return "", fmt.Errorf("host-token must be a regular private file; set its permissions to 0600")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := string(raw)
	if len(token) > 0 && token[len(token)-1] == '\n' {
		token = token[:len(token)-1]
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("host-token must contain 32 base64url-encoded random bytes; restore the token file")
	}
	return token, nil
}

func writeMetadata(dir string, info Metadata) error {
	raw, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".host-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if _, err = f.Write(append(raw, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "host.json"))
}

func validUUID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && len(id) == 36 && strings.EqualFold(parsed.String(), id)
}

// LockState keeps the host and standalone CLI commands from saving the same
// state concurrently. Hold the returned lock until all state writes finish.
func LockState(dir string) (io.Closer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "host.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("a onechat host already serves this state directory; use Anvil Agents Desktop or stop the host first: %w", err)
	}
	return file, nil
}
