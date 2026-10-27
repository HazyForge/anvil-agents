package scenarios

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hazyforge/anvil-agents/internal/onechat"
	"sigs.k8s.io/yaml"
)

func LoadSuites(dir string) ([]Suite, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	suites := []Suite{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yaml" && filepath.Ext(entry.Name()) != ".yml") {
			continue
		}
		suite, err := LoadSuite(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if seen[suite.Name] {
			return nil, fmt.Errorf("duplicate suite name %q", suite.Name)
		}
		seen[suite.Name] = true
		suites = append(suites, suite)
	}
	if len(suites) == 0 {
		return nil, fmt.Errorf("no decide suites found in %s", dir)
	}
	return suites, nil
}

func LoadSuite(path string) (Suite, error) {
	var document struct {
		Name  string          `json:"suite"`
		State string          `json:"state"`
		World json.RawMessage `json:"world"`
		Seed  Seed            `json:"seed"`
		Cases []Case          `json:"cases"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, err
	}
	if err := yaml.UnmarshalStrict(data, &document); err != nil {
		return Suite{}, fmt.Errorf("%s: %w", path, err)
	}
	world, err := loadWorld(path, document.World)
	if err != nil {
		return Suite{}, fmt.Errorf("%s: %w", path, err)
	}
	suite := Suite{Name: document.Name, State: document.State, World: world, Seed: document.Seed, Cases: document.Cases}
	if err := ValidateSuite(suite); err != nil {
		return Suite{}, fmt.Errorf("%s: %w", path, err)
	}
	return suite, nil
}

func loadWorld(suitePath string, raw json.RawMessage) (World, error) {
	var world World
	var name string
	worldPath := suitePath
	if err := json.Unmarshal(raw, &name); err == nil && name != "" {
		if name != filepath.Base(name) || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
			return world, fmt.Errorf("world must name a file under scenarios/worlds")
		}
		worldPath = filepath.Join(filepath.Dir(filepath.Dir(suitePath)), "worlds", name+".yaml")
		data, err := os.ReadFile(worldPath)
		if err != nil {
			return world, err
		}
		if err := yaml.UnmarshalStrict(data, &world); err != nil {
			return world, fmt.Errorf("%s: %w", worldPath, err)
		}
		if world.Name != "" && world.Name != name {
			return world, fmt.Errorf("world %s declares a different name %q", name, world.Name)
		}
		world.Name = name
	} else {
		if len(raw) == 0 || raw[0] != '{' {
			return world, fmt.Errorf("world must be a name or inline mapping")
		}
		if err := yaml.UnmarshalStrict(raw, &world); err != nil {
			return world, fmt.Errorf("invalid inline world: %w", err)
		}
	}
	config := onechat.Config{StateDir: ".", Cloud: world.Backend, Setups: world.Setups}
	if world.Config != "" {
		configPath := world.Config
		if !filepath.IsAbs(configPath) {
			configPath = filepath.Join(filepath.Dir(worldPath), configPath)
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			return world, err
		}
		if err := yaml.UnmarshalStrict(data, &config); err != nil {
			return world, fmt.Errorf("world config %s: %w", configPath, err)
		}
		if world.Setups != nil {
			config.Setups = world.Setups
		}
	}
	world.ConfigData = config
	return world, nil
}

func ValidateSuite(suite Suite) error {
	if strings.TrimSpace(suite.Name) == "" || strings.ContainsAny(suite.Name, `/\\`) {
		return fmt.Errorf("suite needs a name without path separators")
	}
	if err := knownTag("state", suite.State, States); err != nil {
		return err
	}
	// Decide scenarios replace these paths before constructing a manager. Do
	// not require the author's project folders to exist on this machine.
	config := suite.World.ConfigData
	config.StateDir = "."
	config.Local.WorkdirRoots = nil
	config.Setups = append([]onechat.Setup{}, config.Setups...)
	if err := config.Validate(); err != nil {
		return fmt.Errorf("world config: %w", err)
	}
	setups := map[string]bool{}
	for _, setup := range config.Setups {
		setups[setup.ID] = true
	}
	if len(suite.Cases) == 0 {
		return fmt.Errorf("suite needs at least one case")
	}
	names := map[string]bool{}
	for _, c := range suite.Cases {
		if c.Name == "" || strings.ContainsAny(c.Name, `/\\`) || names[c.Name] {
			return fmt.Errorf("case names must be nonempty, unique and without path separators: %q", c.Name)
		}
		names[c.Name] = true
		if err := validateCase(c, suite, setups); err != nil {
			return fmt.Errorf("case %s: %w", c.Name, err)
		}
	}
	return nil
}

func validateCase(c Case, suite Suite, setups map[string]bool) error {
	if c.State != "" {
		if err := knownTag("state", c.State, States); err != nil {
			return err
		}
	}
	for _, move := range c.Moves {
		if err := knownTag("move", move, Moves); err != nil {
			return err
		}
	}
	for _, voice := range c.Voice {
		if err := knownTag("voice", voice, Voices); err != nil {
			return err
		}
	}
	if c.From != "" {
		if err := knownTag("client kind", c.From, []string{"desktop", "ios", "cli", "test"}); err != nil {
			return err
		}
	}
	if len(c.Steps) > 0 {
		if c.Say != "" || c.Expect != nil {
			return fmt.Errorf("use either say and expect, or steps")
		}
	} else if strings.TrimSpace(c.Say) == "" || c.Expect == nil {
		return fmt.Errorf("case needs say and expect, or steps")
	}
	labels := map[string]bool{}
	for _, seed := range []Seed{suite.World.Seed, suite.Seed, c.Seed} {
		if err := validateSeed(seed, labels, setups); err != nil {
			return err
		}
	}
	for i, step := range CaseSteps(c) {
		if strings.TrimSpace(step.Say) == "" || step.Expect == nil {
			return fmt.Errorf("step %d needs say and expect", i+1)
		}
		if err := validateExpectation(*step.Expect, labels, setups, c.Gap != ""); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		if step.After != nil {
			if err := validateSeed(Seed{Sessions: step.After.Sessions}, labels, setups); err != nil {
				return fmt.Errorf("step %d after: %w", i+1, err)
			}
		}
	}
	return nil
}

func validateSeed(seed Seed, labels, setups map[string]bool) error {
	if err := validateMessages(append(append([]MessageSeed{}, seed.History...), seed.HistoryAppend...)); err != nil {
		return err
	}
	for _, session := range seed.Sessions {
		if strings.TrimSpace(session.Label) == "" {
			return fmt.Errorf("session label is required")
		}
		labels[session.Label] = true
		if session.Setup != nil && *session.Setup != "" && !setups[*session.Setup] {
			return fmt.Errorf("session %s has unknown setup %q", session.Label, *session.Setup)
		}
		if session.Location != nil && *session.Location != "local" && *session.Location != "cloud" {
			return fmt.Errorf("session %s location must be local or cloud", session.Label)
		}
		if session.UpdatedAgo != nil {
			if session.UpdatedAt != nil {
				return fmt.Errorf("session %s cannot set both updatedAgo and updatedAt", session.Label)
			}
			if _, err := ParseAgo(*session.UpdatedAgo); err != nil {
				return fmt.Errorf("session %s updatedAgo: %w", session.Label, err)
			}
		}
		if err := validateMessages(append(append([]MessageSeed{}, session.Transcript...), session.TranscriptAppend...)); err != nil {
			return fmt.Errorf("session %s: %w", session.Label, err)
		}
	}
	return nil
}

func validateMessages(messages []MessageSeed) error {
	for _, message := range messages {
		if message.Ago != "" {
			if message.At != nil {
				return fmt.Errorf("message cannot set both ago and at")
			}
			if _, err := ParseAgo(message.Ago); err != nil {
				return fmt.Errorf("message ago: %w", err)
			}
		}
	}
	return nil
}

func validateExpectation(expect Expectation, labels, setups map[string]bool, gap bool) error {
	if expect.Intent != "" {
		if err := knownTag("intent", expect.Intent, Intents); err != nil {
			return err
		}
	}
	for _, action := range append(append([]string{}, expect.Actions...), expect.Not...) {
		if err := validateAction(action, gap); err != nil {
			return err
		}
	}
	for _, label := range expect.NotSessions {
		if !labels[label] {
			return fmt.Errorf("unknown session label %q", label)
		}
	}
	for _, setup := range expect.NotSetups {
		if !setups[setup] {
			return fmt.Errorf("unknown setup %q", setup)
		}
	}
	for _, matcher := range expect.Does {
		if err := validateMatcher(matcher, labels, setups, gap); err != nil {
			return err
		}
	}
	return nil
}

func validateMatcher(matcher Matcher, labels, setups map[string]bool, gap bool) error {
	if matcher.Action != "" {
		if err := validateAction(matcher.Action, gap); err != nil {
			return err
		}
	}
	if matcher.Session != "" && !labels[matcher.Session] {
		return fmt.Errorf("unknown session label %q", matcher.Session)
	}
	if matcher.Setup != "" && !setups[matcher.Setup] {
		return fmt.Errorf("unknown setup %q", matcher.Setup)
	}
	for _, alternative := range matcher.AnyOf {
		if err := validateMatcher(alternative, labels, setups, gap); err != nil {
			return err
		}
	}
	return nil
}

func validateAction(action string, gap bool) error {
	if err := knownTag("action", action, ActionTypes); err != nil {
		return err
	}
	if action == "stop" && !gap {
		return fmt.Errorf("stop is a future action and requires a case gap")
	}
	return nil
}

func knownTag(kind, tag string, values []string) error {
	if !slices.Contains(values, tag) {
		return fmt.Errorf("unknown %s %q", kind, tag)
	}
	return nil
}

// ParseAgo adds whole or fractional days to Go's duration units.
func ParseAgo(value string) (time.Duration, error) {
	var duration time.Duration
	var err error
	if strings.HasSuffix(value, "d") {
		days, parseErr := strconv.ParseFloat(strings.TrimSuffix(value, "d"), 64)
		nanos := days * float64(24*time.Hour)
		if parseErr != nil || math.IsNaN(nanos) || math.IsInf(nanos, 0) || nanos < 0 || nanos >= float64(math.MaxInt64) {
			return 0, fmt.Errorf("invalid nonnegative duration %q", value)
		}
		duration = time.Duration(nanos)
	} else {
		duration, err = time.ParseDuration(value)
	}
	if err != nil || duration < 0 {
		return 0, fmt.Errorf("invalid nonnegative duration %q", value)
	}
	return duration, nil
}
