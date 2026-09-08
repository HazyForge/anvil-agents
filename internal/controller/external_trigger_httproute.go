package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	agentExternalTriggerHTTPRouteReady     = "HTTPRouteReady"
	agentExternalTriggerHTTPRouteComponent = "external-trigger-httproute"
	agentExternalTriggerHTTPRoutePoll      = agentSchedulePollInterval
)

var exactHostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

// ExternalTriggerHTTPRouteParentRef is one Gateway parent for a webhook HTTPRoute.
type ExternalTriggerHTTPRouteParentRef struct {
	Group       string `json:"group,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Name        string `json:"name"`
	SectionName string `json:"sectionName"`
	Port        int32  `json:"port,omitempty"`
}

// ExternalTriggerHTTPRouteConfig is install-time Gateway API configuration for
// controller-owned AgentExternalTrigger HTTPRoutes.
type ExternalTriggerHTTPRouteConfig struct {
	Enabled          bool
	ParentRefs       []ExternalTriggerHTTPRouteParentRef `json:"parentRefs,omitempty"`
	Hostnames        []string                            `json:"hostnames,omitempty"`
	BackendName      string                              `json:"backendName,omitempty"`
	BackendNamespace string                              `json:"backendNamespace,omitempty"`
	BackendPort      int32                               `json:"backendPort,omitempty"`
}

func ParseExternalTriggerHTTPRouteJSON(raw string) (ExternalTriggerHTTPRouteConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return ExternalTriggerHTTPRouteConfig{}, nil
	}
	var cfg ExternalTriggerHTTPRouteConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return ExternalTriggerHTTPRouteConfig{}, fmt.Errorf("parse external trigger HTTPRoute JSON: %w", err)
	}
	cfg.Enabled = false
	return cfg, nil
}

func (cfg ExternalTriggerHTTPRouteConfig) Validate() error {
	if len(cfg.ParentRefs) == 0 {
		return fmt.Errorf("parentRefs must contain at least one Gateway parent")
	}
	for i, parent := range cfg.ParentRefs {
		if strings.TrimSpace(parent.Name) == "" {
			return fmt.Errorf("parentRefs[%d].name is required", i)
		}
		if strings.TrimSpace(parent.SectionName) == "" {
			return fmt.Errorf("parentRefs[%d].sectionName is required", i)
		}
	}
	if len(cfg.Hostnames) == 0 {
		return fmt.Errorf("hostnames must contain an explicit hostname")
	}
	for i, hostname := range cfg.Hostnames {
		if err := validateExactHostname(hostname); err != nil {
			return fmt.Errorf("hostnames[%d]: %w", i, err)
		}
	}
	if strings.TrimSpace(cfg.BackendName) == "" {
		return fmt.Errorf("backendName is required")
	}
	if cfg.BackendPort < 1 || cfg.BackendPort > 65535 {
		return fmt.Errorf("backendPort must be a valid TCP port")
	}
	return nil
}

func validateExactHostname(hostname string) error {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	if strings.Contains(hostname, "*") {
		return fmt.Errorf("hostname %q must be exact, never a wildcard", hostname)
	}
	if !exactHostnamePattern.MatchString(hostname) {
		return fmt.Errorf("hostname %q must be an exact DNS hostname", hostname)
	}
	if net.ParseIP(hostname) != nil {
		return fmt.Errorf("hostname %q must be a DNS name, not an IP", hostname)
	}
	return nil
}

func (r *AgentExternalTriggerReconciler) managesHTTPRoutes() bool {
	return r != nil && r.ExternalTriggersEnabled && r.HTTPRoute.Enabled
}

func agentExternalTriggerHTTPRouteName(obj *controlv1alpha1.AgentExternalTrigger) string {
	return agentRunChildName(obj.Name, "webhook")
}

func agentExternalTriggerPublicURL(hostname, path string) string {
	hostname = strings.TrimSpace(hostname)
	path = strings.TrimSpace(path)
	if hostname == "" || path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "https://" + hostname + path
}

func agentExternalTriggerRouteHostnames(obj *controlv1alpha1.AgentExternalTrigger, cfg ExternalTriggerHTTPRouteConfig) ([]string, error) {
	if obj != nil && obj.Spec.HTTPRoute != nil {
		if host := strings.TrimSpace(obj.Spec.HTTPRoute.Hostname); host != "" {
			if err := validateExactHostname(host); err != nil {
				return nil, err
			}
			return []string{host}, nil
		}
	}
	hostnames := make([]string, 0, len(cfg.Hostnames))
	for _, hostname := range cfg.Hostnames {
		hostname = strings.TrimSpace(hostname)
		if hostname == "" {
			continue
		}
		if err := validateExactHostname(hostname); err != nil {
			return nil, err
		}
		hostnames = append(hostnames, hostname)
	}
	if len(hostnames) == 0 {
		return nil, fmt.Errorf("hostnames must contain an explicit hostname")
	}
	return hostnames, nil
}

func buildAgentExternalTriggerHTTPRoute(obj *controlv1alpha1.AgentExternalTrigger, cfg ExternalTriggerHTTPRouteConfig, hostnames []string, webhookPath string) (*gatewayv1.HTTPRoute, error) {
	if obj == nil {
		return nil, fmt.Errorf("trigger is required")
	}
	path := strings.TrimSpace(webhookPath)
	if path == "" {
		return nil, fmt.Errorf("webhookPath is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if len(hostnames) == 0 {
		return nil, fmt.Errorf("hostnames must contain an explicit hostname")
	}
	for _, hostname := range hostnames {
		if err := validateExactHostname(hostname); err != nil {
			return nil, err
		}
	}
	pathType := gatewayv1.PathMatchExact
	pathValue := path
	port := gatewayv1.PortNumber(cfg.BackendPort)
	backend := gatewayv1.HTTPBackendRef{
		BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{
				Name: gatewayv1.ObjectName(strings.TrimSpace(cfg.BackendName)),
				Port: &port,
			},
		},
	}
	if ns := strings.TrimSpace(cfg.BackendNamespace); ns != "" && ns != obj.Namespace {
		backendNS := gatewayv1.Namespace(ns)
		backend.Namespace = &backendNS
	}
	parents := make([]gatewayv1.ParentReference, 0, len(cfg.ParentRefs))
	for _, parent := range cfg.ParentRefs {
		ref := gatewayv1.ParentReference{
			Name: gatewayv1.ObjectName(strings.TrimSpace(parent.Name)),
		}
		if group := strings.TrimSpace(parent.Group); group != "" {
			value := gatewayv1.Group(group)
			ref.Group = &value
		}
		if kind := strings.TrimSpace(parent.Kind); kind != "" {
			value := gatewayv1.Kind(kind)
			ref.Kind = &value
		}
		if ns := strings.TrimSpace(parent.Namespace); ns != "" {
			value := gatewayv1.Namespace(ns)
			ref.Namespace = &value
		}
		section := gatewayv1.SectionName(strings.TrimSpace(parent.SectionName))
		ref.SectionName = &section
		if parent.Port > 0 {
			value := gatewayv1.PortNumber(parent.Port)
			ref.Port = &value
		}
		parents = append(parents, ref)
	}
	routeHostnames := make([]gatewayv1.Hostname, 0, len(hostnames))
	for _, hostname := range hostnames {
		routeHostnames = append(routeHostnames, gatewayv1.Hostname(hostname))
	}
	return &gatewayv1.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gatewayv1.GroupVersion.String(),
			Kind:       "HTTPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentExternalTriggerHTTPRouteName(obj),
			Namespace: obj.Namespace,
			Labels:    agentExternalTriggerHTTPRouteLabels(obj),
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: parents},
			Hostnames:       routeHostnames,
			Rules: []gatewayv1.HTTPRouteRule{{
				Matches: []gatewayv1.HTTPRouteMatch{{
					Path: &gatewayv1.HTTPPathMatch{
						Type:  &pathType,
						Value: &pathValue,
					},
				}},
				BackendRefs: []gatewayv1.HTTPBackendRef{backend},
			}},
		},
	}, nil
}

func agentExternalTriggerHTTPRouteLabels(obj *controlv1alpha1.AgentExternalTrigger) map[string]string {
	name := sanitizeLabelValue(obj.Name)
	return map[string]string{
		agentManagedByLabel:                       "anvil-agents",
		"app.kubernetes.io/name":                  "anvil-agents-external-trigger",
		"app.kubernetes.io/component":             agentExternalTriggerHTTPRouteComponent,
		controlv1alpha1.AgentExternalTriggerLabel: name,
	}
}

func (r *AgentExternalTriggerReconciler) reconcileHTTPRoute(ctx context.Context, obj *controlv1alpha1.AgentExternalTrigger, status *controlv1alpha1.AgentExternalTriggerStatus, expose bool) (ctrl.Result, error) {
	if !r.managesHTTPRoutes() {
		status.HTTPRoute = nil
		apimeta.RemoveStatusCondition(&status.Conditions, agentExternalTriggerHTTPRouteReady)
		return ctrl.Result{}, nil
	}

	now := metav1.Now()
	if !expose || strings.TrimSpace(status.WebhookPath) == "" || strings.TrimSpace(status.ReceiverID) == "" {
		if err := r.deleteOwnedHTTPRoute(ctx, obj); err != nil {
			return ctrl.Result{}, err
		}
		status.HTTPRoute = nil
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentExternalTriggerHTTPRouteReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "RouteDetached",
			Message:            "Public HTTPRoute is detached for a suspended or blocked trigger.",
		})
		return ctrl.Result{}, nil
	}

	if err := r.HTTPRoute.Validate(); err != nil {
		if delErr := r.deleteOwnedHTTPRoute(ctx, obj); delErr != nil {
			return ctrl.Result{}, delErr
		}
		status.HTTPRoute = nil
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentExternalTriggerHTTPRouteReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "GatewayParentsUnset",
			Message:            "HTTPRoute creation is enabled but Gateway parentRefs/hostnames are missing or invalid: " + err.Error(),
		})
		return ctrl.Result{}, nil
	}

	hostnames, err := agentExternalTriggerRouteHostnames(obj, r.HTTPRoute)
	if err != nil {
		if delErr := r.deleteOwnedHTTPRoute(ctx, obj); delErr != nil {
			return ctrl.Result{}, delErr
		}
		status.HTTPRoute = nil
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentExternalTriggerHTTPRouteReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "InvalidHostname",
			Message:            err.Error(),
		})
		return ctrl.Result{}, nil
	}

	desired, err := buildAgentExternalTriggerHTTPRoute(obj, r.HTTPRoute, hostnames, status.WebhookPath)
	if err != nil {
		if delErr := r.deleteOwnedHTTPRoute(ctx, obj); delErr != nil {
			return ctrl.Result{}, delErr
		}
		status.HTTPRoute = nil
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentExternalTriggerHTTPRouteReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "InvalidHTTPRoute",
			Message:            err.Error(),
		})
		return ctrl.Result{}, nil
	}
	if err := controllerutil.SetControllerReference(obj, desired, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("set HTTPRoute owner: %w", err)
	}

	route, err := r.ensureHTTPRoute(ctx, obj, desired)
	if err != nil {
		status.HTTPRoute = nil
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentExternalTriggerHTTPRouteReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "HTTPRouteEnsureFailed",
			Message:            err.Error(),
		})
		return ctrl.Result{}, err
	}

	accepted, programmed := observeHTTPRouteParentConditions(route)
	publicURL := agentExternalTriggerPublicURL(hostnames[0], status.WebhookPath)
	status.HTTPRoute = &controlv1alpha1.AgentExternalTriggerHTTPRouteStatus{
		Name:       route.Name,
		PublicURL:  publicURL,
		Accepted:   accepted,
		Programmed: programmed,
	}
	ready := accepted != nil && *accepted && (programmed == nil || *programmed)
	condition := metav1.Condition{
		Type:               agentExternalTriggerHTTPRouteReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             "RoutePending",
		Message:            fmt.Sprintf("HTTPRoute %s/%s is waiting for Gateway Accepted.", route.Namespace, route.Name),
	}
	if ready {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "RouteReady"
		condition.Message = fmt.Sprintf("HTTPRoute %s is accepted at %s.", route.Name, publicURL)
	}
	apimeta.SetStatusCondition(&status.Conditions, condition)
	if !ready {
		return ctrl.Result{RequeueAfter: agentExternalTriggerHTTPRoutePoll}, nil
	}
	return ctrl.Result{}, nil
}

func (r *AgentExternalTriggerReconciler) ensureHTTPRoute(ctx context.Context, obj *controlv1alpha1.AgentExternalTrigger, desired *gatewayv1.HTTPRoute) (*gatewayv1.HTTPRoute, error) {
	existing := &gatewayv1.HTTPRoute{}
	err := r.Get(ctx, client.ObjectKey{Namespace: desired.Namespace, Name: desired.Name}, existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			if apierrors.IsAlreadyExists(err) {
				if getErr := r.Get(ctx, client.ObjectKey{Namespace: desired.Namespace, Name: desired.Name}, existing); getErr != nil {
					return nil, getErr
				}
			} else {
				return nil, fmt.Errorf("create HTTPRoute: %w", err)
			}
		} else {
			return desired, nil
		}
	} else if err != nil {
		if apimeta.IsNoMatchError(err) {
			return nil, fmt.Errorf("Gateway API HTTPRoute CRD is not installed")
		}
		return nil, err
	}

	if !metav1.IsControlledBy(existing, obj) {
		return nil, fmt.Errorf("HTTPRoute %s/%s exists and is not owned by AgentExternalTrigger %s", existing.Namespace, existing.Name, obj.Name)
	}
	patch := existing.DeepCopy()
	if patch.Labels == nil {
		patch.Labels = map[string]string{}
	}
	for key, value := range desired.Labels {
		patch.Labels[key] = value
	}
	patch.Spec = desired.Spec
	if err := r.Patch(ctx, patch, client.MergeFrom(existing)); err != nil {
		return nil, fmt.Errorf("patch HTTPRoute: %w", err)
	}
	return patch, nil
}

func (r *AgentExternalTriggerReconciler) deleteOwnedHTTPRoute(ctx context.Context, obj *controlv1alpha1.AgentExternalTrigger) error {
	route := &gatewayv1.HTTPRoute{}
	key := types.NamespacedName{Namespace: obj.Namespace, Name: agentExternalTriggerHTTPRouteName(obj)}
	if err := r.Get(ctx, key, route); err != nil {
		if apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err) {
			return r.deleteOwnedHTTPRoutesByLabel(ctx, obj)
		}
		return err
	}
	if !metav1.IsControlledBy(route, obj) {
		return r.deleteOwnedHTTPRoutesByLabel(ctx, obj)
	}
	if err := r.Delete(ctx, route); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete HTTPRoute: %w", err)
	}
	return r.deleteOwnedHTTPRoutesByLabel(ctx, obj)
}

func (r *AgentExternalTriggerReconciler) deleteOwnedHTTPRoutesByLabel(ctx context.Context, obj *controlv1alpha1.AgentExternalTrigger) error {
	list := &gatewayv1.HTTPRouteList{}
	if err := r.List(ctx, list, client.InNamespace(obj.Namespace), client.MatchingLabels{
		controlv1alpha1.AgentExternalTriggerLabel: sanitizeLabelValue(obj.Name),
	}); err != nil {
		if apimeta.IsNoMatchError(err) {
			return nil
		}
		return err
	}
	for i := range list.Items {
		route := &list.Items[i]
		if !metav1.IsControlledBy(route, obj) {
			continue
		}
		if err := r.Delete(ctx, route); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete HTTPRoute %s: %w", route.Name, err)
		}
	}
	return nil
}

func observeHTTPRouteParentConditions(route *gatewayv1.HTTPRoute) (accepted, programmed *bool) {
	if route == nil || len(route.Status.Parents) == 0 {
		return nil, nil
	}
	acceptedValue := true
	sawAccepted := false
	programmedValue := true
	sawProgrammed := false
	resolvedValue := true
	sawResolved := false
	for _, parent := range route.Status.Parents {
		for _, condition := range parent.Conditions {
			switch condition.Type {
			case string(gatewayv1.RouteConditionAccepted):
				sawAccepted = true
				if condition.Status != metav1.ConditionTrue {
					acceptedValue = false
				}
			case string(gatewayv1.GatewayConditionProgrammed):
				sawProgrammed = true
				if condition.Status != metav1.ConditionTrue {
					programmedValue = false
				}
			case string(gatewayv1.RouteConditionResolvedRefs):
				sawResolved = true
				if condition.Status != metav1.ConditionTrue {
					resolvedValue = false
				}
			}
		}
	}
	if sawAccepted {
		accepted = boolPtr(acceptedValue)
	}
	if sawProgrammed {
		programmed = boolPtr(programmedValue)
	} else if sawAccepted && sawResolved {
		programmed = boolPtr(acceptedValue && resolvedValue)
	}
	return accepted, programmed
}
