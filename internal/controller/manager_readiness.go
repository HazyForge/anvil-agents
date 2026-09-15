package controller

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// controller-runtime v0.23 starts HTTP probes before caches and registers
// controller informers lazily. Its native EnableWarmup makes Warmup wait for
// each controller's sources to sync without starting leader-only workers.
// Track that lifecycle, rather than treating an empty initial cache as ready.
type managerReadiness struct {
	parent        context.Context
	registered    atomic.Int64
	synced        atomic.Int64
	warmupContext atomic.Pointer[context.Context]
}

func (r *managerReadiness) Check(_ *http.Request) error {
	if r.parent.Err() != nil {
		return errors.New("controller manager is stopping")
	}
	if ctx := r.warmupContext.Load(); ctx != nil && (*ctx).Err() != nil {
		return errors.New("controller manager is stopping")
	}
	if r.registered.Load() == 0 || r.synced.Load() != r.registered.Load() {
		return errors.New("controller watch caches have not finished syncing")
	}
	return nil
}

type warmupController interface {
	manager.Runnable
	manager.LeaderElectionRunnable
	Warmup(context.Context) error
}

// Embed the real manager so builders use its cache, client and defaults. Only
// Add is intercepted, preserving each controller's leader-election behavior.
type readinessManager struct {
	ctrl.Manager
	readiness *managerReadiness
}

func (m *readinessManager) Add(runnable manager.Runnable) error {
	controller, ok := runnable.(warmupController)
	if !ok {
		return m.Manager.Add(runnable)
	}
	tracked := &readinessController{warmupController: controller, readiness: m.readiness}
	if err := m.Manager.Add(tracked); err != nil {
		return err
	}
	m.readiness.registered.Add(1)
	return nil
}

type readinessController struct {
	warmupController
	readiness  *managerReadiness
	syncedOnce sync.Once
}

func (c *readinessController) Warmup(ctx context.Context) error {
	if err := c.warmupController.Warmup(ctx); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	c.syncedOnce.Do(func() {
		c.readiness.warmupContext.CompareAndSwap(nil, &ctx)
		c.readiness.synced.Add(1)
	})
	return nil
}
