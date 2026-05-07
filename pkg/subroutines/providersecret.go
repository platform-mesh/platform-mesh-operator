package subroutines

import (
	"context"
	"fmt"
	"net/url"
	"path"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	gcerrors "github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/subroutines"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
)

// HelmGetter is an interface for getting Helm releases
type HelmGetter interface {
	GetRelease(ctx context.Context, client client.Client, name, ns string) (*unstructured.Unstructured, error)
}

// DefaultHelmGetter is the default implementation of HelmGetter
type DefaultHelmGetter struct{}

// GetRelease implements HelmGetter interface
func (g DefaultHelmGetter) GetRelease(ctx context.Context, cli client.Client, name, ns string) (*unstructured.Unstructured, error) {
	return getHelmRelease(ctx, cli, name, ns)
}

func NewProviderSecretSubroutine(
	client client.Client,
	helper KcpHelper,
	helm HelmGetter,
	kcpUrl string,
) *ProvidersecretSubroutine {
	sub := &ProvidersecretSubroutine{
		client:    client,
		kcpHelper: helper,
		helm:      helm,
		kcpUrl:    kcpUrl,
	}
	return sub
}

type ProvidersecretSubroutine struct {
	client    client.Client
	kcpHelper KcpHelper
	helm      HelmGetter
	kcpUrl    string
}

const (
	ProvidersecretSubroutineName      = "ProvidersecretSubroutine"
	ProvidersecretSubroutineFinalizer = "platform-mesh.core.platform-mesh.io/finalizer"
)

func (r *ProvidersecretSubroutine) Finalize(
	_ context.Context, _ client.Object,
) (subroutines.Result, error) {
	return subroutines.OK(), nil
}

func (r *ProvidersecretSubroutine) Process(
	ctx context.Context, runtimeObj client.Object,
) (subroutines.Result, error) {
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)

	scheme := r.client.Scheme()
	if scheme == nil {
		return subroutines.StopWithRequeue(DefaultRequeueInterval, "client scheme is nil"), nil
	}

	instance := runtimeObj.(*corev1alpha1.PlatformMesh)
	log := logger.LoadLoggerFromContext(ctx)

	// Wait for kcp release to be ready before continuing
	rootShard := &unstructured.Unstructured{}
	rootShard.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "RootShard"})
	err := r.client.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.RootShardName, Namespace: operatorCfg.KCP.Namespace}, rootShard)
	if err != nil || !matchesConditionWithStatus(rootShard, "Available", "True") {
		log.Info().Msg("RootShard is not ready..")
		return subroutines.StopWithRequeue(DefaultRequeueInterval, "RootShard is not ready"), nil
	}

	frontProxy := &unstructured.Unstructured{}
	frontProxy.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "FrontProxy"})
	err = r.client.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.FrontProxyName, Namespace: operatorCfg.KCP.Namespace}, frontProxy)
	if err != nil || !matchesConditionWithStatus(frontProxy, "Available", "True") {
		log.Info().Msg("FrontProxy is not ready..")
		return subroutines.StopWithRequeue(DefaultRequeueInterval, "FrontProxy is not ready"), nil
	}

	// Determine which provider connections to use based on configuration:
	var providers []corev1alpha1.ProviderConnection
	hasProv := len(instance.Spec.Kcp.ProviderConnections) > 0
	hasExtraProv := len(instance.Spec.Kcp.ExtraProviderConnections) > 0

	switch {
	case !hasProv && !hasExtraProv:
		providers = DefaultProviderConnections
	case !hasProv && hasExtraProv:
		providers = append(DefaultProviderConnections, instance.Spec.Kcp.ExtraProviderConnections...)
	case hasProv && !hasExtraProv:
		providers = instance.Spec.Kcp.ProviderConnections
	default:
		providers = append(instance.Spec.Kcp.ProviderConnections, instance.Spec.Kcp.ExtraProviderConnections...)
	}

	if HasFeatureToggle(instance, "feature-enable-terminal-controller-manager") == "true" {
		providers = append(providers, corev1alpha1.ProviderConnection{
			Path:      "root:platform-mesh-system",
			Secret:    "terminal-controller-manager-kubeconfig",
			AdminAuth: ptr.To(true),
		})
	}

	// Build kcp kubeconfig
	cfg, err := buildKubeconfig(ctx, r.client, getExternalKcpHost(instance, &operatorCfg))
	if err != nil {
		log.Error().Err(err).Msg("Failed to build kubeconfig")
		return subroutines.OK(), gcerrors.Wrap(err, "Failed to build kubeconfig")
	}

	// Load the raw admin kubeconfig for serializing into provider secrets
	adminKubeconfig, err := loadAdminKubeconfig(ctx, r.client)
	if err != nil {
		log.Error().Err(err).Msg("Failed to load admin kubeconfig")
		return subroutines.OK(), gcerrors.Wrap(err, "Failed to load admin kubeconfig")
	}

	// Append root CA to the admin kubeconfig's cluster CA data.
	rootCASecretName := operatorCfg.KCP.RootShardName + "-ca"
	rootCASecret, rootCAErr := GetSecret(r.client, rootCASecretName, operatorCfg.KCP.Namespace)
	if rootCAErr == nil && rootCASecret != nil {
		if rootCAData, ok := rootCASecret.Data["tls.crt"]; ok && len(rootCAData) > 0 {
			for clusterName, cluster := range adminKubeconfig.Clusters {
				if len(cluster.CertificateAuthorityData) > 0 {
					adminKubeconfig.Clusters[clusterName].CertificateAuthorityData = append(cluster.CertificateAuthorityData, '\n')
					adminKubeconfig.Clusters[clusterName].CertificateAuthorityData = append(adminKubeconfig.Clusters[clusterName].CertificateAuthorityData, rootCAData...)
				}
			}
		}
	}

	for _, pc := range providers {
		// Scoped kubeconfig path (non-admin)
		if !ptr.Deref(pc.AdminAuth, false) {
			if err := writeScopedKubeconfigToSecret(ctx, r.client, r.kcpHelper, cfg, instance, pc); err != nil {
				log.Error().Err(err).Str("secret", pc.Secret).Msg("Failed to write scoped provider kubeconfig")
				return subroutines.OK(), err
			}
			continue
		}

		// Admin kubeconfig path
		if _, connErr := r.HandleProviderConnection(ctx, instance, pc, cfg, adminKubeconfig); connErr != nil {
			log.Error().Err(connErr).Msg("Failed to handle provider connection")
			return subroutines.OK(), connErr
		}
	}
	return subroutines.OK(), nil
}

