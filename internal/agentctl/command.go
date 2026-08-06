package agentctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiValidation "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	"github.com/spf13/pflag"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/runapi"
)

const (
	maxPromptBytes   = 1024 * 1024
	agentRunLabel    = "control.anvil.hazyforge.io/agent-run"
	agentRunJobLabel = "control.anvil.hazyforge.io/agent-run-job"
	agentContainer   = "agent"
)

type BackendFactory func(KubeOptions) (Backend, error)

type App struct {
	In           io.Reader
	Out          io.Writer
	Err          io.Writer
	Factory      BackendFactory
	PollInterval time.Duration
}

type usageError struct {
	message string
}

func (err *usageError) Error() string { return err.message }

func Main(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	app := App{In: in, Out: out, Err: errOut, Factory: NewKubernetesBackend}
	if err := app.Run(ctx, args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return 0
		}
		var diagnosed *diagnosedUnhealthyError
		if errors.As(err, &diagnosed) {
			fmt.Fprintf(errOut, "error: %s\n", terminalSafe(err.Error()))
			return authExitDiagnosedUnhealthy
		}
		fmt.Fprintf(errOut, "error: %s\n", terminalSafe(err.Error()))
		var usage *usageError
		if errors.As(err, &usage) {
			return 2
		}
		return 1
	}
	return 0
}

func (app App) Run(ctx context.Context, args []string) error {
	if app.In == nil {
		app.In = strings.NewReader("")
	}
	if app.Out == nil {
		app.Out = io.Discard
	}
	if app.Err == nil {
		app.Err = io.Discard
	}
	if app.Factory == nil {
		app.Factory = NewKubernetesBackend
	}

	var options KubeOptions
	flags := pflag.NewFlagSet("anvil-agentctl", pflag.ContinueOnError)
	flags.SetOutput(app.Err)
	flags.SetInterspersed(false)
	flags.StringVar(&options.Kubeconfig, "kubeconfig", "", "Path to a kubeconfig file; defaults to the caller's normal loading rules.")
	flags.StringVar(&options.Context, "context", "", "Kubeconfig context override.")
	flags.Usage = func() { writeRootUsage(app.Err) }
	if err := flags.Parse(args); err != nil {
		return err
	}
	remaining := flags.Args()
	if len(remaining) == 0 {
		writeRootUsage(app.Err)
		return &usageError{message: "a command is required"}
	}
	switch remaining[0] {
	case "run":
		if len(remaining) < 2 {
			writeRunUsage(app.Err)
			return &usageError{message: "a run command is required"}
		}
		switch remaining[1] {
		case "create":
			return app.runCreate(ctx, options, remaining[2:])
		case "list":
			return app.runList(ctx, options, remaining[2:])
		case "get":
			return app.runGet(ctx, options, remaining[2:])
		case "logs":
			return app.runLogs(ctx, options, remaining[2:])
		case "debug":
			return app.runDebug(ctx, options, remaining[2:])
		case "help":
			writeRunUsage(app.Out)
			return nil
		default:
			writeRunUsage(app.Err)
			return &usageError{message: fmt.Sprintf("unknown run command %q", remaining[1])}
		}
	case "submit":
		return app.runSubmit(ctx, options, remaining[1:])
	case "profile":
		if len(remaining) < 2 {
			writeProfileUsage(app.Err)
			return &usageError{message: "a profile command is required"}
		}
		switch remaining[1] {
		case "list":
			return app.runProfileList(ctx, options, remaining[2:])
		case "help":
			writeProfileUsage(app.Out)
			return nil
		default:
			writeProfileUsage(app.Err)
			return &usageError{message: fmt.Sprintf("unknown profile command %q", remaining[1])}
		}
	case "auth":
		return app.runAuth(ctx, options, remaining[1:])
	case "volume":
		return app.runVolume(ctx, options, remaining[1:])
	case "self":
		return app.runSelf(remaining[1:])
	case "help":
		writeRootUsage(app.Out)
		return nil
	default:
		writeRootUsage(app.Err)
		return &usageError{message: fmt.Sprintf("unknown command %q", remaining[0])}
	}
}

func writeRootUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Usage: anvil-agentctl [--kubeconfig PATH] [--context NAME] COMMAND")
	fmt.Fprintln(writer, "")
	fmt.Fprintln(writer, "Commands: run, submit, profile, auth, volume, self")
	writeRunUsage(writer)
	writeSubmitUsage(writer)
	writeProfileUsage(writer)
	writeAuthUsage(writer)
	writeVolumeUsage(writer)
	writeSelfUsage(writer)
}

func writeRunUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Run commands: create, list, get, logs, debug")
}

func writeSubmitUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Submit a prompt or idea to any profile as a new AgentRun:")
	fmt.Fprintln(writer, "  anvil-agentctl submit PROMPT --profile NAME [flags]")
}

func writeProfileUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Profile commands: list")
}

type createOptions struct {
	namespace        string
	name             string
	generateName     string
	profile          string
	prompt           string
	promptFile       string
	purpose          string
	intent           string
	sourceAPIVersion string
	sourceKind       string
	sourceNamespace  string
	sourceName       string
	sourceUID        string
	sourceGeneration int64
	dryRun           string
	output           string
}

