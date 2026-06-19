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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/ratelimiter"
	gcerrors "github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/subroutines"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
)

const (
	DeploySubroutineName      = "DeploySubroutine"
	deploySubroutineFinalizer = "providers.platform-mesh.io/runtime-deployments"
	deployRequeueDuration     = 10 * time.Second
)

var (
	deployOCIRepoGVK = schema.GroupVersionKind{
		Group:   "source.toolkit.fluxcd.io",
		Version: "v1",
		Kind:    "OCIRepository",
	}
	deployHelmReleaseGVK = schema.GroupVersionKind{
		Group:   "helm.toolkit.fluxcd.io",
		Version: "v2",
		Kind:    "HelmRelease",
	}
)

// ocmResourceName returns the last path segment of an OCM component name.
// e.g. "github.com/platform-mesh/wildwest-controller" returns "wildwest-controller"
func ocmResourceName(componentName string) string {
	lastSlashIndex := strings.LastIndexByte(componentName, '/') + 1
	return componentName[lastSlashIndex:]
}

// DeploySubroutine creates Flux OCIRepository and HelmRelease objects on the
// runtime cluster for the OCM components referenced in spec.runtimeDeployments.
// Subsequent reconciliations detect drift via the HelmRelease Ready condition.
type DeploySubroutine struct {
	client  client.Client
	limiter workqueue.TypedRateLimiter[*providersv1alpha1.ManagedProvider]
}

func NewDeploySubroutine(cl client.Client) (*DeploySubroutine, error) {
	rl, err := ratelimiter.NewStaticThenExponentialRateLimiter[*providersv1alpha1.ManagedProvider](
		ratelimiter.NewConfig())
	if err != nil {
		return nil, fmt.Errorf("creating RateLimiter: %v", err)
	}
	return &DeploySubroutine{client: cl, limiter: rl}, nil
}

func (r *DeploySubroutine) GetName() string {
	return DeploySubroutineName
}

func (r *DeploySubroutine) Process(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	inst := obj.(*providersv1alpha1.ManagedProvider)

	result, err := r.doRuntimeDeployments(ctx, inst)
	if err != nil {
		return subroutines.OK(), err
	}
	if !result.IsContinue() {
		inst.Status.Phase = providersv1alpha1.ManagedProviderPhaseDeploying
		return result, nil
	}

	inst.Status.Phase = providersv1alpha1.ManagedProviderPhaseReady
	return subroutines.OK(), nil
}

func (r *DeploySubroutine) doRuntimeDeployments(ctx context.Context, managedProvider *providersv1alpha1.ManagedProvider) (subroutines.Result, error) {
	runtimeKubeconfigSecretName := managedProvider.Spec.RuntimeKubeconfigSecretName

	for _, component := range managedProvider.Spec.RuntimeDeployments {
		if component.OCM != nil {
			ocm := component.OCM
			name := ocmResourceName(ocm.ComponentName)
			result, err := r.deployOCMComponent(ctx, managedProvider.Namespace, name, ocm, runtimeKubeconfigSecretName)
			if err != nil {
				return subroutines.OK(), err
			}
			if !result.IsContinue() {
				return result, nil
			}
		}
	}

	return subroutines.OK(), nil
}

