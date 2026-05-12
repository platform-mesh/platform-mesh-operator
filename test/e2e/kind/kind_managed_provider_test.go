package e2e

import (
	"context"
	"time"

	"github.com/creasty/defaults"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/kcp-dev/multicluster-provider/apiexport"
	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/context/keys"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	providerscontroller "github.com/platform-mesh/platform-mesh-operator/internal/controller/providers"
	pmsubs "github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
)

func (s *KindTestSuite) TestManagedProvider01Bootstrap() {
	ctx := context.Background()

	// root:providers workspace must exist before WorkspaceSubroutine can create
	// root:providers:my-managed-provider inside it. TestScoped01KubeconfigKcpPrereq
	// also creates this workspace, but it runs after TestManagedProvider (M < S).
	rootClient, err := s.kcpClientForWorkspace(ctx, "root")
	s.Require().NoError(err, "kcp client for root")
	s.Require().NoError(
		ApplyManifestFromFile(ctx, e2eKcpProviderWorkspacesYAMLDir+"/workspace-providers.yaml", rootClient, make(map[string]string)),
		"apply workspace-providers.yaml",
	)
	s.waitWorkspaceReady(ctx, rootClient, "providers")

	// Run the Providers operator
	s.logger.Info().Msg("starting Providers operator...")
	s.runProviderOperator(ctx)
}