func (app App) runCreate(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	options := createOptions{purpose: string(agentsv1alpha1.AgentRunPurposeManual), sourceKind: "ManualRequest"}
	flags := newCommandFlags("run create", app.Err)
	flags.StringVarP(&options.namespace, "namespace", "n", "", "Namespace for the new AgentRun (required).")
	flags.StringVar(&options.name, "name", "", "Exact name for the new append-only AgentRun.")
	flags.StringVar(&options.generateName, "generate-name", "", "Server-generated name prefix for the new AgentRun.")
	flags.StringVar(&options.profile, "profile", "", "Same-namespace AgentRunProfile name (required).")
	flags.StringVar(&options.prompt, "prompt", "", "One-off run prompt.")
	flags.StringVar(&options.promptFile, "prompt-file", "", "Read the one-off prompt from a file, or - for stdin.")
	flags.StringVar(&options.purpose, "purpose", options.purpose, "Run purpose: manual, adverseSituation, or scheduledHealthCheck.")
	flags.StringVar(&options.intent, "intent", "", "Optional intent override: observe, fixTransient, proposeChange, or cleanup.")
	flags.StringVar(&options.sourceAPIVersion, "source-api-version", "", "Opaque source API version metadata.")
	flags.StringVar(&options.sourceKind, "source-kind", options.sourceKind, "Opaque source kind metadata.")
	flags.StringVar(&options.sourceNamespace, "source-namespace", "", "Opaque source namespace metadata.")
	flags.StringVar(&options.sourceName, "source-name", "", "Opaque source name metadata (required).")
	flags.StringVar(&options.sourceUID, "source-uid", "", "Opaque source UID metadata.")
	flags.Int64Var(&options.sourceGeneration, "source-generation", 0, "Opaque source generation metadata.")
	flags.StringVar(&options.dryRun, "dry-run", "", "Set to client to render without contacting Kubernetes.")
	flags.StringVarP(&options.output, "output", "o", "", "Output format: name, yaml, or json. Client dry-run defaults to yaml.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: "run create does not accept positional arguments"}
	}
	if options.output != "" && options.output != "name" && options.output != "yaml" && options.output != "json" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", options.output)}
	}
	if options.dryRun != "" && options.dryRun != "client" {
		return &usageError{message: "--dry-run supports only client"}
	}
	run, err := app.buildRun(options)
	if err != nil {
		return err
	}
	if options.dryRun == "client" {
		format := options.output
		if format == "" {
			format = "yaml"
		}
		return writeObject(app.Out, run, format)
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	if err := backend.CreateRun(ctx, run); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("AgentRun %s/%s already exists; AgentRuns are append-only, choose a new name: %w", run.Namespace, run.Name, err)
		}
		return err
	}
	format := options.output
	if format == "" {
		format = "name"
	}
	return writeObject(app.Out, run, format)
}

func (app App) buildRun(options createOptions) (*agentsv1alpha1.AgentRun, error) {
	options.namespace = strings.TrimSpace(options.namespace)
	options.name = strings.TrimSpace(options.name)
	options.generateName = strings.TrimSpace(options.generateName)
	options.profile = strings.TrimSpace(options.profile)
	options.sourceKind = strings.TrimSpace(options.sourceKind)
	options.sourceName = strings.TrimSpace(options.sourceName)
	if options.namespace == "" {
		return nil, &usageError{message: "--namespace is required"}
	}
	if problems := apiValidation.NameIsDNSSubdomain(options.namespace, false); len(problems) > 0 {
		return nil, &usageError{message: fmt.Sprintf("invalid --namespace: %s", strings.Join(problems, "; "))}
	}
	if (options.name == "") == (options.generateName == "") {
		return nil, &usageError{message: "set exactly one of --name or --generate-name"}
	}
	if options.name != "" {
		if problems := apiValidation.NameIsDNSSubdomain(options.name, false); len(problems) > 0 {
			return nil, &usageError{message: fmt.Sprintf("invalid --name: %s", strings.Join(problems, "; "))}
		}
	}
	if options.generateName != "" {
		if problems := apiValidation.NameIsDNSSubdomain(options.generateName, true); len(problems) > 0 {
			return nil, &usageError{message: fmt.Sprintf("invalid --generate-name: %s", strings.Join(problems, "; "))}
		}
	}
	if options.profile == "" {
		return nil, &usageError{message: "--profile is required"}
	}
	if options.sourceKind == "" || options.sourceName == "" {
		return nil, &usageError{message: "--source-kind and --source-name are required"}
	}
	if options.sourceGeneration < 0 {
		return nil, &usageError{message: "--source-generation cannot be negative"}
	}
	prompt, err := app.readPrompt(options.prompt, options.promptFile)
	if err != nil {
		return nil, err
	}
	purpose := agentsv1alpha1.AgentRunPurpose(options.purpose)
	if !validPurpose(purpose) {
		return nil, &usageError{message: fmt.Sprintf("invalid --purpose %q", options.purpose)}
	}
	intent := agentsv1alpha1.AgentRunIntent(options.intent)
	if intent != "" && !validIntent(intent) {
		return nil, &usageError{message: fmt.Sprintf("invalid --intent %q", options.intent)}
	}
	run := &agentsv1alpha1.AgentRun{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRun"},
		ObjectMeta: metav1.ObjectMeta{
			Name:         options.name,
			GenerateName: options.generateName,
			Namespace:    options.namespace,
		},
		Spec: agentsv1alpha1.AgentRunSpec{
			Purpose: purpose,
			SourceRef: agentsv1alpha1.AgentRunSourceRef{
				APIVersion: strings.TrimSpace(options.sourceAPIVersion),
				Kind:       options.sourceKind,
				Namespace:  strings.TrimSpace(options.sourceNamespace),
				Name:       options.sourceName,
			},
			SourceUID:        strings.TrimSpace(options.sourceUID),
			SourceGeneration: options.sourceGeneration,
			Prompt:           prompt,
			ProfileRef:       &agentsv1alpha1.NamespacedObjectReference{Name: options.profile},
		},
	}
	if intent != "" {
		run.Spec.Harness.Intent = intent
	}
	return run, nil
}

func (app App) readPrompt(prompt, promptFile string) (string, error) {
	if prompt != "" && promptFile != "" {
		return "", &usageError{message: "set only one of --prompt or --prompt-file"}
	}
	if promptFile != "" {
		reader := app.In
		var closer io.Closer
		if promptFile != "-" {
			file, err := os.Open(promptFile)
			if err != nil {
				return "", err
			}
			reader, closer = file, file
		}
		if closer != nil {
			defer closer.Close()
		}
		body, err := io.ReadAll(io.LimitReader(reader, maxPromptBytes+1))
		if err != nil {
			return "", fmt.Errorf("read prompt: %w", err)
		}
		if len(body) > maxPromptBytes {
			return "", &usageError{message: fmt.Sprintf("prompt exceeds %d bytes", maxPromptBytes)}
		}
		prompt = string(body)
	}
	if strings.TrimSpace(prompt) == "" {
		return "", &usageError{message: "--prompt or --prompt-file is required"}
	}
	return prompt, nil
}

