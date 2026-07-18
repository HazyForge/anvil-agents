package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
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
	if !adverseSituationRecordSignalEvent(firstEvent, firstReport, buffer, &status) {
		t.Fatalf("first signal was not recorded")
	}
	if !adverseSituationRecordSignalEvent(secondEvent, secondReport, buffer, &status) {
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
	if !adverseSituationRecordSignalEvent(thirdEvent, thirdReport, buffer, &status) {
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
	if adverseSituationRecordSignalEvent(event, reportID, controlv1alpha1.AdverseSituationBufferSpec{}, &status) {
		t.Fatalf("late retry should be an idempotent no-op")
	}
	if status.Phase != controlv1alpha1.AdverseSituationPhaseResolved || status.Sequence != 4 || status.EventCount != 1 {
		t.Fatalf("late retry reopened resolved status: %#v", status)
	}
}

func testAdverseSignal(reason string) *controlv1alpha1.AdverseSignal {
	return &controlv1alpha1.AdverseSignal{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-alert-1", Namespace: "store", UID: types.UID("signal-uid")},
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
