package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestAdverseSignalRecordsNamedSituationAndBecomesAccepted(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid")},
		Spec:       controlv1alpha1.AdverseSituationSpec{GroupKey: "application/checkout"},
	}
	signal := testAdverseSignal("provider-timeout")
	reconciler, c := testAdverseSignalReconciler(t, situation, signal)
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: signal.Namespace, Name: signal.Name}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("reconcile signal: %v", err)
	}

	storedSignal := &controlv1alpha1.AdverseSignal{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(signal), storedSignal); err != nil {
		t.Fatalf("get signal: %v", err)
	}
	if storedSignal.Status.Phase != controlv1alpha1.AdverseSignalPhaseAccepted {
		t.Fatalf("signal phase = %q, want Accepted", storedSignal.Status.Phase)
	}
	if len(storedSignal.Finalizers) != 0 {
		t.Fatalf("accepted signal retained finalizers: %#v", storedSignal.Finalizers)
	}
	if storedSignal.Status.SituationUID != "situation-uid" || storedSignal.Status.EventID == "" {
		t.Fatalf("accepted destination = %#v", storedSignal.Status)
	}

	storedSituation := &controlv1alpha1.AdverseSituation{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(situation), storedSituation); err != nil {
		t.Fatalf("get situation: %v", err)
	}
	if len(storedSituation.Status.Events) != 1 || storedSituation.Status.EventCount != 1 {
		t.Fatalf("situation status = %#v, want one event", storedSituation.Status)
	}
	event := storedSituation.Status.Events[0]
	if event.SignalRef == nil || event.SignalRef.Name != signal.Name || event.SourceRef.Kind != "MonitorAlert" {
		t.Fatalf("recorded event = %#v", event)
	}
	if len(event.ReportIDs) != 0 {
		t.Fatalf("accepted delivery receipts = %#v, want cleaned", event.ReportIDs)
	}
}

func TestAdverseSignalInstallsReceiptFinalizerBeforeDelivery(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid")},
	}
	signal := testAdverseSignal("ProviderTimeout")
	signal.Finalizers = nil
	reconciler, c := testAdverseSignalReconciler(t, situation, signal)
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: signal.Namespace, Name: signal.Name}}
	result, err := reconciler.Reconcile(context.Background(), request)
	if err != nil {
		t.Fatalf("install receipt finalizer: %v", err)
	}
	if !result.Requeue {
		t.Fatalf("initial finalizer install did not requeue")
	}
	storedSignal := &controlv1alpha1.AdverseSignal{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(signal), storedSignal); err != nil {
		t.Fatalf("get signal: %v", err)
	}
	if len(storedSignal.Finalizers) != 1 || storedSignal.Finalizers[0] != adverseSignalReceiptFinalizer {
		t.Fatalf("signal finalizers = %#v", storedSignal.Finalizers)
	}
	storedSituation := &controlv1alpha1.AdverseSituation{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(situation), storedSituation); err != nil {
		t.Fatalf("get situation: %v", err)
	}
	if storedSituation.Status.EventCount != 0 || len(storedSituation.Status.Events) != 0 {
		t.Fatalf("signal delivered before finalizer persisted: %#v", storedSituation.Status)
	}
}

func TestTerminalAdverseSignalDoesNotReinstallReceiptFinalizer(t *testing.T) {
	t.Parallel()

	for _, phase := range []controlv1alpha1.AdverseSignalPhase{
		controlv1alpha1.AdverseSignalPhaseAccepted,
		controlv1alpha1.AdverseSignalPhaseRejected,
	} {
		phase := phase
		t.Run(string(phase), func(t *testing.T) {
			situation := &controlv1alpha1.AdverseSituation{
				ObjectMeta: metav1.ObjectMeta{Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid")},
			}
			signal := testAdverseSignal("ProviderTimeout")
			signal.Status.Phase = phase
			reconciler, c := testAdverseSignalReconciler(t, situation, signal)
			request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: signal.Namespace, Name: signal.Name}}

			for attempt := 0; attempt < 2; attempt++ {
				if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
					t.Fatalf("reconcile terminal signal attempt %d: %v", attempt+1, err)
				}
			}

			storedSignal := &controlv1alpha1.AdverseSignal{}
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(signal), storedSignal); err != nil {
				t.Fatalf("get signal: %v", err)
			}
			if len(storedSignal.Finalizers) != 0 {
				t.Fatalf("terminal signal reinstalled finalizers: %#v", storedSignal.Finalizers)
			}
		})
	}
}