func (r *ProvidersecretSubroutine) Finalizers(_ client.Object) []string { // coverage-ignore
	return []string{ProvidersecretSubroutineFinalizer}
}

func (r *ProvidersecretSubroutine) GetName() string {
	return ProvidersecretSubroutineName
}

func (r *ProvidersecretSubroutine) HandleProviderConnection(
	ctx context.Context, instance *corev1alpha1.PlatformMesh, pc corev1alpha1.ProviderConnection, cfg *rest.Config, adminKubeconfig *clientcmdapi.Config,
) (subroutines.Result, error) {
	log := logger.LoadLoggerFromContext(ctx)
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)

	var address *url.URL

	if ptr.Deref(pc.EndpointSliceName, "") != "" {
		kcpClient, err := r.kcpHelper.NewKcpClient(cfg, pc.Path)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create KCP client")
			return subroutines.OK(), err
		}

		var slice kcpapiv1alpha.APIExportEndpointSlice
		err = kcpClient.Get(ctx, client.ObjectKey{Name: *pc.EndpointSliceName}, &slice)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get APIExportEndpointSlice")
			return subroutines.OK(), err
		}

		if len(slice.Status.APIExportEndpoints) == 0 {
			return subroutines.StopWithRequeue(DefaultRequeueInterval, "no endpoints in slice"), nil
		}

		endpointURL := slice.Status.APIExportEndpoints[0].URL
		address, err = url.Parse(endpointURL)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse endpoint URL")
			return subroutines.OK(), err
		}
	} else {
		kcpUrl, err := url.Parse(cfg.Host)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse KCP URL")
			return subroutines.OK(), err
		}
		if ptr.Deref(pc.RawPath, "") != "" {
			kcpUrl.Path = *pc.RawPath
		} else {
			kcpUrl.Path = path.Join("clusters", pc.Path)
		}
		address = kcpUrl
	}

	var hostPort string
	if pc.External {
		hostPort = getExternalKcpHost(instance, &operatorCfg)
	} else {
		hostPort = fmt.Sprintf("https://%s-front-proxy.%s:%s", operatorCfg.KCP.FrontProxyName, operatorCfg.KCP.Namespace, operatorCfg.KCP.FrontProxyPort)
	}
	host, err := url.JoinPath(hostPort, address.Path)
	if err != nil {
		log.Error().Err(err).Msg("Failed to join path for provider connection")
		return subroutines.OK(), err
	}

	// Deep-copy the admin kubeconfig and set the server URL for this provider
	providerKubeconfig := adminKubeconfig.DeepCopy()
	for _, cluster := range providerKubeconfig.Clusters {
		cluster.Server = host
	}
	kcpConfigBytes, err := clientcmd.Write(*providerKubeconfig)
	if err != nil {
		log.Error().Err(err).Msg("Failed to write kubeconfig")
		return subroutines.OK(), err
	}

	namespace := "platform-mesh-system"
	if ptr.Deref(pc.Namespace, "") != "" {
		namespace = *pc.Namespace
	}
	providerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pc.Secret,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"kubeconfig": kcpConfigBytes,
		},
	}
	providerSecret.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"})

	// Apply using SSA (creates if not exists, updates if exists)
	if err := r.client.Patch(ctx, providerSecret, client.Apply, client.FieldOwner("platform-mesh-provider-secret"), client.ForceOwnership); err != nil {
		log.Error().Err(err).Msg("Failed to apply secret")
		return subroutines.OK(), err
	}

	log.Debug().Str("secret", pc.Secret).Msg("Created or updated provider secret")

	return subroutines.OK(), nil
}

