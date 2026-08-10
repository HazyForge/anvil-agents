package agentctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/runapi"
)

type fakeBackend struct {
	defaultNamespace string
	created          *agentsv1alpha1.AgentRun
	createErr        error
	runs             []agentsv1alpha1.AgentRun
	getRun           *agentsv1alpha1.AgentRun
	getRunErr        error
	getRunCalls      int
	logBody          string
	logErr           error
	logErrors        []error
	logCalls         int
	logOptions       corev1.PodLogOptions
	job              *batchv1.Job
	jobErr           error
	pod              *corev1.Pod
	podErr           error
	events           []corev1.Event
	eventsErr        error
	eventUIDs        []types.UID
	dataVolume       *agentsv1alpha1.AgentDataVolume
	dataVolumeErr    error
	secret           *corev1.Secret
	secretErr        error
	createdSecret    *corev1.Secret
	authSession      *agentsv1alpha1.AgentAuthSession
	authSessions     []agentsv1alpha1.AgentAuthSession
	createAuthErr    error
	volumeCopy       *agentsv1alpha1.AgentDataVolumeCopy
	controls         []agentsv1alpha1.AgentRunControl
	controlGet       *agentsv1alpha1.AgentRunControl
	controlGetErr    error
	createdControl   *agentsv1alpha1.AgentRunControl
	updatedControls  []*agentsv1alpha1.AgentRunControl
	schedules        []agentsv1alpha1.AgentSchedule
	updatedSchedules []*agentsv1alpha1.AgentSchedule
}

func (backend *fakeBackend) DefaultNamespace() string { return backend.defaultNamespace }

func (backend *fakeBackend) CreateRun(_ context.Context, run *agentsv1alpha1.AgentRun) error {
	backend.created = run.DeepCopy()
	if backend.createErr != nil {
		return backend.createErr
	}
	if run.Name == "" {
		run.Name = run.GenerateName + "abc12"
	}
	return nil
}

func (backend *fakeBackend) ListRuns(_ context.Context, _ string, _ bool) (*agentsv1alpha1.AgentRunList, error) {
	return &agentsv1alpha1.AgentRunList{Items: append([]agentsv1alpha1.AgentRun(nil), backend.runs...)}, nil
}

func (backend *fakeBackend) GetRun(_ context.Context, _, _ string) (*agentsv1alpha1.AgentRun, error) {
	backend.getRunCalls++
	if backend.getRunErr != nil {
		return nil, backend.getRunErr
	}
	return backend.getRun.DeepCopy(), nil
}

func (backend *fakeBackend) OpenLogs(_ context.Context, _ *agentsv1alpha1.AgentRun, options corev1.PodLogOptions) (io.ReadCloser, error) {
	backend.logCalls++
	backend.logOptions = options
	if len(backend.logErrors) > 0 {
		err := backend.logErrors[0]
		backend.logErrors = backend.logErrors[1:]
		return nil, err
	}
	if backend.logErr != nil {
		return nil, backend.logErr
	}
	return io.NopCloser(strings.NewReader(backend.logBody)), nil
}

func (backend *fakeBackend) GetJob(_ context.Context, _, _ string) (*batchv1.Job, error) {
	if backend.jobErr != nil {
		return nil, backend.jobErr
	}
	return backend.job.DeepCopy(), nil
}

func (backend *fakeBackend) GetPod(_ context.Context, _, _ string) (*corev1.Pod, error) {
	if backend.podErr != nil {
		return nil, backend.podErr
	}
	return backend.pod.DeepCopy(), nil
}

func (backend *fakeBackend) ListEvents(_ context.Context, _ string, uids []types.UID) ([]corev1.Event, error) {
	backend.eventUIDs = append([]types.UID(nil), uids...)
	return append([]corev1.Event(nil), backend.events...), backend.eventsErr
}

func (backend *fakeBackend) GetDataVolume(_ context.Context, _, _ string) (*agentsv1alpha1.AgentDataVolume, error) {
	if backend.dataVolumeErr != nil {
		return nil, backend.dataVolumeErr
	}
	if backend.dataVolume == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: agentsv1alpha1.GroupVersion.Group, Resource: "agentdatavolumes"}, "missing")
	}
	return backend.dataVolume.DeepCopy(), nil
}

