package desktop

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func OpenWindow(target string) error {
	target = strings.TrimSpace(target)
	if err := validateWindowURL(target); err != nil {
		return err
	}
	if chrome := firstLookPath(chromeCandidates()...); chrome != "" {
		args := []string{"--app=" + target, "--new-window"}
		if os.Getenv("ANVIL_DESKTOP_CHROME_NO_SANDBOX") == "1" {
			args = append([]string{"--no-sandbox", "--disable-dev-shm-usage"}, args...)
		}
		if dir := strings.TrimSpace(os.Getenv("ANVIL_DESKTOP_CHROME_USER_DATA_DIR")); dir != "" {
			args = append(args, "--user-data-dir="+dir)
		}
		cmd := exec.Command(chrome, args...) // #nosec G204 G702 -- fixed browser from LookPath, validated loopback URL, separate argv; no shell execution
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Start()
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start() // #nosec G204 -- macOS open of a loopback URL
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start() // #nosec G204 -- Windows URL handler
	default:
		return exec.Command("xdg-open", target).Start() // #nosec G204 -- xdg-open of a loopback URL
	}
}

func chromeCandidates() []string {
	var extra []string
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
			if strings.TrimSpace(root) == "" {
				continue
			}
			extra = append(extra,
				filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
			)
		}
		extra = append(extra, "chrome.exe", "msedge.exe", "chrome")
	}
	return append(extra, "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "msedge")
}

func firstLookPath(names ...string) string {
	for _, name := range names {
		if strings.ContainsRune(name, os.PathSeparator) {
			if info, err := os.Stat(name); err == nil && !info.IsDir() {
				return name
			}
			continue
		}
		if path, err := exec.LookPath(name); err == nil && path != "" {
			return path
		}
	}
	return ""
}

func validateWindowURL(target string) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed == nil || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("desktop window requires a loopback HTTP URL")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("desktop window requires a loopback HTTP URL")
	}
	return nil
}