func TestAcceptedSignalWithoutFinalizerClearsPreUpgradeReceipt(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid")},
	}
	signal := testAdverseSignal("ProviderTimeout")
	event, reportID := adverseSignalEvent(signal, situation)
	now := metav1.Now()
	event.ReportIDs = []string{reportID}
	event.Count = 1
	event.FirstSeenAt = &now
	event.LastSeenAt = &now
	situation.Status = controlv1alpha1.AdverseSituationStatus{
		Phase:      controlv1alpha1.AdverseSituationPhaseOpen,
		Sequence:   1,
		EventCount: 1,
		Events:     []controlv1alpha1.AdverseSituationEvent{event},
	}
	signal.Finalizers = nil
	signal.Status.Phase = controlv1alpha1.AdverseSignalPhaseAccepted

	reconciler, c := testAdverseSignalReconciler(t, situation, signal)
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: signal.Namespace, Name: signal.Name}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("reconcile pre-upgrade accepted signal: %v", err)
	}

	storedSituation := &controlv1alpha1.AdverseSituation{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(situation), storedSituation); err != nil {
		t.Fatalf("get situation: %v", err)
	}
	if len(storedSituation.Status.Events) != 1 || len(storedSituation.Status.Events[0].ReportIDs) != 0 {
		t.Fatalf("accepted signal left pre-upgrade receipt behind: %#v", storedSituation.Status.Events)
	}
	storedSignal := &controlv1alpha1.AdverseSignal{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(signal), storedSignal); err != nil {
		t.Fatalf("get signal: %v", err)
	}
	if len(storedSignal.Finalizers) != 0 {
		t.Fatalf("accepted signal installed finalizers during cleanup: %#v", storedSignal.Finalizers)
	}
}

func TestDeletingSignalClearsReceiptBeforeFinalizerRemoval(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid")},
	}
	signal := testAdverseSignal("ProviderTimeout")
	event, reportID := adverseSignalEvent(signal, situation)
	now := metav1.Now()
	event.ReportIDs = []string{reportID}
	event.Count = 1
	event.FirstSeenAt = &now
	event.LastSeenAt = &now
	situation.Status = controlv1alpha1.AdverseSituationStatus{
		Phase:      controlv1alpha1.AdverseSituationPhaseOpen,
		Sequence:   1,
		EventCount: 1,
		Events:     []controlv1alpha1.AdverseSituationEvent{event},
	}
	signal.DeletionTimestamp = &now

	reconciler, c := testAdverseSignalReconciler(t, situation, signal)
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: signal.Namespace, Name: signal.Name}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("reconcile deleting signal: %v", err)
	}
	storedSituation := &controlv1alpha1.AdverseSituation{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(situation), storedSituation); err != nil {
		t.Fatalf("get situation: %v", err)
	}
	if len(storedSituation.Status.Events) != 1 || len(storedSituation.Status.Events[0].ReportIDs) != 0 {
		t.Fatalf("deleting signal left receipt behind: %#v", storedSituation.Status.Events)
	}
	storedSignal := &controlv1alpha1.AdverseSignal{}
	err := c.Get(context.Background(), client.ObjectKeyFromObject(signal), storedSignal)
	if err == nil && len(storedSignal.Finalizers) != 0 {
		t.Fatalf("deleting signal retained finalizer: %#v", storedSignal.Finalizers)
	}
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("get deleting signal: %v", err)
	}
}