func (backend *fakeBackend) GetSecret(_ context.Context, _, _ string) (*corev1.Secret, error) {
	if backend.secretErr != nil {
		return nil, backend.secretErr
	}
	if backend.secret == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "missing")
	}
	return backend.secret.DeepCopy(), nil
}

func (backend *fakeBackend) CreateSecret(_ context.Context, secret *corev1.Secret) error {
	backend.createdSecret = secret.DeepCopy()
	return nil
}

func (backend *fakeBackend) DeleteSecret(_ context.Context, _, _ string) error { return nil }

func (backend *fakeBackend) CreateAuthSession(_ context.Context, session *agentsv1alpha1.AgentAuthSession) error {
	if backend.createAuthErr != nil {
		return backend.createAuthErr
	}
	backend.authSession = session.DeepCopy()
	if session.Name == "" {
		session.Name = session.GenerateName + "auth"
	}
	session.Status.Phase = agentsv1alpha1.AgentAuthSessionPhaseSucceeded
	backend.authSession = session.DeepCopy()
	return nil
}

func (backend *fakeBackend) GetAuthSession(_ context.Context, _, _ string) (*agentsv1alpha1.AgentAuthSession, error) {
	if backend.authSession == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: agentsv1alpha1.GroupVersion.Group, Resource: "agentauthsessions"}, "missing")
	}
	return backend.authSession.DeepCopy(), nil
}

func (backend *fakeBackend) ListAuthSessions(_ context.Context, _ string) (*agentsv1alpha1.AgentAuthSessionList, error) {
	return &agentsv1alpha1.AgentAuthSessionList{Items: append([]agentsv1alpha1.AgentAuthSession(nil), backend.authSessions...)}, nil
}

func (backend *fakeBackend) CreateDataVolumeCopy(_ context.Context, copyObj *agentsv1alpha1.AgentDataVolumeCopy) error {
	if copyObj.Name == "" {
		copyObj.Name = copyObj.GenerateName + "copy"
	}
	copyObj.Status.Phase = agentsv1alpha1.AgentDataVolumeCopyPhaseSucceeded
	backend.volumeCopy = copyObj.DeepCopy()
	return nil
}

func (backend *fakeBackend) GetDataVolumeCopy(_ context.Context, _, _ string) (*agentsv1alpha1.AgentDataVolumeCopy, error) {
	if backend.volumeCopy == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: agentsv1alpha1.GroupVersion.Group, Resource: "agentdatavolumecopies"}, "missing")
	}
	return backend.volumeCopy.DeepCopy(), nil
}

func (backend *fakeBackend) ListControls(_ context.Context) (*agentsv1alpha1.AgentRunControlList, error) {
	return &agentsv1alpha1.AgentRunControlList{Items: backend.controls}, nil
}

func (backend *fakeBackend) GetControl(_ context.Context, name string) (*agentsv1alpha1.AgentRunControl, error) {
	if backend.controlGet != nil {
		return backend.controlGet.DeepCopy(), backend.controlGetErr
	}
	for i := range backend.controls {
		if backend.controls[i].Name == name {
			return backend.controls[i].DeepCopy(), nil
		}
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: agentsv1alpha1.GroupVersion.Group, Resource: "agentruncontrols"}, name)
}

func (backend *fakeBackend) CreateControl(_ context.Context, control *agentsv1alpha1.AgentRunControl) error {
	backend.createdControl = control.DeepCopy()
	backend.controls = append(backend.controls, *control.DeepCopy())
	return nil
}

func (backend *fakeBackend) UpdateControl(_ context.Context, control *agentsv1alpha1.AgentRunControl) error {
	backend.updatedControls = append(backend.updatedControls, control.DeepCopy())
	for i := range backend.controls {
		if backend.controls[i].Name == control.Name {
			backend.controls[i] = *control.DeepCopy()
			return nil
		}
	}
	backend.controls = append(backend.controls, *control.DeepCopy())
	return nil
}

