package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	adverseSignalAccepted         = "Accepted"
	adverseSignalReceiptFinalizer = "control.anvil.hazyforge.io/adverse-signal-receipt"
	adverseSignalPendingRequeue   = 15 * time.Second
)

// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=adversesignals,verbs=get;list;patch;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=adversesignals/status,verbs=get;patch;update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=adversesignals/finalizers,verbs=update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=adversesituations,verbs=get
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=adversesituations/status,verbs=get;patch;update
type AdverseSignalReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *AdverseSignalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	signal := &controlv1alpha1.AdverseSignal{}
	if err := r.Get(ctx, req.NamespacedName, signal); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !signal.GetDeletionTimestamp().IsZero() {
		if !controllerutil.ContainsFinalizer(signal, adverseSignalReceiptFinalizer) {
			return ctrl.Result{}, nil
		}
		result, err := r.clearAdverseSignalReceipt(ctx, signal, nil)
		if err != nil || result.Requeue || result.RequeueAfter > 0 {
			return result, err
		}
		return r.removeAdverseSignalFinalizer(ctx, client.ObjectKeyFromObject(signal))
	}
	if signal.Status.Phase == controlv1alpha1.AdverseSignalPhaseAccepted {
		result, err := r.clearAdverseSignalReceipt(ctx, signal, nil)
		if err != nil || result.Requeue || result.RequeueAfter > 0 {
			return result, err
		}
		if !controllerutil.ContainsFinalizer(signal, adverseSignalReceiptFinalizer) {
			return ctrl.Result{}, nil
		}
		return r.removeAdverseSignalFinalizer(ctx, client.ObjectKeyFromObject(signal))
	}
	if signal.Status.Phase == controlv1alpha1.AdverseSignalPhaseRejected {
		if !controllerutil.ContainsFinalizer(signal, adverseSignalReceiptFinalizer) {
			return ctrl.Result{}, nil
		}
		return r.removeAdverseSignalFinalizer(ctx, client.ObjectKeyFromObject(signal))
	}
	if !controllerutil.ContainsFinalizer(signal, adverseSignalReceiptFinalizer) {
		original := signal.DeepCopy()
		controllerutil.AddFinalizer(signal, adverseSignalReceiptFinalizer)
		if err := r.Patch(ctx, signal, client.MergeFrom(original)); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("install AdverseSignal receipt finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if reason, message := adverseSignalValidationError(signal); reason != "" {
		return r.patchAdverseSignalStatus(ctx, signal, controlv1alpha1.AdverseSignalPhaseRejected, metav1.ConditionFalse, reason, message, nil, "", 0, nil, 0)
	}

	situation := &controlv1alpha1.AdverseSituation{}
	situationKey := client.ObjectKey{Namespace: signal.Namespace, Name: strings.TrimSpace(signal.Spec.SituationRef.Name)}
	if err := r.Get(ctx, situationKey, situation); err != nil {
		if apierrors.IsNotFound(err) {
			return r.patchAdverseSignalStatus(
				ctx,
				signal,
				controlv1alpha1.AdverseSignalPhasePending,
				metav1.ConditionFalse,
				"SituationNotFound",
				fmt.Sprintf("Waiting for same-namespace AdverseSituation %s/%s.", situationKey.Namespace, situationKey.Name),
				nil,
				"",
				0,
				nil,
				adverseSignalPendingRequeue,
			)
		}
		return ctrl.Result{}, fmt.Errorf("get AdverseSituation for signal: %w", err)
	}
	if !situation.GetDeletionTimestamp().IsZero() {
		return r.patchAdverseSignalStatus(
			ctx,
			signal,
			controlv1alpha1.AdverseSignalPhasePending,
			metav1.ConditionFalse,
			"SituationDeleting",
			fmt.Sprintf("Waiting for a current AdverseSituation %s/%s.", situation.Namespace, situation.Name),
			nil,
			"",
			0,
			nil,
			adverseSignalPendingRequeue,
		)
	}

	event, reportID := adverseSignalEvent(signal, situation)
	originalSituation := situation.DeepCopy()
	situationStatus := situation.Status
	changed, delivered := adverseSituationRecordSignalEvent(event, reportID, adverseSituationBuffer(situation), &situationStatus)
	if !delivered {
		return r.patchAdverseSignalStatus(
			ctx,
			signal,
			controlv1alpha1.AdverseSignalPhasePending,
			metav1.ConditionFalse,
			"SituationBusy",
			fmt.Sprintf("Waiting for delivery capacity in AdverseSituation %s/%s.", situation.Namespace, situation.Name),
			nil,
			"",
			0,
			nil,
			adverseSignalPendingRequeue,
		)
	}
	situationStatus.ObservedGeneration = situation.Generation
	if changed {
		situation.Status = situationStatus
		if err := r.Status().Patch(ctx, situation, client.MergeFrom(originalSituation)); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("record AdverseSignal in situation: %w", err)
		}
	}

	acceptedAt := metav1.Now()
	ref := &controlv1alpha1.NamespacedObjectReference{Name: situation.Name, Namespace: situation.Namespace}
	result, err := r.patchAdverseSignalStatus(
		ctx,
		signal,
		controlv1alpha1.AdverseSignalPhaseAccepted,
		metav1.ConditionTrue,
		"Recorded",
		fmt.Sprintf("Recorded in AdverseSituation %s/%s sequence %d.", situation.Namespace, situation.Name, situationStatus.Sequence),
		ref,
		string(situation.UID),
		situationStatus.Sequence,
		&acceptedAt,
		0,
	)
	if err != nil || result.Requeue || result.RequeueAfter > 0 {
		return result, err
	}
	result, err = r.clearAdverseSignalReceipt(ctx, signal, situation)
	if err != nil || result.Requeue || result.RequeueAfter > 0 {
		return result, err
	}
	return r.removeAdverseSignalFinalizer(ctx, client.ObjectKeyFromObject(signal))
}

