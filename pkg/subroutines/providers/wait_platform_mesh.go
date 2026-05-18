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

package providers

import (
	"context"
	"time"

	gcerrors "github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/subroutines"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
)

const (
	WaitPlatformMeshSubroutineName  = "WaitPlatformMeshSubroutine"
	waitPlatformMeshRequeueDuration = 10 * time.Second
)

// WaitPlatformMeshSubroutine polls the PlatformMesh referenced by
// spec.platformMeshRef until its Ready condition is True.
type WaitPlatformMeshSubroutine struct {
	client client.Client
}

func NewWaitPlatformMeshSubroutine(cl client.Client) *WaitPlatformMeshSubroutine {
	return &WaitPlatformMeshSubroutine{client: cl}
}

func (r *WaitPlatformMeshSubroutine) GetName() string {
	return WaitPlatformMeshSubroutineName
}

func (r *WaitPlatformMeshSubroutine) Process(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	inst := obj.(*providersv1alpha1.ManagedProvider)

	pmName := inst.Spec.PlatformMeshReference.Name
	pm := &corev1alpha1.PlatformMesh{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: pmName, Namespace: inst.Namespace}, pm); err != nil {
		if kerrors.IsNotFound(err) {
			log.Info().Str("platformmesh", pmName).Msg("PlatformMesh not found yet, requeuing")
			return subroutines.StopWithRequeue(waitPlatformMeshRequeueDuration, "PlatformMesh not found yet"), nil
		}
		return subroutines.OK(), gcerrors.Wrap(err, "failed to get PlatformMesh %s", pmName)
	}

	if !apimeta.IsStatusConditionTrue(pm.Status.Conditions, "Ready") {
		log.Info().Str("platformmesh", pmName).Msg("PlatformMesh not Ready yet, requeuing")
		return subroutines.StopWithRequeue(waitPlatformMeshRequeueDuration, "waiting for PlatformMesh to become Ready"), nil
	}

	log.Info().Str("platformmesh", pmName).Msg("PlatformMesh is Ready")
	return subroutines.OK(), nil
}

func (r *WaitPlatformMeshSubroutine) Finalize(_ context.Context, _ client.Object) (subroutines.Result, error) {
	return subroutines.OK(), nil
}

func (r *WaitPlatformMeshSubroutine) Finalizers(_ client.Object) []string {
	return []string{}
}