func (backend *fakeBackend) ListSchedules(_ context.Context, _ string, _ bool) (*agentsv1alpha1.AgentScheduleList, error) {
	return &agentsv1alpha1.AgentScheduleList{Items: append([]agentsv1alpha1.AgentSchedule(nil), backend.schedules...)}, nil
}

func (backend *fakeBackend) GetSchedule(_ context.Context, namespace, name string) (*agentsv1alpha1.AgentSchedule, error) {
	for i := range backend.schedules {
		if backend.schedules[i].Namespace == namespace && backend.schedules[i].Name == name {
			return backend.schedules[i].DeepCopy(), nil
		}
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: agentsv1alpha1.GroupVersion.Group, Resource: "agentschedules"}, name)
}

func (backend *fakeBackend) UpdateSchedule(_ context.Context, schedule *agentsv1alpha1.AgentSchedule) error {
	backend.updatedSchedules = append(backend.updatedSchedules, schedule.DeepCopy())
	for i := range backend.schedules {
		if backend.schedules[i].Namespace == schedule.Namespace && backend.schedules[i].Name == schedule.Name {
			backend.schedules[i] = *schedule.DeepCopy()
			return nil
		}
	}
	backend.schedules = append(backend.schedules, *schedule.DeepCopy())
	return nil
}

