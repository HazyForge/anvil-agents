// local-routing-bench replays manager routing without launching native harnesses.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/hazyforge/anvil-agents/internal/jev"
	"github.com/hazyforge/anvil-agents/internal/localrouting"
	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
)

func main() {
	mode := flag.String("mode", "fixture", "fixture (policy/bookkeeping only) or live (Jev provider)")
	selected := flag.String("scenarios", "", "comma-separated scenario IDs, default all")
	importPath := flag.String("import", "", "JSON array of labelled scenarios to replay in live mode")
	output := flag.String("out", "", "write JSON to a private file instead of stdout")
	model := flag.String("model", jev.DefaultModel, "live Jev model alias or pinned ID")
	flag.Parse()
	options := localrouting.BenchOptions{Mode: *mode}
	if *selected != "" {
		for _, id := range strings.Split(*selected, ",") {
			options.ScenarioIDs = append(options.ScenarioIDs, strings.TrimSpace(id))
		}
	}
	if *importPath != "" {
		file, err := os.Open(*importPath)
		if err != nil {
			fail("Cannot open scenario file")
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 256*1024+1))
		_ = file.Close()
		if readErr != nil || len(data) > 256*1024 {
			fail("Scenario file exceeds the 256 KiB limit or could not be read")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&options.Scenarios)
		if err == nil {
			var trailing any
			if decoder.Decode(&trailing) != io.EOF {
				err = fmt.Errorf("trailing JSON")
			}
		}
		if err != nil || len(options.Scenarios) == 0 {
			fail("Invalid scenario JSON")
		}
	}
	var provider subjectrouting.Provider
	if *mode == "live" {
		client, ok := jev.ClientFromEnv()
		if !ok {
			fail("Live mode requires TYPESAFE_API_KEY in the process environment")
		}
		provider = subjectrouting.JevProvider{Backend: client, Model: *model}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 5*time.Minute)
	defer timeout()
	result, err := localrouting.RunBench(ctx, provider, options)
	runErr := err
	if err != nil && len(result.Scenarios) == 0 {
		fail("Routing benchmark stopped: " + err.Error())
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fail("Cannot encode benchmark result")
	}
	data = append(data, '\n')
	if *output != "" {
		// Refuse to overwrite existing output so prior evidence remains intact.
		file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			fail("Cannot create output file (it may already exist)")
		}
		_, err = file.Write(data)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			fail("Cannot write benchmark result")
		}
	} else {
		if _, err := os.Stdout.Write(data); err != nil {
			fail("Cannot write benchmark result")
		}
	}
	if runErr != nil {
		fail("Routing benchmark interrupted; partial report saved with errorReason=" + result.ErrorReason)
	}
}

func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
