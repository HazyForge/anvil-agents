// Command jev-probe classifies one chat message with the Jev intent router
// (internal/jev) and prints the routing decision as JSON.
//
// Live calls need TYPESAFE_API_KEY in the environment and perform exactly
// one POST to the System One endpoint. Without a key — or with -fake — the
// probe routes through the deterministic keyword fake (no network, no model
// judgment) so CI and keyless exploration stay honest. The probe never
// generates chat text and never touches the cluster; it only decides.
//
// Usage:
//
//	go run ./cmd/jev-probe -message "please create a helper agent"
//	go run ./cmd/jev-probe -fake -message "run the deploy tool"
//	TYPESAFE_API_KEY=... go run ./cmd/jev-probe -message "hello" -recent "prior turn"
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hazyforge/anvil-agents/internal/jev"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "jev-probe: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	message := flag.String("message", "", "User message to classify (required).")
	threshold := flag.Float64("threshold", jev.DefaultConfidenceThreshold, "Confidence review floor; below it the decision gates to unclear.")
	model := flag.String("model", jev.DefaultModel, "Jev model alias or versioned ID.")
	endpoint := flag.String("endpoint", jev.DefaultEndpoint, "System One endpoint (tests only).")
	fake := flag.Bool("fake", false, "Route through the deterministic keyword fake (no network).")
	timeout := flag.Duration("timeout", 30*time.Second, "Live request timeout.")
	var recent recentFlags
	flag.Var(&recent, "recent", "Prior thread message for disambiguation; repeatable, oldest first.")
	flag.Parse()

	if strings.TrimSpace(*message) == "" {
		return fmt.Errorf("a -message is required")
	}
	if *threshold < 0 || *threshold > 1 {
		return fmt.Errorf("threshold must sit in [0,1]")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	var backend jev.Backend
	var mode string
	switch {
	case *fake:
		backend = jev.KeywordFakeBackend()
		mode = "fake-keyword (no network)"
	default:
		client, ok := jev.ClientFromEnv()
		if !ok {
			backend = jev.KeywordFakeBackend()
			mode = "fake-keyword (no network; set TYPESAFE_API_KEY for a live call)"
		} else {
			if strings.TrimSpace(*endpoint) != "" {
				client.Endpoint = strings.TrimSpace(*endpoint)
			}
			backend = client
			mode = "live systemone"
		}
	}

	router := &jev.Router{Backend: backend, Model: strings.TrimSpace(*model), Threshold: *threshold}
	decision, err := router.ClassifyIntent(ctx, jev.MessageContext{Message: *message, Recent: recent})
	if err != nil {
		return err
	}
	out := map[string]any{
		"mode":          mode,
		"model":         *model,
		"intent":        decision.Intent,
		"rawChoice":     decision.RawChoice,
		"confidence":    decision.Confidence,
		"probabilities": decision.Probabilities,
		"unclear":       decision.Unclear,
		"servingModel":  decision.Model,
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode decision: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}

type recentFlags []string

func (flags *recentFlags) String() string { return strings.Join(*flags, "; ") }

// Set implements flag.Value.
func (flags *recentFlags) Set(value string) error {
	*flags = append(*flags, value)
	return nil
}
