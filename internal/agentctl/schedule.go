package agentctl

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	schedulePauseReasonAnnotation    = "control.anvil.hazyforge.io/pause-reason"
	schedulePauseChangedAtAnnotation = "control.anvil.hazyforge.io/pause-changed-at"
)

func writeScheduleUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Schedule commands: list, get, suspend, resume, run-now")
	fmt.Fprintln(writer, "  anvil-agentctl schedule list [-n NS|-A] [-o table|yaml|json]")
	fmt.Fprintln(writer, "  anvil-agentctl schedule get NAME [-n NS] [-o summary|yaml|json]")
	fmt.Fprintln(writer, "  anvil-agentctl schedule suspend NAME --reason TEXT [-n NS]")
	fmt.Fprintln(writer, "  anvil-agentctl schedule resume NAME --reason TEXT [-n NS]")
	fmt.Fprintln(writer, "  anvil-agentctl schedule run-now NAME [-n NS] [--template NAME]")
}

func (app App) runSchedule(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	if len(args) == 0 {
		writeScheduleUsage(app.Err)
		return &usageError{message: "a schedule command is required"}
	}
	switch args[0] {
	case "list":
		return app.scheduleList(ctx, kubeOptions, args[1:])
	case "get":
		return app.scheduleGet(ctx, kubeOptions, args[1:])
	case "suspend":
		return app.scheduleSetSuspend(ctx, kubeOptions, args[1:], true)
	case "resume":
		return app.scheduleSetSuspend(ctx, kubeOptions, args[1:], false)
	case "run-now":
		return app.scheduleRunNow(ctx, kubeOptions, args[1:])
	case "help":
		writeScheduleUsage(app.Out)
		return nil
	default:
		writeScheduleUsage(app.Err)
		return &usageError{message: fmt.Sprintf("unknown schedule command %q", args[0])}
	}
}

