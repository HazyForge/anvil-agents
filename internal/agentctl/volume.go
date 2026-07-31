package agentctl

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func writeVolumeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Volume commands: copy")
	fmt.Fprintln(writer, "  volume copy -n NS --from SOURCE --to DEST --node HOSTNAME [--generate-name PREFIX]")
	fmt.Fprintln(writer, "    Create an append-only AgentDataVolumeCopy that streams source claim bytes")
	fmt.Fprintln(writer, "    onto a new destination AgentDataVolume bound on the target node.")
}

func (app App) runVolume(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	if len(args) == 0 {
		writeVolumeUsage(app.Err)
		return &usageError{message: "a volume command is required"}
	}
	switch args[0] {
	case "copy":
		return app.volumeCopy(ctx, kubeOptions, args[1:])
	case "help":
		writeVolumeUsage(app.Out)
		return nil
	default:
		writeVolumeUsage(app.Err)
		return &usageError{message: fmt.Sprintf("unknown volume command %q", args[0])}
	}
}

func (app App) volumeCopy(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	var (
		namespace        string
		name             string
		generateName     string
		from             string
		to               string
		node             string
		timeout          = 45 * time.Minute
		allowNonEmpty    bool
		noVerify         bool
		storageClassName string
		wait             = true
		output           string
	)
	flags := newCommandFlags("volume copy", app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace containing the AgentDataVolumes (required).")
	flags.StringVar(&name, "name", "", "Exact AgentDataVolumeCopy name.")
	flags.StringVar(&generateName, "generate-name", "", "Server-generated name prefix (default volume-copy-).")
	flags.StringVar(&from, "from", "", "Source AgentDataVolume name (required).")
	flags.StringVar(&to, "to", "", "Destination AgentDataVolume name (required, must differ).")
	flags.StringVar(&node, "node", "", "Destination kubernetes.io/hostname for local-path first-consumer binding (required).")
	flags.StringVar(&storageClassName, "storage-class", "", "Optional destination storage class override.")
	flags.BoolVar(&allowNonEmpty, "allow-non-empty", false, "Allow overwriting a non-empty destination.")
	flags.BoolVar(&noVerify, "no-verify", false, "Skip post-copy file-count verification.")
	flags.DurationVar(&timeout, "timeout", timeout, "How long to wait for the copy to finish.")
	flags.BoolVar(&wait, "wait", true, "Wait for the copy to reach a terminal phase.")
	flags.StringVarP(&output, "output", "o", "", "Optional output format: json|yaml.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: "volume copy does not accept positional arguments"}
	}
	if strings.TrimSpace(namespace) == "" {
		return &usageError{message: "--namespace is required"}
	}
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return &usageError{message: "--from and --to are required"}
	}
	if strings.TrimSpace(from) == strings.TrimSpace(to) {
		return &usageError{message: "--from and --to must differ"}
	}
	if strings.TrimSpace(node) == "" {
		return &usageError{message: "--node is required (destination kubernetes.io/hostname)"}
	}
	if strings.TrimSpace(name) == "" && strings.TrimSpace(generateName) == "" {
		generateName = "volume-copy-"
	}
	if strings.TrimSpace(name) != "" && strings.TrimSpace(generateName) != "" {
		return &usageError{message: "use either --name or --generate-name, not both"}
	}

	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	if _, err := backend.GetDataVolume(ctx, namespace, from); err != nil {
		return err
	}

	verify := !noVerify
	copyObj := &agentsv1alpha1.AgentDataVolumeCopy{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentDataVolumeCopy"},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			Name:         strings.TrimSpace(name),
			GenerateName: strings.TrimSpace(generateName),
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "anvil-agentctl",
				"app.kubernetes.io/component":  "agent-data-volume-copy",
			},
		},
		Spec: agentsv1alpha1.AgentDataVolumeCopySpec{
			SourceRef: corev1.LocalObjectReference{Name: strings.TrimSpace(from)},
			Destination: agentsv1alpha1.AgentDataVolumeCopyDestination{
				Name:             strings.TrimSpace(to),
				NodeSelector:     map[string]string{"kubernetes.io/hostname": strings.TrimSpace(node)},
				StorageClassName: strings.TrimSpace(storageClassName),
				Notes:            fmt.Sprintf("Created by anvil-agentctl volume copy for placement on %s.", strings.TrimSpace(node)),
			},
			Method:                   agentsv1alpha1.AgentDataVolumeCopyMethodStream,
			AllowNonEmptyDestination: allowNonEmpty,
			Verify:                   &verify,
			TimeoutSeconds:           int32(timeout.Seconds()),
		},
	}
	if copyObj.Spec.TimeoutSeconds < 60 {
		copyObj.Spec.TimeoutSeconds = 60
	}

	if err := backend.CreateDataVolumeCopy(ctx, copyObj); err != nil {
		return err
	}
	createdName := copyObj.Name
	fmt.Fprintf(app.Out, "created AgentDataVolumeCopy %s/%s\n", namespace, createdName)

	if !wait {
		return writeVolumeCopyResult(app.Out, copyObj, output)
	}
	finished, err := app.waitVolumeCopy(ctx, backend, namespace, createdName, timeout)
	if err != nil {
		return err
	}
	return writeVolumeCopyResult(app.Out, finished, output)
}

func (app App) waitVolumeCopy(ctx context.Context, backend Backend, namespace, name string, timeout time.Duration) (*agentsv1alpha1.AgentDataVolumeCopy, error) {
	deadline := time.Now().Add(timeout)
	var last *agentsv1alpha1.AgentDataVolumeCopy
	for {
		copyObj, err := backend.GetDataVolumeCopy(ctx, namespace, name)
		if err != nil {
			return last, err
		}
		last = copyObj
		if agentsv1alpha1.AgentDataVolumeCopyIsTerminal(copyObj.Status.Phase) {
			if copyObj.Status.Phase == agentsv1alpha1.AgentDataVolumeCopyPhaseFailed {
				return copyObj, fmt.Errorf("AgentDataVolumeCopy %s/%s failed: %s", namespace, name, firstNonEmpty(copyObj.Status.LastError, string(copyObj.Status.Phase)))
			}
			return copyObj, nil
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("AgentDataVolumeCopy %s/%s did not finish within %s (phase=%s)", namespace, name, timeout, valueOrDash(string(copyObj.Status.Phase)))
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(app.pollInterval()):
		}
	}
}

func writeVolumeCopyResult(writer io.Writer, copyObj *agentsv1alpha1.AgentDataVolumeCopy, output string) error {
	if output == "json" || output == "yaml" {
		return writeObject(writer, copyObj, output)
	}
	fmt.Fprintf(writer, "AgentDataVolumeCopy %s/%s phase=%s source=%s dest=%s sourceNode=%s destNode=%s\n",
		copyObj.Namespace,
		copyObj.Name,
		valueOrDash(string(copyObj.Status.Phase)),
		copyObj.Spec.SourceRef.Name,
		copyObj.Spec.Destination.Name,
		valueOrDash(copyObj.Status.SourceNode),
		valueOrDash(copyObj.Status.DestinationNode),
	)
	if strings.TrimSpace(copyObj.Status.LastError) != "" {
		fmt.Fprintf(writer, "lastError=%s\n", copyObj.Status.LastError)
	}
	fmt.Fprintln(writer, "Next: point the harness dataVolumeRefs at the destination volume after verifying contents, then retire the source through reviewed GitOps.")
	return nil
}

