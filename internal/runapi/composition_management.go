package runapi

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// LabelManagedBy marks composition objects the console/API may mutate.
	// GitOps objects must not carry this value unless operators deliberately
	// migrate them outside the API; GitOps markers still win if present.
	LabelManagedBy = "control.anvil.hazyforge.io/managed-by"
	// ManagedByConsole is the only management value that allows API writes.
	ManagedByConsole = "anvil-agents-console"

	managementReasonConsoleManaged   = "console_managed"
	managementReasonGitOpsProtected  = "gitops_protected"
	managementReasonNotConsoleManaged = "not_console_managed"
)

// CompositionManagement is computed for each composition object so the UI can
// hide Save/Delete without attempting a failing write.
type CompositionManagement struct {
	Writable  bool   `json:"writable"`
	Reason    string `json:"reason"`
	ManagedBy string `json:"managedBy,omitempty"`
}

// evaluateCompositionManagement decides whether the API may mutate an object.
// GitOps is source of truth: any well-known GitOps ownership signal blocks
// writes. Otherwise only objects stamped managed-by=anvil-agents-console are
// writable.
func evaluateCompositionManagement(meta metav1.Object) CompositionManagement {
	if meta == nil {
		return CompositionManagement{Writable: false, Reason: managementReasonNotConsoleManaged}
	}
	if manager, ok := detectGitOpsManager(meta); ok {
		return CompositionManagement{
			Writable:  false,
			Reason:    managementReasonGitOpsProtected,
			ManagedBy: manager,
		}
	}
	labels := meta.GetLabels()
	if labels != nil && labels[LabelManagedBy] == ManagedByConsole {
		return CompositionManagement{
			Writable:  true,
			Reason:    managementReasonConsoleManaged,
			ManagedBy: ManagedByConsole,
		}
	}
	managedBy := ""
	if labels != nil {
		managedBy = strings.TrimSpace(labels[LabelManagedBy])
	}
	if managedBy == "" {
		managedBy = "unmanaged"
	}
	return CompositionManagement{
		Writable:  false,
		Reason:    managementReasonNotConsoleManaged,
		ManagedBy: managedBy,
	}
}

// stampConsoleManaged sets the console management label and strips client-supplied
// managed-by / GitOps ownership markers so creates cannot self-claim GitOps
// identity or forge management.
func stampConsoleManaged(meta metav1.Object) {
	if meta == nil {
		return
	}
	labels := meta.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	} else {
		labels = copyStringMap(labels)
	}
	labels[LabelManagedBy] = ManagedByConsole
	// Prevent clients from carrying Argo/Flux instance labels onto console creates.
	for key := range labels {
		if isGitOpsLabelKey(key) {
			delete(labels, key)
		}
	}
	// Drop generic managed-by values that are not ours after we set console.
	if managedBy, ok := labels["app.kubernetes.io/managed-by"]; ok && !strings.EqualFold(managedBy, ManagedByConsole) {
		delete(labels, "app.kubernetes.io/managed-by")
	}
	meta.SetLabels(labels)

	annotations := meta.GetAnnotations()
	if annotations == nil {
		return
	}
	annotations = copyStringMap(annotations)
	for key := range annotations {
		if isGitOpsAnnotationKey(key) {
			delete(annotations, key)
		}
	}
	if len(annotations) == 0 {
		meta.SetAnnotations(nil)
		return
	}
	meta.SetAnnotations(annotations)
}

func detectGitOpsManager(meta metav1.Object) (string, bool) {
	labels := meta.GetLabels()
	annotations := meta.GetAnnotations()

	if labels != nil {
		if value := strings.TrimSpace(labels["argocd.argoproj.io/instance"]); value != "" {
			return "argocd", true
		}
		if value := strings.TrimSpace(labels["kustomize.toolkit.fluxcd.io/name"]); value != "" {
			return "flux", true
		}
		if value := strings.TrimSpace(labels["helm.toolkit.fluxcd.io/name"]); value != "" {
			return "flux", true
		}
		if value := strings.TrimSpace(labels["app.kubernetes.io/managed-by"]); value != "" {
			switch strings.ToLower(value) {
			case "argocd", "helm", "flux", "kustomize-controller", "helm-controller", "kustomize":
				return strings.ToLower(value), true
			}
		}
		for key := range labels {
			if isGitOpsLabelKey(key) {
				return gitOpsFamilyFromKey(key), true
			}
		}
	}
	if annotations != nil {
		if value := strings.TrimSpace(annotations["argocd.argoproj.io/tracking-id"]); value != "" {
			return "argocd", true
		}
		if value := strings.TrimSpace(annotations["meta.helm.sh/release-name"]); value != "" {
			return "helm", true
		}
		if value := strings.TrimSpace(annotations["meta.helm.sh/release-namespace"]); value != "" {
			return "helm", true
		}
		for key := range annotations {
			if isGitOpsAnnotationKey(key) {
				return gitOpsFamilyFromKey(key), true
			}
		}
	}
	return "", false
}

func isGitOpsLabelKey(key string) bool {
	switch {
	case strings.HasPrefix(key, "argocd.argoproj.io/"):
		return true
	case strings.HasPrefix(key, "kustomize.toolkit.fluxcd.io/"):
		return true
	case strings.HasPrefix(key, "helm.toolkit.fluxcd.io/"):
		return true
	case strings.HasPrefix(key, "helm.fluxcd.io/"):
		return true
	default:
		return false
	}
}

func isGitOpsAnnotationKey(key string) bool {
	switch {
	case strings.HasPrefix(key, "argocd.argoproj.io/"):
		return true
	case strings.HasPrefix(key, "meta.helm.sh/"):
		return true
	case strings.HasPrefix(key, "kustomize.toolkit.fluxcd.io/"):
		return true
	case strings.HasPrefix(key, "helm.toolkit.fluxcd.io/"):
		return true
	default:
		return false
	}
}

func gitOpsFamilyFromKey(key string) string {
	switch {
	case strings.Contains(key, "argocd"):
		return "argocd"
	case strings.Contains(key, "flux"):
		return "flux"
	case strings.Contains(key, "helm"):
		return "helm"
	default:
		return "gitops"
	}
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
