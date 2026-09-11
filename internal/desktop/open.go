package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func OpenWindow(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("open URL is empty")
	}
	if chrome := firstLookPath("google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"); chrome != "" {
		cmd := exec.Command(chrome, "--app="+target, "--new-window") // #nosec G204 -- chrome is LookPath of a known browser; URL is the loopback host
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

func firstLookPath(names ...string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil && path != "" {
			return path
		}
	}
	return ""
}