func TestRunCreateClientDryRunDoesNotLoadKubernetes(t *testing.T) {
	var output, errorOutput strings.Builder
	factoryCalled := false
	app := App{
		In:  strings.NewReader("Inspect the repository.\n"),
		Out: &output,
		Err: &errorOutput,
		Factory: func(KubeOptions) (Backend, error) {
			factoryCalled = true
			return nil, errors.New("unexpected factory call")
		},
	}
	err := app.Run(context.Background(), []string{
		"run", "create",
		"--namespace", "agents",
		"--generate-name", "manual-review-",
		"--profile", "repository-review",
		"--prompt-file", "-",
		"--purpose", "manual",
		"--intent", "observe",
		"--source-api-version", "example.io/v1",
		"--source-kind", "Issue",
		"--source-namespace", "tracker",
		"--source-name", "issue-42",
		"--source-uid", "issue-uid",
		"--source-generation", "7",
		"--dry-run", "client",
		"--output", "yaml",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if factoryCalled {
		t.Fatal("client dry-run loaded Kubernetes")
	}
	for _, expected := range []string{
		"apiVersion: control.anvil.hazyforge.io/v1alpha1",
		"kind: AgentRun",
		"generateName: manual-review-",
		"namespace: agents",
		"profileRef:",
		"name: repository-review",
		"prompt: |",
		"Inspect the repository.",
		"sourceGeneration: 7",
		"intent: observe",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("dry-run output missing %q:\n%s", expected, output.String())
		}
	}
}

func TestRunCreateUsesCreateAndReportsGeneratedName(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "ignored"}
	var output strings.Builder
	app := testApp(backend, &output)
	err := app.Run(context.Background(), []string{
		"run", "create", "-n", "agents", "--generate-name", "review-",
		"--profile", "reviewer", "--prompt", "Review one change.", "--source-name", "change-1",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if backend.created == nil {
		t.Fatal("AgentRun was not created")
	}
	if backend.created.Spec.ProfileRef == nil || backend.created.Spec.ProfileRef.Name != "reviewer" {
		t.Fatalf("profile ref = %#v", backend.created.Spec.ProfileRef)
	}
	if got := output.String(); got != "agentrun.control.anvil.hazyforge.io/review-abc12\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestRunCreateExplainsAppendOnlyNameCollision(t *testing.T) {
	backend := &fakeBackend{createErr: fmt.Errorf("create AgentRun: %w", apierrors.NewAlreadyExists(schema.GroupResource{Group: agentsv1alpha1.GroupVersion.Group, Resource: "agentruns"}, "review-001"))}
	app := testApp(backend, io.Discard)
	err := app.Run(context.Background(), []string{
		"run", "create", "-n", "agents", "--name", "review-001",
		"--profile", "reviewer", "--prompt", "Review one change.", "--source-name", "change-1",
	})
	if err == nil || !strings.Contains(err.Error(), "AgentRuns are append-only, choose a new name") {
		t.Fatalf("collision error = %v", err)
	}
}

func TestRunCreateRejectsAmbiguousPromptAndName(t *testing.T) {
	app := testApp(&fakeBackend{}, io.Discard)
	for name, args := range map[string][]string{
		"prompt": {"run", "create", "-n", "agents", "--name", "run-1", "--profile", "p", "--prompt", "x", "--prompt-file", "-", "--source-name", "s"},
		"name":   {"run", "create", "-n", "agents", "--name", "run-1", "--generate-name", "run-", "--profile", "p", "--prompt", "x", "--source-name", "s"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := app.Run(context.Background(), args); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestRunCreateRejectsInvalidOutputBeforeMutation(t *testing.T) {
	backend := &fakeBackend{}
	app := testApp(backend, io.Discard)
	err := app.Run(context.Background(), []string{
		"run", "create", "-n", "agents", "--name", "run-1", "--profile", "p",
		"--prompt", "x", "--source-name", "s", "--output", "unsupported",
	})
	if err == nil {
		t.Fatal("expected output validation error")
	}
	if backend.created != nil {
		t.Fatal("invalid output mutated Kubernetes")
	}
}

func TestRunListSortsNewestFirst(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "agents", runs: []agentsv1alpha1.AgentRun{
		{ObjectMeta: metav1.ObjectMeta{Name: "older", Namespace: "agents", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour))}, Status: agentsv1alpha1.AgentRunStatus{Phase: agentsv1alpha1.AgentRunPhaseSucceeded}},
		{ObjectMeta: metav1.ObjectMeta{Name: "newer", Namespace: "agents", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Minute))}, Status: agentsv1alpha1.AgentRunStatus{Phase: agentsv1alpha1.AgentRunPhaseRunning}},
	}}
	var output strings.Builder
	if err := testApp(backend, &output).Run(context.Background(), []string{"run", "list"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if strings.Index(output.String(), "newer") > strings.Index(output.String(), "older") {
		t.Fatalf("runs not sorted newest first:\n%s", output.String())
	}
}

func TestRunGetJSONIncludesStatus(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "agents", getRun: testRun()}
	var output strings.Builder
	if err := testApp(backend, &output).Run(context.Background(), []string{"run", "get", "run-1", "-o", "json"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	for _, expected := range []string{`"name": "run-1"`, `"phase": "Failed"`, `"error": "tool verification failed"`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("JSON missing %q:\n%s", expected, output.String())
		}
	}
}

func TestRunGetSummaryKeepsVerboseOutputForDebug(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "agents", getRun: testRun(), jobErr: errors.New("Job not retained")}
	var summary, debug strings.Builder
	if err := testApp(backend, &summary).Run(context.Background(), []string{"run", "get", "run-1"}); err != nil {
		t.Fatalf("get returned error: %v", err)
	}
	if strings.Contains(summary.String(), "connection refused") {
		t.Fatalf("summary included verbose status output:\n%s", summary.String())
	}
	if err := testApp(backend, &debug).Run(context.Background(), []string{"run", "debug", "run-1"}); err != nil {
		t.Fatalf("debug returned error: %v", err)
	}
	if !strings.Contains(debug.String(), "connection refused") {
		t.Fatalf("debug omitted bounded status output:\n%s", debug.String())
	}
}

func TestRunLogsPassesBoundedVerifiedLogOptions(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "agents", getRun: testRun(), logBody: "line one\nline two\n"}
	var output strings.Builder
	if err := testApp(backend, &output).Run(context.Background(), []string{"run", "logs", "run-1", "--follow", "--tail", "50", "--timestamps"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if output.String() != backend.logBody {
		t.Fatalf("logs = %q", output.String())
	}
	if !backend.logOptions.Follow || !backend.logOptions.Timestamps || backend.logOptions.TailLines == nil || *backend.logOptions.TailLines != 50 {
		t.Fatalf("log options = %#v", backend.logOptions)
	}
}

func TestRunLogsFollowWaitsForPendingRunnerPod(t *testing.T) {
	backend := &fakeBackend{
		defaultNamespace: "agents",
		getRun:           testRun(),
		logBody:          "ready\n",
		logErrors:        []error{runapi.ErrLogsPending},
	}
	var output strings.Builder
	app := testApp(backend, &output)
	app.PollInterval = time.Nanosecond
	if err := app.Run(context.Background(), []string{"run", "logs", "run-1", "--follow", "--pod-timeout", "1s"}); err != nil {
		t.Fatalf("logs returned error: %v", err)
	}
	if output.String() != "ready\n" || backend.logCalls != 2 || backend.getRunCalls != 2 {
		t.Fatalf("output=%q logCalls=%d getRunCalls=%d", output.String(), backend.logCalls, backend.getRunCalls)
	}
}

func TestRunLogsFollowWaitsForNotFoundRunnerPod(t *testing.T) {
	backend := &fakeBackend{
		defaultNamespace: "agents",
		getRun:           testRun(),
		logBody:          "ready\n",
		logErrors: []error{fmt.Errorf("get runner Pod: %w", apierrors.NewNotFound(
			schema.GroupResource{Resource: "pods"}, "run-1-pod",
		))},
	}
	var output strings.Builder
	app := testApp(backend, &output)
	app.PollInterval = time.Nanosecond
	if err := app.Run(context.Background(), []string{"run", "logs", "run-1", "--follow", "--pod-timeout", "1s"}); err != nil {
		t.Fatalf("logs returned error: %v", err)
	}
	if output.String() != "ready\n" || backend.logCalls != 2 || backend.getRunCalls != 2 {
		t.Fatalf("output=%q logCalls=%d getRunCalls=%d", output.String(), backend.logCalls, backend.getRunCalls)
	}
}

func TestRunLogsFollowDoesNotRetryOwnershipFailure(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "agents", getRun: testRun(), logErr: errors.New("runner Pod ownership mismatch")}
	app := testApp(backend, io.Discard)
	app.PollInterval = time.Nanosecond
	err := app.Run(context.Background(), []string{"run", "logs", "run-1", "--follow", "--pod-timeout", "1s"})
	if err == nil || !strings.Contains(err.Error(), "ownership mismatch") {
		t.Fatalf("logs error = %v", err)
	}
	if backend.logCalls != 1 {
		t.Fatalf("ownership error retried %d times", backend.logCalls)
	}
}

func TestRunLogsFollowTimesOutWhileRunnerPodIsPending(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "agents", getRun: testRun(), logErr: runapi.ErrLogsPending}
	app := testApp(backend, io.Discard)
	app.PollInterval = time.Second
	err := app.Run(context.Background(), []string{"run", "logs", "run-1", "--follow", "--pod-timeout", "1ms"})
	if err == nil || !strings.Contains(err.Error(), "did not become ready within 1ms") {
		t.Fatalf("logs timeout error = %v", err)
	}
	if backend.logCalls != 1 {
		t.Fatalf("pending log stream opened %d times before timeout", backend.logCalls)
	}
}

