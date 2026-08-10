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
	chainPauseReasonAnnotation    = "control.anvil.hazyforge.io/pause-reason"
	chainPauseChangedAtAnnotation = "control.anvil.hazyforge.io/pause-changed-at"
)

func writeChainUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Chain commands: list, get, suspend, resume, start")
	fmt.Fprintln(writer, "  anvil-agentctl chain list [-n NS|-A] [-o table|yaml|json]")
	fmt.Fprintln(writer, "  anvil-agentctl chain get NAME [-n NS] [-o summary|yaml|json]")
	fmt.Fprintln(writer, "  anvil-agentctl chain suspend NAME --reason TEXT [-n NS]")
	fmt.Fprintln(writer, "  anvil-agentctl chain resume NAME --reason TEXT [-n NS]")
	fmt.Fprintln(writer, "  anvil-agentctl chain start NAME [-n NS]")
}

func (app App) runChain(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	if len(args) == 0 {
		writeChainUsage(app.Err)
		return &usageError{message: "a chain command is required"}
	}
	switch args[0] {
	case "list":
		return app.chainList(ctx, kubeOptions, args[1:])
	case "get":
		return app.chainGet(ctx, kubeOptions, args[1:])
	case "suspend":
		return app.chainSetSuspend(ctx, kubeOptions, args[1:], true)
	case "resume":
		return app.chainSetSuspend(ctx, kubeOptions, args[1:], false)
	case "start":
		return app.chainStart(ctx, kubeOptions, args[1:])
	case "help":
		writeChainUsage(app.Out)
		return nil
	default:
		writeChainUsage(app.Err)
		return &usageError{message: fmt.Sprintf("unknown chain command %q", args[0])}
	}
}

