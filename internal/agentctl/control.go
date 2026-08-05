package agentctl

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiValidation "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func writeControlUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Control commands: list, get, pause, resume")
	fmt.Fprintln(writer, "  anvil-agentctl control list [-o table|yaml|json]")
	fmt.Fprintln(writer, "  anvil-agentctl control get NAME [-o summary|yaml|json]")
	fmt.Fprintln(writer, "  anvil-agentctl control pause --application APP --reason TEXT [--control-name NAME] [--for DURATION|--indefinite] [--source-name ID] [--source-url URL] [--source-actor ACTOR] [--dry-run client]")
	fmt.Fprintln(writer, "  anvil-agentctl control resume --application APP --reason TEXT [--control-name NAME|--all-controls] [--dry-run client]")
}

func (app App) runControl(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	if len(args) == 0 {
		writeControlUsage(app.Err)
		return &usageError{message: "a control command is required"}
	}
	switch args[0] {
	case "list":
		return app.controlList(ctx, kubeOptions, args[1:])
	case "get":
		return app.controlGet(ctx, kubeOptions, args[1:])
	case "pause":
		return app.controlPause(ctx, kubeOptions, args[1:])
	case "resume":
		return app.controlResume(ctx, kubeOptions, args[1:])
	case "help":
		writeControlUsage(app.Out)
		return nil
	default:
		writeControlUsage(app.Err)
		return &usageError{message: fmt.Sprintf("unknown control command %q", args[0])}
	}
}

func (app App) controlList(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var output, application string
	flags := newCommandFlags("control list", app.Err)
	flags.StringVarP(&output, "output", "o", "", "Output format: table, yaml, or json.")
	flags.StringVar(&application, "application", "", "Optional opaque Application scope filter.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: "control list does not accept positional arguments"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	list, err := backend.ListControls(ctx)
	if err != nil {
		return err
	}
	if application = strings.TrimSpace(application); application != "" {
		filtered := list.Items[:0]
		for _, control := range list.Items {
			if strings.TrimSpace(control.Spec.ApplicationRef.Name) == application {
				filtered = append(filtered, control)
			}
		}
		list.Items = filtered
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})
	if output == "yaml" || output == "json" {
		list.TypeMeta = metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRunControlList"}
		return writeObject(app.Out, list, output)
	}
	if output != "" && output != "table" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", output)}
	}
	return writeControlTable(app.Out, list.Items)
}

func (app App) controlGet(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var output string
	flags := newCommandFlags("control get", app.Err)
	flags.StringVarP(&output, "output", "o", "", "Output format: summary, yaml, or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return &usageError{message: "control get requires exactly one AgentRunControl name"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	control, err := backend.GetControl(ctx, flags.Arg(0))
	if err != nil {
		return err
	}
	if output == "yaml" || output == "json" {
		return writeObject(app.Out, control, output)
	}
	if output != "" && output != "summary" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", output)}
	}
	writeControlSummary(app.Out, control)
	return nil
}

type controlPauseOptions struct {
	Application string
	ControlName string
	Duration    string
	Indefinite  bool
	Reason      string
	SourceName  string
	SourceURL   string
	SourceActor string
	DryRun      string
	Output      string
}

