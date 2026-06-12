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

package aggregate

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// LockSecondaryWhenPrimaryElected creates a synthetic resource lock that reflects the same
// elected/lost state as the primary.
func LockSecondaryWhenPrimaryElected(identity string, leaseDurationSeconds int, primaryElected, primaryLost <-chan struct{}) resourcelock.Interface {
	return &secondaryLock{
		identity:             identity,
		primaryElected:       primaryElected,
		primaryLost:          primaryLost,
		leaseDurationSeconds: leaseDurationSeconds,
	}
}

type secondaryLock struct {
	identity       string
	primaryElected <-chan struct{}
	primaryLost    <-chan struct{}

	leaseDurationSeconds int
}

var _ resourcelock.Interface = &secondaryLock{}

func (l *secondaryLock) isElected() bool {
	// Check for primaryLost first.
	select {
	case <-l.primaryLost:
		return false
	default:
	}

	// Now check if primary is elected. This is inherently racy, but eventually consistent on next run.
	select {
	case <-l.primaryElected:
		return true
	default:
	}

	return false
}

func (l *secondaryLock) Get(_ context.Context) (*resourcelock.LeaderElectionRecord, []byte, error) {
	identity := l.identity

	if !l.isElected() {
		// The primary is not the leader, so we (secondary) can't be either.
		// We don't know who it is, but we don't really care. Just pass in
		// identity different from ours. Client's LeaderElector will resolve
		// this as "this replica is not the leader".
		identity += "-winner"
	}
	now := metav1.Now()

	lre := &resourcelock.LeaderElectionRecord{
		HolderIdentity:       identity,
		LeaseDurationSeconds: l.leaseDurationSeconds,
		RenewTime:            now,
		AcquireTime:          now,
	}
	lreJsonBytes, err := json.Marshal(lre)
	if err != nil {
		return nil, nil, err
	}

	return lre, lreJsonBytes, nil
}

func (l *secondaryLock) Create(_ context.Context, _ resourcelock.LeaderElectionRecord) error {
	if l.isElected() {
		return nil
	}
	return fmt.Errorf("primary has not yet won election, will check on next iteration")
}

func (l *secondaryLock) Update(_ context.Context, ler resourcelock.LeaderElectionRecord) error {
	if l.isElected() {
		return nil
	}
	return fmt.Errorf("primary is no longer elected, will release lock on next iteration")
}

func (l *secondaryLock) RecordEvent(string) {}

func (l *secondaryLock) Identity() string { return l.identity }

func (l *secondaryLock) Describe() string {
	return fmt.Sprintf("/secondary/%s", l.identity)
}