func TestAdverseSignalRetryAfterSituationWriteIsIdempotent(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid")},
		Status: controlv1alpha1.AdverseSituationStatus{
			Phase:      controlv1alpha1.AdverseSituationPhaseOpen,
			Sequence:   1,
			EventCount: 1,
		},
	}
	signal := testAdverseSignal("provider-timeout")
	event, reportID := adverseSignalEvent(signal, situation)
	now := metav1.Now()
	event.ReportIDs = []string{reportID}
	event.Count = 1
	event.FirstSeenAt = &now
	event.LastSeenAt = &now
	situation.Status.Events = []controlv1alpha1.AdverseSituationEvent{event}
	situation.Status.LastEventAt = &now

	reconciler, c := testAdverseSignalReconciler(t, situation, signal)
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: signal.Namespace, Name: signal.Name}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("retry signal: %v", err)
	}
	stored := &controlv1alpha1.AdverseSituation{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(situation), stored); err != nil {
		t.Fatalf("get situation: %v", err)
	}
	if stored.Status.EventCount != 1 || stored.Status.DuplicateCount != 0 || stored.Status.Events[0].Count != 1 {
		t.Fatalf("retry changed counters: %#v", stored.Status)
	}
}

func TestAdverseSignalWaitsForMissingSameNamespaceSituation(t *testing.T) {
	t.Parallel()

	signal := testAdverseSignal("provider-timeout")
	reconciler, c := testAdverseSignalReconciler(t, signal)
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: signal.Namespace, Name: signal.Name}}
	result, err := reconciler.Reconcile(context.Background(), request)
	if err != nil {
		t.Fatalf("reconcile missing situation: %v", err)
	}
	if result.RequeueAfter <= 0 {
		t.Fatalf("requeueAfter = %s, want retry", result.RequeueAfter)
	}
	stored := &controlv1alpha1.AdverseSignal{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(signal), stored); err != nil {
		t.Fatalf("get signal: %v", err)
	}
	if stored.Status.Phase != controlv1alpha1.AdverseSignalPhasePending || stored.Status.Conditions[0].Reason != "SituationNotFound" {
		t.Fatalf("missing destination status = %#v", stored.Status)
	}
}

func TestAdverseSignalBackpressuresWhenReceiptSetIsFull(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid")},
	}
	signal := testAdverseSignal("ProviderTimeout")
	event, _ := adverseSignalEvent(signal, situation)
	now := metav1.Now()
	for i := 0; i < adverseSituationMaxReportIDsPerEvent; i++ {
		event.ReportIDs = append(event.ReportIDs, shortHash(fmt.Sprintf("existing-receipt/%d", i)))
	}
	event.Count = int32(len(event.ReportIDs))
	event.FirstSeenAt = &now
	event.LastSeenAt = &now
	situation.Status = controlv1alpha1.AdverseSituationStatus{
		Phase:      controlv1alpha1.AdverseSituationPhaseOpen,
		Sequence:   1,
		EventCount: event.Count,
		Events:     []controlv1alpha1.AdverseSituationEvent{event},
	}

	reconciler, c := testAdverseSignalReconciler(t, situation, signal)
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: signal.Namespace, Name: signal.Name}}
	result, err := reconciler.Reconcile(context.Background(), request)
	if err != nil {
		t.Fatalf("reconcile full receipt set: %v", err)
	}
	if result.RequeueAfter <= 0 {
		t.Fatalf("requeueAfter = %s, want backpressure retry", result.RequeueAfter)
	}
	storedSignal := &controlv1alpha1.AdverseSignal{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(signal), storedSignal); err != nil {
		t.Fatalf("get signal: %v", err)
	}
	if storedSignal.Status.Phase != controlv1alpha1.AdverseSignalPhasePending || storedSignal.Status.Conditions[0].Reason != "SituationBusy" {
		t.Fatalf("backpressured signal status = %#v", storedSignal.Status)
	}
	storedSituation := &controlv1alpha1.AdverseSituation{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(situation), storedSituation); err != nil {
		t.Fatalf("get situation: %v", err)
	}
	if storedSituation.Status.EventCount != event.Count || len(storedSituation.Status.Events[0].ReportIDs) != adverseSituationMaxReportIDsPerEvent {
		t.Fatalf("backpressure mutated situation: %#v", storedSituation.Status)
	}
}

