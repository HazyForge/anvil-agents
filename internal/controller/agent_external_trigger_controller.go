package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	agentExternalTriggerReady              = "Ready"
	agentExternalTriggerHTTPRouteFinalizer = "control.anvil.hazyforge.io/external-trigger-httproute"
)

// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentexternaltriggers,verbs=get;list;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentexternaltriggers/status,verbs=get;patch;update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentexternaltriggers/finalizers,verbs=update
// +kubebuilder:rbac:groups="gateway.networking.k8s.io",resources=httproutes,verbs=create;delete;get;list;patch;update;watch
type AgentExternalTriggerReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	ExternalTriggersEnabled bool
	HTTPRoute               ExternalTriggerHTTPRouteConfig
}

func (r *AgentExternalTriggerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &controlv1alpha1.AgentExternalTrigger{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		if controllerutil.ContainsFinalizer(obj, agentExternalTriggerHTTPRouteFinalizer) || r.managesHTTPRoutes() {
			if err := r.deleteManagedHTTPRoutes(ctx, obj); err != nil {
				return ctrl.Result{}, err
			}
		}
		if controllerutil.ContainsFinalizer(obj, agentExternalTriggerHTTPRouteFinalizer) {
			return r.removeHTTPRouteFinalizer(ctx, client.ObjectKeyFromObject(obj))
		}
		return ctrl.Result{}, nil
	}
	if r.managesHTTPRoutes() && !controllerutil.ContainsFinalizer(obj, agentExternalTriggerHTTPRouteFinalizer) {
		original := obj.DeepCopy()
		controllerutil.AddFinalizer(obj, agentExternalTriggerHTTPRouteFinalizer)
		if err := r.Patch(ctx, obj, client.MergeFrom(original)); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("install AgentExternalTrigger HTTPRoute finalizer: %w", err)
		}
	}

	original := obj.DeepCopy()
	status := obj.Status
	status.ObservedGeneration = obj.Generation
	now := metav1.Now()

	if strings.TrimSpace(status.ReceiverID) == "" {
		receiverID, err := newExternalTriggerReceiverID()
		if err != nil {
			return ctrl.Result{}, err
		}
		status.ReceiverID = receiverID
	}
	status.WebhookPath = externalTriggerWebhookPath(obj.Namespace, obj.Name, status.ReceiverID)

	blockReason, blockMessage := validateAgentExternalTriggerSpec(obj)
	exposeRoute := false
	switch {
	case blockReason != "":
		status.Phase = controlv1alpha1.AgentExternalTriggerPhaseBlocked
		status.LastError = blockMessage
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentExternalTriggerReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             blockReason,
			Message:            blockMessage,
		})
	case obj.Spec.Suspend:
		status.Phase = controlv1alpha1.AgentExternalTriggerPhaseSuspended
		status.LastError = ""
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentExternalTriggerReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "Suspended",
			Message:            "AgentExternalTrigger is suspended.",
		})
	default:
		status.Phase = controlv1alpha1.AgentExternalTriggerPhaseReady
		status.LastError = ""
		exposeRoute = true
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentExternalTriggerReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "ReceiverReady",
			Message:            fmt.Sprintf("Webhook receiver ready at %s.", status.WebhookPath),
		})
	}

	routeResult, err := r.reconcileHTTPRoute(ctx, obj, &status, exposeRoute)
	if err != nil {
		return ctrl.Result{}, err
	}

	obj.Status = status
	if err := r.patchAgentExternalTriggerStatus(ctx, original, obj); err != nil {
		return ctrl.Result{}, err
	}
	return routeResult, nil
}

func (r *AgentExternalTriggerReconciler) patchAgentExternalTriggerStatus(ctx context.Context, original, obj *controlv1alpha1.AgentExternalTrigger) error {
	if apierrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(original), &controlv1alpha1.AgentExternalTrigger{})) {
		return nil
	}
	patched := original.DeepCopy()
	patched.Status = obj.Status
	return r.Status().Patch(ctx, patched, client.MergeFrom(original))
}

func (r *AgentExternalTriggerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&controlv1alpha1.AgentExternalTrigger{})
	if r.managesHTTPRoutes() {
		builder = builder.Watches(&gatewayv1.HTTPRoute{}, handler.EnqueueRequestsFromMapFunc(enqueueAgentExternalTriggerForHTTPRoute))
	}
	return builder.Complete(r)
}