type submitOptions struct {
	namespace        string
	name             string
	generateName     string
	profile          string
	prompt           string
	promptFile       string
	purpose          string
	intent           string
	ticketRepository string
	sourceAPIVersion string
	sourceKind       string
	sourceNamespace  string
	sourceName       string
	sourceUID        string
	sourceGeneration int64
	dryRun           string
	output           string
}

func (app App) runSubmit(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	options := submitOptions{
		purpose:    string(agentsv1alpha1.AgentRunPurposeManual),
		sourceKind: "CLITicket",
		intent:     string(agentsv1alpha1.AgentRunIntentProposeChange),
	}
	flags := newCommandFlags("submit", app.Err)
	flags.StringVarP(&options.namespace, "namespace", "n", "", "Namespace for the new AgentRun (required).")
	flags.StringVar(&options.name, "name", "", "Exact name for the new append-only AgentRun.")
	flags.StringVar(&options.generateName, "generate-name", "", "Server-generated name prefix; defaults to <profile>-.")
	flags.StringVar(&options.profile, "profile", "", "Same-namespace AgentRunProfile to target (required).")
	flags.StringVar(&options.prompt, "prompt", "", "One-off run prompt; alternative to the positional prompt argument.")
	flags.StringVar(&options.promptFile, "prompt-file", "", "Read the one-off prompt from a file, or - for stdin.")
	flags.StringVar(&options.purpose, "purpose", options.purpose, "Run purpose: manual, adverseSituation, or scheduledHealthCheck.")
	flags.StringVar(&options.intent, "intent", options.intent, "Run intent: observe, fixTransient, proposeChange, or cleanup (default proposeChange).")
	flags.StringVar(&options.ticketRepository, "ticket-repository", "", "Owner/name GitHub repository that receives the created ticket (for example HazyForge/anvil-agents).")
	flags.StringVar(&options.sourceAPIVersion, "source-api-version", "", "Opaque source API version metadata.")
	flags.StringVar(&options.sourceKind, "source-kind", options.sourceKind, "Opaque source kind metadata; defaults to CLITicket.")
	flags.StringVar(&options.sourceNamespace, "source-namespace", "", "Opaque source namespace metadata.")
	flags.StringVar(&options.sourceName, "source-name", "", "Opaque source name metadata; defaults to a slug of the prompt.")
	flags.StringVar(&options.sourceUID, "source-uid", "", "Opaque source UID metadata.")
	flags.Int64Var(&options.sourceGeneration, "source-generation", 0, "Opaque source generation metadata.")
	flags.StringVar(&options.dryRun, "dry-run", "", "Set to client to render without contacting Kubernetes.")
	flags.StringVarP(&options.output, "output", "o", "", "Output format: name, yaml, or json. Client dry-run defaults to yaml.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	positional := flags.Args()
	if len(positional) > 1 {
		return &usageError{message: "submit accepts at most one positional prompt argument"}
	}
	if len(positional) == 1 {
		if options.prompt != "" {
			return &usageError{message: "set only one of the positional prompt or --prompt"}
		}
		if options.promptFile != "" {
			return &usageError{message: "set only one of the positional prompt or --prompt-file"}
		}
		options.prompt = positional[0]
	}
	if options.output != "" && options.output != "name" && options.output != "yaml" && options.output != "json" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", options.output)}
	}
	if options.dryRun != "" && options.dryRun != "client" {
		return &usageError{message: "--dry-run supports only client"}
	}
	run, err := app.buildSubmitRun(options)
	if err != nil {
		return err
	}
	if options.dryRun == "client" {
		format := options.output
		if format == "" {
			format = "yaml"
		}
		return writeObject(app.Out, run, format)
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	if err := backend.CreateRun(ctx, run); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("AgentRun %s/%s already exists; AgentRuns are append-only, choose a new name: %w", run.Namespace, run.Name, err)
		}
		return err
	}
	format := options.output
	if format == "" {
		format = "name"
	}
	if err := writeObject(app.Out, run, format); err != nil {
		return err
	}
	if format == "name" {
		fmt.Fprintf(app.Out, "Watch progress: anvil-agentctl run logs -n %s %s --follow\n", run.Namespace, run.Name)
		if options.ticketRepository != "" {
			fmt.Fprintf(app.Out, "Ticket target: %s (the run creates or matches a GitHub issue)\n", options.ticketRepository)
		}
	}
	return nil
}

