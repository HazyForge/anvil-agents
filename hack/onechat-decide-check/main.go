// Command onechat-decide-check asks the real one chat manager to decide on a
// few messages against a seeded world, runs the real validation, and prints
// what one chat would do. Nothing is executed: no session, no harness.
//
//	go run ./hack/onechat-decide-check --world hack/onechat-decide-check/workbench.yaml \
//	  --config examples/onechat/onechat.scenarios.yaml --messages hack/onechat-decide-check/multi-subject.txt
package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/onechat"
	"sigs.k8s.io/yaml"
)

type worldMessage struct {
	Role string `json:"role"`
	Ago  string `json:"ago"`
	Text string `json:"text"`
}

type worldSession struct {
	Label      string         `json:"label"`
	Location   string         `json:"location"`
	Setup      string         `json:"setup"`
	Title      string         `json:"title"`
	Summary    string         `json:"summary"`
	State      string         `json:"state"`
	Workdir    string         `json:"workdir"`
	UpdatedAgo string         `json:"updatedAgo"`
	Transcript []worldMessage `json:"transcript"`
}

type world struct {
	World    string         `json:"world"`
	Config   string         `json:"config"`
	Sessions []worldSession `json:"sessions"`
	History  []worldMessage `json:"history"`
}

func ago(now time.Time, value string) time.Time {
	if value == "" {
		return now
	}
	if strings.HasSuffix(value, "d") {
		var days int
		fmt.Sscanf(value, "%dd", &days)
		return now.Add(-time.Duration(days) * 24 * time.Hour)
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return now
	}
	return now.Add(-d)
}

func main() {
	worldPath := flag.String("world", "hack/onechat-decide-check/workbench.yaml", "seeded world")
	configPath := flag.String("config", "examples/onechat/onechat.scenarios.yaml", "config whose setups the manager sees")
	messagesPath := flag.String("messages", "", "file with one message per block, blocks separated by a line with ---")
	model := flag.String("model", "sonnet", "manager model")
	out := flag.String("out", "", "write every decision as JSON here")
	flag.Parse()

	raw, err := os.ReadFile(*worldPath)
	check(err)
	var w world
	check(yaml.UnmarshalStrict(raw, &w))
	config, err := onechat.LoadConfig(*configPath)
	check(err)
	setups := map[string]onechat.Setup{}
	for _, s := range config.Setups {
		setups[s.ID] = s
	}

	// Local workdirs must exist under a work root for create to validate, as in production.
	home, err := os.MkdirTemp("", "onechat-decide-check-")
	check(err)
	defer os.RemoveAll(home)
	root := filepath.Join(home, "code")
	check(os.MkdirAll(root, 0o755))
	mapDir := func(dir string) string {
		if strings.HasPrefix(dir, "~/code/") {
			return filepath.Join(root, strings.TrimPrefix(dir, "~/code/"))
		}
		return dir
	}

	now := time.Now().UTC()
	byID := map[string]string{}
	var inventory []onechat.Session
	for _, s := range w.Sessions {
		id := uuid.NewHash(sha1.New(), uuid.NameSpaceOID, []byte(w.World+"/"+s.Label), 5).String()
		byID[id] = s.Title
		setup := setups[s.Setup]
		session := onechat.Session{ID: id, Location: s.Location, SetupID: s.Setup, Harness: setup.Harness, Title: s.Title, Summary: s.Summary, State: s.State,
			CreatedAt: ago(now, s.UpdatedAgo), UpdatedAt: ago(now, s.UpdatedAgo), Namespace: setup.Namespace, Profile: setup.Profile}
		if s.Location == "local" {
			session.Workdir = mapDir(s.Workdir)
			check(os.MkdirAll(session.Workdir, 0o755))
		}
		for _, m := range s.Transcript {
			text := m.Text
			if len(text) > 600 {
				text = text[:600]
			}
			session.Transcript = append(session.Transcript, onechat.Message{Role: m.Role, Text: text, At: ago(now, m.Ago)})
		}
		if len(session.Transcript) > 6 {
			session.Transcript = session.Transcript[len(session.Transcript)-6:]
		}
		inventory = append(inventory, session)
	}
	var history []onechat.Message
	for _, m := range w.History {
		history = append(history, onechat.Message{Role: m.Role, Text: m.Text, At: ago(now, m.Ago)})
	}
	if len(history) > 10 {
		history = history[len(history)-10:]
	}

	messages := readMessages(*messagesPath)
	decider := &onechat.ClaudeDecider{Model: *model, Timeout: 4 * time.Minute}
	type result struct {
		Message    string           `json:"message"`
		Decision   onechat.Decision `json:"decision"`
		Validation []string         `json:"validationErrors"`
		Error      string           `json:"error,omitempty"`
		CostUSD    *float64         `json:"costUsd,omitempty"`
	}
	results := make([]result, len(messages))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, message := range messages {
		wg.Add(1)
		go func(i int, message string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			input := onechat.DecideInput{Instructions: onechat.ManagerInstructions, Setups: config.Setups, Inventory: inventory, History: history, Message: message}
			decision, usage, err := decider.Decide(context.Background(), input)
			r := result{Message: message, Decision: decision, CostUSD: usage.CostUSD}
			if err != nil {
				r.Error = err.Error()
			}
			onechat.NormalizeDecision(&decision, config.Setups)
			r.Decision = decision
			r.Validation = onechat.ValidateDecision(decision, config.Setups, inventory, []string{root})
			results[i] = r
		}(i, message)
	}
	wg.Wait()

	total := 0.0
	for i, r := range results {
		fmt.Printf("\n[%d] %s\n", i+1, r.Message)
		if r.Error != "" {
			fmt.Printf("    decider error: %s\n", r.Error)
		}
		fmt.Printf("    why: %s\n", r.Decision.ReasoningSummary)
		for _, a := range r.Decision.Actions {
			target := byID[a.SessionID]
			if a.Type == "create" {
				target = fmt.Sprintf("new %q from %s", a.Title, a.SetupID)
			}
			body := a.Message
			if a.Type == "answer" || a.Type == "ask_user" {
				body = a.Text
			}
			fmt.Printf("    %-11s %-28s %s\n", a.Type, target, oneLine(body, 140))
		}
		if len(r.Validation) > 0 {
			fmt.Printf("    REFUSED by validation: %s\n    -> one chat would only ask: \"I could not safely route this turn...\"\n", strings.Join(r.Validation, "; "))
		} else {
			fmt.Printf("    valid: one chat does all of the above\n")
		}
		if r.CostUSD != nil {
			total += *r.CostUSD
		}
	}
	fmt.Printf("\nmanager cost: $%.2f\n", total)
	if *out != "" {
		data, _ := json.MarshalIndent(results, "", "  ")
		check(os.WriteFile(*out, data, 0o600))
	}
}

func readMessages(path string) []string {
	f, err := os.Open(path)
	check(err)
	defer f.Close()
	var messages []string
	var block []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			if text := strings.TrimSpace(strings.Join(block, "\n")); text != "" {
				messages = append(messages, text)
			}
			block = nil
			continue
		}
		block = append(block, line)
	}
	if text := strings.TrimSpace(strings.Join(block, "\n")); text != "" {
		messages = append(messages, text)
	}
	return messages
}

func oneLine(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > n {
		return text[:n] + "…"
	}
	return text
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
