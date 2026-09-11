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
		args := []string{"--app=" + target, "--new-window"}
		if os.Getenv("ANVIL_DESKTOP_CHROME_NO_SANDBOX") == "1" {
			args = append([]string{"--no-sandbox", "--disable-dev-shm-usage"}, args...)
		}
		if dir := strings.TrimSpace(os.Getenv("ANVIL_DESKTOP_CHROME_USER_DATA_DIR")); dir != "" {
			args = append(args, "--user-data-dir="+dir)
		}
		cmd := exec.Command(chrome, args...) // #nosec G204 -- chrome is LookPath of a known browser; URL is the loopback host; extra flags are env-controlled constants
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
