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
	"fmt"
	"strings"

	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/ratelimiter"
	gcerrors "github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/subroutines"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	pmsubs "github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
)

const (
	WorkspaceSubroutineName      = "WorkspaceSubroutine"
	WorkspaceSubroutineFinalizer = "providers.platform-mesh.io/workspace"

	defaultWorkspaceParent    = "root:providers"
	providerWorkspaceTypeName = "provider"
	providerWorkspaceTypePath = "root"
)

// WorkspaceSubroutine creates the provider workspace in kcp under
// root:providers:<name> (or spec.workspacePath if set).
type WorkspaceSubroutine struct {
	client    client.Client
	kcpHelper pmsubs.KcpHelper
	cfg       *config.OperatorConfig
	kcpUrl    string

	limiter workqueue.TypedRateLimiter[*providersv1alpha1.ManagedProvider]
}

func NewWorkspaceSubroutine(cl client.Client, kcpHelper pmsubs.KcpHelper, cfg *config.OperatorConfig, kcpUrl string) (*WorkspaceSubroutine, error) {
	rl, err := ratelimiter.NewStaticThenExponentialRateLimiter[*providersv1alpha1.ManagedProvider](
		ratelimiter.NewConfig())
	if err != nil {
		return nil, fmt.Errorf("creating RateLimiter: %v", err)
	}
	return &WorkspaceSubroutine{
		client:    cl,
		kcpHelper: kcpHelper,
		cfg:       cfg,
		kcpUrl:    kcpUrl,
		limiter:   rl,
	}, nil
}

func (r *WorkspaceSubroutine) GetName() string {
	return WorkspaceSubroutineName
}

func (r *WorkspaceSubroutine) Process(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	inst := obj.(*providersv1alpha1.ManagedProvider)

	wsPath := workspacePath(inst)
	parentPath, workspaceName, err := splitPath(wsPath)
	if err != nil {
		return subroutines.OK(), err
	}

	log.Debug().Str("parentPath", parentPath).Str("workspaceName", workspaceName).Msg("Ensuring provider workspace")

	restCfg, err := pmsubs.BuildKubeconfigFromConfig(r.client, &r.cfg.KCP, r.kcpUrl)
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to build kcp admin config")
	}

	scopedClient, err := r.kcpHelper.NewKcpClient(restCfg, parentPath)
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to create kcp client for parent workspace %s", parentPath)
	}

	if err := applyWorkspace(ctx, scopedClient,
		workspaceName, wsPath,
		providerWorkspaceTypeName, providerWorkspaceTypePath,
	); err != nil {
		return subroutines.OK(), err
	}

	log.Info().Str("workspace", wsPath).Msg("Ensured provider workspace")
	return subroutines.OK(), nil
}

func applyWorkspace(ctx context.Context, scopedKubeClient client.Client, name, path string, typeName kcptenancyv1alpha.WorkspaceTypeName, typePath string) error {
	ws := &kcptenancyv1alpha.Workspace{}
	ws.APIVersion = kcptenancyv1alpha.SchemeGroupVersion.String()
	ws.Kind = "Workspace"
	ws.Name = name
	ws.Spec.Type = &kcptenancyv1alpha.WorkspaceTypeReference{
		Name: typeName,
		Path: typePath,
	}

	unstructuredWs, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ws)
	if err != nil {
		return gcerrors.Wrap(err, "failed to convert workspace to unstructured")
	}
	unstructuredObj := unstructured.Unstructured{Object: unstructuredWs}

	err = scopedKubeClient.Apply(ctx, client.ApplyConfigurationFromUnstructured(&unstructuredObj),
		client.FieldOwner("platform-mesh-operator"), client.ForceOwnership)
	if err != nil {
		return gcerrors.Wrap(err, "failed to apply workspace %s", path)
	}
	return nil
}

func (r *WorkspaceSubroutine) Finalize(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	inst := obj.(*providersv1alpha1.ManagedProvider)
	if !inst.Spec.CleanupOnDelete {
		return subroutines.OK(), nil
	}

	inst.Status.Phase = "Deleting"

	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	wsPath := workspacePath(inst)
	parentPath, workspaceName, err := splitPath(wsPath)
	if err != nil {
		return subroutines.OK(), err
	}

	restCfg, err := pmsubs.BuildKubeconfigFromConfig(r.client, &r.cfg.KCP, r.kcpUrl)
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to build kcp admin config")
	}

	scopedKcpClient, err := r.kcpHelper.NewKcpClient(restCfg, parentPath)
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "failed to create kcp client for parent workspace %s", parentPath)
	}

	ws := &kcptenancyv1alpha.Workspace{}
	ws.Name = workspaceName
	if err = scopedKcpClient.Delete(ctx, ws); err != nil {
		if kerrors.IsNotFound(err) {
			log.Info().Str("workspace", wsPath).Msg("Deleted provider workspace")
			r.limiter.Forget(inst)
			return subroutines.OK(), nil
		}
		return subroutines.OK(), gcerrors.Wrap(err, "failed to delete workspace %s", wsPath)
	}

	return subroutines.StopWithRequeue(r.limiter.When(inst), "Waiting for workspace to be deleted"), nil
}

func (r *WorkspaceSubroutine) Finalizers(_ client.Object) []string {
	return []string{WorkspaceSubroutineFinalizer}
}

func workspacePath(inst *providersv1alpha1.ManagedProvider) string {
	if inst.Spec.WorkspacePath != "" {
		return inst.Spec.WorkspacePath
	}
	return fmt.Sprintf("%s:%s", defaultWorkspaceParent, inst.Name)
}

func splitPath(wsPath string) (parentPath, name string, err error) {
	i := strings.LastIndex(wsPath, ":")
	if i == -1 {
		return "", "", fmt.Errorf("invalid workspace path %q: must be of the form parent:name", wsPath)
	}
	return wsPath[:i], wsPath[i+1:], nil
}
