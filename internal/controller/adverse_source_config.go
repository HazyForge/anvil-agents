package controller

import (
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

// AdverseSourceConfig is an administrator-owned pull integration. Resource is
// consumed by the Helm chart to generate exact read RBAC; the controller uses
// APIVersion and Kind for its unstructured watch.
type AdverseSourceConfig struct {
	Name           string                                     `json:"name,omitempty"`
	APIVersion     string                                     `json:"apiVersion"`
	Kind           string                                     `json:"kind"`
	Resource       string                                     `json:"resource"`
	Namespaces     []string                                   `json:"namespaces,omitempty"`
	ObjectSelector *metav1.LabelSelector                      `json:"objectSelector,omitempty"`
	SituationRef   *controlv1alpha1.NamespacedObjectReference `json:"situationRef,omitempty"`
	GroupKey       string                                     `json:"groupKey,omitempty"`
	Classifier     AdverseSourceClassifier                    `json:"classifier,omitempty"`
}

type AdverseSourceClassifier struct {
	RequireObservedGeneration *bool    `json:"requireObservedGeneration,omitempty"`
	ObservedGenerationPath    string   `json:"observedGenerationPath,omitempty"`
	PhasePath                 string   `json:"phasePath,omitempty"`
	ConditionsPath            string   `json:"conditionsPath,omitempty"`
	AdversePhases             []string `json:"adversePhases,omitempty"`
	AdverseConditionTypes     []string `json:"adverseConditionTypes,omitempty"`
	DetailConditionType       string   `json:"detailConditionType,omitempty"`
}

func ParseAdverseSourcesJSON(raw string) ([]AdverseSourceConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil, nil
	}
	var sources []AdverseSourceConfig
	if err := json.Unmarshal([]byte(raw), &sources); err != nil {
		return nil, fmt.Errorf("parse adverse sources JSON: %w", err)
	}
	for i := range sources {
		if err := validateAdverseSourceConfig(sources[i]); err != nil {
			return nil, fmt.Errorf("adverse source %d: %w", i, err)
		}
	}
	return sources, nil
}

func validateAdverseSourceConfig(source AdverseSourceConfig) error {
	if strings.TrimSpace(source.APIVersion) == "" {
		return fmt.Errorf("apiVersion is required")
	}
	if strings.TrimSpace(source.Kind) == "" {
		return fmt.Errorf("kind is required")
	}
	resource := strings.TrimSpace(source.Resource)
	if resource == "" {
		return fmt.Errorf("resource is required for exact read RBAC")
	}
	if strings.ContainsAny(resource, "/*, ") {
		return fmt.Errorf("resource %q must be one exact plural resource name", resource)
	}
	if problems := utilvalidation.IsDNS1035Label(resource); len(problems) > 0 {
		return fmt.Errorf("resource %q is invalid: %s", resource, strings.Join(problems, "; "))
	}
	if _, err := adverseSourceGVK(source); err != nil {
		return err
	}
	if source.SituationRef != nil && strings.TrimSpace(source.SituationRef.Name) == "" {
		return fmt.Errorf("situationRef.name is required when situationRef is set")
	}
	if source.SituationRef != nil {
		if problems := utilvalidation.IsDNS1123Subdomain(strings.TrimSpace(source.SituationRef.Name)); len(problems) > 0 {
			return fmt.Errorf("situationRef.name is invalid: %s", strings.Join(problems, "; "))
		}
		if namespace := strings.TrimSpace(source.SituationRef.Namespace); namespace != "" {
			if problems := utilvalidation.IsDNS1123Label(namespace); len(problems) > 0 {
				return fmt.Errorf("situationRef.namespace is invalid: %s", strings.Join(problems, "; "))
			}
		}
	}
	for _, namespace := range source.Namespaces {
		if problems := utilvalidation.IsDNS1123Label(strings.TrimSpace(namespace)); len(problems) > 0 {
			return fmt.Errorf("namespace %q is invalid: %s", namespace, strings.Join(problems, "; "))
		}
	}
	if source.ObjectSelector != nil {
		if _, err := metav1.LabelSelectorAsSelector(source.ObjectSelector); err != nil {
			return fmt.Errorf("objectSelector: %w", err)
		}
	}
	for name, path := range map[string]string{
		"observedGenerationPath": source.Classifier.ObservedGenerationPath,
		"phasePath":              source.Classifier.PhasePath,
		"conditionsPath":         source.Classifier.ConditionsPath,
	} {
		if strings.TrimSpace(path) != "" && len(adverseSourceFieldPath(path)) == 0 {
			return fmt.Errorf("classifier.%s must contain a field path", name)
		}
	}
	return nil
}

func adverseSourceGVK(source AdverseSourceConfig) (schema.GroupVersionKind, error) {
	return parseAdverseSourceGVK(strings.TrimSpace(source.APIVersion) + "/" + strings.TrimSpace(source.Kind))
}

func adverseSourceFieldPath(value string) []string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(value), "."), ".")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func adverseSourceClassifierOrDefault(classifier AdverseSourceClassifier) AdverseSourceClassifier {
	result := classifier
	if strings.TrimSpace(result.ObservedGenerationPath) == "" {
		result.ObservedGenerationPath = "status.observedGeneration"
	}
	if strings.TrimSpace(result.PhasePath) == "" {
		result.PhasePath = "status.phase"
	}
	if strings.TrimSpace(result.ConditionsPath) == "" {
		result.ConditionsPath = "status.conditions"
	}
	if len(result.AdversePhases) == 0 {
		result.AdversePhases = []string{"Failed", "Error", "NeedsHuman", "ActionRequired"}
	}
	if len(result.AdverseConditionTypes) == 0 {
		result.AdverseConditionTypes = []string{"Failed", "NeedsHuman", "ActionRequired"}
	}
	if strings.TrimSpace(result.DetailConditionType) == "" {
		result.DetailConditionType = "Ready"
	}
	return result
}

func adverseSourceRequiresObservedGeneration(classifier AdverseSourceClassifier) bool {
	return classifier.RequireObservedGeneration == nil || *classifier.RequireObservedGeneration
}
