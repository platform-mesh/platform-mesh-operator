package subroutines

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/rbacpresets"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
)

func writeProviderPresetKubeconfigToSecret(
	ctx context.Context,
	presetLoader *rbacpresets.Loader,
	k8sClient client.Client,
	kcpHelper KcpHelper,
	cfg *rest.Config,
	instance *corev1alpha1.PlatformMesh,
	pc corev1alpha1.ProviderConnection,
) error {
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
	rawPath := strings.TrimSpace(ptr.Deref(pc.RawPath, ""))
	presetName := strings.TrimSpace(ptr.Deref(pc.ProviderRBACPreset, ""))
	rendered, err := presetLoader.LoadPreset(presetName, rbacpresets.PresetTemplateData{
		ProviderPath: strings.TrimSpace(pc.Path),
		RawPath:      rawPath,
		Suffix:       pc.Secret,
	})
	if err != nil {
		return err
	}
	if rendered.Spec.ServiceAccountWorkspace == "" {
		return fmt.Errorf("preset %q did not define a ServiceAccount workspace", presetName)
	}
	if rendered.Spec.ServiceAccountName == "" {
		return fmt.Errorf("preset %q did not define a ServiceAccount name", presetName)
	}

	serverURL, err := buildPresetServerURL(ctx, kcpHelper, cfg, operatorCfg, instance, pc, rendered.Spec.ServerTarget)
	if err != nil {
		return err
	}
	if err := applyPresetManifests(ctx, kcpHelper, cfg, rendered.ByWorkspace); err != nil {
		return err
	}

	saWorkspaceClient, err := kcpHelper.NewKcpClient(rest.CopyConfig(cfg), rendered.Spec.ServiceAccountWorkspace)
	if err != nil {
		return errors.Wrap(err, "kcp client for preset ServiceAccount workspace")
	}
	token, err := createTokenForSA(ctx, saWorkspaceClient, defaultScopedSANamespace, rendered.Spec.ServiceAccountName, defaultTokenExpirationSeconds)
	if err != nil {
		return errors.Wrap(err, "create token for preset ServiceAccount")
	}

	caData := cfg.TLSClientConfig.CAData
	if caData == nil {
		caData = []byte{}
	}
	caData = AppendRootShardCAPEMIfMissing(ctx, k8sClient, &operatorCfg, caData)
	kubeconfigBytes, err := clientcmd.Write(*buildScopedKubeconfig(serverURL, token, caData))
	if err != nil {
		return errors.Wrap(err, "write preset kubeconfig")
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: pc.Secret, Namespace: ptr.Deref(pc.Namespace, operatorCfg.KCP.Namespace)},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, secret, func() error {
		secret.Data = map[string][]byte{"kubeconfig": kubeconfigBytes}
		return nil
	})
	if err != nil {
		return errors.Wrap(err, "write preset provider secret")
	}
	return nil
}

func buildPresetServerURL(
	ctx context.Context,
	kcpHelper KcpHelper,
	cfg *rest.Config,
	operatorCfg config.OperatorConfig,
	instance *corev1alpha1.PlatformMesh,
	pc corev1alpha1.ProviderConnection,
	target rbacpresets.ServerTarget,
) (string, error) {
	switch target.Type {
	case rbacpresets.ServerTargetWorkspaceCluster:
		if strings.TrimSpace(pc.Path) == "" {
			return "", fmt.Errorf("workspaceCluster preset target requires provider connection path")
		}
		return createScopedKubeconfigURLForAPIExportName(operatorCfg, instance, strings.TrimSpace(pc.Path), pc.External)
	case rbacpresets.ServerTargetRawPath:
		rawPath := strings.TrimSpace(target.RawPath)
		if rawPath == "" {
			rawPath = strings.TrimSpace(ptr.Deref(pc.RawPath, ""))
		}
		if rawPath == "" {
			return "", fmt.Errorf("rawPath preset target requires serverTarget.rawPath or provider connection rawPath")
		}
		return joinPresetHostPath(operatorCfg, instance, pc.External, rawPath)
	case rbacpresets.ServerTargetPathRawPath:
		if strings.TrimSpace(pc.Path) == "" {
			return "", fmt.Errorf("pathRawPath preset target requires provider connection path")
		}
		rawPath := strings.TrimSpace(target.RawPath)
		if rawPath == "" {
			rawPath = strings.TrimSpace(ptr.Deref(pc.RawPath, ""))
		}
		if rawPath == "" {
			return "", fmt.Errorf("pathRawPath preset target requires serverTarget.rawPath or provider connection rawPath")
		}
		return joinPresetHostPath(operatorCfg, instance, pc.External, rawPath)
	case rbacpresets.ServerTargetWorkspaceTypeVirtualWorkspace:
		workspaceTypeName := strings.TrimSpace(target.WorkspaceTypeName)
		if workspaceTypeName == "" {
			return "", fmt.Errorf("workspaceTypeVirtualWorkspace preset target requires workspaceTypeName")
		}
		workspaceTypePath := strings.TrimSpace(target.WorkspaceTypePath)
		if workspaceTypePath == "" {
			workspaceTypePath = strings.TrimSpace(pc.Path)
		}
		if workspaceTypePath == "" {
			return "", fmt.Errorf("workspaceTypeVirtualWorkspace preset target requires workspaceTypePath or provider connection path")
		}
		kcpClient, err := kcpHelper.NewKcpClient(rest.CopyConfig(cfg), workspaceTypePath)
		if err != nil {
			return "", errors.Wrap(err, "kcp client for WorkspaceType virtual workspace")
		}
		wt := &kcptenancyv1alpha.WorkspaceType{}
		if err := kcpClient.Get(ctx, types.NamespacedName{Name: workspaceTypeName}, wt); err != nil {
			return "", fmt.Errorf("get WorkspaceType %s in workspace %s: %w", workspaceTypeName, workspaceTypePath, err)
		}
		if len(wt.Status.VirtualWorkspaces) == 0 || strings.TrimSpace(wt.Status.VirtualWorkspaces[0].URL) == "" {
			return "", fmt.Errorf("WorkspaceType %s in workspace %s has no virtual workspace URL", workspaceTypeName, workspaceTypePath)
		}
		return rewriteScopedVirtualWorkspaceURLToFrontProxy(wt.Status.VirtualWorkspaces[0].URL, operatorCfg, instance, pc.External)
	default:
		return "", fmt.Errorf("unsupported preset server target type %q", target.Type)
	}
}