func (app App) scheduleList(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var namespace, output, application string
	var allNamespaces bool
	flags := newCommandFlags("schedule list", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	flags.BoolVarP(&allNamespaces, "all-namespaces", "A", false, "List AgentSchedules across all namespaces allowed by caller RBAC.")
	flags.StringVar(&application, "application", "", "Optional opaque Application scope filter.")
	flags.StringVarP(&output, "output", "o", "", "Output format: table, yaml, or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: "schedule list does not accept positional arguments"}
	}
	if allNamespaces && strings.TrimSpace(namespace) != "" {
		return &usageError{message: "use either --namespace or --all-namespaces, not both"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	if namespace = resolvedNamespace(namespace, backend); allNamespaces {
		namespace = ""
	}
	list, err := backend.ListSchedules(ctx, namespace, allNamespaces)
	if err != nil {
		return err
	}
	if application = strings.TrimSpace(application); application != "" {
		filtered := list.Items[:0]
		for _, schedule := range list.Items {
			if scheduleApplicationName(schedule) == application {
				filtered = append(filtered, schedule)
			}
		}
		list.Items = filtered
	}
	sort.Slice(list.Items, func(i, j int) bool {
		if list.Items[i].Namespace != list.Items[j].Namespace {
			return list.Items[i].Namespace < list.Items[j].Namespace
		}
		return list.Items[i].Name < list.Items[j].Name
	})
	if output == "yaml" || output == "json" {
		list.TypeMeta = metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentScheduleList"}
		return writeObject(app.Out, list, output)
	}
	if output != "" && output != "table" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", output)}
	}
	return writeScheduleTable(app.Out, list.Items, allNamespaces)
}

func (app App) scheduleGet(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var namespace, output string
	flags := newCommandFlags("schedule get", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	flags.StringVarP(&output, "output", "o", "", "Output format: summary, yaml, or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return &usageError{message: "schedule get requires exactly one AgentSchedule name"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	schedule, err := backend.GetSchedule(ctx, resolvedNamespace(namespace, backend), flags.Arg(0))
	if err != nil {
		return err
	}
	if output == "yaml" || output == "json" {
		return writeObject(app.Out, schedule, output)
	}
	if output != "" && output != "summary" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", output)}
	}
	writeScheduleSummary(app.Out, schedule)
	return nil
}

func (app App) scheduleSetSuspend(ctx context.Context, kubeOptions KubeOptions, args []string, suspend bool) error {
	commandName := "schedule resume"
	if suspend {
		commandName = "schedule suspend"
	}
	var namespace, reason string
	flags := newCommandFlags(commandName, app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	flags.StringVar(&reason, "reason", "", "Auditable reason for the suspend or resume (required).")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return &usageError{message: commandName + " requires exactly one AgentSchedule name"}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return &usageError{message: "--reason is required"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	ns := resolvedNamespace(namespace, backend)
	name := flags.Arg(0)
	schedule, err := backend.GetSchedule(ctx, ns, name)
	if err != nil {
		return err
	}
	if schedule.Annotations == nil {
		schedule.Annotations = map[string]string{}
	}
	schedule.Annotations[schedulePauseReasonAnnotation] = reason
	schedule.Annotations[schedulePauseChangedAtAnnotation] = time.Now().UTC().Format(time.RFC3339)
	schedule.Spec.Suspend = suspend
	if err := backend.UpdateSchedule(ctx, schedule); err != nil {
		return err
	}
	return writeObject(app.Out, schedule, "name")
}

func (app App) scheduleRunNow(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var namespace, template string
	flags := newCommandFlags("schedule run-now", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	flags.StringVar(&template, "template", "", "Named run template to select for this nudge.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return &usageError{message: "schedule run-now requires exactly one AgentSchedule name"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	ns := resolvedNamespace(namespace, backend)
	name := flags.Arg(0)
	schedule, err := backend.GetSchedule(ctx, ns, name)
	if err != nil {
		return err
	}
	if schedule.Annotations == nil {
		schedule.Annotations = map[string]string{}
	}
	schedule.Annotations[agentsv1alpha1.AgentScheduleRunNowAnnotation] = time.Now().UTC().Format(time.RFC3339Nano)
	delete(schedule.Annotations, agentsv1alpha1.AgentScheduleRunTemplateAnnotation)
	if template = strings.TrimSpace(template); template != "" {
		schedule.Annotations[agentsv1alpha1.AgentScheduleRunTemplateAnnotation] = template
	}
	if err := backend.UpdateSchedule(ctx, schedule); err != nil {
		return err
	}
	return writeObject(app.Out, schedule, "name")
}

func scheduleApplicationName(schedule agentsv1alpha1.AgentSchedule) string {
	if schedule.Spec.ApplicationRef != nil {
		return strings.TrimSpace(schedule.Spec.ApplicationRef.Name)
	}
	return ""
}

func writeScheduleTable(writer io.Writer, schedules []agentsv1alpha1.AgentSchedule, allNamespaces bool) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if allNamespaces {
		fmt.Fprintln(table, "NAMESPACE\tNAME\tAPPLICATION\tPHASE\tSUSPEND\tINTERVAL\tNEXT RUN")
	} else {
		fmt.Fprintln(table, "NAME\tAPPLICATION\tPHASE\tSUSPEND\tINTERVAL\tNEXT RUN")
	}
	for i := range schedules {
		schedule := &schedules[i]
		application := scheduleApplicationName(*schedule)
		if application == "" {
			application = "-"
		}
		next := "-"
		if schedule.Status.NextRunAt != nil && !schedule.Status.NextRunAt.IsZero() {
			next = schedule.Status.NextRunAt.UTC().Format(time.RFC3339)
		}
		phase := string(schedule.Status.Phase)
		if phase == "" {
			phase = "-"
		}
		if allNamespaces {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%t\t%ds\t%s\n",
				terminalSafe(schedule.Namespace),
				terminalSafe(schedule.Name),
				terminalSafe(application),
				terminalSafe(phase),
				schedule.Spec.Suspend,
				schedule.Spec.IntervalSeconds,
				next,
			)
			continue
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%t\t%ds\t%s\n",
			terminalSafe(schedule.Name),
			terminalSafe(application),
			terminalSafe(phase),
			schedule.Spec.Suspend,
			schedule.Spec.IntervalSeconds,
			next,
		)
	}
	return table.Flush()
}

func writeScheduleSummary(writer io.Writer, schedule *agentsv1alpha1.AgentSchedule) {
	application := scheduleApplicationName(*schedule)
	if application == "" {
		application = "-"
	}
	fmt.Fprintf(writer, "Name:         %s\n", terminalSafe(schedule.Name))
	fmt.Fprintf(writer, "Namespace:    %s\n", terminalSafe(schedule.Namespace))
	fmt.Fprintf(writer, "Application:  %s\n", terminalSafe(application))
	fmt.Fprintf(writer, "Phase:        %s\n", terminalSafe(string(schedule.Status.Phase)))
	fmt.Fprintf(writer, "Suspend:      %t\n", schedule.Spec.Suspend)
	fmt.Fprintf(writer, "Interval:     %ds\n", schedule.Spec.IntervalSeconds)
	fmt.Fprintf(writer, "Next Run:     %s\n", timeValue(schedule.Status.NextRunAt))
	if active := referenceName(schedule.Status.ActiveRunRef); active != "-" {
		fmt.Fprintf(writer, "Active Run:   %s\n", active)
	}
	if reason := strings.TrimSpace(schedule.Annotations[schedulePauseReasonAnnotation]); reason != "" {
		fmt.Fprintf(writer, "Pause Reason: %s\n", terminalSafe(reason))
	}
}