func TestRunLogsFollowReturnsParentCancellation(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "agents", getRun: testRun(), logErr: runapi.ErrLogsPending}
	app := testApp(backend, io.Discard)
	app.PollInterval = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := app.Run(ctx, []string{"run", "logs", "run-1", "--follow", "--pod-timeout", "1m"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("logs cancellation error = %v", err)
	}
	if strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("parent cancellation reported as pod timeout: %v", err)
	}
	if backend.logCalls != 1 {
		t.Fatalf("pending log stream opened %d times before cancellation", backend.logCalls)
	}
}

func TestRunLogsRejectsPodTimeoutWithoutFollow(t *testing.T) {
	backend := &fakeBackend{}
	err := testApp(backend, io.Discard).Run(context.Background(), []string{"run", "logs", "run-1", "--pod-timeout", "1s"})
	if err == nil || !strings.Contains(err.Error(), "--pod-timeout requires --follow") {
		t.Fatalf("logs validation error = %v", err)
	}
	if backend.getRunCalls != 0 {
		t.Fatal("invalid flags contacted Kubernetes")
	}
}

func TestRunLogsKeepsRawBytes(t *testing.T) {
	backend := &fakeBackend{defaultNamespace: "agents", getRun: testRun(), logBody: "\x1b[31mraw\x1b[0m\n"}
	var output strings.Builder
	if err := testApp(backend, &output).Run(context.Background(), []string{"run", "logs", "run-1"}); err != nil {
		t.Fatalf("logs returned error: %v", err)
	}
	if output.String() != backend.logBody {
		t.Fatalf("raw logs changed from %q to %q", backend.logBody, output.String())
	}
}

