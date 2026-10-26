package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hazyforge/anvil-agents/internal/onechat"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "onechat:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("onechat", flag.ContinueOnError)
	configPath := flags.String("config", "onechat.yaml", "configuration file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	args = flags.Args()
	if len(args) == 0 {
		return fmt.Errorf("usage: onechat --config FILE login | sessions | say TEXT | scenario run FILE --out DIR")
	}
	config, err := onechat.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if args[0] == "login" {
		if len(args) != 1 {
			return fmt.Errorf("login takes no arguments")
		}
		return onechat.Login(ctx, config, os.Stdout)
	}
	if args[0] == "scenario" {
		if len(args) < 3 || args[1] != "run" {
			return fmt.Errorf("usage: scenario run FILE --out DIR")
		}
		scenarioFlags := flag.NewFlagSet("scenario run", flag.ContinueOnError)
		out := scenarioFlags.String("out", "", "scenario output directory")
		if err := scenarioFlags.Parse(args[3:]); err != nil {
			return err
		}
		if *out == "" || scenarioFlags.NArg() != 0 {
			return fmt.Errorf("scenario run requires --out DIR")
		}
		scenario, err := onechat.LoadScenario(args[2])
		if err != nil {
			return err
		}
		report, err := onechat.RunScenario(ctx, config, scenario, *out, nil)
		if report.TracePath != "" {
			fmt.Println("Trace:", report.TracePath)
		}
		return err
	}
	if args[0] != "sessions" && args[0] != "say" {
		return fmt.Errorf("unknown command %q", args[0])
	}
	if args[0] == "sessions" && len(args) != 1 {
		return fmt.Errorf("sessions takes no arguments")
	}
	if args[0] == "say" && (len(args) < 2 || strings.TrimSpace(strings.Join(args[1:], " ")) == "") {
		return fmt.Errorf("say requires a message")
	}
	manager, err := onechat.Open(ctx, config)
	if err != nil {
		return err
	}
	if args[0] == "sessions" {
		inventory, err := manager.Inventory(ctx)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(inventory)
	}
	trace, err := manager.Turn(ctx, strings.Join(args[1:], " "))
	fmt.Println(trace.Answer)
	if trace.TracePath != "" {
		fmt.Println("Trace:", trace.TracePath)
	}
	return err
}
