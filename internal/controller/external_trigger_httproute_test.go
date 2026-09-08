package controller

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func testExternalTriggerScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func testHTTPRouteConfig() ExternalTriggerHTTPRouteConfig {
	return ExternalTriggerHTTPRouteConfig{
		Enabled: true,
		ParentRefs: []ExternalTriggerHTTPRouteParentRef{{
			Name:        "public",
			Namespace:   "gateway-system",
			SectionName: "https",
		}},
		Hostnames:        []string{"agents.example.com"},
		BackendName:      "anvil-agents-api",
		BackendNamespace: "anvil-agents-system",
		BackendPort:      8082,
		RouteNamespace:   "anvil-agents-system",
	}
}

func testExternalTrigger(name string) *controlv1alpha1.AgentExternalTrigger {
	return &controlv1alpha1.AgentExternalTrigger{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "agents",
			UID:        types.UID(name + "-uid"),
			Generation: 1,
		},
		Spec: controlv1alpha1.AgentExternalTriggerSpec{
			Source: controlv1alpha1.AgentExternalTriggerSourceSpec{
				Kind: controlv1alpha1.AgentExternalTriggerSourceGitHubWebhook,
				GitHub: &controlv1alpha1.AgentExternalTriggerGitHubSpec{
					Repositories: []string{"HazyForge/anvil-agents"},
				},
			},
			SecretRef: controlv1alpha1.AgentExternalTriggerSecretRef{Name: "hook"},
			Targets: []controlv1alpha1.AgentExternalTriggerTargetSpec{{
				Kind: controlv1alpha1.AgentExternalTriggerTargetAgentRunProfile,
				Name: "operator",
			}},
		},
	}
}

func testTriggerReconciler(t *testing.T, objects ...client.Object) (*AgentExternalTriggerReconciler, client.Client) {
	t.Helper()
	scheme := testExternalTriggerScheme(t)
	trigger := &controlv1alpha1.AgentExternalTrigger{}
	builder := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(trigger)
	if len(objects) > 0 {
		builder = builder.WithObjects(objects...)
	}
	kube := builder.Build()
	return &AgentExternalTriggerReconciler{
		Client:                  kube,
		Scheme:                  scheme,
		ExternalTriggersEnabled: true,
		HTTPRoute:               testHTTPRouteConfig(),
	}, kube
}

func getHTTPRoute(t *testing.T, kube client.Client, namespace, name string) *gatewayv1.HTTPRoute {
	t.Helper()
	route := &gatewayv1.HTTPRoute{}
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, route); err != nil {
		t.Fatal(err)
	}
	return route
}

func assertNoHTTPRoute(t *testing.T, kube client.Client, namespace, name string) {
	t.Helper()
	route := &gatewayv1.HTTPRoute{}
	err := kube.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, route)
	if err == nil {
		t.Fatalf("HTTPRoute %s/%s still exists", namespace, name)
	}
	if client.IgnoreNotFound(err) != nil {
		t.Fatal(err)
	}
}

func TestParseExternalTriggerHTTPRouteJSONFromHelm(t *testing.T) {
	raw := `{"backendName":"contract-anvil-agents-api","backendNamespace":"default","backendPort":8082,"hostnames":["agents.example.com"],"parentRefs":[{"name":"public","namespace":"gateway-system","sectionName":"https"}],"routeNamespace":"default"}`
	cfg, err := ParseExternalTriggerHTTPRouteJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.BackendName != "contract-anvil-agents-api" || cfg.BackendPort != 8082 || cfg.RouteNamespace != "default" {
		t.Fatalf("cfg = %#v", cfg)
	}
}

