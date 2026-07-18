package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/hazyforge/anvil-agents/internal/agentctl"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(agentctl.Main(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