func (r *ProvidersecretSubroutine) HandleInitializerConnection(
	ctx context.Context, instance *corev1alpha1.PlatformMesh, ic corev1alpha1.InitializerConnection, restCfg *rest.Config, adminKubeconfig *clientcmdapi.Config,
) (subroutines.Result, error) {
	log := logger.LoadLoggerFromContext(ctx)

	kcpClient, err := r.kcpHelper.NewKcpClient(restCfg, ic.Path)
	if err != nil {
		log.Error().Err(err).Msg("creating kcp client for initializer")
		return subroutines.OK(), err
	}

	wt := &kcptenancyv1alpha.WorkspaceType{}
	if err := kcpClient.Get(ctx, types.NamespacedName{Name: ic.WorkspaceTypeName}, wt); err != nil {
		log.Error().Err(err).Msg("getting WorkspaceType")
		return subroutines.OK(), err
	}
	if len(wt.Status.VirtualWorkspaces) == 0 {
		err = fmt.Errorf("no virtual workspaces found in %s", ic.WorkspaceTypeName)
		log.Error().Err(err).Msg("bad WorkspaceType")
		return subroutines.StopWithRequeue(DefaultRequeueInterval, err.Error()), nil
	}

	vwURL, err := url.Parse(wt.Status.VirtualWorkspaces[0].URL)
	if err != nil {
		log.Error().Err(err).Msg("parsing virtual workspace URL")
		return subroutines.OK(), err
	}
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
	vwURL.Host = fmt.Sprintf("%s-front-proxy:%s", operatorCfg.KCP.FrontProxyName, operatorCfg.KCP.FrontProxyPort)

	// Deep-copy the admin kubeconfig and set the server URL
	initKubeconfig := adminKubeconfig.DeepCopy()
	for _, cluster := range initKubeconfig.Clusters {
		cluster.Server = vwURL.String()
	}
	log.Debug().Str("url", vwURL.String()).Msg("modified virtual workspace URL")

	data, err := clientcmd.Write(*initKubeconfig)
	if err != nil {
		log.Error().Err(err).Msg("writing modified kubeconfig")
		return subroutines.OK(), err
	}

	namespace := "platform-mesh-system"
	if ic.Namespace != "" {
		namespace = ic.Namespace
	}
	initializerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ic.Secret,
			Namespace: namespace,
		},
		Data: map[string][]byte{"kubeconfig": data},
	}
	initializerSecret.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"})

	// Apply using SSA (creates if not exists, updates if exists)
	if err := r.client.Patch(ctx, initializerSecret, client.Apply, client.FieldOwner("platform-mesh-provider-secret"), client.ForceOwnership); err != nil {
		log.Error().Err(err).Msg("creating/updating initializer Secret")
		return subroutines.OK(), err
	}

	return subroutines.OK(), nil
}