func TestParseExternalTriggerHTTPRouteJSONRejectsWildcards(t *testing.T) {
	cfg, err := ParseExternalTriggerHTTPRouteJSON(`{"hostnames":["*.example.com"],"parentRefs":[{"name":"public","sectionName":"https"}],"backendName":"api","backendPort":8082}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("Validate() = %v, want wildcard error", err)
	}
}

func TestValidateExactHostnameRejectsWildcards(t *testing.T) {
	if err := validateExactHostname("*.hooks.example.com"); err == nil {
		t.Fatal("expected wildcard hostname to be rejected")
	}
	if err := validateExactHostname("agents.example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestResolvedRouteNamespaceFallsBackToPodNamespace(t *testing.T) {
	t.Setenv(podNamespaceEnv, "from-pod")
	cfg := ExternalTriggerHTTPRouteConfig{}
	if got := cfg.resolvedRouteNamespace(); got != "from-pod" {
		t.Fatalf("resolvedRouteNamespace = %q, want from-pod", got)
	}
	cfg.RouteNamespace = "from-flag"
	if got := cfg.resolvedRouteNamespace(); got != "from-flag" {
		t.Fatalf("resolvedRouteNamespace = %q, want from-flag", got)
	}
}

func TestAgentExternalTriggerHTTPRouteCreatedWhenReceiverReady(t *testing.T) {
	obj := testExternalTrigger("gh")
	reconciler, kube := testTriggerReconciler(t, obj)
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}); err != nil {
		t.Fatal(err)
	}
	fresh := &controlv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "gh"}, fresh); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(fresh, agentExternalTriggerHTTPRouteFinalizer) {
		t.Fatal("expected HTTPRoute finalizer on the trigger")
	}
	wantName := agentExternalTriggerHTTPRouteName(fresh)
	if fresh.Status.HTTPRoute == nil || fresh.Status.HTTPRoute.Name != wantName {
		t.Fatalf("status.httpRoute = %#v, want name %q (phase=%s conditions=%#v)", fresh.Status.HTTPRoute, wantName, fresh.Status.Phase, fresh.Status.Conditions)
	}
	if fresh.Status.HTTPRoute.Namespace != "anvil-agents-system" {
		t.Fatalf("status.httpRoute.namespace = %q, want anvil-agents-system", fresh.Status.HTTPRoute.Namespace)
	}
	wantURL := "https://agents.example.com" + fresh.Status.WebhookPath
	if fresh.Status.HTTPRoute.PublicURL != wantURL {
		t.Fatalf("publicURL = %q want %q", fresh.Status.HTTPRoute.PublicURL, wantURL)
	}

	route := getHTTPRoute(t, kube, "anvil-agents-system", wantName)
	if metav1.IsControlledBy(route, fresh) {
		t.Fatal("HTTPRoute must not use a cross-namespace OwnerReference")
	}
	if !httpRouteManagedByTrigger(route, fresh) {
		t.Fatal("HTTPRoute is not labeled as managed by the trigger")
	}
	if route.Labels[controlv1alpha1.AgentExternalTriggerNamespaceLabel] != "agents" {
		t.Fatalf("trigger namespace label = %q", route.Labels[controlv1alpha1.AgentExternalTriggerNamespaceLabel])
	}
	if route.Annotations[agentExternalTriggerUIDAnnotation] != string(fresh.UID) {
		t.Fatalf("uid annotation = %q", route.Annotations[agentExternalTriggerUIDAnnotation])
	}
	assertNoHTTPRoute(t, kube, "agents", wantName)
	assertNoHTTPRoute(t, kube, "agents", agentExternalTriggerLegacyHTTPRouteName(fresh))
	if len(route.Spec.Rules) != 1 || len(route.Spec.Rules[0].Matches) != 1 || route.Spec.Rules[0].Matches[0].Path == nil {
		t.Fatalf("route matches = %#v", route.Spec.Rules)
	}
	path := route.Spec.Rules[0].Matches[0].Path
	if path.Type == nil || *path.Type != gatewayv1.PathMatchExact {
		t.Fatalf("path type = %#v, want Exact", path.Type)
	}
	if path.Value == nil || *path.Value != fresh.Status.WebhookPath {
		t.Fatalf("path value = %#v want %q", path.Value, fresh.Status.WebhookPath)
	}
	if !strings.Contains(fresh.Status.WebhookPath, fresh.Status.ReceiverID) {
		t.Fatalf("webhook path %q does not include receiverID", fresh.Status.WebhookPath)
	}
	if len(route.Spec.Rules[0].BackendRefs) != 1 {
		t.Fatalf("backendRefs = %#v", route.Spec.Rules[0].BackendRefs)
	}
	backend := route.Spec.Rules[0].BackendRefs[0]
	if string(backend.Name) != "anvil-agents-api" {
		t.Fatalf("backend name = %s", backend.Name)
	}
	if backend.Namespace != nil {
		t.Fatalf("backend namespace = %#v, want omitted for same-namespace Service", backend.Namespace)
	}
	if len(route.Spec.ParentRefs) != 1 || string(route.Spec.ParentRefs[0].Name) != "public" {
		t.Fatalf("parentRefs = %#v", route.Spec.ParentRefs)
	}
	if route.Spec.ParentRefs[0].SectionName == nil || string(*route.Spec.ParentRefs[0].SectionName) != "https" {
		t.Fatalf("sectionName = %#v", route.Spec.ParentRefs[0].SectionName)
	}
	if len(route.Spec.Hostnames) != 1 || string(route.Spec.Hostnames[0]) != "agents.example.com" {
		t.Fatalf("hostnames = %#v", route.Spec.Hostnames)
	}
}

func TestAgentExternalTriggerHTTPRouteNotCreatedInTriggerNamespace(t *testing.T) {
	obj := testExternalTrigger("gh")
	reconciler, kube := testTriggerReconciler(t, obj)
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}); err != nil {
		t.Fatal(err)
	}
	routes := &gatewayv1.HTTPRouteList{}
	if err := kube.List(context.Background(), routes, client.InNamespace("agents")); err != nil {
		t.Fatal(err)
	}
	if len(routes.Items) != 0 {
		t.Fatalf("created HTTPRoute in trigger namespace: %#v", routes.Items)
	}
	install := &gatewayv1.HTTPRouteList{}
	if err := kube.List(context.Background(), install, client.InNamespace("anvil-agents-system")); err != nil {
		t.Fatal(err)
	}
	if len(install.Items) != 1 {
		t.Fatalf("install-namespace HTTPRoutes = %#v, want 1", install.Items)
	}
}

func TestAgentExternalTriggerHTTPRouteCleansLegacyTriggerNamespaceRoute(t *testing.T) {
	obj := testExternalTrigger("gh")
	legacy := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentExternalTriggerLegacyHTTPRouteName(obj),
			Namespace: obj.Namespace,
			Labels: map[string]string{
				agentManagedByLabel:                       "anvil-agents",
				"app.kubernetes.io/component":             agentExternalTriggerHTTPRouteComponent,
				controlv1alpha1.AgentExternalTriggerLabel: sanitizeLabelValue(obj.Name),
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: controlv1alpha1.GroupVersion.String(),
				Kind:       "AgentExternalTrigger",
				Name:       obj.Name,
				UID:        obj.UID,
				Controller: boolPtr(true),
			}},
		},
	}
	reconciler, kube := testTriggerReconciler(t, obj, legacy)
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}); err != nil {
		t.Fatal(err)
	}
	fresh := &controlv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "gh"}, fresh); err != nil {
		t.Fatal(err)
	}
	_ = getHTTPRoute(t, kube, "anvil-agents-system", agentExternalTriggerHTTPRouteName(fresh))
	assertNoHTTPRoute(t, kube, "agents", agentExternalTriggerLegacyHTTPRouteName(fresh))
}

func TestAgentExternalTriggerHTTPRouteDistinctNamesAcrossTriggerNamespaces(t *testing.T) {
	first := testExternalTrigger("gh")
	second := testExternalTrigger("gh")
	second.Namespace = "other"
	second.UID = "other-gh-uid"
	reconciler, kube := testTriggerReconciler(t, first, second)
	for _, ns := range []string{"agents", "other"} {
		if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "gh"}}); err != nil {
			t.Fatal(err)
		}
	}
	routes := &gatewayv1.HTTPRouteList{}
	if err := kube.List(context.Background(), routes, client.InNamespace("anvil-agents-system")); err != nil {
		t.Fatal(err)
	}
	if len(routes.Items) != 2 {
		t.Fatalf("install-namespace HTTPRoutes = %#v, want 2", routes.Items)
	}
	if routes.Items[0].Name == routes.Items[1].Name {
		t.Fatalf("HTTPRoute names collided: %s", routes.Items[0].Name)
	}
}

func TestAgentExternalTriggerHTTPRouteHostnameOverride(t *testing.T) {
	obj := testExternalTrigger("gh")
	obj.Spec.HTTPRoute = &controlv1alpha1.AgentExternalTriggerHTTPRouteSpec{Hostname: "hooks.example.com"}
	reconciler, kube := testTriggerReconciler(t, obj)
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}); err != nil {
		t.Fatal(err)
	}
	fresh := &controlv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "gh"}, fresh); err != nil {
		t.Fatal(err)
	}
	route := getHTTPRoute(t, kube, "anvil-agents-system", agentExternalTriggerHTTPRouteName(fresh))
	if len(route.Spec.Hostnames) != 1 || string(route.Spec.Hostnames[0]) != "hooks.example.com" {
		t.Fatalf("hostnames = %#v", route.Spec.Hostnames)
	}
	if fresh.Status.HTTPRoute == nil || fresh.Status.HTTPRoute.PublicURL != "https://hooks.example.com"+fresh.Status.WebhookPath {
		t.Fatalf("publicURL = %#v", fresh.Status.HTTPRoute)
	}
}

func TestAgentExternalTriggerHTTPRouteSkippedWhenParentsUnset(t *testing.T) {
	obj := testExternalTrigger("gh")
	reconciler, kube := testTriggerReconciler(t, obj)
	reconciler.HTTPRoute = ExternalTriggerHTTPRouteConfig{Enabled: true, BackendName: "api", BackendPort: 8082, RouteNamespace: "anvil-agents-system"}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}); err != nil {
		t.Fatal(err)
	}
	fresh := &controlv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "gh"}, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Status.Phase != controlv1alpha1.AgentExternalTriggerPhaseReady {
		t.Fatalf("phase = %s", fresh.Status.Phase)
	}
	if fresh.Status.HTTPRoute != nil {
		t.Fatalf("httpRoute status = %#v, want nil", fresh.Status.HTTPRoute)
	}
	routes := &gatewayv1.HTTPRouteList{}
	if err := kube.List(context.Background(), routes); err != nil {
		t.Fatal(err)
	}
	if len(routes.Items) != 0 {
		t.Fatalf("created %d HTTPRoutes, want 0", len(routes.Items))
	}
}

func TestAgentExternalTriggerHTTPRouteNotCreatedWhenDisabled(t *testing.T) {
	obj := testExternalTrigger("gh")
	scheme := testExternalTriggerScheme(t)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).Build()
	reconciler := &AgentExternalTriggerReconciler{Client: kube, Scheme: scheme, HTTPRoute: testHTTPRouteConfig()}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}); err != nil {
		t.Fatal(err)
	}
	routes := &gatewayv1.HTTPRouteList{}
	if err := kube.List(context.Background(), routes); err != nil {
		t.Fatal(err)
	}
	if len(routes.Items) != 0 {
		t.Fatalf("created %d HTTPRoutes while feature disabled", len(routes.Items))
	}
}

func TestAgentExternalTriggerHTTPRouteCleanedUpOnSuspend(t *testing.T) {
	obj := testExternalTrigger("gh")
	reconciler, kube := testTriggerReconciler(t, obj)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	fresh := &controlv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(context.Background(), req.NamespacedName, fresh); err != nil {
		t.Fatal(err)
	}
	routeName := agentExternalTriggerHTTPRouteName(fresh)
	_ = getHTTPRoute(t, kube, "anvil-agents-system", routeName)

	fresh.Spec.Suspend = true
	if err := kube.Update(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(context.Background(), req.NamespacedName, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Status.Phase != controlv1alpha1.AgentExternalTriggerPhaseSuspended {
		t.Fatalf("phase = %s", fresh.Status.Phase)
	}
	if fresh.Status.HTTPRoute != nil {
		t.Fatalf("httpRoute status left after suspend: %#v", fresh.Status.HTTPRoute)
	}
	assertNoHTTPRoute(t, kube, "anvil-agents-system", routeName)
	assertNoHTTPRoute(t, kube, "agents", routeName)
	if !controllerutil.ContainsFinalizer(fresh, agentExternalTriggerHTTPRouteFinalizer) {
		t.Fatal("suspend should keep the HTTPRoute finalizer")
	}
}

func TestAgentExternalTriggerHTTPRouteCleanedUpOnDelete(t *testing.T) {
	obj := testExternalTrigger("gh")
	obj.Finalizers = []string{"test.anvil.hazyforge.io/hold"}
	reconciler, kube := testTriggerReconciler(t, obj)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	fresh := &controlv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(context.Background(), req.NamespacedName, fresh); err != nil {
		t.Fatal(err)
	}
	routeName := agentExternalTriggerHTTPRouteName(fresh)
	_ = getHTTPRoute(t, kube, "anvil-agents-system", routeName)
	if err := kube.Delete(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	assertNoHTTPRoute(t, kube, "anvil-agents-system", routeName)
	assertNoHTTPRoute(t, kube, "agents", routeName)
	if err := kube.Get(context.Background(), req.NamespacedName, fresh); err != nil {
		t.Fatal(err)
	}
	if controllerutil.ContainsFinalizer(fresh, agentExternalTriggerHTTPRouteFinalizer) {
		t.Fatal("HTTPRoute finalizer should be removed after route cleanup")
	}
}

func TestAgentExternalTriggerHTTPRouteCleanedUpWhenBlocked(t *testing.T) {
	obj := testExternalTrigger("gh")
	reconciler, kube := testTriggerReconciler(t, obj)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	fresh := &controlv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(context.Background(), req.NamespacedName, fresh); err != nil {
		t.Fatal(err)
	}
	routeName := agentExternalTriggerHTTPRouteName(fresh)
	fresh.Spec.Source.GitHub.Repositories = nil
	if err := kube.Update(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(context.Background(), req.NamespacedName, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Status.Phase != controlv1alpha1.AgentExternalTriggerPhaseBlocked {
		t.Fatalf("phase = %s", fresh.Status.Phase)
	}
	if fresh.Status.HTTPRoute != nil {
		t.Fatalf("httpRoute status left while blocked: %#v", fresh.Status.HTTPRoute)
	}
	assertNoHTTPRoute(t, kube, "anvil-agents-system", routeName)
}

func TestAgentExternalTriggerWildcardHostnameDoesNotCreateRoute(t *testing.T) {
	obj := testExternalTrigger("gh")
	obj.Spec.HTTPRoute = &controlv1alpha1.AgentExternalTriggerHTTPRouteSpec{Hostname: "*.hooks.example.com"}
	reconciler, kube := testTriggerReconciler(t, obj)
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}); err != nil {
		t.Fatal(err)
	}
	fresh := &controlv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "gh"}, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Status.Phase != controlv1alpha1.AgentExternalTriggerPhaseBlocked {
		t.Fatalf("phase = %s", fresh.Status.Phase)
	}
	routes := &gatewayv1.HTTPRouteList{}
	if err := kube.List(context.Background(), routes); err != nil {
		t.Fatal(err)
	}
	if len(routes.Items) != 0 {
		t.Fatalf("created HTTPRoute for wildcard hostname: %#v", routes.Items)
	}
}

func TestBuildAgentExternalTriggerHTTPRouteRejectsWildcardHostnames(t *testing.T) {
	obj := testExternalTrigger("gh")
	obj.Status.WebhookPath = "/api/v1/external-triggers/agents/gh/abcd"
	cfg := testHTTPRouteConfig()
	if _, err := buildAgentExternalTriggerHTTPRoute(obj, cfg, []string{"*.example.com"}, obj.Status.WebhookPath, "anvil-agents-system"); err == nil {
		t.Fatal("expected wildcard hostname to be rejected")
	}
}

func TestAgentExternalTriggerHTTPRouteObservesAcceptedAndProgrammed(t *testing.T) {
	obj := testExternalTrigger("gh")
	reconciler, kube := testTriggerReconciler(t, obj)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	fresh := &controlv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(context.Background(), req.NamespacedName, fresh); err != nil {
		t.Fatal(err)
	}
	route := getHTTPRoute(t, kube, "anvil-agents-system", agentExternalTriggerHTTPRouteName(fresh))
	route.Status.Parents = []gatewayv1.RouteParentStatus{{
		ControllerName: "gateway.example/controller",
		Conditions: []metav1.Condition{
			{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue, Reason: "Accepted", Message: "ok", LastTransitionTime: metav1.Now()},
			{Type: string(gatewayv1.RouteConditionResolvedRefs), Status: metav1.ConditionTrue, Reason: "ResolvedRefs", Message: "ok", LastTransitionTime: metav1.Now()},
			{Type: string(gatewayv1.GatewayConditionProgrammed), Status: metav1.ConditionTrue, Reason: "Programmed", Message: "ok", LastTransitionTime: metav1.Now()},
		},
	}}
	if err := kube.Update(context.Background(), route); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(context.Background(), req.NamespacedName, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Status.HTTPRoute == nil || fresh.Status.HTTPRoute.Accepted == nil || !*fresh.Status.HTTPRoute.Accepted {
		t.Fatalf("accepted = %#v", fresh.Status.HTTPRoute)
	}
	if fresh.Status.HTTPRoute.Programmed == nil || !*fresh.Status.HTTPRoute.Programmed {
		t.Fatalf("programmed = %#v", fresh.Status.HTTPRoute)
	}
}

func TestEnqueueAgentExternalTriggerForHTTPRoute(t *testing.T) {
	obj := testExternalTrigger("gh")
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentExternalTriggerHTTPRouteName(obj),
			Namespace: "anvil-agents-system",
			Labels:    agentExternalTriggerHTTPRouteLabels(obj),
		},
	}
	reqs := enqueueAgentExternalTriggerForHTTPRoute(context.Background(), route)
	if len(reqs) != 1 || reqs[0].Namespace != "agents" || reqs[0].Name != "gh" {
		t.Fatalf("enqueue = %#v", reqs)
	}
}
