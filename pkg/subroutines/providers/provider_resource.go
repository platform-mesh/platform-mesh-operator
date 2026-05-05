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

	gcerrors "github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/subroutines"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	pmsubs "github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
)

const ProviderResourceSubroutineName = "ProviderResourceSubroutine"

// ProviderResourceSubroutine creates a Provider resource inside the provider
// workspace, triggering the Provider controller to bootstrap SA, RBAC, and the
// kubeconfig Secret on the kcp side.
type ProviderResourceSubroutine struct {
	client    client.Client
	kcpHelper pmsubs.KcpHelper
	cfg       *config.OperatorConfig
	kcpUrl    string
}

func NewProviderResourceSubroutine(cl client.Client, kcpHelper pmsubs.KcpHelper, cfg *config.OperatorConfig, kcpUrl string) *ProviderResourceSubroutine {
	return &ProviderResourceSubroutine{
		client:    cl,
		kcpHelper: kcpHelper,
		cfg:       cfg,
		kcpUrl:    kcpUrl,
	}
}

func (r *ProviderResourceSubroutine) GetName() string {
	return ProviderResourceSubroutineName
}

func (r *ProviderResourceSubroutine) Process(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	inst := obj.(*providersv1alpha1.ManagedProvider)

	wsPath := workspacePath(inst)

	restCfg, err := pmsubs.BuildKcpAdminConfig(r.client, &r.cfg.KCP, r.kcpUrl)
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to build kcp admin config")
	}

	scopedClient, err := r.kcpHelper.NewKcpClient(restCfg, wsPath)
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to create kcp client for provider workspace %s", wsPath)
	}

	if err := applyProvider(ctx, scopedClient, inst.Name, func(p *providersv1alpha1.Provider) {}); err != nil {
		return subroutines.Result{}, err
	}

	log.Info().Str("workspace", wsPath).Msg("Ensured provider workspace")
	return subroutines.OK(), nil
}

func applyProvider(ctx context.Context, scopedKubeClient client.Client, name string, patch func(*providersv1alpha1.Provider)) error {
	provider := &providersv1alpha1.Provider{}
	provider.APIVersion = providersv1alpha1.SchemeGroupVersion.String()
	provider.Kind = "Provider"
	provider.Name = name
	patch(provider)

	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(provider)
	if err != nil {
		return gcerrors.Wrap(err, "failed to convert Provider to unstructured")
	}
	uObj := unstructured.Unstructured{Object: u}

	err = scopedKubeClient.Apply(ctx, client.ApplyConfigurationFromUnstructured(&uObj),
		client.FieldOwner("platform-mesh-operator"), client.ForceOwnership)
	if err != nil {
		return gcerrors.Wrap(err, "failed to apply provider %s", name)
	}
	return nil
}

func (r *ProviderResourceSubroutine) Finalize(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	inst := obj.(*providersv1alpha1.ManagedProvider)
	if !inst.Spec.CleanupOnDelete {
		return subroutines.OK(), nil
	}

	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	wsPath := workspacePath(inst)

	restCfg, err := pmsubs.BuildKcpAdminConfig(r.client, &r.cfg.KCP, r.kcpUrl)
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to build kcp admin config")
	}

	scopedKubeClient, err := r.kcpHelper.NewKcpClient(restCfg, wsPath)
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to create kcp client for provider workspace %s", wsPath)
	}

	provider := &providersv1alpha1.Provider{}
	provider.Name = inst.Name
	if err := client.IgnoreNotFound(scopedKubeClient.Delete(ctx, provider)); err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to delete provider %s", provider.Name)
	}

	log.Info().Str("workspace", wsPath).Msg("Deleted provider")
	return subroutines.OK(), nil
}

func (r *ProviderResourceSubroutine) Finalizers(_ client.Object) []string {
	return []string{}
}
