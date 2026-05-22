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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	pmsubs "github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
)

const (
	KubeconfigCopySubroutineName  = "KubeconfigCopySubroutine"
	kubeconfigCopyFinalizer       = "providers.platform-mesh.io/kubeconfig-secret"
	kubeconfigCopyRequeueDuration = 10 * time.Second
)

// KubeconfigCopySubroutine copies the kubeconfig Secret produced by the
// Provider controller from the kcp provider workspace into the runtime
// namespace, and records its name in status.kubeconfigSecretRef.
type KubeconfigCopySubroutine struct {
	client      client.Client
	kcpHelper   pmsubs.KcpHelper
	operatorCfg *config.OperatorConfig
	kcpUrl      string
}

func NewKubeconfigCopySubroutine(cl client.Client, kcpHelper pmsubs.KcpHelper, operatorCfg *config.OperatorConfig, kcpUrl string) *KubeconfigCopySubroutine {
	return &KubeconfigCopySubroutine{
		client:      cl,
		kcpHelper:   kcpHelper,
		operatorCfg: operatorCfg,
		kcpUrl:      kcpUrl,
	}
}

func (r *KubeconfigCopySubroutine) GetName() string {
	return KubeconfigCopySubroutineName
}

func (r *KubeconfigCopySubroutine) Process(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	inst := obj.(*providersv1alpha1.ManagedProvider)

	wsPath := workspacePath(inst)

	restCfg, err := pmsubs.BuildKcpAdminConfig(r.client, &r.operatorCfg.KCP, r.kcpUrl)
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to build kcp admin config")
	}

	scopedClient, err := r.kcpHelper.NewKcpClient(restCfg, wsPath)
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to create kcp client for provider workspace %s", wsPath)
	}

	// Fetch the Provider to find the kubeconfig Secret name set by the Provider controller.
	provider := &providersv1alpha1.Provider{}
	if err := scopedClient.Get(ctx, types.NamespacedName{Name: inst.Name}, provider); err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to get Provider %s from workspace %s", inst.Name, wsPath)
	}
	if provider.Status.KubeconfigSecretRef == nil {
		log.Info().Str("workspace", wsPath).Msg("Provider kubeconfigSecretRef not set yet, requeuing")
		inst.Status.Phase = "CopyingKubeconfig"
		return subroutines.StopWithRequeue(kubeconfigCopyRequeueDuration, "waiting for Provider to set kubeconfigSecretRef"), nil
	}

	// Fetch the kubeconfig Secret from the provider workspace.
	origSecret := &corev1.Secret{}
	if err := scopedClient.Get(ctx, types.NamespacedName{Name: provider.Status.KubeconfigSecretRef.Name, Namespace: provider.Status.KubeconfigSecretRef.Namespace}, origSecret); err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to get kubeconfig Secret %s from workspace %s", provider.Status.KubeconfigSecretRef.Name, wsPath)
	}

	var kcpKubeconfig []byte
	if origSecret.Data != nil {
		kcpKubeconfig = origSecret.Data["kubeconfig"]
	}

	if len(kcpKubeconfig) == 0 {
		inst.Status.Phase = "CopyingKubeconfig"
		return subroutines.StopWithRequeue(kubeconfigCopyRequeueDuration, "waiting for Provider to set kubeconfig in secret"), nil
	}

	copySecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      provider.Status.KubeconfigSecretRef.Name,
			Namespace: inst.Namespace,
		},
		Data: map[string][]byte{
			"kubeconfig": kcpKubeconfig,
		},
	}
	if _, err = controllerruntime.CreateOrUpdate(ctx, r.client, &copySecret, func() error {
		copySecret.Data = map[string][]byte{
			"kubeconfig": kcpKubeconfig,
		}
		return nil
	}); err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to copy kubeconfig Secret %s from workspace %s into namespace %s", provider.Status.KubeconfigSecretRef.Name, wsPath, inst.Namespace)
	}

	inst.Status.KubeconfigSecretRef = &corev1.SecretReference{
		Name:      copySecret.Name,
		Namespace: copySecret.Namespace,
	}

	log.Info().Str("workspace", wsPath).Str("secret", provider.Status.KubeconfigSecretRef.Name).Msg("Copied kubeconfig Secret to runtime namespace")
	return subroutines.OK(), nil
}

func (r *KubeconfigCopySubroutine) Finalize(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	inst := obj.(*providersv1alpha1.ManagedProvider)
	if inst.Status.KubeconfigSecretRef == nil {
		return subroutines.OK(), nil
	}

	inst.Status.Phase = "Deleting"

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      inst.Status.KubeconfigSecretRef.Name,
			Namespace: inst.Namespace,
		},
	}
	if err := client.IgnoreNotFound(r.client.Delete(ctx, &secret)); err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "delete %T %s", secret, secret.GetName())
	}

	return subroutines.OK(), nil
}

func (r *KubeconfigCopySubroutine) Finalizers(_ client.Object) []string {
	return []string{kubeconfigCopyFinalizer}
}