func (r *DeploySubroutine) deployOCMComponent(ctx context.Context, namespace, name string, ocm *providersv1alpha1.OCMComponentSpec, runtimeKubeconfigSecretName string) (subroutines.Result, error) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", DeploySubroutineName).ChildLogger("component", name)

	ociURL := fmt.Sprintf("oci://%s/%s", ocm.Registry, ocm.ComponentName)

	ociRepo := &unstructured.Unstructured{}
	ociRepo.SetGroupVersionKind(deployOCIRepoGVK)
	ociRepo.SetName(name)
	ociRepo.SetNamespace(namespace)
	ociResult, err := controllerutil.CreateOrUpdate(ctx, r.client, ociRepo, func() error {
		if err := unstructured.SetNestedField(ociRepo.Object, ociURL, "spec", "url"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(ociRepo.Object, ocm.Version, "spec", "ref", "tag"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(ociRepo.Object, "generic", "spec", "provider"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(ociRepo.Object, "1m0s", "spec", "interval"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(ociRepo.Object, ocm.Insecure, "spec", "insecure"); err != nil {
			return err
		}
		return unstructured.SetNestedMap(ociRepo.Object, map[string]interface{}{
			"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
			"operation": "copy",
		}, "spec", "layerSelector")
	})
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to reconcile OCIRepository %s/%s", namespace, name)
	}

	var values map[string]interface{}
	if len(ocm.Values.Raw) > 0 {
		if err := json.Unmarshal(ocm.Values.Raw, &values); err != nil {
			return subroutines.OK(), gcerrors.Wrap(err, "failed to unmarshal values for %s", name)
		}
	}

	helmRelease := &unstructured.Unstructured{}
	helmRelease.SetGroupVersionKind(deployHelmReleaseGVK)
	helmRelease.SetName(name)
	helmRelease.SetNamespace(namespace)
	hrResult, err := controllerutil.CreateOrUpdate(ctx, r.client, helmRelease, func() error {
		if err := unstructured.SetNestedField(helmRelease.Object, "5m", "spec", "interval"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(helmRelease.Object, "OCIRepository", "spec", "chartRef", "kind"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(helmRelease.Object, name, "spec", "chartRef", "name"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(helmRelease.Object, namespace, "spec", "chartRef", "namespace"); err != nil {
			return err
		}
		if runtimeKubeconfigSecretName != "" {
			// The user requests to deploy this in a different runtime cluster.
			if err := unstructured.SetNestedMap(helmRelease.Object, map[string]interface{}{
				"name": runtimeKubeconfigSecretName,
				"key":  "kubeconfig",
			}, "spec", "kubeConfig", "secretRef"); err != nil {
				return err
			}
		}
		if values != nil {
			return unstructured.SetNestedMap(helmRelease.Object, values, "spec", "values")
		}
		return nil
	})
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to reconcile HelmRelease %s/%s", namespace, name)
	}

	// If either resource was just created or updated, skip condition checks — any existing
	// Ready=True on the HelmRelease is stale with respect to the new spec.
	if ociResult != controllerutil.OperationResultNone || hrResult != controllerutil.OperationResultNone {
		log.Info().Str("ociResult", string(ociResult)).Str("hrResult", string(hrResult)).Msg("Resource modified, requeuing before checking conditions")
		return subroutines.StopWithRequeue(deployRequeueDuration, fmt.Sprintf("waiting for %s to be reconciled", name)), nil
	}

	// Verify Flux has fully processed the current OCIRepository spec generation before
	// trusting HelmRelease conditions. A mismatch means Flux hasn't fetched the new
	// artifact yet, so the HelmRelease conditions still reflect the previous version.
	ociGeneration := ociRepo.GetGeneration()
	ociObservedGeneration, _, _ := unstructured.NestedInt64(ociRepo.Object, "status", "observedGeneration")
	if ociGeneration != ociObservedGeneration {
		log.Info().Int64("generation", ociGeneration).Int64("observedGeneration", ociObservedGeneration).Msg("OCIRepository not yet reconciled, requeuing")
		return subroutines.StopWithRequeue(deployRequeueDuration, fmt.Sprintf("waiting for OCIRepository %s/%s to be reconciled", namespace, name)), nil
	}

	ready, err := r.helmReleaseReady(ctx, namespace, name)
	if err != nil {
		return subroutines.OK(), err
	}
	if !ready {
		log.Info().Msg("HelmRelease not ready yet, requeuing")
		return subroutines.StopWithRequeue(deployRequeueDuration, fmt.Sprintf("HelmRelease %s not ready", name)), nil
	}

	return subroutines.OK(), nil
}

func (r *DeploySubroutine) helmReleaseReady(ctx context.Context, namespace, name string) (bool, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(deployHelmReleaseGVK)
	if err := r.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
		if kerrors.IsNotFound(err) {
			return false, nil
		}
		return false, gcerrors.Wrap(err, "failed to get HelmRelease %s/%s", namespace, name)
	}
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if cond["type"] == "Ready" && cond["status"] == "True" {
			return true, nil
		}
	}
	return false, nil
}

func (r *DeploySubroutine) Finalize(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	inst := obj.(*providersv1alpha1.ManagedProvider)
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	inst.Status.Phase = providersv1alpha1.ManagedProviderPhaseDeleting

	needsDeletion := false
	for _, component := range inst.Spec.RuntimeDeployments {
		if component.OCM != nil {
			ocm := component.OCM
			name := ocmResourceName(ocm.ComponentName)

			hr := &unstructured.Unstructured{}
			hr.SetGroupVersionKind(deployHelmReleaseGVK)
			hr.SetName(name)
			hr.SetNamespace(inst.Namespace)
			if err := r.client.Delete(ctx, hr); err != nil {
				if !kerrors.IsNotFound(err) {
					return subroutines.OK(), gcerrors.Wrap(err, "failed to delete HelmRelease %s/%s", inst.Namespace, name)
				}
			} else {
				needsDeletion = true
			}

			oci := &unstructured.Unstructured{}
			oci.SetGroupVersionKind(deployOCIRepoGVK)
			oci.SetName(name)
			oci.SetNamespace(inst.Namespace)
			if err := r.client.Delete(ctx, oci); err != nil {
				if !kerrors.IsNotFound(err) {
					return subroutines.OK(), gcerrors.Wrap(err, "failed to delete OCIRepository %s/%s", inst.Namespace, name)
				}
			} else {
				needsDeletion = true
			}
		}
	}

	if !needsDeletion {
		log.Info().Msg("Deleted all Flux resources")
		r.limiter.Forget(inst)
		return subroutines.OK(), nil
	}

	return subroutines.StopWithRequeue(r.limiter.When(inst), "Waiting for Flux resources to be deleted"), nil
}

func (r *DeploySubroutine) Finalizers(_ client.Object) []string {
	return []string{deploySubroutineFinalizer}
}
