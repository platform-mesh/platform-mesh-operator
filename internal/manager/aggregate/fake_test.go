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
	"net/http"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	crmanager "sigs.k8s.io/controller-runtime/pkg/manager"

	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

// ---------------------------------------------------------------------------
// fakeLocalCrManager — minimal crmanager.Manager returned by GetLocalManager.
// Holds the per-manager scheme and cache so tests can control sync state.
// ---------------------------------------------------------------------------

type fakeLocalCrManager struct {
	crmanager.Manager // nil embedded — panics on unimplemented methods
	scheme            *runtime.Scheme
	fcache            *fakeCache
}

func (m *fakeLocalCrManager) GetScheme() *runtime.Scheme { return m.scheme }
func (m *fakeLocalCrManager) GetCache() cache.Cache      { return m.fcache }

// ---------------------------------------------------------------------------
// fakeMcManager — implements mcmanager.Manager. Used for both the primary and
// secondary managers in tests. Methods not overridden below panic via the nil
// embedded interface.
// ---------------------------------------------------------------------------

type fakeMcManager struct {
	mcmanager.Manager // nil embedded — panics on unimplemented methods

	local   *fakeLocalCrManager
	elected chan struct{}

	mu                sync.Mutex
	runnables         []mcmanager.Runnable
	readyzChecks      map[string]healthz.Checker
	addErr            error
	addReadyzCheckErr error

	startOnce sync.Once
	startedCh chan struct{} // closed the first time Start is called
	startErr  error         // if non-nil, Start returns it immediately
}

func newFakeMcManager() *fakeMcManager {
	return &fakeMcManager{
		local: &fakeLocalCrManager{
			scheme: runtime.NewScheme(),
			fcache: &fakeCache{synced: make(chan struct{})},
		},
		elected:      make(chan struct{}),
		readyzChecks: make(map[string]healthz.Checker),
		startedCh:    make(chan struct{}),
	}
}

func (m *fakeMcManager) GetLocalManager() crmanager.Manager { return m.local }
func (m *fakeMcManager) Elected() <-chan struct{}           { return m.elected }

func (m *fakeMcManager) Add(r mcmanager.Runnable) error {
	if m.addErr != nil {
		return m.addErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runnables = append(m.runnables, r)
	return nil
}

func (m *fakeMcManager) AddReadyzCheck(name string, check healthz.Checker) error {
	if m.addReadyzCheckErr != nil {
		return m.addReadyzCheckErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readyzChecks[name] = check
	return nil
}

func (m *fakeMcManager) AddHealthzCheck(_ string, _ healthz.Checker) error { return nil }

// Start closes startedCh on first call, returns startErr if set, otherwise
// runs all registered mc runnables (including the lostSentinel) and blocks
// until ctx is done.
func (m *fakeMcManager) Start(ctx context.Context) error {
	m.startOnce.Do(func() { close(m.startedCh) })

	if m.startErr != nil {
		return m.startErr
	}

	m.mu.Lock()
	runnables := make([]mcmanager.Runnable, len(m.runnables))
	copy(runnables, m.runnables)
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, r := range runnables {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Start(ctx)
		}()
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

// ---------------------------------------------------------------------------
// fakeCache — implements cache.Cache; only WaitForCacheSync is overridden.
// ---------------------------------------------------------------------------

type fakeCache struct {
	cache.Cache // nil embedded
	synced      chan struct{}
}

func (c *fakeCache) WaitForCacheSync(ctx context.Context) bool {
	select {
	case <-c.synced:
		return true
	case <-ctx.Done():
		return false
	}
}

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

// waitFor asserts that ch is closed within 200 ms.
func waitFor(t *testing.T, ch <-chan struct{}, desc string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("timed out waiting for: %s", desc)
	}
}

// readyzRequest returns an HTTP request with a 100 ms deadline for readyz probe tests.
func readyzRequest(t *testing.T) *http.Request {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil)
	return req
}