func (app App) controlPause(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	options := controlPauseOptions{Duration: "4h"}
	flags := newCommandFlags("control pause", app.Err)
	flags.StringVar(&options.Application, "application", "", "Opaque Application scope whose launches are paused (required).")
	flags.StringVar(&options.ControlName, "control-name", "", "AgentRunControl name; defaults to the Application name.")
	flags.StringVar(&options.Duration, "for", options.Duration, "Bounded pause duration, for example 2h or 30m (default 4h).")
	flags.BoolVar(&options.Indefinite, "indefinite", false, "Create an explicit human-owned pause without expiry.")
	flags.StringVar(&options.Reason, "reason", "", "Auditable reason for the pause (required).")
	flags.StringVar(&options.SourceName, "source-name", "", "Stable source event or directive identifier.")
	flags.StringVar(&options.SourceURL, "source-url", "", "Trusted source URL, such as the verified pull request.")
	flags.StringVar(&options.SourceActor, "source-actor", "", "Verified maintainer actor associated with the source.")
	flags.StringVar(&options.DryRun, "dry-run", "", "Set to client to render without contacting Kubernetes.")
	flags.StringVarP(&options.Output, "output", "o", "", "Output format: name, yaml, or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: "control pause does not accept positional arguments"}
	}
	if options.DryRun != "" && options.DryRun != "client" {
		return &usageError{message: "--dry-run supports only client"}
	}
	if options.Output != "" && options.Output != "name" && options.Output != "yaml" && options.Output != "json" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", options.Output)}
	}
	application := strings.TrimSpace(options.Application)
	if application == "" {
		return &usageError{message: "--application is required"}
	}
	if problems := apiValidation.NameIsDNSSubdomain(application, false); len(problems) > 0 {
		return &usageError{message: fmt.Sprintf("invalid --application %q: %s", application, strings.Join(problems, "; "))}
	}
	controlName := firstNonEmpty(strings.TrimSpace(options.ControlName), application)
	if problems := apiValidation.NameIsDNSSubdomain(controlName, false); len(problems) > 0 {
		return &usageError{message: fmt.Sprintf("invalid --control-name %q: %s", controlName, strings.Join(problems, "; "))}
	}
	reason := strings.TrimSpace(options.Reason)
	if reason == "" {
		return &usageError{message: "--reason is required when pausing"}
	}
	if options.Indefinite && flags.Changed("for") {
		return &usageError{message: "use either --for or --indefinite, not both"}
	}
	now := time.Now().UTC()
	var expiresAt *metav1.Time
	if !options.Indefinite {
		duration, err := time.ParseDuration(strings.TrimSpace(options.Duration))
		if err != nil || duration <= 0 {
			return &usageError{message: fmt.Sprintf("--for must be a positive Go duration: %q", options.Duration)}
		}
		expiry := metav1.NewTime(now.Add(duration))
		expiresAt = &expiry
	}

	control := &agentsv1alpha1.AgentRunControl{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRunControl"},
		ObjectMeta: metav1.ObjectMeta{
			Name: controlName,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "anvil-agentctl",
			},
		},
		Spec: agentsv1alpha1.AgentRunControlSpec{
			ApplicationRef: agentsv1alpha1.ApplicationReferenceSpec{Name: application},
			LaunchPolicy:   agentsv1alpha1.AgentRunControlLaunchPolicyPaused,
			Reason:         reason,
			ExpiresAt:      expiresAt,
			Source:         controlSource(options.SourceName, options.SourceURL, options.SourceActor),
		},
	}

	if options.DryRun == "client" {
		format := options.Output
		if format == "" {
			format = "yaml"
		}
		return writeObject(app.Out, control, format)
	}

	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	existing, err := backend.GetControl(ctx, controlName)
	switch {
	case apierrors.IsNotFound(err):
		if err := backend.CreateControl(ctx, control); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		existing.Spec.LaunchPolicy = control.Spec.LaunchPolicy
		existing.Spec.Reason = control.Spec.Reason
		existing.Spec.ExpiresAt = control.Spec.ExpiresAt
		existing.Spec.Source = control.Spec.Source
		if err := backend.UpdateControl(ctx, existing); err != nil {
			return err
		}
		control = existing
	}
	format := options.Output
	if format == "" {
		format = "name"
	}
	return writeObject(app.Out, control, format)
}