func (s *KindTestSuite) TestManagedProvider02Lifecycle() {
	ctx := s.T().Context()

	s.Run("Ensure life-cycling ManagedProvider works", func() {
		// kcp client scoped to :root:providers:my-managed-provider.
		kcpAdminCfg, err := pmsubs.BuildKcpAdminConfig(s.client, &defaultKcpOperatorConfig, defaultKcpOperatorConfig.Url)
		s.NoError(err, "getting kcp admin rest config should succeed")
		providerScopedKcpAdminCfg := rest.CopyConfig(kcpAdminCfg)
		providerScopedKcpAdminCfg.Host += "/clusters/root:providers:my-managed-provider"
		providerScopedKcpAdminClient, err := client.New(providerScopedKcpAdminCfg, client.Options{
			Scheme: s.scheme,
		})
		s.NoError(err, "creating kcp admin client should succeed")

		// This test life-cycles ManagedProvider twice, validating ManagedProvider.spec.cleanupOnDelete.
		// In both cases, ManagedProvider is expected to create a Deployment in the runtime cluster,
		// and a kubeconfig scoped to provider's workspace.

		ns := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "e2e-managed-provider",
			},
		}
		s.logger.Info().Msgf("Creating namespace %q", ns.Name)
		err = s.client.Create(ctx, &ns)
		s.NoError(err, "creating namespace for a ManagedProvider should succeed")
		s.T().Cleanup(func() {
			cleanupCtx := context.Background()
			mpList := &providersv1alpha1.ManagedProviderList{}
			if err := s.client.List(cleanupCtx, mpList, client.InNamespace(ns.Name)); err == nil {
				for i := range mpList.Items {
					mp := &mpList.Items[i]
					patch := client.MergeFrom(mp.DeepCopy())
					mp.Finalizers = nil
					_ = s.client.Patch(cleanupCtx, mp, patch)
					_ = client.IgnoreNotFound(s.client.Delete(cleanupCtx, mp))
				}
			}
			_ = client.IgnoreNotFound(s.client.Delete(cleanupCtx, &ns))
		})
		s.logger.Info().Msgf("Namespace %q created", ns.Name)

		// First variant, with cleanupOnDelete=false. Only runtime resources are expected to be deleted on ManagedProvider deletion.

		waitForManagedProviderAndValidate(ctx, s, providerScopedKcpAdminClient, func(mp *providersv1alpha1.ManagedProvider) {
			mp.Spec.CleanupOnDelete = false
		})
		// Deleting ManagedProvider should NOT delete its artifacts on kcp side since spec.cleanupOnDelete=false.
		// The Deployment should be deleted though.
		s.logger.Info().Msgf("Deleting ManagedProvider with cleanupOnDelete=false")
		managedProvider := providersv1alpha1.ManagedProvider{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "e2e-managed-provider",
				Name:      "my-managed-provider",
			},
		}
		err = s.client.Delete(ctx, &managedProvider)
		s.NoError(err, "deleting ManagedProvider should succeed")
		s.Eventually(func() bool {
			err = s.client.Get(ctx, types.NamespacedName{
				Name:      "my-managed-provider",
				Namespace: ns.Name,
			}, &managedProvider)
			return kerrors.IsNotFound(err)
		}, 240*time.Second, 5*time.Second, "waiting for ManagedProvider to be deleted, but has err=%q", err)

		var provider providersv1alpha1.Provider
		err = providerScopedKcpAdminClient.Get(ctx, types.NamespacedName{
			Namespace: "default",
			Name:      "my-managed-provider",
		}, &provider)
		s.NoError(err, "getting Provider with scopedKcpAdminClient should succeed")
		s.Equal("Ready", provider.Status.Phase, "Provider on kcp side should have reached Phase=Ready")
		s.Nil(provider.DeletionTimestamp, "Provider should not be marked for deletion")

		// Second variant, with cleanupOnDelete=true. Everything is expected to be gone once ManagedProvider is deleted.

		s.logger.Info().Msgf("Re-creating ManagedProvider with cleanupOnDelete=true")
		waitForManagedProviderAndValidate(ctx, s, providerScopedKcpAdminClient, func(mp *providersv1alpha1.ManagedProvider) {
			mp.Spec.CleanupOnDelete = true
		})
		s.logger.Info().Msgf("Deleting ManagedProvider with cleanupOnDelete=true")
		err = s.client.Delete(ctx, &providersv1alpha1.ManagedProvider{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "e2e-managed-provider",
				Name:      "my-managed-provider",
			},
		})
		s.NoError(err, "deleting ManagedProvider should succeed")
		s.logger.Info().Msgf("ManagedProvider deleted, checking that :root:providers:my-managed-provider workspace is deleted")
		s.Eventually(func() bool {
			err = s.client.Get(ctx, types.NamespacedName{
				Namespace: "e2e-managed-provider",
				Name:      "my-managed-provider",
			}, &providersv1alpha1.ManagedProvider{})
			return kerrors.IsNotFound(err)
		}, 240*time.Second, 5*time.Second, "waiting for ManagedProvider to be deleted, but has err=%v", err)
		providersScopedAdminCfg := rest.CopyConfig(kcpAdminCfg)
		providersScopedAdminCfg.Host += "/clusters/root:providers"
		providersScopedAdminClient, err := client.New(providersScopedAdminCfg, client.Options{
			Scheme: s.scheme,
		})
		s.NoError(err, "creating kcp admin client should succeed")
		s.Eventually(func() bool {
			err = providersScopedAdminClient.Get(ctx, types.NamespacedName{
				Name: "my-managed-provider",
			}, &kcptenancyv1alpha.Workspace{})
			return kerrors.IsNotFound(err)
		}, 240*time.Second, 5*time.Second, "waiting for provider's workspace :root:providers:my-managed-provider to be deleted, but has err=%v", err)
	})
}