func TestDistinctSignalsDedupeWithinWindowAndStartNewEventOutsideIt(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{ObjectMeta: metav1.ObjectMeta{
		Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid"),
	}}
	buffer := controlv1alpha1.AdverseSituationBufferSpec{DedupeWindowSeconds: 60}
	status := controlv1alpha1.AdverseSituationStatus{}
	first := testAdverseSignal("ProviderTimeout")
	first.Name = "signal-1"
	first.UID = types.UID("signal-uid-1")
	second := first.DeepCopy()
	second.Name = "signal-2"
	second.UID = types.UID("signal-uid-2")

	firstEvent, firstReport := adverseSignalEvent(first, situation)
	secondEvent, secondReport := adverseSignalEvent(second, situation)
	if changed, delivered := adverseSituationRecordSignalEvent(firstEvent, firstReport, buffer, &status); !changed || !delivered {
		t.Fatalf("first signal was not recorded")
	}
	if changed, delivered := adverseSituationRecordSignalEvent(secondEvent, secondReport, buffer, &status); !changed || !delivered {
		t.Fatalf("second signal was not recorded")
	}
	if len(status.Events) != 1 || status.EventCount != 2 || status.DuplicateCount != 1 || status.Events[0].Count != 2 {
		t.Fatalf("deduped status = %#v", status)
	}
	if status.Events[0].SignalRef == nil || status.Events[0].SignalRef.Name != "signal-2" {
		t.Fatalf("deduped event did not retain latest report context: %#v", status.Events[0])
	}

	old := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	status.Events[0].LastSeenAt = &old
	third := first.DeepCopy()
	third.Name = "signal-3"
	third.UID = types.UID("signal-uid-3")
	thirdEvent, thirdReport := adverseSignalEvent(third, situation)
	if changed, delivered := adverseSituationRecordSignalEvent(thirdEvent, thirdReport, buffer, &status); !changed || !delivered {
		t.Fatalf("third signal was not recorded")
	}
	if len(status.Events) != 2 || status.EventCount != 3 || status.DuplicateCount != 1 {
		t.Fatalf("outside-window status = %#v", status)
	}
}

func TestReporterObservedAtDoesNotControlQuietWindow(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{ObjectMeta: metav1.ObjectMeta{UID: types.UID("situation-uid")}}
	signal := testAdverseSignal("ProviderTimeout")
	future := metav1.NewTime(time.Now().Add(24 * time.Hour))
	signal.Spec.Trigger.ObservedAt = &future
	event, reportID := adverseSignalEvent(signal, situation)
	status := controlv1alpha1.AdverseSituationStatus{}
	before := time.Now()
	adverseSituationRecordSignalEvent(event, reportID, controlv1alpha1.AdverseSituationBufferSpec{QuietPeriodSeconds: 60}, &status)
	if status.QuietUntil == nil || status.QuietUntil.After(before.Add(2*time.Minute)) {
		t.Fatalf("quiet window trusted reporter time: observedAt=%s quietUntil=%v", future.Time, status.QuietUntil)
	}
}

func TestSignalAfterResolutionStartsOneNewSequence(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{ObjectMeta: metav1.ObjectMeta{UID: types.UID("situation-uid")}}
	status := controlv1alpha1.AdverseSituationStatus{
		Phase: controlv1alpha1.AdverseSituationPhaseResolved, Sequence: 4, EventCount: 9,
		Events: []controlv1alpha1.AdverseSituationEvent{{ID: "old"}},
	}
	first := testAdverseSignal("ProviderTimeout")
	firstEvent, firstReport := adverseSignalEvent(first, situation)
	adverseSituationRecordSignalEvent(firstEvent, firstReport, controlv1alpha1.AdverseSituationBufferSpec{}, &status)
	second := first.DeepCopy()
	second.Name = "signal-next"
	second.UID = types.UID("signal-next-uid")
	secondEvent, secondReport := adverseSignalEvent(second, situation)
	adverseSituationRecordSignalEvent(secondEvent, secondReport, controlv1alpha1.AdverseSituationBufferSpec{}, &status)
	if status.Sequence != 5 || len(status.Events) != 1 || status.EventCount != 2 {
		t.Fatalf("new sequence status = %#v", status)
	}
}

