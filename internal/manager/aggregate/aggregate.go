/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package aggregate provides AggregatingManager, which wires multiple in-process
// managers together so they share health probes, metrics, webhook server, and
// leader election as a single operational unit.
package aggregate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sync"

	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"sigs.k8s.io/controller-runtime/pkg/manager"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

type NewManagerFunc func(*rest.Config, multicluster.Provider, mcmanager.Options, ...mcmanager.Option) (mcmanager.Manager, error)

// AggregatingManager aggregates multiple multicluster-runtime Manager instances
// into a single one, with shared infra for healthz & readiness, metrics and
// leader election.
//
// In general, there is a concept of a "primary" Manager that owns the infrastructure,
// and >=0 of secondary Managers that follow and aggregate with the primary.
// It is thread-safe, and secondaries may be added at any point in time, i.e. before
// or after Start() as long as the main context is running.
//
// NOTE: Call Start() through AggregatingManager, not your Managers!
type AggregatingManager struct {
	primary     mcmanager.Manager
	primaryOpts mcmanager.Options
	newManager  NewManagerFunc

	mu           sync.Mutex
	secondaries  []aggregatedSecondary
	leaderLostCh chan struct{}
	started      bool
	ctx          context.Context    // Passed to all Manager.Start() calls.
	cancel       context.CancelFunc // Cancels ctx from above ^^.
	wg           sync.WaitGroup     // Tracks Manager.Start() goroutines.
	errCh        chan error
}

type aggregatedSecondary struct {
	mgr  mcmanager.Manager
	name string
}

// closeOnLeaderLost closes its channel when the manager's base context cancels.
// We use this with the primary mgr. The idea is that when primary loses leadership,
// closeOnLeaderLost closes ch, which then triggers secondaryLock to also report that
// it has lost.
type closeOnLeaderLost struct{ ch chan struct{} }

func (s closeOnLeaderLost) NeedLeaderElection() bool { return true }
func (s closeOnLeaderLost) Start(ctx context.Context) error {
	<-ctx.Done()
	close(s.ch)
	return nil
}
func (s closeOnLeaderLost) Engage(_ context.Context, _ multicluster.ClusterName, _ cluster.Cluster) error {
	return nil
}

var _ mcmanager.Runnable = &closeOnLeaderLost{}
var _ manager.LeaderElectionRunnable = &closeOnLeaderLost{}

// New creates an instance of AggregatingManager. You pass in your primary Manager,
// along with the options it was created with.
func New(primary mcmanager.Manager, opts mcmanager.Options) (*AggregatingManager, error) {
	a := &AggregatingManager{
		primary:      primary,
		primaryOpts:  opts,
		newManager:   mcmanager.New,
		leaderLostCh: make(chan struct{}),
	}

	if err := primary.Add(closeOnLeaderLost{ch: a.leaderLostCh}); err != nil {
		return nil, fmt.Errorf("failed to add closeOnLeaderLost runnable: %v", err)
	}
	// TODO: add more sophisticated readyz & healthz callback passing from secondaries to primary.
	if err := primary.AddReadyzCheck("secondaries", healthz.Ping); err != nil {
		return nil, fmt.Errorf("failed to register readyz check on secondaries: %v", err)
	}
	if err := primary.AddHealthzCheck("secondaries", healthz.Ping); err != nil {
		return nil, fmt.Errorf("failed to register healthz check on secondaries: %v", err)
	}

	return a, nil
}

// Primary returns the primary manager.
func (a *AggregatingManager) Primary() mcmanager.Manager { return a.primary }

// Start starts all registered managers and blocks until all have stopped.
// May be called once.
func (a *AggregatingManager) Start(parent context.Context) error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return fmt.Errorf("aggregating manager already started")
	}
	ctx, cancel := context.WithCancel(parent)
	a.ctx = ctx
	a.cancel = cancel
	a.errCh = make(chan error, 1)
	a.started = true
	secondaries := a.secondaries
	a.mu.Unlock()

	// Keep wg counter > 0 until ctx is cancelled, so that wg.Wait() is prevented
	// from returning while a concurrent AddSecondary may still call wg.Add.
	a.wg.Go(func() {
		<-ctx.Done()
	})

	// Start the primary. Stops group ctx when finished, stopping all secondaries.
	a.wg.Go(func() {
		if err := a.primary.Start(ctx); err != nil {
			select {
			case a.errCh <- err:
			default:
			}
		}
		cancel()
	})

	// Start all pre-Start() secondaries.
	for _, s := range secondaries {
		a.wg.Go(func() { startSecondary(ctx, cancel, a.errCh, s.mgr) })
	}

	a.wg.Wait()

	select {
	case err := <-a.errCh:
		return err
	default:
		return nil
	}
}