func adverseSignalValidationError(signal *controlv1alpha1.AdverseSignal) (string, string) {
	if signal == nil {
		return "InvalidSignal", "AdverseSignal is required."
	}
	if strings.TrimSpace(signal.Spec.SituationRef.Name) == "" {
		return "InvalidSituationRef", "spec.situationRef.name is required."
	}
	if strings.TrimSpace(signal.Spec.SourceRef.Kind) == "" || strings.TrimSpace(signal.Spec.SourceRef.Name) == "" {
		return "InvalidSourceRef", "spec.sourceRef.kind and spec.sourceRef.name are required."
	}
	if strings.TrimSpace(signal.Spec.Trigger.Reason) == "" {
		return "InvalidTrigger", "spec.trigger.reason is required."
	}
	return "", ""
}

func adverseSignalEvent(signal *controlv1alpha1.AdverseSignal, situation *controlv1alpha1.AdverseSituation) (controlv1alpha1.AdverseSituationEvent, string) {
	reportID := adverseSignalReportID(signal)
	dedupeKey := strings.TrimSpace(signal.Spec.DedupeKey)
	if dedupeKey == "" {
		dedupeKey = fmt.Sprintf(
			"%s/%s/%s/%s/%s/%s/%s",
			signal.Spec.SourceRef.APIVersion,
			signal.Spec.SourceRef.Kind,
			signal.Spec.SourceRef.Namespace,
			signal.Spec.SourceRef.Name,
			signal.Spec.Trigger.Phase,
			signal.Spec.Trigger.ConditionType,
			signal.Spec.Trigger.Reason,
		)
	}
	eventID := shortHash(fmt.Sprintf("adverse-signal-event/%s/%s", situation.UID, dedupeKey))
	return controlv1alpha1.AdverseSituationEvent{
		ID: eventID,
		SignalRef: &controlv1alpha1.NamespacedObjectReference{
			Name:      signal.Name,
			Namespace: signal.Namespace,
		},
		SourceRef:        signal.Spec.SourceRef,
		SourceUID:        strings.TrimSpace(signal.Spec.SourceUID),
		SourceURL:        strings.TrimSpace(signal.Spec.SourceURL),
		SourceGeneration: signal.Spec.Trigger.ObservedGeneration,
		Phase:            strings.TrimSpace(signal.Spec.Trigger.Phase),
		ConditionType:    strings.TrimSpace(signal.Spec.Trigger.ConditionType),
		ConditionStatus:  signal.Spec.Trigger.ConditionStatus,
		Reason:           strings.TrimSpace(signal.Spec.Trigger.Reason),
		Message:          strings.TrimSpace(signal.Spec.Trigger.Message),
		ResourceVersion:  strings.TrimSpace(signal.Spec.Trigger.ResourceVersion),
		ObservedAt:       signal.Spec.Trigger.ObservedAt,
	}, reportID
}

func adverseSignalReportID(signal *controlv1alpha1.AdverseSignal) string {
	if signal == nil {
		return ""
	}
	reportIdentity := firstNonEmpty(string(signal.UID), signal.Namespace+"/"+signal.Name)
	return shortHash("adverse-signal-report/" + reportIdentity)
}