func TestResolvedSituationBackpressuresNewSequenceUntilReceiptCleanup(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{ObjectMeta: metav1.ObjectMeta{UID: types.UID("situation-uid")}}
	oldSignal := testAdverseSignal("OldFailure")
	oldSignal.Spec.DedupeKey = "old"
	oldEvent, oldReportID := adverseSignalEvent(oldSignal, situation)
	now := metav1.Now()
	oldEvent.ReportIDs = []string{oldReportID}
	oldEvent.Count = 1
	oldEvent.FirstSeenAt = &now
	oldEvent.LastSeenAt = &now
	status := controlv1alpha1.AdverseSituationStatus{
		Phase:      controlv1alpha1.AdverseSituationPhaseResolved,
		Sequence:   4,
		EventCount: 1,
		Events:     []controlv1alpha1.AdverseSituationEvent{oldEvent},
	}
	newSignal := testAdverseSignal("NewFailure")
	newSignal.UID = types.UID("new-signal-uid")
	newSignal.Spec.DedupeKey = "new"
	newEvent, newReportID := adverseSignalEvent(newSignal, situation)

	changed, delivered := adverseSituationRecordSignalEvent(newEvent, newReportID, controlv1alpha1.AdverseSituationBufferSpec{}, &status)
	if changed || delivered {
		t.Fatalf("resolved receipt result = changed %t delivered %t, want backpressure", changed, delivered)
	}
	if status.Phase != controlv1alpha1.AdverseSituationPhaseResolved || status.Sequence != 4 || len(status.Events) != 1 {
		t.Fatalf("backpressure reset resolved sequence: %#v", status)
	}

	status.Events[0].ReportIDs = nil
	changed, delivered = adverseSituationRecordSignalEvent(newEvent, newReportID, controlv1alpha1.AdverseSituationBufferSpec{}, &status)
	if !changed || !delivered || status.Sequence != 5 || len(status.Events) != 1 || status.Events[0].ID != newEvent.ID {
		t.Fatalf("receipt cleanup did not admit new sequence: changed=%t delivered=%t status=%#v", changed, delivered, status)
	}
}

func TestLateSignalRetryDoesNotReopenResolvedSequence(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{ObjectMeta: metav1.ObjectMeta{UID: types.UID("situation-uid")}}
	signal := testAdverseSignal("ProviderTimeout")
	event, reportID := adverseSignalEvent(signal, situation)
	now := metav1.Now()
	event.ReportIDs = []string{reportID}
	event.Count = 1
	event.FirstSeenAt = &now
	event.LastSeenAt = &now
	status := controlv1alpha1.AdverseSituationStatus{
		Phase: controlv1alpha1.AdverseSituationPhaseResolved, Sequence: 4, EventCount: 1,
		Events: []controlv1alpha1.AdverseSituationEvent{event},
	}
	changed, delivered := adverseSituationRecordSignalEvent(event, reportID, controlv1alpha1.AdverseSituationBufferSpec{}, &status)
	if changed || !delivered {
		t.Fatalf("late retry should be an idempotent no-op")
	}
	if status.Phase != controlv1alpha1.AdverseSituationPhaseResolved || status.Sequence != 4 || status.EventCount != 1 {
		t.Fatalf("late retry reopened resolved status: %#v", status)
	}
}

func TestSignalReceiptCapacityBackpressuresWithoutEviction(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{ObjectMeta: metav1.ObjectMeta{UID: types.UID("situation-uid")}}
	signal := testAdverseSignal("ProviderTimeout")
	event, reportID := adverseSignalEvent(signal, situation)
	now := metav1.Now()
	receipts := make([]string, adverseSituationMaxReportIDsPerEvent)
	for i := range receipts {
		receipts[i] = shortHash(fmt.Sprintf("existing-receipt/%d", i))
	}
	receiptCount := int32(len(receipts))
	event.ReportIDs = receipts
	event.Count = receiptCount
	event.FirstSeenAt = &now
	event.LastSeenAt = &now
	status := controlv1alpha1.AdverseSituationStatus{
		Phase:      controlv1alpha1.AdverseSituationPhaseOpen,
		Sequence:   1,
		EventCount: receiptCount,
		Events:     []controlv1alpha1.AdverseSituationEvent{event},
	}

	changed, delivered := adverseSituationRecordSignalEvent(event, reportID, controlv1alpha1.AdverseSituationBufferSpec{}, &status)
	if changed || delivered {
		t.Fatalf("full receipt set result = changed %t delivered %t, want backpressure", changed, delivered)
	}
	if status.EventCount != receiptCount || status.Events[0].Count != receiptCount || len(status.Events[0].ReportIDs) != len(receipts) {
		t.Fatalf("backpressure mutated counters or receipts: %#v", status)
	}

	status.Events[0].ReportIDs = status.Events[0].ReportIDs[1:]
	changed, delivered = adverseSituationRecordSignalEvent(event, reportID, controlv1alpha1.AdverseSituationBufferSpec{}, &status)
	if !changed || !delivered {
		t.Fatalf("available receipt slot result = changed %t delivered %t, want recorded", changed, delivered)
	}
}

