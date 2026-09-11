package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const prefsFileName = "config.json"

// Prefs are operator-local desktop settings. They never store tokens.
type Prefs struct {
	APIOrigin string `json:"apiOrigin,omitempty"`
}

type prefsFile struct {
	APIOrigin  string `json:"apiOrigin"`
	ConsoleURL string `json:"consoleURL"` // legacy field from the kube-wrap scaffold
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
	var wire prefsFile
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Prefs{}, fmt.Errorf("parse %s: %w", path, err)
	}
	origin := strings.TrimSpace(wire.APIOrigin)
	if origin == "" {
		origin = strings.TrimSpace(wire.ConsoleURL)
	}
	if origin == "" {
		return Prefs{}, nil
	}
	parsed, err := ParseAPIOrigin(origin)
	if err != nil {
		if strings.TrimSpace(wire.APIOrigin) != "" {
			return Prefs{}, fmt.Errorf("parse %s: %w", path, err)
		}
		return Prefs{}, nil
	}
	return Prefs{APIOrigin: parsed}, nil
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
	if strings.TrimSpace(prefs.APIOrigin) == "" {
		return nil
	}
	_, err := ParseAPIOrigin(prefs.APIOrigin)
	return err
}

func normalizePrefs(prefs Prefs) (Prefs, error) {
	origin := strings.TrimSpace(prefs.APIOrigin)
	if origin == "" {
		return Prefs{}, nil
	}
	parsed, err := ParseAPIOrigin(origin)
	if err != nil {
		return Prefs{}, err
	}
	return Prefs{APIOrigin: parsed}, nil
}