func (app App) buildSubmitRun(options submitOptions) (*agentsv1alpha1.AgentRun, error) {
	options.namespace = strings.TrimSpace(options.namespace)
	options.name = strings.TrimSpace(options.name)
	options.generateName = strings.TrimSpace(options.generateName)
	options.profile = strings.TrimSpace(options.profile)
	options.sourceKind = strings.TrimSpace(options.sourceKind)
	options.sourceName = strings.TrimSpace(options.sourceName)
	options.ticketRepository = strings.TrimSpace(options.ticketRepository)
	if options.namespace == "" {
		return nil, &usageError{message: "--namespace is required"}
	}
	if problems := apiValidation.NameIsDNSSubdomain(options.namespace, false); len(problems) > 0 {
		return nil, &usageError{message: fmt.Sprintf("invalid --namespace: %s", strings.Join(problems, "; "))}
	}
	if options.profile == "" {
		return nil, &usageError{message: "--profile is required"}
	}
	if options.name != "" && options.generateName != "" {
		return nil, &usageError{message: "set only one of --name or --generate-name"}
	}
	if options.name != "" {
		if problems := apiValidation.NameIsDNSSubdomain(options.name, false); len(problems) > 0 {
			return nil, &usageError{message: fmt.Sprintf("invalid --name: %s", strings.Join(problems, "; "))}
		}
	}
	if options.generateName == "" {
		options.generateName = sanitizeLabelValue(options.profile) + "-"
	}
	if problems := apiValidation.NameIsDNSSubdomain(options.generateName, true); len(problems) > 0 {
		return nil, &usageError{message: fmt.Sprintf("invalid --generate-name: %s", strings.Join(problems, "; "))}
	}
	prompt, err := app.readPrompt(options.prompt, options.promptFile)
	if err != nil {
		return nil, err
	}
	sourceSlug := promptSlug(prompt)
	if options.ticketRepository != "" {
		if !validRepository(options.ticketRepository) {
			return nil, &usageError{message: fmt.Sprintf("invalid --ticket-repository %q; expected owner/name", options.ticketRepository)}
		}
		prompt += fmt.Sprintf(ticketPromptSuffix, options.ticketRepository, options.ticketRepository)
	}
	purpose := agentsv1alpha1.AgentRunPurpose(options.purpose)
	if !validPurpose(purpose) {
		return nil, &usageError{message: fmt.Sprintf("invalid --purpose %q", options.purpose)}
	}
	intent := agentsv1alpha1.AgentRunIntent(options.intent)
	if intent != "" && !validIntent(intent) {
		return nil, &usageError{message: fmt.Sprintf("invalid --intent %q", options.intent)}
	}
	if options.sourceKind == "" {
		return nil, &usageError{message: "--source-kind cannot be empty"}
	}
	if options.sourceName == "" {
		options.sourceName = sourceSlug
		if options.sourceName == "" {
			options.sourceName = options.profile
		}
	}
	if options.sourceGeneration < 0 {
		return nil, &usageError{message: "--source-generation cannot be negative"}
	}
	run := &agentsv1alpha1.AgentRun{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRun"},
		ObjectMeta: metav1.ObjectMeta{
			Name:         options.name,
			GenerateName: options.generateName,
			Namespace:    options.namespace,
		},
		Spec: agentsv1alpha1.AgentRunSpec{
			Purpose: purpose,
			SourceRef: agentsv1alpha1.AgentRunSourceRef{
				APIVersion: strings.TrimSpace(options.sourceAPIVersion),
				Kind:       options.sourceKind,
				Namespace:  strings.TrimSpace(options.sourceNamespace),
				Name:       options.sourceName,
			},
			SourceUID:        strings.TrimSpace(options.sourceUID),
			SourceGeneration: options.sourceGeneration,
			Prompt:           prompt,
			ProfileRef:       &agentsv1alpha1.NamespacedObjectReference{Name: options.profile},
		},
	}
	if intent != "" {
		run.Spec.Harness.Intent = intent
	}
	if options.ticketRepository != "" {
		run.Spec.IssueTracking = &agentsv1alpha1.AgentRunIssueTrackingSpec{
			Provider:     agentsv1alpha1.AgentRunIssueTrackingProviderGitHub,
			Repository:   options.ticketRepository,
			UpdatePolicy: agentsv1alpha1.AgentRunIssueUpdatePolicyTriage,
		}
	}
	return run, nil
}

const ticketPromptSuffix = `

TICKET REQUEST
The operator submitted this idea through the CLI and asked the system to create a ticket in %s.

Create a GitHub issue in %s that captures this idea. Before creating anything, search for an existing issue that already tracks the same intent; if one exists, do not duplicate it — instead add one concise comment with any new evidence and report the existing issue number. When you create a new issue:
- give it a concise, searchable title derived from the idea;
- write a body that states the requested feature, its motivation, and any acceptance criteria described above;
- include the originating AgentRun identity (name, namespace, UID) and this prompt text;
- only add labels or a milestone when repository conventions and evidence support them.
Report the created or matched issue number and URL in your final status summary.
`

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func validRepository(value string) bool {
	return repositoryPattern.MatchString(strings.TrimSpace(value))
}

func promptSlug(prompt string) string {
	return sanitizeLabelValue(prompt)
}

func validPurpose(value agentsv1alpha1.AgentRunPurpose) bool {
	switch value {
	case agentsv1alpha1.AgentRunPurposeManual, agentsv1alpha1.AgentRunPurposeAdverseSituation, agentsv1alpha1.AgentRunPurposeScheduledHealthCheck:
		return true
	default:
		return false
	}
}

func validIntent(value agentsv1alpha1.AgentRunIntent) bool {
	switch value {
	case agentsv1alpha1.AgentRunIntentObserve, agentsv1alpha1.AgentRunIntentFixTransient, agentsv1alpha1.AgentRunIntentProposeChange, agentsv1alpha1.AgentRunIntentCleanup:
		return true
	default:
		return false
	}
}

func (app App) runList(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var namespace, output string
	var allNamespaces bool
	flags := newCommandFlags("run list", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	flags.BoolVarP(&allNamespaces, "all-namespaces", "A", false, "List AgentRuns across all namespaces allowed by caller RBAC.")
	flags.StringVarP(&output, "output", "o", "", "Output format: table, yaml, or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: "run list does not accept positional arguments"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	if namespace = resolvedNamespace(namespace, backend); allNamespaces {
		namespace = ""
	}
	list, err := backend.ListRuns(ctx, namespace, allNamespaces)
	if err != nil {
		return err
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].CreationTimestamp.After(list.Items[j].CreationTimestamp.Time)
	})
	if output == "yaml" || output == "json" {
		list.TypeMeta = metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRunList"}
		return writeObject(app.Out, list, output)
	}
	if output != "" && output != "table" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", output)}
	}
	return writeRunTable(app.Out, list.Items, allNamespaces)
}

func (app App) runProfileList(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var namespace, output string
	var allNamespaces bool
	flags := newCommandFlags("profile list", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	flags.BoolVarP(&allNamespaces, "all-namespaces", "A", false, "List AgentRunProfiles across all namespaces allowed by caller RBAC.")
	flags.StringVarP(&output, "output", "o", "", "Output format: table, yaml, or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: "profile list does not accept positional arguments"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	namespace = resolvedNamespace(namespace, backend)
	if allNamespaces {
		namespace = ""
	}
	list, err := backend.ListProfiles(ctx, namespace, allNamespaces)
	if err != nil {
		return err
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].CreationTimestamp.After(list.Items[j].CreationTimestamp.Time)
	})
	if output == "yaml" || output == "json" {
		list.TypeMeta = metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRunProfileList"}
		return writeObject(app.Out, list, output)
	}
	if output != "" && output != "table" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", output)}
	}
	return writeProfileTable(app.Out, list.Items, allNamespaces)
}

