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

package aggregate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	"github.com/platform-mesh/platform-mesh-operator/internal/manager/aggregate"
)

// ---- helpers ----------------------------------------------------------------

// newDM creates a AggregatingManager backed by a fresh fakeMcManager. The scheme
// is set on the local manager and Options so applyDelegationOverrides copies it
// via primaryOpts.Scheme.
func newDM(t *testing.T, scheme *runtime.Scheme) (*aggregate.AggregatingManager, *fakeMcManager) {
	t.Helper()
	primary := newFakeMcManager()
	primary.local.scheme = scheme
	dm, err := aggregate.New(primary, mcmanager.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("aggregate.New: %v", err)
	}
	return dm, primary
}

// addFake injects a factory returning sec into dm, then calls AddSecondary.
func addFake(t *testing.T, dm *aggregate.AggregatingManager, name string) *fakeMcManager {
	t.Helper()
	sec := newFakeMcManager()
	aggregate.SetNewManager_test(dm, func(_ *rest.Config, _ multicluster.Provider, _ mcmanager.Options, _ ...mcmanager.Option) (mcmanager.Manager, error) {
		return sec, nil
	})
	_, err := dm.AddSecondary(name, nil, nil, mcmanager.Options{})
	if err != nil {
		t.Fatalf("AddSecondary(%q): %v", name, err)
	}
	return sec
}

// startDM launches dm.Start in a goroutine and returns a channel that receives
// its return value.
func startDM(t *testing.T, dm *aggregate.AggregatingManager, ctx context.Context) <-chan error {
	t.Helper()
	ch := make(chan error, 1)
	go func() { ch <- dm.Start(ctx) }()
	return ch
}

// ---- New --------------------------------------------------------------------