func (app App) controlResume(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var application, controlName, reason, sourceName, sourceURL, sourceActor, dryRun, output string
	var allControls bool
	flags := newCommandFlags("control resume", app.Err)
	flags.StringVar(&application, "application", "", "Opaque Application scope whose launches are resumed (required).")
	flags.StringVar(&controlName, "control-name", "", "AgentRunControl to resume; defaults to the Application name.")
	flags.BoolVar(&allControls, "all-controls", false, "Resume every active Paused control for the Application.")
	flags.StringVar(&reason, "reason", "", "Auditable reason for the resume (required).")
	flags.StringVar(&sourceName, "source-name", "", "Stable source event or directive identifier.")
	flags.StringVar(&sourceURL, "source-url", "", "Trusted source URL, such as the verified pull request.")
	flags.StringVar(&sourceActor, "source-actor", "", "Verified maintainer actor associated with the source.")
	flags.StringVar(&dryRun, "dry-run", "", "Set to client to render without contacting Kubernetes.")
	flags.StringVarP(&output, "output", "o", "", "Output format: name, yaml, or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: "control resume does not accept positional arguments"}
	}
	if allControls && flags.Changed("control-name") {
		return &usageError{message: "use either --control-name or --all-controls, not both"}
	}
	if dryRun != "" && dryRun != "client" {
		return &usageError{message: "--dry-run supports only client"}
	}
	if output != "" && output != "name" && output != "yaml" && output != "json" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", output)}
	}
	application = strings.TrimSpace(application)
	if application == "" {
		return &usageError{message: "--application is required"}
	}
	if problems := apiValidation.NameIsDNSSubdomain(application, false); len(problems) > 0 {
		return &usageError{message: fmt.Sprintf("invalid --application %q: %s", application, strings.Join(problems, "; "))}
	}
	controlName = firstNonEmpty(strings.TrimSpace(controlName), application)
	if !allControls {
		if problems := apiValidation.NameIsDNSSubdomain(controlName, false); len(problems) > 0 {
			return &usageError{message: fmt.Sprintf("invalid --control-name %q: %s", controlName, strings.Join(problems, "; "))}
		}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return &usageError{message: "--reason is required when resuming"}
	}

	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	list, err := backend.ListControls(ctx)
	if err != nil {
		return err
	}
	activeNames := activePausedControlNames(list.Items, application, time.Now().UTC())
	targets := activeNames
	if !allControls {
		targets = nil
		for _, name := range activeNames {
			if name == controlName {
				targets = []string{name}
				break
			}
		}
	}
	if len(targets) == 0 {
		if !allControls {
			fmt.Fprintf(app.Out, "AgentRunControl %s is not an active pause for Application %s.\n", controlName, application)
		}
		fmt.Fprintf(app.Out, "Application %s has no active Paused AgentRunControls.\n", application)
		return nil
	}
	source := controlSource(sourceName, sourceURL, sourceActor)

	for _, name := range targets {
		control, err := backend.GetControl(ctx, name)
		if err != nil {
			return err
		}
		control.Spec.LaunchPolicy = agentsv1alpha1.AgentRunControlLaunchPolicyAllowed
		control.Spec.Reason = reason
		control.Spec.ExpiresAt = nil
		if source != nil {
			control.Spec.Source = source
		} else {
			control.Spec.Source = &agentsv1alpha1.AgentRunControlSourceSpec{Kind: "Operator"}
		}
		if dryRun == "client" {
			format := output
			if format == "" {
				format = "yaml"
			}
			if err := writeObject(app.Out, control, format); err != nil {
				return err
			}
			continue
		}
		if err := backend.UpdateControl(ctx, control); err != nil {
			return err
		}
		format := output
		if format == "" {
			format = "name"
		}
		if err := writeObject(app.Out, control, format); err != nil {
			return err
		}
	}
	if dryRun == "client" {
		fmt.Fprintf(app.Out, "# Dry run: no controls were mutated; active controls remain %s.\n", strings.Join(activeNames, ", "))
		return nil
	}
	readback, err := backend.ListControls(ctx)
	if err != nil {
		return fmt.Errorf("read back AgentRunControls after resume: %w", err)
	}
	remaining := activePausedControlNames(readback.Items, application, time.Now().UTC())
	for _, target := range targets {
		for _, name := range remaining {
			if name == target {
				return fmt.Errorf("AgentRunControl %s remains an active pause after resume", target)
			}
		}
	}
	if allControls && len(remaining) > 0 {
		return fmt.Errorf("Application %s still has active Paused AgentRunControls after --all-controls resume: %s", application, strings.Join(remaining, ", "))
	}
	fmt.Fprintf(app.Out, "Application %s has no active Paused AgentRunControls.\n", application)
	return nil
}

