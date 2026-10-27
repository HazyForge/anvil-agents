package scenarios

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

func Coverage(w io.Writer, suites []Suite) error {
	grid := map[string]map[string]int{}
	intents, voices := map[string]int{}, map[string]int{}
	cases, untaggedIntents, untaggedVoices := 0, 0, 0
	for _, suite := range suites {
		for _, c := range suite.Cases {
			cases++
			state := c.State
			if state == "" {
				state = suite.State
			}
			if grid[state] == nil {
				grid[state] = map[string]int{}
			}
			for _, move := range uniqueTags(c.Moves) {
				grid[state][move]++
			}
			caseIntents := []string{}
			if c.Expect != nil && c.Expect.Intent != "" {
				caseIntents = append(caseIntents, c.Expect.Intent)
			}
			for _, step := range c.Steps {
				if step.Expect != nil && step.Expect.Intent != "" {
					caseIntents = append(caseIntents, step.Expect.Intent)
				}
			}
			for _, intent := range uniqueTags(caseIntents) {
				intents[intent]++
			}
			if len(caseIntents) == 0 {
				untaggedIntents++
			}
			for _, voice := range uniqueTags(c.Voice) {
				voices[voice]++
			}
			if len(c.Voice) == 0 {
				untaggedVoices++
			}
		}
	}
	var b strings.Builder
	table := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprint(table, "State")
	for _, move := range Moves {
		fmt.Fprintf(table, "\t%s", move)
	}
	fmt.Fprintln(table)
	for _, state := range States {
		fmt.Fprint(table, state)
		for _, move := range Moves {
			fmt.Fprintf(table, "\t%d", grid[state][move])
		}
		fmt.Fprintln(table)
	}
	if err := table.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(&b, "\nMoves without a case:")
	for _, state := range States {
		missing := []string{}
		for _, move := range Moves {
			if grid[state][move] == 0 {
				missing = append(missing, move)
			}
		}
		label := strings.Join(missing, ", ")
		if len(missing) == 0 {
			label = "none"
		}
		fmt.Fprintf(&b, "%s: %s\n", state, label)
	}
	fmt.Fprintln(&b, "\nIntents (cases; a case can have several):")
	for _, intent := range Intents {
		fmt.Fprintf(&b, "%s: %d\n", intent, intents[intent])
	}
	fmt.Fprintf(&b, "untagged: %d\n", untaggedIntents)
	fmt.Fprintln(&b, "\nVoices (cases; a case can have several):")
	for _, voice := range Voices {
		fmt.Fprintf(&b, "%s: %d\n", voice, voices[voice])
	}
	fmt.Fprintf(&b, "untagged: %d\n\nSuites: %d\nCases: %d\n", untaggedVoices, len(suites), cases)
	_, err := io.WriteString(w, b.String())
	return err
}

func uniqueTags(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