func joinPresetHostPath(operatorCfg config.OperatorConfig, instance *corev1alpha1.PlatformMesh, external bool, rawPath string) (string, error) {
	hostPort, err := scopedProviderHostPort(operatorCfg, instance, external)
	if err != nil {
		return "", err
	}
	out, err := url.JoinPath(hostPort, rawPath)
	if err != nil {
		return "", errors.Wrap(err, "build preset server URL")
	}
	return out, nil
}

func scopedProviderHostPort(operatorCfg config.OperatorConfig, instance *corev1alpha1.PlatformMesh, external bool) (string, error) {
	if external {
		if instance.Spec.Exposure == nil {
			return "", fmt.Errorf("provider connection with external: true requires spec.exposure")
		}
		return fmt.Sprintf("https://kcp.api.%s:%d", instance.Spec.Exposure.BaseDomain, instance.Spec.Exposure.Port), nil
	}
	return fmt.Sprintf("https://%s-front-proxy.%s:%s", operatorCfg.KCP.FrontProxyName, operatorCfg.KCP.Namespace, operatorCfg.KCP.FrontProxyPort), nil
}

func applyPresetManifests(ctx context.Context, kcpHelper KcpHelper, cfg *rest.Config, manifestsByWorkspace []rbacpresets.WorkspaceManifests) error {
	for _, workspaceManifests := range manifestsByWorkspace {
		workspace := strings.TrimSpace(workspaceManifests.Workspace)
		if workspace == "" {
			return fmt.Errorf("preset manifest workspace is empty")
		}
		kcpClient, err := kcpHelper.NewKcpClient(rest.CopyConfig(cfg), workspace)
		if err != nil {
			return errors.Wrap(err, "kcp client for preset workspace %s", workspace)
		}
		if err := ensureScopedNamespaceExists(ctx, kcpClient, defaultScopedSANamespace); err != nil {
			return errors.Wrap(err, "ensure namespace %s for preset workspace %s", defaultScopedSANamespace, workspace)
		}
		for i := range workspaceManifests.Manifests {
			if err := createOrUpdatePresetManifest(ctx, kcpClient, &workspaceManifests.Manifests[i]); err != nil {
				return errors.Wrap(err, "apply preset manifest in workspace %s", workspace)
			}
		}
	}
	return nil
}

func createOrUpdatePresetManifest(ctx context.Context, kcpClient client.Client, manifest *unstructured.Unstructured) error {
	if manifest == nil || manifest.Object == nil {
		return nil
	}
	desired := manifest.DeepCopy()
	if desired.GetName() == "" {
		return fmt.Errorf("manifest %s has empty name", desired.GetKind())
	}
	defaultPresetManifestNamespace(desired)

	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(desired.GroupVersionKind())
	key := client.ObjectKey{Name: desired.GetName(), Namespace: desired.GetNamespace()}
	if err := kcpClient.Get(ctx, key, current); err != nil {
		if !kerrors.IsNotFound(err) {
			return err
		}
		return kcpClient.Create(ctx, desired)
	}
	desired.SetResourceVersion(current.GetResourceVersion())
	return kcpClient.Update(ctx, desired)
}

func defaultPresetManifestNamespace(obj *unstructured.Unstructured) {
	switch obj.GetKind() {
	case "ServiceAccount", "RoleBinding":
		if obj.GetNamespace() == "" {
			obj.SetNamespace(defaultScopedSANamespace)
		}
	}
}