func TestSignalEventRingBackpressuresBeforeEvictingReceipt(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{ObjectMeta: metav1.ObjectMeta{UID: types.UID("situation-uid")}}
	oldSignal := testAdverseSignal("OldFailure")
	oldSignal.Spec.DedupeKey = "old"
	oldEvent, oldReportID := adverseSignalEvent(oldSignal, situation)
	now := metav1.Now()
	oldEvent.ReportIDs = []string{oldReportID}
	oldEvent.Count = 1
	oldEvent.FirstSeenAt = &now
	oldEvent.LastSeenAt = &now
	status := controlv1alpha1.AdverseSituationStatus{
		Phase:      controlv1alpha1.AdverseSituationPhaseOpen,
		Sequence:   1,
		EventCount: 1,
		Events:     []controlv1alpha1.AdverseSituationEvent{oldEvent},
	}
	newSignal := testAdverseSignal("NewFailure")
	newSignal.UID = types.UID("new-signal-uid")
	newSignal.Spec.DedupeKey = "new"
	newEvent, newReportID := adverseSignalEvent(newSignal, situation)
	buffer := controlv1alpha1.AdverseSituationBufferSpec{MaxEvents: 1}

	changed, delivered := adverseSituationRecordSignalEvent(newEvent, newReportID, buffer, &status)
	if changed || delivered {
		t.Fatalf("receipt-bearing eviction result = changed %t delivered %t, want backpressure", changed, delivered)
	}
	if len(status.Events) != 1 || status.Events[0].ID != oldEvent.ID || status.EventCount != 1 {
		t.Fatalf("backpressure evicted receipt-bearing event: %#v", status)
	}

	status.Events[0].ReportIDs = nil
	changed, delivered = adverseSituationRecordSignalEvent(newEvent, newReportID, buffer, &status)
	if !changed || !delivered || len(status.Events) != 1 || status.Events[0].ID != newEvent.ID {
		t.Fatalf("cleared event ring did not admit new signal: changed=%t delivered=%t status=%#v", changed, delivered, status)
	}
}

func testAdverseSignal(reason string) *controlv1alpha1.AdverseSignal {
	return &controlv1alpha1.AdverseSignal{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "checkout-alert-1",
			Namespace:  "store",
			UID:        types.UID("signal-uid"),
			Finalizers: []string{adverseSignalReceiptFinalizer},
		},
		Spec: controlv1alpha1.AdverseSignalSpec{
			SituationRef: corev1.LocalObjectReference{Name: "checkout-health"},
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				APIVersion: "monitoring.example.io/v1",
				Kind:       "MonitorAlert",
				Namespace:  "store",
				Name:       "checkout-latency",
			},
			DedupeKey: "checkout/provider-timeout",
			Trigger:   controlv1alpha1.AdverseSignalTriggerSpec{Phase: "Firing", Reason: reason, Message: "provider timeout rate exceeded"},
		},
	}
}

func testAdverseSignalReconciler(t *testing.T, objects ...client.Object) (*AdverseSignalReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(
		&controlv1alpha1.AdverseSignal{},
		&controlv1alpha1.AdverseSituation{},
	).Build()
	return &AdverseSignalReconciler{Client: c, Scheme: scheme}, c
}
