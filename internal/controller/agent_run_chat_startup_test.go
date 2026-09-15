package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func startupChatRun() *controlv1alpha1.AgentRun {
	run := testPendingApplicationRun("chat-startup", "")
	run.UID = "chat-startup-uid"
	run.CreationTimestamp = metav1.NewTime(time.Now().Add(-6 * time.Minute))
	run.Labels = map[string]string{controlv1alpha1.AgentRunChatThreadLabel: "thread-1", agentRunChatTurnLabel: "turn-1"}
	run.Spec.SourceRef = controlv1alpha1.AgentRunSourceRef{Kind: "ChatThread", Name: "thread-1", Namespace: run.Namespace}
	run.Spec.Purpose = controlv1alpha1.AgentRunPurposeManual
	run.Spec.ScheduleRef = nil
	run.Spec.Harness.Backend = controlv1alpha1.AgentRunHarnessBackendSpec{Kind: controlv1alpha1.AgentRunHarnessBackendCustom, Image: "busybox:1.37.0", Custom: &controlv1alpha1.AgentRunCustomBackendSpec{Command: []string{"/bin/true"}}}
	return run
}

func startupTestReconciler(t *testing.T, run *controlv1alpha1.AgentRun, objects ...client.Object) *AgentRunReconciler {
	t.Helper()
	scheme := newAgentControlTestScheme(t)
	objects = append(objects, run)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(run).WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
		if obj.GetUID() == "" {
			obj.SetUID(types.UID("fake-" + obj.GetName()))
		}
		return c.Create(ctx, obj, opts...)
	}}).Build()
	return &AgentRunReconciler{Client: c, Scheme: scheme}
}

func TestChatStartupExpiryBeforeLaunch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*controlv1alpha1.AgentRun)
		expired bool
	}{
		{"stale chat", nil, true},
		{"fresh chat", func(r *controlv1alpha1.AgentRun) { r.CreationTimestamp = metav1.Now() }, false},
		{"non chat", func(r *controlv1alpha1.AgentRun) { r.Spec.SourceRef.Kind = "Manual" }, false},
		{"mismatched thread", func(r *controlv1alpha1.AgentRun) { r.Labels[controlv1alpha1.AgentRunChatThreadLabel] = "different" }, false},
		{"missing turn label", func(r *controlv1alpha1.AgentRun) { delete(r.Labels, agentRunChatTurnLabel) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			run := startupChatRun()
			if tc.change != nil {
				tc.change(run)
			}
			originalSpec := run.Spec.DeepCopy()
			r := startupTestReconciler(t, run)
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}
			for i := 0; i < 3; i++ {
				if _, err := r.Reconcile(ctx, req); err != nil {
					t.Fatal(err)
				}
			}
			got := &controlv1alpha1.AgentRun{}
			if err := r.Get(ctx, req.NamespacedName, got); err != nil {
				t.Fatal(err)
			}
			jobs := &batchv1.JobList{}
			if err := r.List(ctx, jobs); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Spec, *originalSpec) {
				t.Fatal("execution spec changed")
			}
			if tc.expired {
				condition := apimeta.FindStatusCondition(got.Status.Conditions, agentRunReady)
				if got.Status.Phase != controlv1alpha1.AgentRunPhaseFailed || condition == nil || condition.Reason != agentRunChatStartupReason || got.Status.Error != agentRunChatStartupMessage || got.Status.CompletedAt == nil || got.Status.StartedAt != nil || len(jobs.Items) != 0 {
					t.Fatalf("expired status=%#v jobs=%d", got.Status, len(jobs.Items))
				}
			} else if len(jobs.Items) != 1 || got.Status.Phase != controlv1alpha1.AgentRunPhaseRunning {
				t.Fatalf("unaffected status=%#v jobs=%d", got.Status, len(jobs.Items))
			}
		})
	}
}

// Seed the exact Job/payload as if Create succeeded before status persistence.
func startupExistingJob(t *testing.T, run *controlv1alpha1.AgentRun, scheme *runtime.Scheme) (*corev1.ConfigMap, *batchv1.Job) {
	t.Helper()
	ctx := context.Background()
	seed := &AgentRunReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
	effective, composition, phase, _, message, err := seed.resolveAgentRunComposition(ctx, run)
	if err != nil || phase != "" {
		t.Fatalf("composition: %s %v", message, err)
	}
	prompt := buildAgentRunPrompt(effective)
	hash := shortHash(prompt)
	effective.Status.ResolvedComposition = composition.DeepCopy()
	body, err := seed.agentRunContextJSON(ctx, effective)
	if err != nil {
		t.Fatal(err)
	}
	data, err := seed.agentRunConfigMapData(ctx, effective, prompt, string(body))
	if err != nil {
		t.Fatal(err)
	}
	controller := true
	owner := metav1.OwnerReference{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller}
	payload := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: agentRunChildName(run.Name, "context", hash), Namespace: run.Namespace, UID: "payload-uid", Labels: agentRunLabels(effective, ""), OwnerReferences: []metav1.OwnerReference{owner}}, Data: data, Immutable: boolPtr(true)}
	composition.PayloadDigest = digestJSON(data)
	effective.Status.ResolvedComposition = composition.DeepCopy()
	job := seed.agentRunJob(effective, agentRunChildName(run.Name, "harness", hash), payload.Name, nil)
	job.UID = "already-started-job"
	job.OwnerReferences = []metav1.OwnerReference{owner}
	job.Status.Active = 1
	return payload, job
}

