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
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	pmsubs "github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
)

const (
	WaitProviderSubroutineName  = "WaitProviderSubroutine"
	waitProviderRequeueDuration = 10 * time.Second
)

// WaitProviderSubroutine polls the Provider resource in the kcp workspace
// until status.phase == "Ready", indicating that SA, RBAC, and the kubeconfig
// Secret have been created by the Provider controller.
type WaitProviderSubroutine struct {
	client    client.Client
	kcpHelper pmsubs.KcpHelper
	cfg       *config.OperatorConfig
	kcpUrl    string
}

func NewWaitProviderSubroutine(cl client.Client, kcpHelper pmsubs.KcpHelper, cfg *config.OperatorConfig, kcpUrl string) *WaitProviderSubroutine {
	return &WaitProviderSubroutine{
		client:    cl,
		kcpHelper: kcpHelper,
		cfg:       cfg,
		kcpUrl:    kcpUrl,
	}
}

func (r *WaitProviderSubroutine) GetName() string {
	return WaitProviderSubroutineName
}

func (r *WaitProviderSubroutine) Process(ctx context.Context, obj client.Object) (subroutines.Result, error) {
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

	provider := &providersv1alpha1.Provider{}
	if err := scopedClient.Get(ctx, types.NamespacedName{Name: inst.Name}, provider); err != nil {
		if kerrors.IsNotFound(err) {
			log.Info().Str("workspace", wsPath).Msg("Provider not found yet, requeuing")
			inst.Status.Phase = "WaitingForProvider"
			return subroutines.StopWithRequeue(waitProviderRequeueDuration, "Provider not found yet"), nil
		}
		return subroutines.OK(), gcerrors.Wrap(err, "failed to get Provider %s from workspace %s", inst.Name, wsPath)
	}

	if provider.Status.Phase != "Ready" {
		log.Info().Str("workspace", wsPath).Str("phase", provider.Status.Phase).Msg("Provider not Ready yet, requeuing")
		inst.Status.Phase = "WaitingForProvider"
		return subroutines.StopWithRequeue(waitProviderRequeueDuration, "waiting for Provider to become Ready"), nil
	}

	log.Info().Str("workspace", wsPath).Msg("Provider is Ready")
	return subroutines.OK(), nil
}

func (r *WaitProviderSubroutine) Finalize(_ context.Context, _ client.Object) (subroutines.Result, error) {
	return subroutines.OK(), nil
}

func (r *WaitProviderSubroutine) Finalizers(_ client.Object) []string {
	return []string{}
}
