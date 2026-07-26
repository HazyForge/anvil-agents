package agentctl

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (app App) runSelf(args []string) error {
	if len(args) == 0 {
		writeSelfUsage(app.Err)
		return &usageError{message: "a self command is required"}
	}
	switch args[0] {
	case "report":
		return app.selfReport(args[1:])
	case "help":
		writeSelfUsage(app.Out)
		return nil
	default:
		writeSelfUsage(app.Err)
		return &usageError{message: fmt.Sprintf("unknown self command %q", args[0])}
	}
}

func writeSelfUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Self commands: report")
	fmt.Fprintln(writer, "  anvil-agentctl self report progress --stage STAGE --summary TEXT")
	fmt.Fprintln(writer, "  anvil-agentctl self report decision --summary TEXT [--action ACTION]")
	fmt.Fprintln(writer, "  anvil-agentctl self report needsHuman --summary TEXT")
}

func (app App) selfReport(args []string) error {
	reportType := "progress"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		reportType = args[0]
		args = args[1:]
	}

	var level, stage, classification, action, summary, detail, pullRequestURL, residualRisk, humanFollowUp string
	var needsHuman bool
	flags := newCommandFlags("self report", app.Err)
	flags.StringVar(&reportType, "type", reportType, "Report type: progress, decision, or needsHuman.")
	flags.StringVar(&level, "level", "info", "Report level.")
	flags.StringVar(&stage, "stage", "", "Optional stage marker.")
	flags.StringVar(&classification, "classification", "", "Optional classification.")
	flags.StringVar(&action, "action", "", "Optional action.")
	flags.StringVar(&summary, "summary", "", "Short summary.")
	flags.StringVar(&detail, "detail", "", "Optional detail.")
	flags.StringVar(&pullRequestURL, "pull-request-url", "", "Optional pull request URL.")
	flags.StringVar(&pullRequestURL, "pr-url", pullRequestURL, "Alias for --pull-request-url.")
	flags.StringVar(&residualRisk, "residual-risk", "", "Optional residual risk.")
	flags.StringVar(&humanFollowUp, "human-follow-up", "", "Optional human follow-up guidance.")
	flags.BoolVar(&needsHuman, "needs-human", false, "Mark the report as needing human follow-up.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// Positional leftovers become summary text for ergonomic agent calls.
	if flags.NArg() > 0 {
		extra := strings.Join(flags.Args(), " ")
		if summary == "" {
			summary = extra
		} else {
			summary = summary + " " + extra
		}
	}
	if reportType == "needs-human" {
		reportType = "needsHuman"
	}
	if reportType == "needsHuman" {
		needsHuman = true
	}
	switch reportType {
	case "progress", "decision", "needsHuman":
	default:
		return &usageError{message: fmt.Sprintf("unsupported report type %q", reportType)}
	}

	payload := map[string]any{
		"type":       reportType,
		"observedAt": time.Now().UTC().Format(time.RFC3339),
		"level":      level,
	}
	setIf := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			payload[key] = value
		}
	}
	setIf("stage", stage)
	setIf("classification", classification)
	setIf("action", action)
	setIf("summary", summary)
	setIf("detail", detail)
	setIf("pullRequestURL", pullRequestURL)
	setIf("residualRisk", residualRisk)
	setIf("humanFollowUp", humanFollowUp)
	if needsHuman {
		payload["needsHuman"] = true
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode status report: %w", err)
	}
	statusFile := firstNonEmpty(os.Getenv("ANVIL_AGENT_RUN_STATUS_FILE"), "/tmp/anvil-agent-run-status/status.jsonl")
	if err := os.MkdirAll(filepath.Dir(statusFile), 0o755); err != nil {
		return fmt.Errorf("create status directory: %w", err)
	}
	file, err := os.OpenFile(statusFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open status file: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%s\n", body); err != nil {
		file.Close()
		return fmt.Errorf("write status file: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}

	prefix := firstNonEmpty(os.Getenv("ANVIL_AGENT_RUN_STATUS_LOG_PREFIX"), "ANVIL_AGENT_RUN_STATUS_JSON=")
	logFD := firstNonEmpty(os.Getenv("ANVIL_AGENT_RUN_STATUS_LOG_FD"), "/proc/1/fd/1")
	if logFD != "" {
		if out, err := os.OpenFile(logFD, os.O_WRONLY|os.O_APPEND, 0); err == nil {
			fmt.Fprintf(out, "%s%s\n", prefix, body)
			out.Close()
		}
	}
	fmt.Fprintf(app.Out, "agent status recorded: %s\n", firstNonEmpty(summary, reportType))
	return nil
}
