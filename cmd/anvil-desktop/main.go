package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/pflag"

	"github.com/hazyforge/anvil-agents/internal/desktop"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:]))
}

func run(ctx context.Context, args []string) int {
	flags := pflag.NewFlagSet("anvil-desktop", pflag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:1738", "Loopback address for Anvil Agents Desktop.")
	uiDir := flags.String("ui-dir", "", "Directory of built web/desktop assets. Defaults to the embedded stub.")
	apiOrigin := flags.String("api-origin", "", "anvil-agents OIDC API origin (http or https). Not a kube-apiserver.")
	configDir := flags.String("config-dir", "", "Directory for desktop prefs. Defaults to the user config dir.")
	pathDirs := flags.String("path", "", "PATH-formatted directories to search for harness CLIs. When set, only these directories are searched.")
	oidcClientID := flags.String("oidc-client-id", "", "Override ui-config oidc.clientId (Kind desktop client). Empty keeps the API value.")
	oidcRedirectPath := flags.String("oidc-redirect-path", "", "Override OIDC redirect path (default /auth/callback). Kind desktop client uses /callback.")
	openWindow := flags.Bool("open", false, "Open a Chrome --app window on the loopback UI.")
	snapshotOnly := flags.Bool("snapshot", false, "Print the local harness and API snapshot as JSON and exit.")
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: anvil-desktop [--listen 127.0.0.1:1738] [--ui-dir PATH] [--api-origin URL] [--config-dir PATH] [--path PATH] [--oidc-client-id ID] [--oidc-redirect-path /callback] [--open]")
		fmt.Fprintln(os.Stderr, "       anvil-desktop --snapshot")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if err == pflag.ErrHelp {
			return 0
		}
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 2
	}

	opts := desktop.Options{
		Listen:           *listen,
		UIDir:            *uiDir,
		APIOrigin:        *apiOrigin,
		ConfigDir:        *configDir,
		OIDCClientID:     *oidcClientID,
		OIDCRedirectPath: *oidcRedirectPath,
		Discoverer: desktop.Discoverer{
			Path: *pathDirs,
		},
		OnListen: func(addr string) {
			fmt.Fprintf(os.Stderr, "anvil-desktop listening on http://%s\n", addr)
			if *openWindow {
				if err := desktop.OpenWindow("http://" + addr); err != nil {
					fmt.Fprintf(os.Stderr, "warning: open window: %s\n", err)
				}
			}
		},
	}

	server, err := desktop.NewServer(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}

	if *snapshotOnly {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(server.Snapshot(ctx)); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			return 1
		}
		return 0
	}

	if err := server.Start(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}
	return 0
}
