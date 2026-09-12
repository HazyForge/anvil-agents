package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	agentRunStatusActionRequestPeer          = "requestPeer"
	agentRunStatusActionInterruptDuplicate   = "interruptDuplicate"
	agentRunPeerParentLabel                  = "control.anvil.hazyforge.io/agent-run-peer-parent"
	agentRunCollaborationRequestKeyLabel     = "control.anvil.hazyforge.io/collaboration-request-key"
	agentRunInterruptedByLabel               = "control.anvil.hazyforge.io/interrupted-by-agent-run"
	agentRunCollaborationTriggerReasonPeer   = "MidRunPeerRequest"
	agentRunCollaborationTriggerReasonDup    = "MidRunDuplicateInterrupt"
)

func (r *AgentRunReconciler) reconcileAgentRunCollaboration(
	ctx context.Context,
	obj *controlv1alpha1.AgentRun,
	status *controlv1alpha1.AgentRunStatus,
) error {
	if obj == nil || status == nil {
		return nil
	}
	if agentRunPhaseTerminal(status.Phase) {
		return nil
	}
	if len(status.Reports) == 0 {
		return nil
	}
	if status.Collaboration == nil {
		status.Collaboration = &controlv1alpha1.AgentRunCollaborationStatus{}
	}
	honored := agentRunCollaborationHonoredKeys(status.Collaboration.Requests)
	for _, report := range status.Reports {
		action := strings.TrimSpace(report.Action)
		switch action {
		case agentRunStatusActionRequestPeer, agentRunStatusActionInterruptDuplicate:
		default:
			continue
		}
		key := agentRunCollaborationRequestKey(report)
		if key == "" {
			continue
		}
		if _, done := honored[key]; done {
			continue
		}
		record := controlv1alpha1.AgentRunCollaborationRequestStatus{
			Action:     action,
			Key:        key,
			Summary:    strings.TrimSpace(report.Summary),
			ObservedAt: report.ObservedAt,
		}
		switch action {
		case agentRunStatusActionRequestPeer:
			peer, err := r.createAgentRunPeer(ctx, obj, report)
			if err != nil {
				record.Error = err.Error()
			} else {
				record.PeerRunRef = &controlv1alpha1.NamespacedObjectReference{Name: peer.Name, Namespace: peer.Namespace}
			}
		case agentRunStatusActionInterruptDuplicate:
			interrupted, err := r.interruptDuplicateAgentRun(ctx, obj, report)
			if err != nil {
				record.Error = err.Error()
			} else {
				record.InterruptedRunRef = &controlv1alpha1.NamespacedObjectReference{Name: interrupted.Name, Namespace: interrupted.Namespace}
			}
		}
		status.Collaboration.Requests = append(status.Collaboration.Requests, record)
		honored[key] = struct{}{}
	}
	return nil
}

func agentRunCollaborationHonoredKeys(records []controlv1alpha1.AgentRunCollaborationRequestStatus) map[string]struct{} {
	out := map[string]struct{}{}
	for _, record := range records {
		if key := strings.TrimSpace(record.Key); key != "" {
			out[key] = struct{}{}
		}
	}
	return out
}

func agentRunCollaborationRequestKey(report controlv1alpha1.AgentRunStatusReport) string {
	parts := []string{
		strings.TrimSpace(report.Action),
		strings.TrimSpace(report.PeerProfileName),
		strings.TrimSpace(report.PeerPrompt),
		strings.TrimSpace(report.DuplicateRunName),
	}
	if report.ObservedAt != nil {
		parts = append(parts, report.ObservedAt.Time.UTC().Format(time.RFC3339Nano))
	}
	parts = append(parts, strings.TrimSpace(report.Summary))
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:8])
}

