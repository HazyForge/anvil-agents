package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hazyforge/anvil-agents/internal/onechat"
	"github.com/hazyforge/anvil-agents/internal/onechat/host"
	"sigs.k8s.io/yaml"
)

type exitStatus struct{ code int }

func (e *exitStatus) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func cliPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	} else if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("path %q must use ~ or ~/", path)
	}
	return filepath.Abs(path)
}

func cliConfigPath(path string) (string, error) {
	if path == "" {
		path = "~/.config/onechat/onechat.yaml"
	}
	return cliPath(path)
}

func missingConfig(path string) error {
	return fmt.Errorf("configuration %s is missing; copy examples/onechat/onechat.prod.yaml to that path and edit it, or pass --config FILE", path)
}

func loadCLIConfig(path string) (onechat.Config, error) {
	config, err := onechat.LoadConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			return config, missingConfig(path)
		}
	}
	return config, err
}

type daemonPaths struct {
	config        string
	state         string
	unit          string
	configured    bool
	configMessage string
}

// Inspecting and stopping a daemon must work even if a configured workdir was
// removed. These commands only need stateDir, not a usable execution config.
func resolveDaemonPaths(config, override string) (daemonPaths, error) {
	p := daemonPaths{config: config}
	unit, err := cliPath("~/.config/systemd/user/onechat.service")
	if err != nil {
		return p, err
	}
	p.unit = unit
	raw, err := os.ReadFile(config)
	var c onechat.Config
	switch {
	case errors.Is(err, os.ErrNotExist):
		p.configMessage = missingConfig(config).Error()
	case err != nil:
		p.configMessage = fmt.Sprintf("read configuration %s: %v", config, err)
	default:
		if err := yaml.UnmarshalStrict(raw, &c); err != nil {
			p.configMessage = fmt.Sprintf("read configuration %s: %v", config, err)
		} else if c.StateDir == "" {
			p.configMessage = "configuration " + config + " needs stateDir"
		} else {
			p.configured = true
		}
	}
	state := override
	if state == "" {
		state = c.StateDir
	}
	if state == "" {
		state = "~/.config/onechat"
	}
	p.state, err = cliPath(state)
	return p, err
}

func (p daemonPaths) systemd() bool {
	info, err := os.Stat(p.unit)
	return err == nil && info.Mode().IsRegular()
}

func runDaemon(ctx context.Context, configPath string, args []string) (resultErr error) {
	if len(args) == 0 {
		return fmt.Errorf("usage: onechat daemon run|start|stop|status|logs|bootstrap|proxy")
	}
	op := args[0]
	if op == "proxy" {
		defer func() {
			if resultErr != nil {
				resultErr = errors.New(strings.Join(strings.Fields(resultErr.Error()), " "))
			}
		}()
	}
	flags := flag.NewFlagSet("onechat daemon "+op, flag.ContinueOnError)
	if op == "proxy" {
		flags.SetOutput(io.Discard)
	}
	var stateDir, listen string
	var jsonOutput, follow bool
	switch op {
	case "run":
		flags.StringVar(&listen, "listen", host.DefaultListen, "loopback IP and port")
		flags.StringVar(&stateDir, "state-dir", "", "override configuration state directory")
	case "start", "status":
		flags.BoolVar(&jsonOutput, "json", false, "print daemon status as JSON")
		flags.StringVar(&stateDir, "state-dir", "", "override configuration state directory")
	case "stop", "proxy":
		flags.StringVar(&stateDir, "state-dir", "", "override configuration state directory")
	case "logs":
		flags.StringVar(&stateDir, "state-dir", "", "override configuration state directory")
		flags.BoolVar(&follow, "f", false, "follow new log output")
	case "bootstrap":
	default:
		return fmt.Errorf("unknown daemon command %q", op)
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("daemon %s takes flags only", op)
	}
	paths, err := resolveDaemonPaths(configPath, stateDir)
	if err != nil {
		return err
	}
	if !paths.configured && stateDir == "" && (op == "stop" || op == "logs" || op == "proxy") {
		return errors.New(paths.configMessage)
	}
	switch op {
	case "run":
		useLoginPath()
		config, err := loadCLIConfig(configPath)
		if err != nil {
			return err
		}
		config.StateDir = paths.state
		server, err := host.Start(ctx, host.Options{StateDir: paths.state, Listen: listen, Version: buildVersion(), Logger: log.New(os.Stderr, "onechat: ", log.LstdFlags), OpenManager: func(ctx context.Context) (*onechat.Manager, error) { return onechat.Open(ctx, config) }})
		if err != nil {
			return err
		}
		info := server.Info()
		fmt.Println("Listen:", info.Listen)
		fmt.Println("Host:", info.HostID)
		fmt.Println("Socket:", filepath.Join(paths.state, "onechat.sock"))
		fmt.Println("Discovery:", filepath.Join(paths.state, "host.json"))
		fmt.Println("Token file:", filepath.Join(paths.state, "host-token"))
		return server.Wait()
	case "status":
		status := inspectDaemon(ctx, paths)
		if err := printDaemonStatus(status, jsonOutput); err != nil {
			return err
		}
		if !status.Running {
			return &exitStatus{3}
		}
		return nil
	case "start":
		status, err := startDaemon(ctx, paths, stateDir != "")
		if jsonOutput || err == nil {
			if outputErr := printDaemonStatus(status, jsonOutput); outputErr != nil {
				return outputErr
			}
		}
		return err
	case "stop":
		return stopDaemon(ctx, paths)
	case "logs":
		return daemonLogs(ctx, paths, follow)
	case "bootstrap":
		return bootstrapDaemon(ctx, paths)
	case "proxy":
		return proxyDaemon(ctx, filepath.Join(paths.state, "onechat.sock"))
	}
	return nil
}