func (app App) chainList(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var namespace, output, application string
	var allNamespaces bool
	flags := newCommandFlags("chain list", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	flags.BoolVarP(&allNamespaces, "all-namespaces", "A", false, "List AgentChains across all namespaces allowed by caller RBAC.")
	flags.StringVar(&application, "application", "", "Optional opaque Application scope filter.")
	flags.StringVarP(&output, "output", "o", "", "Output format: table, yaml, or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: "chain list does not accept positional arguments"}
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
	list, err := backend.ListChains(ctx, namespace, allNamespaces)
	if err != nil {
		return err
	}
	if application = strings.TrimSpace(application); application != "" {
		filtered := list.Items[:0]
		for _, chain := range list.Items {
			if chainApplicationName(chain) == application {
				filtered = append(filtered, chain)
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
		list.TypeMeta = metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentChainList"}
		return writeObject(app.Out, list, output)
	}
	if output != "" && output != "table" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", output)}
	}
	return writeChainTable(app.Out, list.Items, allNamespaces)
}

func (app App) chainGet(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var namespace, output string
	flags := newCommandFlags("chain get", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	flags.StringVarP(&output, "output", "o", "", "Output format: summary, yaml, or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return &usageError{message: "chain get requires exactly one AgentChain name"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	chain, err := backend.GetChain(ctx, resolvedNamespace(namespace, backend), flags.Arg(0))
	if err != nil {
		return err
	}
	if output == "yaml" || output == "json" {
		return writeObject(app.Out, chain, output)
	}
	if output != "" && output != "summary" {
		return &usageError{message: fmt.Sprintf("unsupported output format %q", output)}
	}
	writeChainSummary(app.Out, chain)
	return nil
}

func (app App) chainSetSuspend(ctx context.Context, kubeOptions KubeOptions, args []string, suspend bool) error {
	commandName := "chain resume"
	if suspend {
		commandName = "chain suspend"
	}
	var namespace, reason string
	flags := newCommandFlags(commandName, app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	flags.StringVar(&reason, "reason", "", "Operator reason recorded on the object.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return &usageError{message: commandName + " requires exactly one AgentChain name"}
	}
	if strings.TrimSpace(reason) == "" {
		return &usageError{message: "--reason is required"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	ns := resolvedNamespace(namespace, backend)
	name := flags.Arg(0)
	chain, err := backend.GetChain(ctx, ns, name)
	if err != nil {
		return err
	}
	if chain.Annotations == nil {
		chain.Annotations = map[string]string{}
	}
	chain.Annotations[chainPauseReasonAnnotation] = reason
	chain.Annotations[chainPauseChangedAtAnnotation] = time.Now().UTC().Format(time.RFC3339)
	chain.Spec.Suspend = suspend
	if err := backend.UpdateChain(ctx, chain); err != nil {
		return err
	}
	return writeObject(app.Out, chain, "name")
}

func (app App) chainStart(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var namespace string
	flags := newCommandFlags("chain start", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace; defaults to the current kubeconfig context.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return &usageError{message: "chain start requires exactly one AgentChain name"}
	}
	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	ns := resolvedNamespace(namespace, backend)
	name := flags.Arg(0)
	chain, err := backend.GetChain(ctx, ns, name)
	if err != nil {
		return err
	}
	if chain.Annotations == nil {
		chain.Annotations = map[string]string{}
	}
	chain.Annotations[agentsv1alpha1.AgentChainStartNowAnnotation] = time.Now().UTC().Format(time.RFC3339Nano)
	if err := backend.UpdateChain(ctx, chain); err != nil {
		return err
	}
	return writeObject(app.Out, chain, "name")
}

func chainApplicationName(chain agentsv1alpha1.AgentChain) string {
	if chain.Spec.ApplicationRef != nil {
		return strings.TrimSpace(chain.Spec.ApplicationRef.Name)
	}
	return ""
}

func writeChainTable(writer io.Writer, chains []agentsv1alpha1.AgentChain, allNamespaces bool) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if allNamespaces {
		fmt.Fprintln(table, "NAMESPACE\tNAME\tAPPLICATION\tPHASE\tSUSPEND\tACTIVE STEP\tINSTANCE")
	} else {
		fmt.Fprintln(table, "NAME\tAPPLICATION\tPHASE\tSUSPEND\tACTIVE STEP\tINSTANCE")
	}
	for i := range chains {
		chain := &chains[i]
		application := chainApplicationName(*chain)
		if application == "" {
			application = "-"
		}
		phase := string(chain.Status.Phase)
		if phase == "" {
			phase = "-"
		}
		step := chain.Status.ActiveStep
		if step == "" {
			step = "-"
		}
		instance := chain.Status.ActiveInstanceID
		if instance == "" {
			instance = "-"
		}
		if allNamespaces {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
				terminalSafe(chain.Namespace),
				terminalSafe(chain.Name),
				terminalSafe(application),
				terminalSafe(phase),
				chain.Spec.Suspend,
				terminalSafe(step),
				terminalSafe(instance),
			)
			continue
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%t\t%s\t%s\n",
			terminalSafe(chain.Name),
			terminalSafe(application),
			terminalSafe(phase),
			chain.Spec.Suspend,
			terminalSafe(step),
			terminalSafe(instance),
		)
	}
	return table.Flush()
}

func writeChainSummary(writer io.Writer, chain *agentsv1alpha1.AgentChain) {
	fmt.Fprintf(writer, "Name:\t%s/%s\n", chain.Namespace, chain.Name)
	fmt.Fprintf(writer, "Application:\t%s\n", orDash(chainApplicationName(*chain)))
	fmt.Fprintf(writer, "Phase:\t%s\n", orDash(string(chain.Status.Phase)))
	fmt.Fprintf(writer, "Suspend:\t%t\n", chain.Spec.Suspend)
	fmt.Fprintf(writer, "ActiveInstance:\t%s\n", orDash(chain.Status.ActiveInstanceID))
	fmt.Fprintf(writer, "ActiveStep:\t%s\n", orDash(chain.Status.ActiveStep))
	if chain.Status.ActiveRunRef != nil {
		fmt.Fprintf(writer, "ActiveRun:\t%s/%s\n", chain.Status.ActiveRunRef.Namespace, chain.Status.ActiveRunRef.Name)
	} else {
		fmt.Fprintf(writer, "ActiveRun:\t-\n")
	}
	fmt.Fprintf(writer, "Steps:\t%d\n", len(chain.Spec.Steps))
	for i, step := range chain.Spec.Steps {
		fmt.Fprintf(writer, "  [%d] %s\n", i, step.Name)
	}
	if chain.Status.LastError != "" {
		fmt.Fprintf(writer, "LastError:\t%s\n", terminalSafe(chain.Status.LastError))
	}
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