func TestNew_RegistersSentinelAndReadyzCheck(t *testing.T) {
	primary := newFakeMcManager()
	dm, err := aggregate.New(primary, mcmanager.Options{Scheme: runtime.NewScheme()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if dm == nil {
		t.Fatal("New returned nil")
	}

	// lostSentinel is added at the mc level via primary.Add.
	primary.mu.Lock()
	runnables := primary.runnables
	_, hasReadyz := primary.readyzChecks["secondaries"]
	primary.mu.Unlock()

	if len(runnables) != 1 {
		t.Fatalf("expected 1 runnable (lost sentinel), got %d", len(runnables))
	}

	// Avoid importing crmanager.LeaderElectionRunnable; use a local interface.
	type leaderElectionRunnable interface{ NeedLeaderElection() bool }
	le, ok := runnables[0].(leaderElectionRunnable)
	if !ok {
		t.Fatal("sentinel does not implement NeedLeaderElection")
	}
	if !le.NeedLeaderElection() {
		t.Fatal("sentinel.NeedLeaderElection() = false, want true")
	}
	if !hasReadyz {
		t.Fatal("\"secondaries\" readyz check not registered on primary")
	}
}

func TestNew_AddFails(t *testing.T) {
	primary := newFakeMcManager()
	primary.addErr = errors.New("add failed")
	_, err := aggregate.New(primary, mcmanager.Options{})
	if err == nil {
		t.Fatal("expected error when primary.Add fails")
	}
}

func TestNew_AddReadyzCheckFails(t *testing.T) {
	primary := newFakeMcManager()
	primary.addReadyzCheckErr = errors.New("readyz failed")
	_, err := aggregate.New(primary, mcmanager.Options{})
	if err == nil {
		t.Fatal("expected error when primary.AddReadyzCheck fails")
	}
}

// ---- Primary ----------------------------------------------------------------

func TestPrimary(t *testing.T) {
	primary := newFakeMcManager()
	dm, _ := aggregate.New(primary, mcmanager.Options{Scheme: runtime.NewScheme()})
	if got := dm.Primary(); got != primary {
		t.Fatal("Primary() returned unexpected manager")
	}
}

// ---- AddSecondary delegation overrides --------------------------------------

func TestAddSecondary_DelegationOverrides(t *testing.T) {
	scheme := runtime.NewScheme()
	leaseDur := 20 * time.Second
	renewDead := 10 * time.Second
	retryPer := 2 * time.Second

	primary := newFakeMcManager()
	primary.local.scheme = scheme
	primaryOpts := mcmanager.Options{
		Scheme:                        scheme,
		LeaderElection:                true,
		LeaseDuration:                 ptr.To(leaseDur),
		RenewDeadline:                 ptr.To(renewDead),
		RetryPeriod:                   ptr.To(retryPer),
		LeaderElectionReleaseOnCancel: true,
	}
	dm, err := aggregate.New(primary, primaryOpts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var capturedOpts mcmanager.Options
	aggregate.SetNewManager_test(dm, func(_ *rest.Config, _ multicluster.Provider, opts mcmanager.Options, _ ...mcmanager.Option) (mcmanager.Manager, error) {
		capturedOpts = opts
		return newFakeMcManager(), nil
	})
	_, err = dm.AddSecondary("sec-1", nil, nil, mcmanager.Options{})
	if err != nil {
		t.Fatalf("AddSecondary: %v", err)
	}

	if capturedOpts.HealthProbeBindAddress != "0" {
		t.Errorf("HealthProbeBindAddress = %q, want \"0\"", capturedOpts.HealthProbeBindAddress)
	}
	if capturedOpts.Metrics.BindAddress != "0" {
		t.Errorf("Metrics.BindAddress = %q, want \"0\"", capturedOpts.Metrics.BindAddress)
	}
	if capturedOpts.PprofBindAddress != "0" {
		t.Errorf("PprofBindAddress = %q, want \"0\"", capturedOpts.PprofBindAddress)
	}
	if !capturedOpts.LeaderElection {
		t.Error("LeaderElection not set to true")
	}
	if capturedOpts.LeaderElectionResourceLockInterface == nil {
		t.Error("LeaderElectionResourceLockInterface not set (ElectedGateLock missing)")
	}
	if id := capturedOpts.LeaderElectionResourceLockInterface.Identity(); id != "sec-1" {
		t.Errorf("lock identity = %q, want \"sec-1\"", id)
	}
	if capturedOpts.LeaseDuration == nil || *capturedOpts.LeaseDuration != leaseDur {
		t.Errorf("LeaseDuration = %v, want %v", capturedOpts.LeaseDuration, leaseDur)
	}
	if capturedOpts.RenewDeadline == nil || *capturedOpts.RenewDeadline != renewDead {
		t.Errorf("RenewDeadline = %v, want %v", capturedOpts.RenewDeadline, renewDead)
	}
	if capturedOpts.RetryPeriod == nil || *capturedOpts.RetryPeriod != retryPer {
		t.Errorf("RetryPeriod = %v, want %v", capturedOpts.RetryPeriod, retryPer)
	}
}

// When the primary has LeaderElection disabled, the secondary must also have it
// disabled and must not receive an ElectedGateLock.
func TestAddSecondary_NoLeaderElection(t *testing.T) {
	primary := newFakeMcManager()
	dm, err := aggregate.New(primary, mcmanager.Options{LeaderElection: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var capturedOpts mcmanager.Options
	aggregate.SetNewManager_test(dm, func(_ *rest.Config, _ multicluster.Provider, opts mcmanager.Options, _ ...mcmanager.Option) (mcmanager.Manager, error) {
		capturedOpts = opts
		return newFakeMcManager(), nil
	})
	_, err = dm.AddSecondary("sec", nil, nil, mcmanager.Options{})
	if err != nil {
		t.Fatalf("AddSecondary: %v", err)
	}
	if capturedOpts.LeaderElection {
		t.Error("LeaderElection should be false when primary has LE disabled")
	}
}

// ---- Start ------------------------------------------------------------------

func TestStart_NoSecondaries(t *testing.T) {
	dm, primary := newDM(t, runtime.NewScheme())
	ctx, cancel := context.WithCancel(context.Background())

	doneCh := startDM(t, dm, ctx)
	waitFor(t, primary.startedCh, "primary Start")
	cancel()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("Start returned unexpected error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Start did not return after context cancellation")
	}
}

func TestStart_WithPreregisteredSecondary(t *testing.T) {
	dm, primary := newDM(t, runtime.NewScheme())
	sec := addFake(t, dm, "sec-1")

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := startDM(t, dm, ctx)

	waitFor(t, primary.startedCh, "primary Start")
	waitFor(t, sec.startedCh, "secondary Start")

	cancel()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Start did not return after context cancellation")
	}
}

func TestStart_SecondaryError(t *testing.T) {
	dm, _ := newDM(t, runtime.NewScheme())

	secErr := errors.New("secondary exploded")
	sec := newFakeMcManager()
	sec.startErr = secErr

	aggregate.SetNewManager_test(dm, func(_ *rest.Config, _ multicluster.Provider, _ mcmanager.Options, _ ...mcmanager.Option) (mcmanager.Manager, error) {
		return sec, nil
	})
	_, err := dm.AddSecondary("bad", nil, nil, mcmanager.Options{})
	if err != nil {
		t.Fatalf("AddSecondary: %v", err)
	}

	err = dm.Start(context.Background())
	if !errors.Is(err, secErr) {
		t.Fatalf("expected secondary error %v, got %v", secErr, err)
	}
}

func TestStart_AddAfterStart(t *testing.T) {
	dm, primary := newDM(t, runtime.NewScheme())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	doneCh := startDM(t, dm, ctx)

	// Wait until the primary's Start has been called — at that point d.started=true
	// and d.gctx is set, so AddSecondary will take the post-Start path.
	waitFor(t, primary.startedCh, "primary Start")

	sec := newFakeMcManager()
	aggregate.SetNewManager_test(dm, func(_ *rest.Config, _ multicluster.Provider, _ mcmanager.Options, _ ...mcmanager.Option) (mcmanager.Manager, error) {
		return sec, nil
	})
	_, err := dm.AddSecondary("dynamic", nil, nil, mcmanager.Options{})
	if err != nil {
		t.Fatalf("AddSecondary after Start: %v", err)
	}

	waitFor(t, sec.startedCh, "adding secondary after Start")

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Start did not return after context cancellation")
	}
}

// ---- Add-after-stop behaviour -----------------------------------------------

func TestStart_AddAfterStop_ReturnsError(t *testing.T) {
	dm, primary := newDM(t, runtime.NewScheme())
	ctx, cancel := context.WithCancel(context.Background())

	doneCh := startDM(t, dm, ctx)
	waitFor(t, primary.startedCh, "primary Start")

	cancel()
	select {
	case <-doneCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Start did not stop")
	}

	aggregate.SetNewManager_test(dm, func(_ *rest.Config, _ multicluster.Provider, _ mcmanager.Options, _ ...mcmanager.Option) (mcmanager.Manager, error) {
		return newFakeMcManager(), nil
	})
	_, err := dm.AddSecondary("late", nil, nil, mcmanager.Options{})
	if err == nil {
		t.Fatal("expected error when adding secondary after group has stopped")
	}
}

func TestStart_AddAfterStop_Slow(t *testing.T) {
	dm, primary := newDM(t, runtime.NewScheme())
	ctx, cancel := context.WithCancel(context.Background())

	doneCh := startDM(t, dm, ctx)
	waitFor(t, primary.startedCh, "primary Start")

	cancel()
	select {
	case <-doneCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Start did not stop")
	}

	lateSecondary := newFakeMcManager()
	aggregate.SetNewManager_test(dm, func(_ *rest.Config, _ multicluster.Provider, _ mcmanager.Options, _ ...mcmanager.Option) (mcmanager.Manager, error) {
		return lateSecondary, nil
	})
	_, _ = dm.AddSecondary("ghost", nil, nil, mcmanager.Options{})

	// Fetch the registered readyz check and call it directly.
	primary.mu.Lock()
	check := primary.readyzChecks["secondaries"]
	primary.mu.Unlock()

	if err := check(readyzRequest(t)); err != nil {
		t.Fatalf("readyz check returned error — secondary is present: %v", err)
	}
}

// ---- Readyz check -----------------------------------------------------------

func TestReadyzCheck_EmptyList(t *testing.T) {
	_, primary := newDM(t, runtime.NewScheme())

	primary.mu.Lock()
	check := primary.readyzChecks["secondaries"]
	primary.mu.Unlock()

	if err := check(readyzRequest(t)); err != nil {
		t.Fatalf("empty readyz check returned error: %v", err)
	}
}

func TestReadyzCheck_Ready(t *testing.T) {
	dm, primary := newDM(t, runtime.NewScheme())
	sec := addFake(t, dm, "sec-1")

	close(sec.local.fcache.synced)
	close(sec.elected)

	primary.mu.Lock()
	check := primary.readyzChecks["secondaries"]
	primary.mu.Unlock()

	if err := check(readyzRequest(t)); err != nil {
		t.Fatalf("readyz check failed for synced+elected secondary: %v", err)
	}
}