func TestStaleChatPreservesExistingJobAndCrashGapRecovery(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		t.Run(map[bool]string{false: "status absent", true: "status recorded"}[recorded], func(t *testing.T) {
			ctx := context.Background()
			run := startupChatRun()
			scheme := newAgentControlTestScheme(t)
			payload, job := startupExistingJob(t, run, scheme)
			if recorded {
				run.Status = controlv1alpha1.AgentRunStatus{Phase: controlv1alpha1.AgentRunPhaseRunning, JobRef: &controlv1alpha1.NamespacedObjectReference{Name: job.Name, Namespace: job.Namespace}, JobUID: string(job.UID)}
			}
			r := startupTestReconciler(t, run, payload, job)
			before := &batchv1.Job{}
			if err := r.Get(ctx, client.ObjectKeyFromObject(job), before); err != nil {
				t.Fatal(err)
			}
			r.APIReader = r.Client
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
				t.Fatal(err)
			}
			got := &controlv1alpha1.AgentRun{}
			if err := r.Get(ctx, client.ObjectKeyFromObject(run), got); err != nil {
				t.Fatal(err)
			}
			if got.Status.Phase != controlv1alpha1.AgentRunPhaseRunning || got.Status.JobUID != string(job.UID) {
				t.Fatalf("existing execution not recovered: %#v", got.Status)
			}
			jobs := &batchv1.JobList{}
			if err := r.List(ctx, jobs); err != nil {
				t.Fatal(err)
			}
			if len(jobs.Items) != 1 || !reflect.DeepEqual(jobs.Items[0].Spec, before.Spec) {
				t.Fatal("existing Job changed or duplicate launched")
			}
		})
	}
}

func TestChatStartupExpiryRequiresAuthoritativeJobAbsence(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		t.Run(map[bool]string{false: "unlabelled owned Job", true: "deleting owned Job"}[deleting], func(t *testing.T) {
			ctx := context.Background()
			run := startupChatRun()
			r := startupTestReconciler(t, run)
			_, job := startupExistingJob(t, run, r.Scheme)
			job.Labels = nil
			if deleting {
				now := metav1.Now()
				job.DeletionTimestamp = &now
				job.Finalizers = []string{"test.example/hold"}
			}
			// The cached Client has no Job. Only the authoritative API reader sees it.
			r.APIReader = fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(job).Build()
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)})
			if err != nil {
				t.Fatal(err)
			}
			got := &controlv1alpha1.AgentRun{}
			if err := r.Get(ctx, client.ObjectKeyFromObject(run), got); err != nil {
				t.Fatal(err)
			}
			if agentRunPhaseTerminal(got.Status.Phase) || result.RequeueAfter == 0 {
				t.Fatalf("launched work expired: %#v", got.Status)
			}
			jobs := &batchv1.JobList{}
			if err := r.List(ctx, jobs); err != nil {
				t.Fatal(err)
			}
			if len(jobs.Items) != 0 {
				t.Fatal("launched duplicate Job")
			}
		})
	}
}

func TestChatStartupExpiryDoesNotOverwriteConcurrentLaunchReceipt(t *testing.T) {
	ctx := context.Background()
	run := startupChatRun()
	r := startupTestReconciler(t, run)
	r.APIReader = fake.NewClientBuilder().WithScheme(r.Scheme).WithInterceptorFuncs(interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
		options := &client.ListOptions{}
		options.ApplyOptions(opts)
		if _, ok := list.(*batchv1.JobList); ok && options.LabelSelector == nil {
			latest := &controlv1alpha1.AgentRun{}
			if err := r.Get(ctx, client.ObjectKeyFromObject(run), latest); err != nil {
				return err
			}
			now := metav1.Now()
			latest.Status.JobCreateAttemptedAt = &now
			if err := r.Status().Update(ctx, latest); err != nil {
				return err
			}
		}
		return c.List(ctx, list, opts...)
	}}).Build()
	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)})
	if err != nil {
		t.Fatal(err)
	}
	got := &controlv1alpha1.AgentRun{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(run), got); err != nil {
		t.Fatal(err)
	}
	if !result.Requeue || got.Status.JobCreateAttemptedAt == nil || agentRunPhaseTerminal(got.Status.Phase) {
		t.Fatalf("overwrote concurrent receipt: %#v, result %#v", got.Status, result)
	}
}

func TestChatStartupDeadlineIdentityAndBoundary(t *testing.T) {
	now := time.Now()
	run := startupChatRun()
	run.CreationTimestamp = metav1.NewTime(now.Add(-agentRunChatStartupBudget))
	if !agentRunChatStartupExpired(run, &run.Status, now) {
		t.Fatal("deadline boundary must expire")
	}
	if agentRunChatStartupExpired(run, &run.Status, now.Add(-time.Nanosecond)) {
		t.Fatal("expired before creation-based budget elapsed")
	}
	for _, mutate := range []func(*controlv1alpha1.AgentRun){
		func(r *controlv1alpha1.AgentRun) { r.CreationTimestamp = metav1.Time{} },
		func(r *controlv1alpha1.AgentRun) { r.Labels[agentRunChatTurnLabel] = "invalid label" },
		func(r *controlv1alpha1.AgentRun) { r.Spec.SourceRef.Kind = "chatthread" },
		func(r *controlv1alpha1.AgentRun) { r.Spec.SourceRef.Namespace = "different" },
		func(r *controlv1alpha1.AgentRun) { r.Status.Phase = controlv1alpha1.AgentRunPhaseRunning },
		func(r *controlv1alpha1.AgentRun) { r.Status.StartedAt = &metav1.Time{Time: now} },
		func(r *controlv1alpha1.AgentRun) { r.Status.JobUID = "launched" },
	} {
		other := run.DeepCopy()
		mutate(other)
		if agentRunChatStartupExpired(other, &other.Status, now) {
			t.Fatalf("ineligible run expired: %#v", other)
		}
	}
}