func waitForManagedProviderAndValidate(ctx context.Context, s *KindTestSuite, providersScopedKcpAdminClient client.Client, patchManagedProviderCreate func(*providersv1alpha1.ManagedProvider)) {
	s.T().Helper()

	managedProviderName := types.NamespacedName{
		Namespace: "e2e-managed-provider",
		Name:      "my-managed-provider",
	}
	managedProvider := providersv1alpha1.ManagedProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      managedProviderName.Name,
			Namespace: managedProviderName.Namespace,
		},
		Spec: providersv1alpha1.ManagedProviderSpec{
			Controller: providersv1alpha1.ProviderComponentSpec{
				OCM: providersv1alpha1.OCMComponentSpec{
					ComponentName: "example-httpbin-operator",
					Registry:      "ghcr.io/platform-mesh/helm-charts",
					Version:       "0.5.14",
				},
			},
		},
	}
	patchManagedProviderCreate(&managedProvider)

	s.logger.Info().Msgf("Creating ManagedProvider %q", managedProvider.Name)
	err := s.client.Create(ctx, &managedProvider)
	s.NoError(err, "creating ManagedProvider should succeed")
	s.logger.Info().Msgf("ManagedProvider %q created", managedProvider.Name)

	s.logger.Info().Msgf("Waiting until ManagedProvider has its Status.KubeconfigSecretRef populated")
	s.Eventually(func() bool {
		err := s.client.Get(ctx, managedProviderName, &managedProvider)
		if err != nil {
			return false
		}
		return managedProvider.Status.KubeconfigSecretRef != nil &&
			managedProvider.Status.KubeconfigSecretRef.Name == "platform-mesh-provider-kubeconfig-my-managed-provider" &&
			managedProvider.Status.KubeconfigSecretRef.Namespace == managedProviderName.Namespace
	}, 240*time.Second, 5*time.Second, "waiting for kubeconfig ref in ManagedProvider")
	s.logger.Info().Msgf("ManagedProvider has its Status.KubeconfigSecretRef populated")

	// At this point the Provider on kcp side should be in Phase=Ready.

	s.logger.Info().Msgf("Validate Provider on kcp side")
	var provider providersv1alpha1.Provider
	err = providersScopedKcpAdminClient.Get(ctx, types.NamespacedName{
		Namespace: "default",
		Name:      "my-managed-provider",
	}, &provider)
	s.NoError(err, "getting Provider with scopedKcpAdminClient should succeed")
	s.Equal("Ready", provider.Status.Phase, "Provider on kcp side should have reached Phase=Ready")
	s.NotNil(provider.Status.KubeconfigSecretRef, "Provider should have its KubeconfigSecretRef populated")
	s.Equal("platform-mesh-provider-kubeconfig-my-managed-provider", provider.Status.KubeconfigSecretRef.Name, "Provider has unexpected kubeconfig secret name")
	s.Equal("default", provider.Status.KubeconfigSecretRef.Namespace, "Provider has unexpected kubeconfig secret namespace")

	// Validate provider kubeconfigs on kcp and kind side.

	s.logger.Info().Msgf("Getting provider kubeconfig secret on kcp side")
	var providerKubeconfigSecretInKcp corev1.Secret
	err = providersScopedKcpAdminClient.Get(ctx, types.NamespacedName{
		Namespace: "default",
		Name:      "platform-mesh-provider-kubeconfig-my-managed-provider",
	}, &providerKubeconfigSecretInKcp)
	s.NoError(err, "getting provider kubeconfig secret using scopedKcpAdminClient should succeed")

	s.logger.Info().Msgf("Getting provider kubeconfig secret on runtime sice")
	var providerKubeconfigSecretInKind corev1.Secret
	s.Eventually(func() bool {
		err = s.client.Get(ctx, types.NamespacedName{
			Namespace: managedProviderName.Namespace,
			Name:      "platform-mesh-provider-kubeconfig-my-managed-provider",
		}, &providerKubeconfigSecretInKind)
		return err == nil
	}, 240*time.Second, 5*time.Second, "waiting for provider kubeconfig secret locally in namespace e2e-managed-provider")

	providerKubeconfig := providerKubeconfigSecretInKcp.Data["kubeconfig"]
	s.NotEmpty(providerKubeconfig, "kubeconfig not set in provider kubeconfig secret")
	s.NotEmpty(providerKubeconfigSecretInKind.Data["kubeconfig"], "provider kubeconfig not copied to runtime cluster")
	s.Equal(providerKubeconfig, providerKubeconfigSecretInKind.Data["kubeconfig"], "kcp and kind provider kubeconfigs differ")

	// List ConfigMaps using the provider kubeconfig just to see if it works.

	providerClient, _, err := createKubernetesClient(providerKubeconfig, s.scheme)
	s.Require().NoError(err, "creating client from provider kubeconfig")

	s.logger.Info().Msgf("Listing ConfigMaps in provider workspace using provider's kubeconfig")
	cmList := &corev1.ConfigMapList{}
	err = providerClient.List(ctx, cmList, client.InNamespace("default"))
	s.NoError(err, "listing ConfigMaps in provider workspace using scoped kubeconfig should succeed")
	s.Greater(len(cmList.Items), 0,
		"listing ConfigMap in provider workspace using scoped kubeconfig should return non-zero items") // Should contain kube-root-ca.crt CM at least.

	// Check that ManagedProvider reaches Deployed phase and that the Deployment exists.

	s.logger.Info().Msgf("Waiting until ManagedProvider reaches Phase=Deployed")
	s.Eventually(func() bool {
		err = s.client.Get(ctx, managedProviderName, &managedProvider)
		if err != nil {
			return false
		}
		return managedProvider.Status.Phase == "Deployed"
	}, 240*time.Second, 5*time.Second, "waiting for ManagedProvider to reach Phase=Deployed, but has err=%q Phase=%q", err, managedProvider.Status.Phase)

	s.logger.Info().Msgf("Waiting until Deployment my-managed-provider-controller-example-httpbin-operator appears")
	s.Eventually(func() bool {
		err = s.client.Get(ctx, types.NamespacedName{
			Namespace: managedProviderName.Namespace,
			Name:      "my-managed-provider-controller-example-httpbin-operator",
		}, &appsv1.Deployment{})
		return err == nil
	}, 240*time.Second, 5*time.Second, "waiting for Deployment my-managed-provider-controller-example-httpbin-operator, but has err=%v", err)
}

