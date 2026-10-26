package onechat

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

const defaultTimeout = 10 * time.Minute

type Config struct {
	StateDir string `json:"stateDir"`
	Manager  struct {
		Model string `json:"model"`
	} `json:"manager"`
	Cloud CloudConfig `json:"cloud"`
	Local struct {
		WorkdirRoots []string `json:"workdirRoots"`
		Timeout      string   `json:"timeout"`
	} `json:"local"`
	Setups []Setup `json:"setups"`
}

type CloudConfig struct {
	BaseURL      string   `json:"baseURL"`
	Namespaces   []string `json:"namespaces"`
	TokenCommand []string `json:"tokenCommand"`
	SessionFile  string   `json:"sessionFile"`
	Login        struct {
		Port int `json:"port"`
	} `json:"login"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = yaml.UnmarshalStrict(b, &c); err != nil {
		return c, fmt.Errorf("invalid onechat config: %w", err)
	}
	err = c.Validate()
	return c, err
}

func (c Config) timeout() time.Duration {
	d, err := time.ParseDuration(c.Local.Timeout)
	if err != nil || d <= 0 {
		return defaultTimeout
	}
	return d
}

func (c *Config) Validate() error {
	if c.StateDir == "" {
		return fmt.Errorf("stateDir is required")
	}
	var err error
	c.StateDir, err = expandPath(c.StateDir)
	if err != nil {
		return err
	}
	if c.Manager.Model == "" {
		c.Manager.Model = "sonnet"
	}
	if c.Local.Timeout != "" {
		if d, err := time.ParseDuration(c.Local.Timeout); err != nil || d <= 0 {
			return fmt.Errorf("local.timeout must be a positive duration")
		}
	}
	for i, root := range c.Local.WorkdirRoots {
		c.Local.WorkdirRoots[i], err = canonicalDir(root)
		if err != nil {
			return fmt.Errorf("workdir root: %w", err)
		}
	}
	if c.Cloud.BaseURL != "" {
		u, err := url.Parse(c.Cloud.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("cloud.baseURL must be an HTTP(S) URL without credentials, query or fragment")
		}
		c.Cloud.BaseURL = strings.TrimRight(c.Cloud.BaseURL, "/")
	}
	if c.Cloud.Login.Port == 0 {
		c.Cloud.Login.Port = 1738
	}
	if c.Cloud.Login.Port < 1 || c.Cloud.Login.Port > 65535 {
		return fmt.Errorf("cloud.login.port must be between 1 and 65535")
	}
	if c.Cloud.SessionFile == "" {
		c.Cloud.SessionFile = filepath.Join(c.StateDir, "cloud-session.json")
	}
	c.Cloud.SessionFile, err = expandPath(c.Cloud.SessionFile)
	if err != nil {
		return err
	}
	seen, profiles := map[string]bool{}, map[string]bool{}
	for i := range c.Setups {
		s := &c.Setups[i]
		if s.ID == "" || seen[s.ID] {
			return fmt.Errorf("setup ids must be nonempty and unique")
		}
		seen[s.ID] = true
		if s.Location == "local" {
			if err := localSettings(s); err != nil {
				return err
			}
		}
		// Local harnesses are limited by localSettings above. A cloud setup runs
		// whatever its AgentRunProfile names, so it accepts the cluster's
		// harness kinds (the AgentRun backend.kind enum).
		switch s.Harness {
		case "claudeCode", "codex", "openCode", "hermesAgent", "openClaw", "grokBuild", "piAgent", "primeAgent", "agy", "custom":
		default:
			return fmt.Errorf("unsupported harness for setup %s", s.ID)
		}
		switch s.Location {
		case "local":
			if s.Namespace != "" || s.Profile != "" {
				return fmt.Errorf("local setup %s has cloud fields", s.ID)
			}
		case "cloud":
			if s.Harness == "claudeCode" {
				return fmt.Errorf("Claude Code is local only")
			}
			if c.Cloud.BaseURL == "" || s.Namespace == "" || s.Profile == "" {
				return fmt.Errorf("cloud setup %s needs baseURL, namespace and profile", s.ID)
			}
			found := false
			for _, ns := range c.Cloud.Namespaces {
				if ns == s.Namespace {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("setup %s namespace is not configured for discovery", s.ID)
			}
			key := s.Namespace + "/" + s.Profile
			if profiles[key] {
				return fmt.Errorf("ambiguous cloud profile mapping: %s", key)
			}
			profiles[key] = true
		default:
			return fmt.Errorf("setup %s location must be local or cloud", s.ID)
		}
	}
	return nil
}

func canonicalDir(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("workdir is required")
	}
	abs, err := expandPath(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workdir must be a directory")
	}
	return real, nil
}

func expandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	} else if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("only ~ or ~/ paths are supported")
	}
	return filepath.Abs(path)
}

func allowedWorkdir(path string, roots []string) (string, error) {
	real, err := canonicalDir(path)
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		r, err := canonicalDir(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(r, real)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return real, nil
		}
	}
	return "", fmt.Errorf("workdir must exist under a configured local.workdirRoots directory")
}