func writeProfileTable(writer io.Writer, profiles []agentsv1alpha1.AgentRunProfile, allNamespaces bool) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if allNamespaces {
		fmt.Fprint(table, "NAMESPACE\t")
	}
	fmt.Fprintln(table, "NAME\tINTENT\tBACKEND\tHARNESS PROFILE\tAPPLICATION\tTARGET\tAGE")
	now := time.Now()
	for i := range profiles {
		profile := &profiles[i]
		if allNamespaces {
			fmt.Fprintf(table, "%s\t", profile.Namespace)
		}
		harnessProfile := "-"
		if profile.Spec.HarnessProfileRef != nil {
			harnessProfile = valueOrDash(profile.Spec.HarnessProfileRef.Name)
		}
		application := "-"
		if profile.Spec.Scope.ApplicationRef != nil {
			application = valueOrDash(profile.Spec.Scope.ApplicationRef.Name)
		}
		target := "-"
		if profile.Spec.Scope.ApplicationTargetRef != nil {
			target = valueOrDash(profile.Spec.Scope.ApplicationTargetRef.Name)
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			profile.Name,
			valueOrDash(string(profile.Spec.Harness.Intent)),
			valueOrDash(string(profile.Spec.Harness.Backend.Kind)),
			harnessProfile,
			application,
			target,
			humanAge(now.Sub(profile.CreationTimestamp.Time)),
		)
	}
	return table.Flush()
}