func TestRunDebugVerifiesChildrenAndAggregatesEvents(t *testing.T) {
	run := testRun()
	controller := true
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "run-1-job", Namespace: "agents", UID: "job-uid",
			Labels: map[string]string{agentRunLabel: "run-1"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller,
			}},
		},
		Status: batchv1.JobStatus{Failed: 1, Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded", Message: "Job failed after retry"}}},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "run-1-pod", Namespace: "agents", UID: "pod-uid",
			Labels: map[string]string{agentRunLabel: "run-1", agentRunJobLabel: job.Name},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: batchv1.SchemeGroupVersion.String(), Kind: "Job", Name: job.Name, UID: job.UID, Controller: &controller,
			}},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: agentContainer}}},
		Status: corev1.PodStatus{Phase: corev1.PodFailed, ContainerStatuses: []corev1.ContainerStatus{{
			Name: agentContainer, State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}},
		}}},
	}
	backend := &fakeBackend{
		defaultNamespace: "agents", getRun: run, job: job, pod: pod,
		events: []corev1.Event{{ObjectMeta: metav1.ObjectMeta{UID: "event-uid"}, Type: corev1.EventTypeWarning, Reason: "BackoffLimitExceeded", Message: "Job has reached the backoff limit", InvolvedObject: corev1.ObjectReference{Kind: "Job", Name: job.Name}}},
	}
	var output strings.Builder
	if err := testApp(backend, &output).Run(context.Background(), []string{"run", "debug", "run-1"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	for _, expected := range []string{
		"verified: agents/run-1-job uid=job-uid",
		"verified: agents/run-1-pod uid=pod-uid",
		"reason=BackoffLimitExceeded",
		"tool verification failed",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("debug output missing %q:\n%s", expected, output.String())
		}
	}
	if got, want := backend.eventUIDs, []types.UID{"run-uid", "job-uid", "pod-uid"}; !equalUIDs(got, want) {
		t.Fatalf("event UIDs = %v, want %v", got, want)
	}
}

