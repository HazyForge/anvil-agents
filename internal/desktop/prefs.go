package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const prefsFileName = "config.json"

// Prefs are operator-local desktop settings. They never store tokens.
type Prefs struct {
	Kubeconfig string `json:"kubeconfig,omitempty"`
	Context    string `json:"kubeContext,omitempty"`
	ConsoleURL string `json:"consoleURL,omitempty"`
}

func prefsPath(configDir string) (string, error) {
	dir := strings.TrimSpace(configDir)
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve config dir: %w", err)
		}
		dir = filepath.Join(base, "anvil-desktop")
	}
	return filepath.Join(dir, prefsFileName), nil
}

func loadPrefs(configDir string) (Prefs, error) {
	path, err := prefsPath(configDir)
	if err != nil {
		return Prefs{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Prefs{}, nil
		}
		return Prefs{}, err
	}
	var prefs Prefs
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return Prefs{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return prefs, nil
}

func savePrefs(configDir string, prefs Prefs) error {
	if err := validatePrefs(prefs); err != nil {
		return err
	}
	path, err := prefsPath(configDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func validatePrefs(prefs Prefs) error {
	if path := strings.TrimSpace(prefs.Kubeconfig); path != "" {
		if strings.Contains(path, "\x00") || !filepath.IsAbs(path) {
			return fmt.Errorf("kubeconfig path must be an absolute path")
		}
	}
	if origin := strings.TrimSpace(prefs.ConsoleURL); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("console URL must be an absolute http(s) origin")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("console URL must be http or https")
		}
		if parsed.User != nil {
			return fmt.Errorf("console URL must not include credentials")
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("console URL must not include a query or fragment")
		}
	}
	return nil
}
