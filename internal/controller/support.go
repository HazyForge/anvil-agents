package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CommonReconcilerOptions struct {
	RESTConfig *rest.Config
	APIReader  client.Reader
	Options    *Options
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func boolPtr(value bool) *bool {
	return &value
}

func jobFailureMessage(job *batchv1.Job) string {
	if job == nil {
		return ""
	}
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && strings.TrimSpace(condition.Message) != "" {
			return strings.TrimSpace(condition.Message)
		}
	}
	return ""
}

func podTerminationMessage(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Terminated != nil {
			return firstNonEmpty(strings.TrimSpace(status.State.Terminated.Message), strings.TrimSpace(status.State.Terminated.Reason))
		}
	}
	return ""
}

func extractStatusMap(obj *unstructured.Unstructured) (map[string]any, error) {
	rawStatus, found := obj.Object["status"]
	if !found || rawStatus == nil {
		return map[string]any{}, nil
	}
	statusMap, ok := rawStatus.(map[string]any)
	if !ok {
		return nil, errors.New("status is not an object")
	}
	copied, err := deepCopyStatusValue(statusMap)
	if err != nil {
		return nil, fmt.Errorf("copy status: %w", err)
	}
	result, ok := copied.(map[string]any)
	if !ok {
		return nil, errors.New("copied status is not an object")
	}
	return result, nil
}

func deepCopyStatusValue(value any) (any, error) {
	switch typed := value.(type) {
	case nil, bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return typed, nil
	case []any:
		out := make([]any, len(typed))
		for index := range typed {
			copied, err := deepCopyStatusValue(typed[index])
			if err != nil {
				return nil, err
			}
			out[index] = copied
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			copied, err := deepCopyStatusValue(item)
			if err != nil {
				return nil, err
			}
			out[key] = copied
		}
		return out, nil
	case []string:
		return append([]string(nil), typed...), nil
	default:
		return nil, fmt.Errorf("unsupported status value type %T", value)
	}
}

func extractConditionsFromStatusMap(statusMap map[string]any) ([]metav1.Condition, error) {
	rawConditions, found, err := unstructured.NestedSlice(statusMap, "conditions")
	if err != nil {
		return nil, fmt.Errorf("read status.conditions: %w", err)
	}
	if !found || len(rawConditions) == 0 {
		return nil, nil
	}
	out := make([]metav1.Condition, 0, len(rawConditions))
	for _, item := range rawConditions {
		asMap, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("status.conditions entry is not an object")
		}
		var condition metav1.Condition
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(asMap, &condition); err != nil {
			return nil, fmt.Errorf("convert condition: %w", err)
		}
		out = append(out, condition)
	}
	return out, nil
}
