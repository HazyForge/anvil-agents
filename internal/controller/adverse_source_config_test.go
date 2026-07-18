package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestParseAdverseSourcesJSONAndRouteNamedSituation(t *testing.T) {
	t.Parallel()

	sources, err := ParseAdverseSourcesJSON(`[{"name":"checkout","apiVersion":"apps.example.io/v1","kind":"Release","resource":"releases","namespaces":["store"],"objectSelector":{"matchLabels":{"adverse":"enabled"}},"situationRef":{"name":"checkout-health"},"groupKey":"application/checkout"}]`)
	if err != nil {
		t.Fatalf("parse structured sources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources = %#v, want one", sources)
	}
	source := &unstructured.Unstructured{}
	source.SetNamespace("store")
	source.SetLabels(map[string]string{"adverse": "enabled"})
	if !adverseSourceConfigMatches(sources[0], source) {
		t.Fatalf("source should match namespace and selector")
	}
	namespace, name, groupKey := adverseSituationRouteForSource(source, sources[0])
	if namespace != "store" || name != "checkout-health" || groupKey != "application/checkout" {
		t.Fatalf("route = %s/%s group=%q", namespace, name, groupKey)
	}
}

func TestCustomAdverseSourceClassifierUsesConfiguredPathsAndFreshConditions(t *testing.T) {
	t.Parallel()

	source := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps.example.io/v1",
		"kind":       "Release",
		"metadata": map[string]any{
			"name":       "checkout",
			"namespace":  "store",
			"generation": int64(7),
		},
		"state": map[string]any{
			"observed": int64(7),
			"stage":    "Degraded",
			"signals": []any{
				map[string]any{"type": "Blocked", "status": "True", "observedGeneration": int64(6), "reason": "OldFailure"},
				map[string]any{"type": "Blocked", "status": "True", "observedGeneration": int64(7), "reason": "ProviderTimeout", "message": "provider did not respond"},
			},
		},
	}}
	classifier := AdverseSourceClassifier{
		ObservedGenerationPath: "state.observed",
		PhasePath:              "state.stage",
		ConditionsPath:         "state.signals",
		AdversePhases:          []string{"Degraded"},
		AdverseConditionTypes:  []string{"Blocked"},
		DetailConditionType:    "Healthy",
	}
	trigger, ok := agentRunTriggerForSourceWithClassifier(source, classifier)
	if !ok {
		t.Fatalf("fresh configured adverse condition was not classified")
	}
	if trigger.Reason != "ProviderTimeout" || trigger.ObservedGeneration != 7 {
		t.Fatalf("trigger = %#v, want fresh ProviderTimeout", trigger)
	}
}

func TestAdverseSourceConfigurationRequiresExactResource(t *testing.T) {
	t.Parallel()

	_, err := ParseAdverseSourcesJSON(`[{"apiVersion":"apps.example.io/v1","kind":"Release"}]`)
	if err == nil {
		t.Fatalf("missing plural resource should be rejected")
	}
}

func TestAdverseSituationRouteAllowsAdministratorOwnedCrossNamespaceTarget(t *testing.T) {
	t.Parallel()

	source := &unstructured.Unstructured{}
	source.SetNamespace("application")
	integration := AdverseSourceConfig{
		SituationRef: &controlv1alpha1.NamespacedObjectReference{Name: "shared-health", Namespace: "operations"},
	}
	namespace, name, _ := adverseSituationRouteForSource(source, integration)
	if namespace != "operations" || name != "shared-health" {
		t.Fatalf("administrator route = %s/%s", namespace, name)
	}
}

func TestAdverseSourceSelectorRejectsNonMatchingObject(t *testing.T) {
	t.Parallel()

	source := &unstructured.Unstructured{}
	source.SetNamespace("store")
	source.SetLabels(map[string]string{"adverse": "disabled"})
	integration := AdverseSourceConfig{
		Namespaces:     []string{"store"},
		ObjectSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"adverse": "enabled"}},
	}
	if adverseSourceConfigMatches(integration, source) {
		t.Fatalf("non-matching selector should be ignored")
	}
}