func enqueueAgentExternalTriggerForHTTPRoute(_ context.Context, obj client.Object) []reconcile.Request {
	route, ok := obj.(*gatewayv1.HTTPRoute)
	if !ok || route == nil {
		return nil
	}
	if route.Labels[agentManagedByLabel] != "anvil-agents" {
		return nil
	}
	if route.Labels["app.kubernetes.io/component"] != agentExternalTriggerHTTPRouteComponent {
		return nil
	}
	name := strings.TrimSpace(route.Labels[controlv1alpha1.AgentExternalTriggerLabel])
	namespace := strings.TrimSpace(route.Labels[controlv1alpha1.AgentExternalTriggerNamespaceLabel])
	if namespace == "" {
		namespace = route.Namespace
	}
	if name == "" || namespace == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: namespace, Name: name}}}
}

func (r *AgentExternalTriggerReconciler) removeHTTPRouteFinalizer(ctx context.Context, key client.ObjectKey) (ctrl.Result, error) {
	obj := &controlv1alpha1.AgentExternalTrigger{}
	if err := r.Get(ctx, key, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !controllerutil.ContainsFinalizer(obj, agentExternalTriggerHTTPRouteFinalizer) {
		return ctrl.Result{}, nil
	}
	original := obj.DeepCopy()
	controllerutil.RemoveFinalizer(obj, agentExternalTriggerHTTPRouteFinalizer)
	if err := r.Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("remove AgentExternalTrigger HTTPRoute finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func newExternalTriggerReceiverID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate receiverID: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func externalTriggerWebhookPath(namespace, name, receiverID string) string {
	return fmt.Sprintf("/api/v1/external-triggers/%s/%s/%s",
		strings.TrimSpace(namespace),
		strings.TrimSpace(name),
		strings.TrimSpace(receiverID),
	)
}

func validateAgentExternalTriggerSpec(obj *controlv1alpha1.AgentExternalTrigger) (reason, message string) {
	if obj.Spec.Source.Kind != controlv1alpha1.AgentExternalTriggerSourceGitHubWebhook {
		return "UnsupportedSource", "spec.source.kind must be githubWebhook."
	}
	if obj.Spec.Source.GitHub == nil || len(obj.Spec.Source.GitHub.Repositories) == 0 {
		return "InvalidSource", "spec.source.github.repositories must contain at least one owner/name repository."
	}
	for i, repo := range obj.Spec.Source.GitHub.Repositories {
		repo = strings.TrimSpace(repo)
		if repo == "" || !strings.Contains(repo, "/") || strings.HasPrefix(repo, "/") || strings.HasSuffix(repo, "/") {
			return "InvalidRepository", fmt.Sprintf("spec.source.github.repositories[%d] must be owner/name.", i)
		}
	}
	if strings.TrimSpace(obj.Spec.SecretRef.Name) == "" {
		return "InvalidSecretRef", "spec.secretRef.name is required."
	}
	if len(obj.Spec.Targets) == 0 {
		return "InvalidTargets", "spec.targets must contain at least one target."
	}
	for i, target := range obj.Spec.Targets {
		if strings.TrimSpace(target.Name) == "" {
			return "InvalidTarget", fmt.Sprintf("spec.targets[%d].name is required.", i)
		}
		switch target.Kind {
		case controlv1alpha1.AgentExternalTriggerTargetAgentRunProfile,
			controlv1alpha1.AgentExternalTriggerTargetAgentCouncil,
			controlv1alpha1.AgentExternalTriggerTargetAgentRun:
		default:
			return "InvalidTarget", fmt.Sprintf("spec.targets[%d].kind is unsupported.", i)
		}
	}
	if obj.Spec.HTTPRoute != nil {
		if host := strings.TrimSpace(obj.Spec.HTTPRoute.Hostname); host != "" {
			if err := validateExactHostname(host); err != nil {
				return "InvalidHTTPRouteHostname", "spec.httpRoute.hostname must be an exact hostname, never a wildcard."
			}
		}
	}
	if obj.Spec.MaxDeliveriesPerDay < 0 {
		return "InvalidMaxDeliveriesPerDay", "spec.maxDeliveriesPerDay cannot be negative."
	}
	switch obj.Spec.ConcurrencyPolicy {
	case "", controlv1alpha1.AgentExternalTriggerConcurrencyForbid, controlv1alpha1.AgentExternalTriggerConcurrencyAllow:
	default:
		return "InvalidConcurrencyPolicy", "spec.concurrencyPolicy must be Forbid or Allow."
	}
	return "", ""
}