func (app App) runGet(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var namespace, output string
	flags := newCommandFlags("run get", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	flags.StringVarP(&output, "output", "o", "", "Output format: summary, yaml, or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return &usageError{message: "run get requires exactly one AgentRun name"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	run, err := backend.GetRun(ctx, resolvedNamespace(namespace, backend), flags.Arg(0))
	if err != nil {
		return err
	}
	if output == "yaml" || output == "json" {
		return writeObject(app.Out, run, output)
	}
	if output != "" && output != "summary" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", output)}
	}
	writeRunSummary(app.Out, run, false)
	return nil
}

func (app App) runLogs(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var namespace string
	var follow, timestamps bool
	var tail int64 = 200
	var podTimeout time.Duration = 2 * time.Minute
	flags := newCommandFlags("run logs", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	flags.BoolVarP(&follow, "follow", "f", false, "Follow the verified agent container log stream.")
	flags.BoolVar(&timestamps, "timestamps", false, "Include Kubernetes log timestamps.")
	flags.Int64Var(&tail, "tail", tail, "Number of recent lines; use -1 for all available logs.")
	flags.DurationVar(&podTimeout, "pod-timeout", podTimeout, "When following, wait this long for the runner Pod and logs to become ready.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return &usageError{message: "run logs requires exactly one AgentRun name"}
	}
	if tail == 0 || tail < -1 {
		return &usageError{message: "--tail must be -1 or a positive integer"}
	}
	if podTimeout <= 0 {
		return &usageError{message: "--pod-timeout must be positive"}
	}
	if !follow && flags.Changed("pod-timeout") {
		return &usageError{message: "--pod-timeout requires --follow"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	run, err := backend.GetRun(ctx, resolvedNamespace(namespace, backend), flags.Arg(0))
	if err != nil {
		return err
	}
	options := corev1.PodLogOptions{Follow: follow, Timestamps: timestamps}
	if tail >= 0 {
		options.TailLines = &tail
	}
	stream, err := app.openLogs(ctx, backend, run, options, follow, podTimeout)
	if err != nil {
		return fmt.Errorf("open verified AgentRun logs: %w", err)
	}
	defer stream.Close()
	if _, err := io.Copy(app.Out, stream); err != nil {
		return fmt.Errorf("read AgentRun logs: %w", err)
	}
	return nil
}

func (app App) openLogs(ctx context.Context, backend Backend, run *agentsv1alpha1.AgentRun, options corev1.PodLogOptions, wait bool, timeout time.Duration) (io.ReadCloser, error) {
	if !wait {
		return backend.OpenLogs(ctx, run, options)
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for {
		stream, err := backend.OpenLogs(waitCtx, run, options)
		if err == nil {
			return stream, nil
		}
		if !errors.Is(err, runapi.ErrLogsPending) && !apierrors.IsNotFound(err) {
			return nil, err
		}
		lastErr = err
		timer := time.NewTimer(app.pollInterval())
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("runner Pod/logs did not become ready within %s: %w", timeout, lastErr)
		case <-timer.C:
		}
		latest, err := backend.GetRun(waitCtx, run.Namespace, run.Name)
		if err != nil {
			return nil, err
		}
		run = latest
	}
}

func (app App) pollInterval() time.Duration {
	if app.PollInterval > 0 {
		return app.PollInterval
	}
	return 500 * time.Millisecond
}

func (app App) runDebug(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var namespace string
	flags := newCommandFlags("run debug", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return &usageError{message: "run debug requires exactly one AgentRun name"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	run, err := backend.GetRun(ctx, resolvedNamespace(namespace, backend), flags.Arg(0))
	if err != nil {
		return err
	}
	writeRunSummary(app.Out, run, true)

	verifiedUIDs := []types.UID{run.UID}
	var job *batchv1.Job
	fmt.Fprintln(app.Out, "\nJOB")
	if run.Status.JobRef == nil || strings.TrimSpace(run.Status.JobRef.Name) == "" {
		fmt.Fprintln(app.Out, "  unavailable: AgentRun status has no Job reference")
	} else if childNamespace(run.Namespace, run.Status.JobRef.Namespace) != run.Namespace {
		fmt.Fprintf(app.Out, "  verification failed: Job reference crosses namespaces to %q\n", terminalSafe(run.Status.JobRef.Namespace))
	} else {
		job, err = backend.GetJob(ctx, run.Namespace, run.Status.JobRef.Name)
		if err != nil {
			fmt.Fprintf(app.Out, "  unavailable: %s\n", terminalSafe(err.Error()))
		} else if err := verifyJob(run, job); err != nil {
			fmt.Fprintf(app.Out, "  verification failed: %s\n", terminalSafe(err.Error()))
			job = nil
		} else {
			verifiedUIDs = append(verifiedUIDs, job.UID)
			writeJobDebug(app.Out, job)
		}
	}

	var pod *corev1.Pod
	fmt.Fprintln(app.Out, "\nPOD")
	if run.Status.RunnerPodRef == nil || strings.TrimSpace(run.Status.RunnerPodRef.Name) == "" {
		fmt.Fprintln(app.Out, "  unavailable: AgentRun status has no runner Pod reference")
	} else if childNamespace(run.Namespace, run.Status.RunnerPodRef.Namespace) != run.Namespace {
		fmt.Fprintf(app.Out, "  verification failed: Pod reference crosses namespaces to %q\n", terminalSafe(run.Status.RunnerPodRef.Namespace))
	} else if job == nil {
		fmt.Fprintln(app.Out, "  not verified: the referenced Job was not verified")
	} else {
		pod, err = backend.GetPod(ctx, run.Namespace, run.Status.RunnerPodRef.Name)
		if err != nil {
			fmt.Fprintf(app.Out, "  unavailable: %s\n", terminalSafe(err.Error()))
		} else if err := verifyPod(run, job, pod); err != nil {
			fmt.Fprintf(app.Out, "  verification failed: %s\n", terminalSafe(err.Error()))
			pod = nil
		} else {
			verifiedUIDs = append(verifiedUIDs, pod.UID)
			writePodDebug(app.Out, pod)
		}
	}

	fmt.Fprintln(app.Out, "\nEVENTS")
	events, eventsErr := backend.ListEvents(ctx, run.Namespace, verifiedUIDs)
	if eventsErr != nil {
		fmt.Fprintf(app.Out, "  unavailable: %s\n", terminalSafe(eventsErr.Error()))
	} else {
		writeEvents(app.Out, events)
	}

	fmt.Fprintln(app.Out, "\nLIKELY CAUSE")
	fmt.Fprintf(app.Out, "  %s\n", terminalSafe(likelyCause(run, job, pod, events)))
	return nil
}

func newCommandFlags(name string, output io.Writer) *pflag.FlagSet {
	flags := pflag.NewFlagSet(name, pflag.ContinueOnError)
	flags.SetOutput(output)
	return flags
}

func resolvedNamespace(value string, backend Backend) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	if value = strings.TrimSpace(backend.DefaultNamespace()); value != "" {
		return value
	}
	return "default"
}

func writeObject(writer io.Writer, object any, format string) error {
	switch format {
	case "name":
		run, ok := object.(*agentsv1alpha1.AgentRun)
		if !ok {
			return &usageError{message: "name output is supported only for one AgentRun"}
		}
		name := run.Name
		if name == "" {
			name = run.GenerateName
		}
		_, err := fmt.Fprintf(writer, "agentrun.control.anvil.hazyforge.io/%s\n", name)
		return err
	case "json":
		cleaned, err := cleanObject(object)
		if err != nil {
			return err
		}
		body, err := json.MarshalIndent(cleaned, "", "  ")
		if err != nil {
			return fmt.Errorf("encode JSON: %w", err)
		}
		_, err = fmt.Fprintf(writer, "%s\n", body)
		return err
	case "yaml":
		cleaned, err := cleanObject(object)
		if err != nil {
			return err
		}
		body, err := yaml.Marshal(cleaned)
		if err != nil {
			return fmt.Errorf("encode YAML: %w", err)
		}
		_, err = writer.Write(body)
		return err
	default:
		return &usageError{message: fmt.Sprintf("unsupported output format %q", format)}
	}
}

func cleanObject(object any) (any, error) {
	body, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode object: %w", err)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("normalize object: %w", err)
	}
	cleaned, _ := pruneEmptyContainers(value)
	return cleaned, nil
}

func pruneEmptyContainers(value any) (any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, false
	case map[string]any:
		cleaned := make(map[string]any, len(typed))
		for key, item := range typed {
			if pruned, keep := pruneEmptyContainers(item); keep {
				cleaned[key] = pruned
			}
		}
		return cleaned, len(cleaned) > 0
	case []any:
		cleaned := make([]any, 0, len(typed))
		for _, item := range typed {
			pruned, _ := pruneEmptyContainers(item)
			cleaned = append(cleaned, pruned)
		}
		return cleaned, len(cleaned) > 0
	default:
		return value, true
	}
}

func writeRunTable(writer io.Writer, runs []agentsv1alpha1.AgentRun, allNamespaces bool) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if allNamespaces {
		fmt.Fprint(table, "NAMESPACE\t")
	}
	fmt.Fprintln(table, "NAME\tPHASE\tBACKEND\tINTENT\tSOURCE\tAGE")
	now := time.Now()
	for i := range runs {
		run := &runs[i]
		if allNamespaces {
			fmt.Fprintf(table, "%s\t", run.Namespace)
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s/%s\t%s\n",
			run.Name,
			valueOrDash(string(run.Status.Phase)),
			valueOrDash(run.Status.Backend),
			valueOrDash(run.Status.Intent),
			valueOrDash(run.Spec.SourceRef.Kind),
			valueOrDash(run.Spec.SourceRef.Name),
			humanAge(now.Sub(run.CreationTimestamp.Time)),
		)
	}
	return table.Flush()
}

func writeRunSummary(writer io.Writer, run *agentsv1alpha1.AgentRun, includeOutput bool) {
	fmt.Fprintln(writer, "RUN")
	fmt.Fprintf(writer, "  Name: %s/%s\n", run.Namespace, run.Name)
	fmt.Fprintf(writer, "  UID: %s\n", valueOrDash(string(run.UID)))
	fmt.Fprintf(writer, "  Phase: %s\n", valueOrDash(string(run.Status.Phase)))
	fmt.Fprintf(writer, "  Backend: %s\n", valueOrDash(run.Status.Backend))
	fmt.Fprintf(writer, "  Intent: %s\n", valueOrDash(firstNonEmpty(run.Status.Intent, string(run.Spec.Harness.Intent))))
	fmt.Fprintf(writer, "  Source: %s/%s\n", valueOrDash(run.Spec.SourceRef.Kind), valueOrDash(run.Spec.SourceRef.Name))
	fmt.Fprintf(writer, "  Job: %s\n", referenceName(run.Status.JobRef))
	fmt.Fprintf(writer, "  Runner Pod: %s\n", referenceName(run.Status.RunnerPodRef))
	fmt.Fprintf(writer, "  Started: %s\n", timeValue(run.Status.StartedAt))
	fmt.Fprintf(writer, "  Completed: %s\n", timeValue(run.Status.CompletedAt))
	if run.Status.ResolvedComposition != nil {
		fmt.Fprintln(writer, "  Composition:")
		fmt.Fprintf(writer, "    Effective digest: %s\n", valueOrDash(run.Status.ResolvedComposition.EffectiveDigest))
		fmt.Fprintf(writer, "    Payload digest: %s\n", valueOrDash(run.Status.ResolvedComposition.PayloadDigest))
	}
	if len(run.Status.Conditions) > 0 {
		fmt.Fprintln(writer, "  Conditions:")
		for _, condition := range run.Status.Conditions {
			fmt.Fprintf(writer, "    %s=%s reason=%s message=%s\n", valueOrDash(condition.Type), valueOrDash(string(condition.Status)), valueOrDash(condition.Reason), valueOrDash(condition.Message))
		}
	}
	if len(run.Status.Reports) > 0 {
		fmt.Fprintln(writer, "  Reports:")
		for _, report := range run.Status.Reports {
			fmt.Fprintf(writer, "    %s stage=%s level=%s summary=%s\n", valueOrDash(report.Type), valueOrDash(report.Stage), valueOrDash(report.Level), valueOrDash(report.Summary))
		}
	}
	if run.Status.Error != "" {
		fmt.Fprintf(writer, "  Error: %s\n", terminalSafe(run.Status.Error))
	}
	if run.Status.PullRequestURL != "" {
		fmt.Fprintf(writer, "  Pull request: %s\n", terminalSafe(run.Status.PullRequestURL))
	}
	if includeOutput && run.Status.Output != "" {
		fmt.Fprintln(writer, "  Status output:")
		for _, line := range strings.Split(strings.TrimSpace(run.Status.Output), "\n") {
			fmt.Fprintf(writer, "    %s\n", terminalSafe(line))
		}
	}
}

func verifyJob(run *agentsv1alpha1.AgentRun, job *batchv1.Job) error {
	if run.UID == "" {
		return fmt.Errorf("AgentRun UID is empty")
	}
	if job.Namespace != run.Namespace {
		return fmt.Errorf("Job namespace %q does not match AgentRun namespace %q", job.Namespace, run.Namespace)
	}
	if job.Labels[agentRunLabel] != sanitizeLabelValue(run.Name) {
		return fmt.Errorf("Job label %s does not identify this AgentRun", agentRunLabel)
	}
	owner := metav1.GetControllerOf(job)
	if owner == nil || owner.APIVersion != agentsv1alpha1.GroupVersion.String() || owner.Kind != "AgentRun" || owner.Name != run.Name || owner.UID != run.UID {
		return fmt.Errorf("Job is not controller-owned by AgentRun %s/%s", run.Namespace, run.Name)
	}
	if run.Status.JobUID != "" && string(job.UID) != run.Status.JobUID {
		return fmt.Errorf("Job UID does not match the AgentRun status receipt")
	}
	return nil
}

func verifyPod(run *agentsv1alpha1.AgentRun, job *batchv1.Job, pod *corev1.Pod) error {
	if pod.Namespace != run.Namespace {
		return fmt.Errorf("Pod namespace %q does not match AgentRun namespace %q", pod.Namespace, run.Namespace)
	}
	if pod.Labels[agentRunLabel] != sanitizeLabelValue(run.Name) || pod.Labels[agentRunJobLabel] != job.Name {
		return fmt.Errorf("Pod labels do not identify the verified AgentRun Job")
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != batchv1.SchemeGroupVersion.String() || owner.Kind != "Job" || owner.Name != job.Name || owner.UID != job.UID {
		return fmt.Errorf("Pod is not controller-owned by Job %s/%s", job.Namespace, job.Name)
	}
	if run.Status.RunnerPodUID != "" && string(pod.UID) != run.Status.RunnerPodUID {
		return fmt.Errorf("Pod UID does not match the AgentRun status receipt")
	}
	for _, container := range pod.Spec.Containers {
		if container.Name == agentContainer {
			return nil
		}
	}
	return fmt.Errorf("Pod does not contain the required %q container", agentContainer)
}

func writeJobDebug(writer io.Writer, job *batchv1.Job) {
	fmt.Fprintf(writer, "  verified: %s/%s uid=%s\n", job.Namespace, job.Name, job.UID)
	fmt.Fprintf(writer, "  active=%d succeeded=%d failed=%d\n", job.Status.Active, job.Status.Succeeded, job.Status.Failed)
	for _, condition := range job.Status.Conditions {
		fmt.Fprintf(writer, "  condition %s=%s reason=%s message=%s\n", valueOrDash(string(condition.Type)), valueOrDash(string(condition.Status)), valueOrDash(condition.Reason), valueOrDash(condition.Message))
	}
}

func writePodDebug(writer io.Writer, pod *corev1.Pod) {
	fmt.Fprintf(writer, "  verified: %s/%s uid=%s node=%s phase=%s\n", pod.Namespace, pod.Name, pod.UID, valueOrDash(pod.Spec.NodeName), valueOrDash(string(pod.Status.Phase)))
	for _, status := range append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		fmt.Fprintf(writer, "  container %s ready=%t restarts=%d state=%s\n", terminalSafe(status.Name), status.Ready, status.RestartCount, terminalSafe(containerState(status.State)))
	}
}

func writeEvents(writer io.Writer, events []corev1.Event) {
	if len(events) == 0 {
		fmt.Fprintln(writer, "  none for verified resource UIDs")
		return
	}
	sort.Slice(events, func(i, j int) bool { return eventTime(events[i]).Before(eventTime(events[j])) })
	for _, event := range events {
		fmt.Fprintf(writer, "  %s %s %s/%s reason=%s count=%d message=%s\n",
			eventTime(event).UTC().Format(time.RFC3339), valueOrDash(event.Type), terminalSafe(event.InvolvedObject.Kind),
			terminalSafe(event.InvolvedObject.Name), valueOrDash(event.Reason), event.Count, valueOrDash(event.Message))
	}
}

func likelyCause(run *agentsv1alpha1.AgentRun, job *batchv1.Job, pod *corev1.Pod, events []corev1.Event) string {
	if run.Status.Phase == agentsv1alpha1.AgentRunPhaseFailed {
		if failure := agentRunToolFailure(run.Status.Output); failure != "" {
			return failure
		}
	}
	if text := strings.TrimSpace(run.Status.Error); text != "" {
		return text
	}
	for i := len(run.Status.Conditions) - 1; i >= 0; i-- {
		condition := run.Status.Conditions[i]
		if condition.Status == metav1.ConditionFalse && strings.TrimSpace(condition.Message) != "" {
			return condition.Message
		}
	}
	if pod != nil {
		statuses := append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...)
		for _, status := range statuses {
			if status.State.Waiting != nil {
				return firstNonEmpty(status.State.Waiting.Message, status.State.Waiting.Reason, "container is waiting")
			}
			if status.State.Terminated != nil && status.State.Terminated.ExitCode != 0 {
				return fmt.Sprintf("container %s exited %d: %s", status.Name, status.State.Terminated.ExitCode, firstNonEmpty(status.State.Terminated.Message, status.State.Terminated.Reason, "unspecified failure"))
			}
		}
	}
	if job != nil {
		for _, condition := range job.Status.Conditions {
			if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
				return firstNonEmpty(condition.Message, condition.Reason, "Job failed")
			}
		}
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == corev1.EventTypeWarning {
			return firstNonEmpty(events[i].Message, events[i].Reason, "Kubernetes warning event")
		}
	}
	if run.Status.Phase == agentsv1alpha1.AgentRunPhaseSucceeded {
		return "run completed successfully; no failure evidence found"
	}
	return "no single cause is recorded; inspect `anvil-agentctl run logs` for the verified agent container"
}

