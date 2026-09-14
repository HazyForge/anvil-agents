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
	APIOrigin     string `json:"apiOrigin,omitempty"`
	HarnessTarget string `json:"harnessTarget,omitempty"` // native | wsl; empty = auto
	WSLDistro     string `json:"wslDistro,omitempty"`     // empty = default distro
}

type prefsFile struct {
	APIOrigin     string `json:"apiOrigin"`
	ConsoleURL    string `json:"consoleURL"` // legacy field from the kube-wrap scaffold
	HarnessTarget string `json:"harnessTarget"`
	WSLDistro     string `json:"wslDistro"`
}

type prefsPatch struct {
	APIOrigin     *string `json:"apiOrigin"`
	HarnessTarget *string `json:"harnessTarget"`
	WSLDistro     *string `json:"wslDistro"`
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
	prefs := Prefs{
		HarnessTarget: strings.TrimSpace(wire.HarnessTarget),
		WSLDistro:     strings.TrimSpace(wire.WSLDistro),
	}
	if origin == "" {
		return normalizePrefs(prefs)
	}
	parsed, err := ParseAPIOrigin(origin)
	if err != nil {
		if strings.TrimSpace(wire.APIOrigin) != "" {
			return Prefs{}, fmt.Errorf("parse %s: %w", path, err)
		}
		return normalizePrefs(prefs)
	}
	prefs.APIOrigin = parsed
	return normalizePrefs(prefs)
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

func applyPrefsPatch(cur Prefs, patch prefsPatch) (Prefs, error) {
	if patch.APIOrigin != nil {
		cur.APIOrigin = strings.TrimSpace(*patch.APIOrigin)
	}
	if patch.HarnessTarget != nil {
		cur.HarnessTarget = strings.TrimSpace(*patch.HarnessTarget)
	}
	if patch.WSLDistro != nil {
		cur.WSLDistro = strings.TrimSpace(*patch.WSLDistro)
	}
	return normalizePrefs(cur)
}

func validatePrefs(prefs Prefs) error {
	if strings.TrimSpace(prefs.APIOrigin) != "" {
		if _, err := ParseAPIOrigin(prefs.APIOrigin); err != nil {
			return err
		}
	}
	switch strings.ToLower(strings.TrimSpace(prefs.HarnessTarget)) {
	case "", HarnessTargetNative, HarnessTargetWSL:
	default:
		return fmt.Errorf("harnessTarget must be native, wsl, or empty")
	}
	if d := strings.TrimSpace(prefs.WSLDistro); d != "" && !safeDistroName(d) {
		return fmt.Errorf("wslDistro is not a valid WSL distro name")
	}
	return nil
}

func normalizePrefs(prefs Prefs) (Prefs, error) {
	origin := strings.TrimSpace(prefs.APIOrigin)
	target := strings.ToLower(strings.TrimSpace(prefs.HarnessTarget))
	distro := strings.TrimSpace(prefs.WSLDistro)
	out := Prefs{HarnessTarget: target, WSLDistro: distro}
	if origin == "" {
		return out, validatePrefs(out)
	}
	parsed, err := ParseAPIOrigin(origin)
	if err != nil {
		return Prefs{}, err
	}
	out.APIOrigin = parsed
	return out, validatePrefs(out)
}