func (r *AgentRunReconciler) createAgentRunPeer(
	ctx context.Context,
	parent *controlv1alpha1.AgentRun,
	report controlv1alpha1.AgentRunStatusReport,
) (*controlv1alpha1.AgentRun, error) {
	prompt := strings.TrimSpace(report.PeerPrompt)
	if prompt == "" {
		return nil, fmt.Errorf("requestPeer requires peerPrompt in status JSON")
	}
	profileName := strings.TrimSpace(report.PeerProfileName)
	if profileName == "" {
		if parent.Spec.ProfileRef == nil || strings.TrimSpace(parent.Spec.ProfileRef.Name) == "" {
			return nil, fmt.Errorf("requestPeer requires peerProfileName when the requesting run has no profileRef")
		}
		profileName = strings.TrimSpace(parent.Spec.ProfileRef.Name)
	}
	requestKey := agentRunCollaborationRequestKey(report)
	name := agentRunChildName(parent.Name, "peer", requestKey)
	labels := map[string]string{
		agentRunPeerParentLabel:              sanitizeLabelValue(parent.Name),
		agentRunCollaborationRequestKeyLabel: sanitizeLabelValue(requestKey),
	}
	spec := controlv1alpha1.AgentRunSpec{
		Purpose: controlv1alpha1.AgentRunPurposeManual,
		SourceRef: controlv1alpha1.AgentRunSourceRef{
			APIVersion: controlv1alpha1.GroupVersion.String(),
			Kind:       "AgentRun",
			Namespace:  parent.Namespace,
			Name:       parent.Name,
		},
		SourceUID:        string(parent.UID),
		SourceGeneration: parent.Generation,
		Prompt:           prompt,
		ProfileRef:       &controlv1alpha1.NamespacedObjectReference{Name: profileName},
		HarnessProfileRef: parent.Spec.HarnessProfileRef,
		Scope:             parent.Spec.Scope,
		Trigger: controlv1alpha1.AgentRunTriggerSnapshot{
			Reason:     agentRunCollaborationTriggerReasonPeer,
			Message:    firstNonEmpty(strings.TrimSpace(report.Summary), "Peer AgentRun requested mid-run."),
			DetectedAt: report.ObservedAt,
		},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: parent.Namespace,
			Labels:    labels,
		},
		Spec: spec,
	}
	if err := r.Create(ctx, run); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(run), run); err != nil {
			return nil, err
		}
		if run.Labels[agentRunPeerParentLabel] != sanitizeLabelValue(parent.Name) ||
			run.Labels[agentRunCollaborationRequestKeyLabel] != sanitizeLabelValue(requestKey) {
			return nil, fmt.Errorf("AgentRun %s/%s already exists for a different peer request", run.Namespace, run.Name)
		}
	}
	return run, nil
}

func (r *AgentRunReconciler) interruptDuplicateAgentRun(
	ctx context.Context,
	requester *controlv1alpha1.AgentRun,
	report controlv1alpha1.AgentRunStatusReport,
) (*controlv1alpha1.AgentRun, error) {
	targetName := strings.TrimSpace(report.DuplicateRunName)
	if targetName == "" {
		return nil, fmt.Errorf("interruptDuplicate requires duplicateRunName in status JSON")
	}
	if targetName == requester.Name {
		return nil, fmt.Errorf("interruptDuplicate cannot target the requesting run")
	}
	target := &controlv1alpha1.AgentRun{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: requester.Namespace, Name: targetName}, target); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("duplicate AgentRun %s/%s not found", requester.Namespace, targetName)
		}
		return nil, err
	}
	if agentRunPhaseTerminal(target.Status.Phase) {
		return target, nil
	}
	now := metav1.Now()
	targetOriginal := target.DeepCopy()
	message := firstNonEmpty(strings.TrimSpace(report.Summary), fmt.Sprintf("Interrupted as duplicate work requested by AgentRun %s/%s.", requester.Namespace, requester.Name))
	targetStatus := targetOriginal.Status
	targetStatus.Phase = controlv1alpha1.AgentRunPhaseFailed
	targetStatus.CompletedAt = &now
	targetStatus.Error = message
	targetStatus.Decision = &controlv1alpha1.AgentRunDecisionStatus{
		Classification: "duplicate work",
		Action:         agentRunStatusActionInterruptDuplicate,
		Summary:        message,
	}
	apimeta.SetStatusCondition(&targetStatus.Conditions, metav1.Condition{
		Type:               agentRunReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: target.Generation,
		LastTransitionTime: now,
		Reason:             agentRunCollaborationTriggerReasonDup,
		Message:            message,
	})
	if target.Labels == nil {
		target.Labels = map[string]string{}
	}
	target.Labels[agentRunInterruptedByLabel] = sanitizeLabelValue(requester.Name)
	labelOriginal := target.DeepCopy()
	if err := r.Patch(ctx, target, client.MergeFrom(labelOriginal)); err != nil {
		return nil, err
	}
	statusOriginal := target.DeepCopy()
	target.Status = targetStatus
	if err := r.Status().Patch(ctx, target, client.MergeFrom(statusOriginal)); err != nil {
		return nil, err
	}
	return target, nil
}