func agentRunToolFailure(output string) string {
	lines := strings.Split(output, "\n")
	for index, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case hasHarnessMarker(line, "ANVIL_AGENT_RUN_TOOL_VERIFY_FAILED"):
			return "tool verification failed: " + boundedDiagnostic(line)
		case hasHarnessMarker(line, "ANVIL_AGENT_RUN_TOOL_CALL_FAILED"):
			return "tool call failed: " + boundedDiagnostic(line)
		case hasHarnessMarker(line, "ANVIL_AGENT_RUN_TOOL_VERIFY_START"):
			if index+1 >= len(lines) {
				continue
			}
			next := strings.TrimSpace(lines[index+1])
			if len(next) < len("error:") || !strings.EqualFold(next[:len("error:")], "error:") {
				continue
			}
			tool := strings.TrimSpace(strings.TrimPrefix(line, "ANVIL_AGENT_RUN_TOOL_VERIFY_START"))
			if tool != "" {
				return fmt.Sprintf("tool verification failed (%s): %s", boundedDiagnostic(tool), boundedDiagnostic(next))
			}
			return "tool verification failed: " + boundedDiagnostic(next)
		}
	}
	return ""
}

func hasHarnessMarker(line, marker string) bool {
	return line == marker || strings.HasPrefix(line, marker+" ")
}