func TestRunDebugRejectsUnownedJobAndDoesNotTrustItsEvents(t *testing.T) {
	run := testRun()
	backend := &fakeBackend{
		defaultNamespace: "agents",
		getRun:           run,
		job: &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
			Name: "run-1-job", Namespace: "agents", UID: "attacker-job", Labels: map[string]string{agentRunLabel: "run-1"},
		}},
	}
	var output strings.Builder
	if err := testApp(backend, &output).Run(context.Background(), []string{"run", "debug", "run-1"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(output.String(), "verification failed: Job is not controller-owned") {
		t.Fatalf("unowned Job was not rejected:\n%s", output.String())
	}
	if got, want := backend.eventUIDs, []types.UID{"run-uid"}; !equalUIDs(got, want) {
		t.Fatalf("event UIDs = %v, want only run UID", got)
	}
}

func TestLikelyCauseSurfacesToolVerificationErrorBeforeJobBackoff(t *testing.T) {
	run := testRun()
	run.Status.Error = "Job has reached the specified backoff limit"
	run.Status.Output = strings.Join([]string{
		"ANVIL_AGENT_RUN_TOOL_VERIFY_START name=anvilctl",
		`Error: unknown command "agent" for "anvilctl"`,
		"Run 'anvilctl --help' for usage.",
	}, "\n")

	cause := likelyCause(run, nil, nil, nil)
	if !strings.Contains(cause, "tool verification failed (name=anvilctl)") || !strings.Contains(cause, `unknown command "agent"`) {
		t.Fatalf("likely cause = %q", cause)
	}
	if strings.Contains(cause, "backoff") {
		t.Fatalf("generic backoff hid tool failure: %q", cause)
	}
}

func TestLikelyCauseSurfacesRedactedToolFailureMarker(t *testing.T) {
	run := testRun()
	run.Status.Error = "Job has reached the specified backoff limit"
	run.Status.Output = "ANVIL_AGENT_RUN_TOOL_CALL_FAILED name=knowledge-search error=<redacted>"

	cause := likelyCause(run, nil, nil, nil)
	if cause != "tool call failed: ANVIL_AGENT_RUN_TOOL_CALL_FAILED name=knowledge-search error=<redacted>" {
		t.Fatalf("likely cause = %q", cause)
	}
}

func TestLikelyCauseIgnoresUnassociatedModelErrorProse(t *testing.T) {
	run := testRun()
	run.Status.Error = "Job has reached the specified backoff limit"
	run.Status.Output = strings.Join([]string{
		"The model reviewed an example from the issue description.",
		`Error: unknown command "agent" for "anvilctl"`,
		"It concluded that the quoted example needs a regression test.",
	}, "\n")

	if cause := likelyCause(run, nil, nil, nil); cause != run.Status.Error {
		t.Fatalf("model prose changed likely cause to %q", cause)
	}
}

func TestTerminalSafeEscapesControlSequences(t *testing.T) {
	input := "before\x1b]52;c;payload\x07after\nnext\t\u009b31m"
	want := `before\u001b]52;c;payload\u0007after\nnext\t\u009b31m`
	if got := terminalSafe(input); got != want {
		t.Fatalf("terminalSafe() = %q, want %q", got, want)
	}
}

func TestRunDebugSanitizesStructuredAgentText(t *testing.T) {
	run := testRun()
	run.Status.Output = "message \x1b[31mred\x1b[0m"
	run.Status.Error = "failed\x07bell"
	backend := &fakeBackend{defaultNamespace: "agents", getRun: run, jobErr: errors.New("not retained\x1b[2J")}
	var output strings.Builder
	if err := testApp(backend, &output).Run(context.Background(), []string{"run", "debug", "run-1"}); err != nil {
		t.Fatalf("debug returned error: %v", err)
	}
	if strings.ContainsAny(output.String(), "\x1b\x07") {
		t.Fatalf("structured debug retained terminal control bytes: %q", output.String())
	}
	for _, escaped := range []string{`\u001b[31m`, `\u0007bell`, `\u001b[2J`} {
		if !strings.Contains(output.String(), escaped) {
			t.Fatalf("debug output missing escaped %q: %s", escaped, output.String())
		}
	}
}

func testApp(backend Backend, output io.Writer) App {
	return App{
		In:  strings.NewReader(""),
		Out: output,
		Err: io.Discard,
		Factory: func(KubeOptions) (Backend, error) {
			return backend, nil
		},
		PollInterval: time.Nanosecond,
	}
}

func testRun() *agentsv1alpha1.AgentRun {
	return &agentsv1alpha1.AgentRun{
		TypeMeta:   metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRun"},
		ObjectMeta: metav1.ObjectMeta{Name: "run-1", Namespace: "agents", UID: "run-uid"},
		Spec: agentsv1alpha1.AgentRunSpec{
			Purpose:   agentsv1alpha1.AgentRunPurposeManual,
			SourceRef: agentsv1alpha1.AgentRunSourceRef{Kind: "ManualRequest", Name: "test"},
			Harness:   agentsv1alpha1.AgentRunHarnessSpec{Intent: agentsv1alpha1.AgentRunIntentObserve},
		},
		Status: agentsv1alpha1.AgentRunStatus{
			Phase:        agentsv1alpha1.AgentRunPhaseFailed,
			Backend:      "grokBuild",
			Intent:       "observe",
			JobRef:       &agentsv1alpha1.NamespacedObjectReference{Name: "run-1-job", Namespace: "agents"},
			JobUID:       "job-uid",
			RunnerPodRef: &agentsv1alpha1.NamespacedObjectReference{Name: "run-1-pod", Namespace: "agents"},
			RunnerPodUID: "pod-uid",
			Error:        "tool verification failed",
			Output:       "ANVIL_AGENT_RUN_TOOL_VERIFY_START name=knowledge-search\nerror: connection refused",
		},
	}
}

func equalUIDs(left, right []types.UID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