type daemonStatus struct {
	Running    bool      `json:"running"`
	Configured bool      `json:"configured"`
	PID        int       `json:"pid"`
	Socket     string    `json:"socket"`
	Listen     string    `json:"listen"`
	HostID     string    `json:"hostId"`
	Version    string    `json:"version"`
	StartedAt  time.Time `json:"startedAt"`
	ManagedBy  string    `json:"managedBy"`
	StateDir   string    `json:"stateDir"`
	Config     string    `json:"config"`
	Message    string    `json:"message"`
}

func readDaemonMetadata(dir string) (host.Metadata, error) {
	var info host.Metadata
	file, err := os.Open(filepath.Join(dir, "host.json"))
	if err != nil {
		return info, err
	}
	defer file.Close()
	if err := json.NewDecoder(file).Decode(&info); err != nil {
		return info, fmt.Errorf("read host.json: %w", err)
	}
	return info, nil
}

func inspectDaemon(ctx context.Context, p daemonPaths) daemonStatus {
	status := daemonStatus{Configured: p.configured, Socket: filepath.Join(p.state, "onechat.sock"), StateDir: p.state, Config: p.config, ManagedBy: "none"}
	if p.systemd() {
		status.ManagedBy = "systemd"
	}
	info, err := readDaemonMetadata(p.state)
	if err != nil {
		status.Message = "onechat is not running; run onechat daemon start"
		if !errors.Is(err, os.ErrNotExist) {
			status.Message = err.Error() + "; restore host.json or run onechat daemon start"
		}
	} else {
		status.PID, status.Listen, status.HostID, status.Version, status.StartedAt = info.PID, info.Listen, info.HostID, info.Version, info.StartedAt
		switch {
		case info.StoppedAt != nil:
			status.Message = "onechat is stopped; run onechat daemon start"
		case !daemonProcessAlive(info.PID):
			status.Message = "onechat's recorded process is not running; run onechat daemon start"
		default:
			status.ManagedBy = "process"
			if daemonInService(info.PID) {
				status.ManagedBy = "systemd"
			}
			if err := probeDaemon(ctx, status.Socket, info.HostID); err != nil {
				status.Message = "onechat control socket is unavailable: " + err.Error() + "; check onechat daemon logs and restart it"
			} else {
				status.Running = true
			}
		}
	}
	if !p.configured {
		status.Message = p.configMessage
	}
	return status
}

func printDaemonStatus(status daemonStatus, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(status)
	}
	if status.Running {
		fmt.Printf("onechat running (pid %d, %s)\nSocket: %s\nHost: %s\n", status.PID, status.ManagedBy, status.Socket, status.HostID)
	} else {
		fmt.Println(status.Message)
	}
	return nil
}