func boundedDiagnostic(value string) string {
	value = terminalSafe(strings.TrimSpace(value))
	const maxRunes = 500
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return value
}

func childNamespace(parent, child string) string {
	if child = strings.TrimSpace(child); child != "" {
		return child
	}
	return parent
}

func referenceName(reference *agentsv1alpha1.NamespacedObjectReference) string {
	if reference == nil || strings.TrimSpace(reference.Name) == "" {
		return "-"
	}
	if strings.TrimSpace(reference.Namespace) == "" {
		return terminalSafe(reference.Name)
	}
	return terminalSafe(reference.Namespace + "/" + reference.Name)
}

func timeValue(value *metav1.Time) string {
	if value == nil || value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func eventTime(event corev1.Event) time.Time {
	if !event.EventTime.IsZero() {
		return event.EventTime.Time
	}
	if !event.LastTimestamp.IsZero() {
		return event.LastTimestamp.Time
	}
	if !event.FirstTimestamp.IsZero() {
		return event.FirstTimestamp.Time
	}
	return event.CreationTimestamp.Time
}

func containerState(state corev1.ContainerState) string {
	if state.Waiting != nil {
		return "waiting reason=" + valueOrDash(state.Waiting.Reason) + " message=" + valueOrDash(state.Waiting.Message)
	}
	if state.Running != nil {
		return "running since=" + state.Running.StartedAt.UTC().Format(time.RFC3339)
	}
	if state.Terminated != nil {
		return "terminated exit=" + strconv.FormatInt(int64(state.Terminated.ExitCode), 10) + " reason=" + valueOrDash(state.Terminated.Reason) + " message=" + valueOrDash(state.Terminated.Message)
	}
	return "unknown"
}

func sanitizeLabelValue(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var builder strings.Builder
	lastDash := false
	for _, character := range value {
		valid := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
		if valid {
			builder.WriteRune(character)
			lastDash = false
		} else if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	value = strings.Trim(builder.String(), "-")
	if len(value) > 63 {
		value = value[:63]
	}
	return value
}

func valueOrDash(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return terminalSafe(value)
	}
	return "-"
}

func terminalSafe(value string) string {
	var builder strings.Builder
	for _, character := range value {
		switch {
		case character == '\n':
			builder.WriteString(`\n`)
		case character == '\r':
			builder.WriteString(`\r`)
		case character == '\t':
			builder.WriteString(`\t`)
		case character < 0x20 || character == 0x7f || (character >= 0x80 && character <= 0x9f):
			fmt.Fprintf(&builder, `\u%04x`, character)
		default:
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func humanAge(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%dh", int(duration.Hours()))
	}
	return fmt.Sprintf("%dd", int(duration.Hours()/24))
}