func (r *AdverseSignalReconciler) clearAdverseSignalReceipt(ctx context.Context, signal *controlv1alpha1.AdverseSignal, situation *controlv1alpha1.AdverseSituation) (ctrl.Result, error) {
	if signal == nil {
		return ctrl.Result{}, nil
	}
	if situation == nil {
		situationName := strings.TrimSpace(signal.Spec.SituationRef.Name)
		if signal.Status.SituationRef != nil && strings.TrimSpace(signal.Status.SituationRef.Name) != "" {
			situationName = strings.TrimSpace(signal.Status.SituationRef.Name)
		}
		if situationName == "" {
			return ctrl.Result{}, nil
		}
		situation = &controlv1alpha1.AdverseSituation{}
		key := client.ObjectKey{Namespace: signal.Namespace, Name: situationName}
		if err := r.Get(ctx, key, situation); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}
	if signal.Status.SituationUID != "" && string(situation.UID) != signal.Status.SituationUID {
		return ctrl.Result{}, nil
	}
	reportID := adverseSignalReportID(signal)
	original := situation.DeepCopy()
	changed := false
	for i := range situation.Status.Events {
		receipts := situation.Status.Events[i].ReportIDs[:0]
		for _, receipt := range situation.Status.Events[i].ReportIDs {
			if receipt == reportID {
				changed = true
				continue
			}
			receipts = append(receipts, receipt)
		}
		situation.Status.Events[i].ReportIDs = receipts
	}
	if !changed {
		return ctrl.Result{}, nil
	}
	if err := r.Status().Patch(ctx, situation, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("clear AdverseSignal receipt: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *AdverseSignalReconciler) removeAdverseSignalFinalizer(ctx context.Context, key client.ObjectKey) (ctrl.Result, error) {
	signal := &controlv1alpha1.AdverseSignal{}
	if err := r.Get(ctx, key, signal); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !controllerutil.ContainsFinalizer(signal, adverseSignalReceiptFinalizer) {
		return ctrl.Result{}, nil
	}
	original := signal.DeepCopy()
	controllerutil.RemoveFinalizer(signal, adverseSignalReceiptFinalizer)
	if err := r.Patch(ctx, signal, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("remove AdverseSignal receipt finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *AdverseSignalReconciler) patchAdverseSignalStatus(
	ctx context.Context,
	signal *controlv1alpha1.AdverseSignal,
	phase controlv1alpha1.AdverseSignalPhase,
	conditionStatus metav1.ConditionStatus,
	reason string,
	message string,
	situationRef *controlv1alpha1.NamespacedObjectReference,
	situationUID string,
	sequence int64,
	acceptedAt *metav1.Time,
	requeueAfter time.Duration,
) (ctrl.Result, error) {
	original := signal.DeepCopy()
	signal.Status.ObservedGeneration = signal.Generation
	signal.Status.Phase = phase
	signal.Status.SituationRef = situationRef
	signal.Status.SituationUID = situationUID
	signal.Status.SituationSequence = sequence
	signal.Status.AcceptedAt = acceptedAt
	if phase == controlv1alpha1.AdverseSignalPhaseAccepted {
		signal.Status.EventID = adverseSignalStatusEventID(signal, situationUID)
	} else {
		signal.Status.EventID = ""
	}
	now := metav1.Now()
	apimeta.SetStatusCondition(&signal.Status.Conditions, metav1.Condition{
		Type:               adverseSignalAccepted,
		Status:             conditionStatus,
		ObservedGeneration: signal.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	if err := r.Status().Patch(ctx, signal, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	if requeueAfter > 0 {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	return ctrl.Result{}, nil
}

func adverseSignalStatusEventID(signal *controlv1alpha1.AdverseSignal, situationUID string) string {
	dedupeKey := strings.TrimSpace(signal.Spec.DedupeKey)
	if dedupeKey == "" {
		dedupeKey = fmt.Sprintf(
			"%s/%s/%s/%s/%s/%s/%s",
			signal.Spec.SourceRef.APIVersion,
			signal.Spec.SourceRef.Kind,
			signal.Spec.SourceRef.Namespace,
			signal.Spec.SourceRef.Name,
			signal.Spec.Trigger.Phase,
			signal.Spec.Trigger.ConditionType,
			signal.Spec.Trigger.Reason,
		)
	}
	return shortHash(fmt.Sprintf("adverse-signal-event/%s/%s", situationUID, dedupeKey))
}

func (r *AdverseSignalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("adversesignal").
		For(&controlv1alpha1.AdverseSignal{}).
		Complete(r)
}
