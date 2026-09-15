package controller

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	controllerconfig "sigs.k8s.io/controller-runtime/pkg/config"
	runtimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type readinessTestManager struct {
	ctrl.Manager
	runnables []manager.Runnable
	addError  error
}

func (m *readinessTestManager) Add(r manager.Runnable) error {
	if m.addError != nil {
		return m.addError
	}
	m.runnables = append(m.runnables, r)
	return nil
}
func (m *readinessTestManager) GetControllerOptions() controllerconfig.Controller {
	warmup := true
	return controllerconfig.Controller{EnableWarmup: &warmup}
}

type readinessTestSource struct {
	started   chan struct{}
	synced    chan struct{}
	syncError error
}

func (s *readinessTestSource) Start(_ context.Context, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
	queue.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "test", Name: "queued-before-election"}})
	close(s.started)
	return nil
}
func (s *readinessTestSource) WaitForSync(ctx context.Context) error {
	select {
	case <-s.synced:
		return s.syncError
	case <-ctx.Done():
		return ctx.Err()
	}
}

func assertManagerReadiness(t *testing.T, readiness *managerReadiness, wantReady bool) {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- readiness.Check(httptest.NewRequest("GET", "/readyz", nil)) }()
	select {
	case err := <-result:
		if (err == nil) != wantReady {
			t.Fatalf("ready=%v, wanted %v (error: %v)", err == nil, wantReady, err)
		}
	case <-time.After(time.Second):
		t.Fatal("readiness probe blocked on a stalled watch")
	}
}
func awaitReadinessSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("controller lifecycle event did not arrive")
	}
}
func awaitReadinessResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("controller lifecycle did not finish")
		return nil
	}
}

// Exercise the real controller-runtime controller and syncing source lifecycle:
// queued work must not run during follower warmup, and every controller's
// sources must finish syncing before the instantaneous probe becomes ready.
func TestManagerReadinessWaitsForRealControllerWatchesWithoutStartingFollowerWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readiness := &managerReadiness{parent: context.Background()}
	base := &readinessTestManager{}
	mgr := &readinessManager{Manager: base, readiness: readiness}
	assertManagerReadiness(t, readiness, false)
	reconciled := make(chan struct{}, 2)
	sources := make([]*readinessTestSource, 2)
	warmups := make([]chan error, 2)
	for i := range sources {
		skipValidation := true
		c, err := runtimecontroller.New(t.Name()+string(rune('a'+i)), mgr, runtimecontroller.Options{
			SkipNameValidation: &skipValidation,
			Reconciler: reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
				reconciled <- struct{}{}
				return reconcile.Result{}, nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		sources[i] = &readinessTestSource{started: make(chan struct{}), synced: make(chan struct{})}
		if err := c.Watch(sources[i]); err != nil {
			t.Fatal(err)
		}
		wrapped, ok := base.runnables[i].(warmupController)
		if !ok {
			t.Fatal("registered controller lost warmup or leader-election interface")
		}
		if !wrapped.NeedLeaderElection() {
			t.Fatal("controller lost leader-election fence")
		}
		warmups[i] = make(chan error, 1)
		go func() { warmups[i] <- wrapped.Warmup(ctx) }()
		awaitReadinessSignal(t, sources[i].started)
	}
	assertManagerReadiness(t, readiness, false)
	close(sources[0].synced)
	if err := awaitReadinessResult(t, warmups[0]); err != nil {
		t.Fatal(err)
	}
	assertManagerReadiness(t, readiness, false)
	close(sources[1].synced)
	if err := awaitReadinessResult(t, warmups[1]); err != nil {
		t.Fatal(err)
	}
	assertManagerReadiness(t, readiness, true)
	select {
	case <-reconciled:
		t.Fatal("follower warmup ran queued reconciliation")
	default:
	}
	// A repeated warmup must not count the same controller twice.
	if err := base.runnables[0].(warmupController).Warmup(ctx); err != nil {
		t.Fatal(err)
	}
	assertManagerReadiness(t, readiness, true)
	// Manager may start workers only after it acquires leadership. The wrapper
	// delegates that lifecycle unchanged and the already queued request executes.
	stopped := make(chan error, 1)
	go func() { stopped <- base.runnables[0].Start(ctx) }()
	awaitReadinessSignal(t, reconciled)
	cancel()
	if err := awaitReadinessResult(t, stopped); err != nil {
		t.Fatal(err)
	}
	assertManagerReadiness(t, readiness, false)
}

func TestManagerReadinessRejectsFailedSourceSync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readiness := &managerReadiness{parent: ctx}
	base := &readinessTestManager{}
	mgr := &readinessManager{Manager: base, readiness: readiness}
	skipValidation := true
	c, err := runtimecontroller.New(t.Name(), mgr, runtimecontroller.Options{
		SkipNameValidation: &skipValidation,
		Reconciler: reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
			t.Error("failed source started worker")
			return reconcile.Result{}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := errors.New("source list forbidden")
	source := &readinessTestSource{started: make(chan struct{}), synced: make(chan struct{}), syncError: expected}
	close(source.synced)
	if err := c.Watch(source); err != nil {
		t.Fatal(err)
	}
	if err := base.runnables[0].(warmupController).Warmup(ctx); !errors.Is(err, expected) {
		t.Fatalf("warmup error=%v", err)
	}
	assertManagerReadiness(t, readiness, false)
}

func TestManagerReadinessPreservesOrdinaryRunnablesAndRegistrationErrors(t *testing.T) {
	readiness := &managerReadiness{parent: context.Background()}
	base := &readinessTestManager{}
	mgr := &readinessManager{Manager: base, readiness: readiness}
	ordinary := manager.RunnableFunc(func(context.Context) error { return nil })
	if err := mgr.Add(ordinary); err != nil {
		t.Fatal(err)
	}
	if _, ok := base.runnables[0].(manager.RunnableFunc); !ok {
		t.Fatal("ordinary runnable was wrapped")
	}
	if readiness.registered.Load() != 0 {
		t.Fatal("ordinary runnable counted as controller")
	}
	base.addError = errors.New("manager stopped")
	skipValidation := true
	_, err := runtimecontroller.New(t.Name(), mgr, runtimecontroller.Options{
		SkipNameValidation: &skipValidation,
		Reconciler:         reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) { return reconcile.Result{}, nil }),
	})
	if !errors.Is(err, base.addError) {
		t.Fatalf("registration error=%v", err)
	}
	if readiness.registered.Load() != 0 {
		t.Fatal("rejected registration counted as controller")
	}
	assertManagerReadiness(t, readiness, false)
}
