package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hazyforge/anvil-agents/internal/onechat"
)

func runSessionCommand(ctx context.Context, configPath, op string, args []string) error {
	flags := flag.NewFlagSet("onechat "+op, flag.ContinueOnError)
	stateDir := flags.String("state-dir", "", "daemon state directory override")
	var target, text string
	if op != "agents" {
		flags.StringVar(&target, "session", "", "session ID or exact title")
	}
	if op == "queue" {
		flags.StringVar(&text, "message", "", "message to queue for the session")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%s takes flags only", op)
	}
	if op != "agents" && strings.TrimSpace(target) == "" {
		return fmt.Errorf("%s requires --session ID or an exact title", op)
	}
	if op == "queue" && (strings.TrimSpace(text) == "" || len(text) > 64<<10) {
		return fmt.Errorf("queue requires --message with 1–65536 bytes of text")
	}
	paths, err := resolveDaemonPaths(configPath, *stateDir)
	if err != nil {
		return err
	}
	if !paths.configured && *stateDir == "" {
		return errors.New(paths.configMessage)
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	client, err := openDaemonRPC(ctx, filepath.Join(paths.state, "onechat.sock"), "onechat "+op)
	if err != nil {
		return err
	}
	defer client.Close()
	sessions, err := daemonSessions(client)
	if err != nil {
		return err
	}
	if op == "agents" {
		return printAgents(client, sessions)
	}
	id, err := findDaemonSession(sessions, target)
	if err != nil {
		return err
	}
	body := map[string]string{"sessionId": id}
	if op == "queue" {
		body["text"] = text
	}
	raw, err := client.call("session."+op, body)
	if err != nil {
		return err
	}
	if op == "stop" {
		fmt.Println("Stop requested for", id)
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(raw)
}

func daemonSessions(client *daemonRPC) ([]onechat.Session, error) {
	raw, err := client.call("sessions.list", map[string]bool{"refresh": false})
	if err != nil {
		return nil, err
	}
	var body struct {
		Sessions []onechat.Session `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("onechat returned invalid sessions")
	}
	return body.Sessions, nil
}

func findDaemonSession(sessions []onechat.Session, target string) (string, error) {
	for _, session := range sessions {
		if session.ID == target {
			return session.ID, nil
		}
	}
	id := ""
	for _, session := range sessions {
		if session.Title != target {
			continue
		}
		if id != "" {
			return "", fmt.Errorf("more than one session is titled %q; use its ID from onechat agents", target)
		}
		id = session.ID
	}
	if id == "" {
		return "", fmt.Errorf("session %q was not found; use an ID or exact title from onechat agents", target)
	}
	return id, nil
}

func printAgents(client *daemonRPC, sessions []onechat.Session) error {
	raw, err := client.call("session.list", map[string]any{})
	if err != nil {
		return err
	}
	type agent struct {
		SessionID string    `json:"sessionId"`
		Title     string    `json:"title"`
		Location  string    `json:"location"`
		Harness   string    `json:"harness"`
		State     string    `json:"state"`
		Since     time.Time `json:"since"`
		Activity  string    `json:"activity"`
		Queued    int       `json:"queued"`
	}
	var body struct {
		Sessions []agent `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return fmt.Errorf("onechat returned invalid live session state")
	}
	for i := range body.Sessions {
		for _, session := range sessions {
			if session.ID == body.Sessions[i].SessionID {
				body.Sessions[i].Title = session.Title
				body.Sessions[i].Location = session.Location
				body.Sessions[i].Harness = session.Harness
				break
			}
		}
	}
	if body.Sessions == nil {
		body.Sessions = []agent{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(body.Sessions)
}