func controlSource(name, sourceURL, actor string) *agentsv1alpha1.AgentRunControlSourceSpec {
	name = strings.TrimSpace(name)
	sourceURL = strings.TrimSpace(sourceURL)
	actor = strings.TrimSpace(actor)
	source := &agentsv1alpha1.AgentRunControlSourceSpec{Kind: "Operator"}
	if strings.Contains(strings.ToLower(sourceURL), "github.com/") {
		source.Kind = "PullRequest"
	}
	if name != "" {
		source.Name = name
	}
	if sourceURL != "" {
		source.URL = sourceURL
	}
	if actor != "" {
		source.Actor = actor
	}
	if name == "" && sourceURL == "" && actor == "" {
		return nil
	}
	return source
}

func activePausedControlNames(controls []agentsv1alpha1.AgentRunControl, application string, now time.Time) []string {
	names := []string{}
	for _, control := range controls {
		if strings.TrimSpace(control.Spec.ApplicationRef.Name) != application || control.Spec.LaunchPolicy != agentsv1alpha1.AgentRunControlLaunchPolicyPaused || strings.TrimSpace(control.Spec.Reason) == "" {
			continue
		}
		if control.Spec.ExpiresAt != nil && !now.Before(control.Spec.ExpiresAt.Time) {
			continue
		}
		names = append(names, control.Name)
	}
	sort.Strings(names)
	return names
}

func writeControlTable(writer io.Writer, controls []agentsv1alpha1.AgentRunControl) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tAPPLICATION\tPOLICY\tPHASE\tSCHEDULES\tPENDING\tACTIVE\tEXPIRES")
	for i := range controls {
		control := &controls[i]
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			control.Name,
			valueOrDash(control.Spec.ApplicationRef.Name),
			valueOrDash(string(control.Spec.LaunchPolicy)),
			valueOrDash(string(control.Status.Phase)),
			control.Status.AffectedScheduleCount,
			control.Status.PendingRunCount,
			control.Status.ActiveRunCount,
			timeValue(control.Spec.ExpiresAt),
		)
	}
	return table.Flush()
}

func writeControlSummary(writer io.Writer, control *agentsv1alpha1.AgentRunControl) {
	fmt.Fprintln(writer, "CONTROL")
	fmt.Fprintf(writer, "  Name: %s\n", control.Name)
	fmt.Fprintf(writer, "  Application: %s\n", valueOrDash(control.Spec.ApplicationRef.Name))
	fmt.Fprintf(writer, "  Policy: %s\n", valueOrDash(string(control.Spec.LaunchPolicy)))
	fmt.Fprintf(writer, "  Phase: %s\n", valueOrDash(string(control.Status.Phase)))
	fmt.Fprintf(writer, "  Reason: %s\n", valueOrDash(control.Spec.Reason))
	fmt.Fprintf(writer, "  Expires: %s\n", timeValue(control.Spec.ExpiresAt))
	fmt.Fprintf(writer, "  Schedules: %d Pending: %d Active: %d\n", control.Status.AffectedScheduleCount, control.Status.PendingRunCount, control.Status.ActiveRunCount)
	if control.Spec.Source != nil {
		fmt.Fprintln(writer, "  Source:")
		fmt.Fprintf(writer, "    kind=%s name=%s url=%s actor=%s\n",
			valueOrDash(control.Spec.Source.Kind),
			valueOrDash(control.Spec.Source.Name),
			valueOrDash(control.Spec.Source.URL),
			valueOrDash(control.Spec.Source.Actor),
		)
	}
	for _, condition := range control.Status.Conditions {
		fmt.Fprintf(writer, "  Condition: %s=%s reason=%s message=%s\n", valueOrDash(condition.Type), valueOrDash(string(condition.Status)), valueOrDash(condition.Reason), valueOrDash(condition.Message))
	}
}