func (s *KindTestSuite) runProviderOperator(ctx context.Context) {
	appConfig := config.NewProvidersConfig()
	if err := defaults.Set(&appConfig); err != nil {
		s.logger.Error().Err(err).Msg("Failed to set default Provider operator config")
		return
	}

	appConfig.ProvidersAPIExportEndpointSliceName = "providers.platform-mesh.io"
	appConfig.ProvidersAPIExportEndpointSliceWorkspace = "root:platform-mesh-system"
	appConfig.KCP = defaultKcpOperatorConfig

	commonConfig := &pmconfig.CommonServiceConfig{
		IsLocal: true,
	}

	ctx = context.WithValue(ctx, keys.ConfigCtxKey, appConfig)

	runtimeClient, err := client.New(s.config, client.Options{})
	s.NoError(err, "failed to create kube client for runtime cluster")

	var kcpAdminCfg *rest.Config
	s.Eventually(func() bool {
		kcpAdminCfg, err = pmsubs.BuildKcpAdminConfig(runtimeClient, &appConfig.KCP, appConfig.KCP.Url)
		return err == nil
	}, 240*time.Second, 5*time.Second, "waiting for kcp REST config")

	scopedKcpAdminCfg := rest.CopyConfig(kcpAdminCfg)
	scopedKcpAdminCfg.Host += "/clusters/" + appConfig.ProvidersAPIExportEndpointSliceWorkspace

	providersVW, err := apiexport.New(scopedKcpAdminCfg, appConfig.ProvidersAPIExportEndpointSliceName, apiexport.Options{
		Scheme: s.scheme,
	})
	s.NoError(err, "failed to create APIExport mc provider")

	mgr, err := mcmanager.New(s.config, providersVW, ctrl.Options{
		Scheme:      s.scheme,
		BaseContext: func() context.Context { return ctx },
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	s.NoError(err, "failed to create manager for providers operator")
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to create manager")
		return
	}

	rec, err := providerscontroller.NewProviderReconciler(mgr, &appConfig, commonConfig)
	s.NoError(err, "failed to ProviderReconciler controller")
	s.NoError(rec.SetupWithManager(mgr, commonConfig), "failed to setup ProviderReconciler with manager")

	go func() {
		err := mgr.Start(ctx)
		s.NoError(err, "providers operator should Start")
	}()
	s.logger.Info().Msg("PlatformMesh operator started")
}