func startSecondary(ctx context.Context, cancel context.CancelFunc, errCh chan<- error, mgr mcmanager.Manager) {
	if err := mgr.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		cancel()
		select {
		case errCh <- err:
		default:
		}
	}
}

// AddSecondary creates a new multicluster-runtime manager with the supplied args,
// and aggregates its infrastructure with the primary. If the AggregatingManager
// has already Start()'ed, this secondary is started immediately. Does not block.
func (a *AggregatingManager) AddSecondary(name string, cfg *rest.Config, provider multicluster.Provider, opts mcmanager.Options) (mcmanager.Manager, error) {
	// Prepare opts. We need to override a few of the with primary's, e.g. disabling
	// healthz & readiness check servers and metrics server because those are owned by the primary.
	opts = copyPrimaryOptsToSecondary(opts, a.primaryOpts)
	// We have our own LE lock.
	leaseDurationSeconds := ptr.Deref(a.primaryOpts.LeaseDuration, time.Duration(time.Second*15) /* <- Default as per the docs. */)
	opts.LeaderElectionResourceLockInterface = LockSecondaryWhenPrimaryElected(
		name, int(leaseDurationSeconds.Seconds()), a.primary.Elected(), a.leaderLostCh,
	)

	mgr, err := a.newManager(cfg, provider, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create secondary manager %q: %v", name, err)
	}

	if err := a.registerSecondary(mgr, name); err != nil {
		return nil, err
	}
	return mgr, nil
}

// copyPrimaryOptsToSecondary secondary opts with fields aggregated into primary.
func copyPrimaryOptsToSecondary(secondary, primary mcmanager.Options) mcmanager.Options {
	secondary.HealthProbeBindAddress = "0"
	secondary.Metrics = metricsserver.Options{BindAddress: "0"}
	secondary.PprofBindAddress = "0"
	secondary.GracefulShutdownTimeout = primary.GracefulShutdownTimeout
	// TODO: support Options.WebhookServer once we decide how we want to deal with this in a multi-manager setup.

	secondary.LeaderElection = primary.LeaderElection
	secondary.LeaseDuration = primary.LeaseDuration
	secondary.RenewDeadline = primary.RenewDeadline
	secondary.RetryPeriod = primary.RetryPeriod
	secondary.LeaderElectionReleaseOnCancel = primary.LeaderElectionReleaseOnCancel
	// Resetting the LE opts below just to make it explicit we are not using these anywhere.
	secondary.LeaderElectionResourceLock = ""
	secondary.LeaderElectionNamespace = ""
	secondary.LeaderElectionID = ""
	secondary.LeaderElectionConfig = nil
	secondary.LeaderElectionLabels = nil

	return secondary
}

// registerSecondary appends mgr to the secondaries list and, if Start has already
// been called, launches mgr immediately into the running context.
func (a *AggregatingManager) registerSecondary(mgr mcmanager.Manager, name string) error {
	s := aggregatedSecondary{mgr: mgr, name: name}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.secondaries {
		if a.secondaries[i].name == name {
			return fmt.Errorf("secondary manager %q already registered", name)
		}
	}

	if a.started {
		select {
		case <-a.ctx.Done():
			// We are actually closing down...
			return fmt.Errorf("secondary manager %q won't start because primary already stopped", name)
		default:
		}
	}

	a.secondaries = append(a.secondaries, s)

	if !a.started {
		// Haven't started yet. AggregatingManager.Start() then starts all accummulated secondaries.
		return nil
	}

	// Otherwise, we're a-go!
	a.wg.Go(func() { startSecondary(a.ctx, a.cancel, a.errCh, s.mgr) })

	return nil
}

// SetNewManager replaces d's manager constructor. For testing only.
func SetNewManager_test(d *AggregatingManager, f NewManagerFunc) {
	d.newManager = f
}
