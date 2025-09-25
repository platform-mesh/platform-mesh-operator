package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"time"

	certmanager "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/rs/zerolog/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/suite"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/pkg/kapply"

	fluxcdv2 "github.com/fluxcd/helm-controller/api/v2"
	helmv2beta "github.com/fluxcd/helm-controller/api/v2beta1"
	fluxcdv1 "github.com/fluxcd/source-controller/api/v1beta2"
	pmconfig "github.com/platform-mesh/golang-commons/config"
	"k8s.io/client-go/rest"

	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/internal/controller"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type KindTestSuite struct {
	kubernetesManager ctrl.Manager
	suite.Suite
	client client.Client
	config *rest.Config
	scheme *runtime.Scheme
	logger *logger.Logger

	cancel context.CancelFunc
}

var clusterName = "platform-mesh"

// runCommand executes a shell command and returns its output.
func runCommand(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stderr = os.Stderr

	// Get the current environment
	env := os.Environ()
	// Add or override specific variables
	goos := goruntime.GOOS
	if goos == "darwin" {
		env = append(env, "DOCKER_HOST=unix:///var/run/docker.sock")
	} else {
		env = append(env, "DOCKER_HOST=unix:///run/docker.sock")
	}
	cmd.Env = env

	return cmd.Output()
}

// checkClusterExists checks if a Kind cluster with the given name exists.
func checkClusterExists(clusterName string) (bool, error) {
	output, err := runCommand("kind", "get", "clusters")
	if err != nil {
		return false, fmt.Errorf("failed to get Kind clusters: %w", err)
	}

	if strings.Contains(string(output), clusterName) {
		return true, nil
	}
	return false, nil
}

// createKubernetesClient creates a Kubernetes client from the given kubeconfig.
func createKubernetesClient(kubeconfig []byte, s *runtime.Scheme) (client.Client, *rest.Config, error) {
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create REST config from kubeconfig: %w", err)
	}

	k8sClient, err := client.New(config, client.Options{
		Scheme: s,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return k8sClient, config, nil
}

func (s *KindTestSuite) createLogger() error {
	logConfig := logger.DefaultConfig()
	logConfig.NoJSON = true
	logConfig.Level = "debug"
	logConfig.Name = "KindTestSuite"
	if log, err := logger.New(logConfig); err != nil {
		return err
	} else {
		s.logger = log
		ctrl.SetLogger(s.logger.Logr())
	}
	return nil

}

func (s *KindTestSuite) createKindCluster() error {
	// Check if Kind cluster already exists if not create it
	s.logger.Info().Msg("Checking if Kind cluster exists...")
	var clusterExists bool
	var err error
	if clusterExists, err = checkClusterExists(clusterName); err != nil {
		return err
	}

	if clusterExists {
		s.logger.Info().Msg("Kind cluster already exists, skipping creation")
	} else {
		s.logger.Info().Msg("Creating Kind cluster...")
		if out, err := runCommand("docker", "system", "prune", "-f"); err != nil {
			return errors.Join(err, errors.New(string(out)))
		}

		if out, err := runCommand("docker", "ps", "-a"); err != nil {
			return errors.Join(err, errors.New(string(out)))
		}

		if out, err := runCommand("kind", "--version"); err != nil {
			return errors.Join(err, errors.New(string(out)))
		}

		s.logger.Info().Msg("Creating Kind cluster...")
		if _, err = runCommand("kind", "create", "cluster", "--config", "../../../kind-config.yaml", "--name", clusterName, "--image=kindest/node:v1.30.2"); err != nil {
			return err
		}
	}

	// Get kubeconfig for the Kind cluster
	s.logger.Info().Msg("Retrieving kubeconfig for Kind cluster...")
	var kubeconfig []byte
	if kubeconfig, err = runCommand("kind", "get", "kubeconfig", "--name", clusterName); err != nil {
		return err
	}

	// register scheme
	s.scheme = runtime.NewScheme()
	utilruntime.Must(v1alpha1.AddToScheme(s.scheme))
	utilruntime.Must(helmv2beta.AddToScheme(s.scheme))
	utilruntime.Must(corev1.AddToScheme(s.scheme))
	utilruntime.Must(appsv1.AddToScheme(s.scheme))
	utilruntime.Must(certmanager.AddToScheme(s.scheme))
	utilruntime.Must(fluxcdv1.AddToScheme(s.scheme))
	utilruntime.Must(fluxcdv2.AddToScheme(s.scheme))

	gvk := fluxcdv2.GroupVersion.WithKind("HelmRelease")
	s.logger.Info().Msgf("Registering GVK: %s", gvk.String())

	if _, err = s.scheme.New(gvk); err != nil {
		return err
	}

	// Pass kubeconfig directly to the Kubernetes client
	s.logger.Info().Msg("Creating Kubernetes client using kubeconfig...")
	if cl, configClient, err := createKubernetesClient(kubeconfig, s.scheme); err != nil {
		return err
	} else {
		s.client = cl
		s.config = configClient
	}

	pods := &corev1.PodList{}
	err = s.client.List(context.TODO(), pods, &client.ListOptions{
		Namespace: "kube-system",
	})
	if err != nil {
		return err
	}
	if len(pods.Items) == 0 {
		return errors.New("No pods found in kube-system namespace, this might be an issue")
	}
	return nil
}

func (s *KindTestSuite) createCerts() ([]byte, error) {
	// mkcert
	_, err := runCommand("mkdir", "-p", "certs")
	s.Require().NoError(err, "Error creating certs directory")
	if _, err = runCommand("../../../bin/mkcert", "-cert-file=certs/cert.crt", "-key-file=certs/cert.key", "*.dev.local", "*.portal.dev.local"); err != nil {
		return nil, err
	}
	dirRootPath, err := runCommand("../../../bin/mkcert", "-CAROOT")
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to get mkcert CAROOT")
		return nil, err
	}

	// generate webhook certificates
	if _, err := runCommand("scripts/gen-certs.sh"); err != nil {
		return nil, err
	}

	return dirRootPath, nil
}

func (s *KindTestSuite) createSecrets(ctx context.Context, dirRootPath []byte) error {
	carootPath := fmt.Sprintf("%s/rootCA.pem", strings.TrimSuffix(string(dirRootPath), "\n"))
	var caRootBytes []byte
	var err error
	if caRootBytes, err = os.ReadFile(carootPath); err != nil {
		return err
	}
	certBytes, err := os.ReadFile("certs/cert.crt")
	if err != nil {
		return err
	}
	keyBytes, err := os.ReadFile("certs/cert.key")
	if err != nil {
		return err
	}
	caIamRootCABytes, err := os.ReadFile("webhook-config/ca.crt")
	if err != nil {
		return err
	}
	iamCertBytes, err := os.ReadFile("webhook-config/tls.crt")
	if err != nil {
		return err
	}
	iamKeyBytes, err := os.ReadFile("webhook-config/tls.key")
	if err != nil {
		return err
	}

	s.logger.Debug().Str("gh-token", os.Getenv("GH_TOKEN")).Msg("Using GitHub token for Docker secrets")

	// create docker secrets
	dockerCfg := map[string]interface{}{
		"auths": map[string]interface{}{
			"ghcr.io": map[string]string{
				"username": "platform-mesh-technical-user",
				"password": os.Getenv("GH_TOKEN"),
				"auth":     base64.StdEncoding.EncodeToString([]byte("platform-mesh-technical-user:" + os.Getenv("GH_TOKEN"))),
			},
		},
	}
	jsonBytes, _ := json.Marshal(dockerCfg)

	github := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "github",
			Namespace: "default",
		},
		Data: map[string][]byte{
			".dockerconfigjson": jsonBytes,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
	github_pms := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "github",
			Namespace: "platform-mesh-system",
		},
		Data: map[string][]byte{
			".dockerconfigjson": jsonBytes,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
	keycloak_admin := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "keycloak-admin",
			Namespace: "platform-mesh-system",
		},
		Data: map[string][]byte{
			"secret": []byte("admin"),
		},
		Type: corev1.SecretTypeOpaque,
	}
	ocm := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ocm-oci-github-pull",
			Namespace: "default",
		},
		Data: map[string][]byte{
			".dockerconfigjson": jsonBytes,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
	domain_certificate := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "domain-certificate",
			Namespace: "istio-system",
		},
		Data: map[string][]byte{
			"ca.crt":  caRootBytes,
			"tls.crt": certBytes,
			"tls.key": keyBytes,
		},
		Type: corev1.SecretTypeTLS,
	}
	rbac_webhook_ca := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rebac-authz-webhook-ca",
			Namespace: "platform-mesh-system",
		},
		Data: map[string][]byte{
			"ca.crt":  caIamRootCABytes,
			"tls.crt": iamCertBytes,
			"tls.key": iamKeyBytes,
		},
		Type: corev1.SecretTypeTLS,
	}
	domain_certificate_ca := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "domain-certificate-ca",
			Namespace: "platform-mesh-system",
		},
		Data: map[string][]byte{
			"tls.crt": caRootBytes,
			"tls.key": keyBytes,
		},
		Type: corev1.SecretTypeTLS,
	}
	createIfNotExists := func(obj client.Object) error {
		if err := s.client.Create(ctx, obj); err != nil {
			if k8serrors.IsAlreadyExists(err) {
				return nil
			}
			return err
		}
		return nil
	}

	secrets := []client.Object{
		github,
		github_pms,
		ocm,
		keycloak_admin,
		domain_certificate,
		rbac_webhook_ca,
		domain_certificate_ca,
	}

	for _, sec := range secrets {
		if err := createIfNotExists(sec); err != nil {
			return err
		}
	}
	return nil
}

func (s *KindTestSuite) createReleases(ctx context.Context) error {
	if err := ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/flux2-v2.6.4/flux2-install.yaml", s.client, make(map[string]string)); err != nil {
		return err
	}
	if err := ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/cert-manager/namespace.yaml", s.client, make(map[string]string)); err != nil {
		return err
	}
	if err := ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/cert-manager/repository.yaml", s.client, make(map[string]string)); err != nil {
		return err
	}
	if err := ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/cert-manager/release.yaml", s.client, make(map[string]string)); err != nil {
		return err
	}

	avail := s.Eventually(func() bool {
		helmRelease := fluxcdv2.HelmRelease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cert-manager",
				Namespace: "cert-manager",
			},
		}

		err := s.client.Get(ctx, client.ObjectKeyFromObject(&helmRelease), &helmRelease)
		if err != nil {
			return false
		}

		deployment := &appsv1.Deployment{}

		err = s.client.Get(ctx, client.ObjectKey{
			Name:      "cert-manager-webhook",
			Namespace: "cert-manager",
		}, deployment)
		if err != nil {
			s.logger.Warn().Msg("Not getting cert-manager-webhook deployment")
			return false
		}
		certManagerWebhookReady := (deployment.Status.ReadyReplicas > 0)

		err = s.client.Get(ctx, client.ObjectKey{
			Name:      "cert-manager",
			Namespace: "cert-manager",
		}, deployment)
		if err != nil {
			s.logger.Warn().Msg("Not getting cert-manager deployment")
			return false
		}
		certManagerReady := (deployment.Status.ReadyReplicas > 0)

		err = s.client.Get(ctx, client.ObjectKey{
			Name:      "helm-controller",
			Namespace: "flux-system",
		}, deployment)
		if err != nil {
			s.logger.Warn().Msg("Not getting helm-controller deployment")
			return false
		}
		helmControllerReady := (deployment.Status.ReadyReplicas > 0)

		err = s.client.Get(ctx, client.ObjectKey{
			Name:      "source-controller",
			Namespace: "flux-system",
		}, deployment)
		if err != nil {
			s.logger.Warn().Msg("Not getting source-controller deployment")
			return false
		}
		sourceControllerReady := (deployment.Status.ReadyReplicas > 0)

		return certManagerWebhookReady && certManagerReady && helmControllerReady && sourceControllerReady
	}, 180*time.Second, 5*time.Second, "cert-manager helmrelease did not become ready")

	if !avail {
		return errors.New("cert-manager helmrelease is not available")
	}

	s.logger.Info().Msg("cert-manager, fluxcd helmreleases ready")

	if err := ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/kyverno/kyverno-namespace.yaml", s.client, make(map[string]string)); err != nil {
		return err
	}

	if err := ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/kyverno/helmrepository.yaml", s.client, make(map[string]string)); err != nil {
		return err
	}

	if err := ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/kyverno/helmrelease.yaml", s.client, make(map[string]string)); err != nil {
		return err
	}

	if err := ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/virtual-workspaces/vws-cert.yaml", s.client, make(map[string]string)); err != nil {
		return err
	}

	ok := s.Eventually(func() bool {
		deployment := &appsv1.Deployment{}
		err := s.client.Get(ctx, client.ObjectKey{Name: "kyverno-admission-controller", Namespace: "kyverno-system"}, deployment)
		if err != nil {
			return false
		}
		if deployment.Status.ReadyReplicas < 1 {
			return false
		}
		err = s.client.Get(ctx, client.ObjectKey{Name: "kyverno-background-controller", Namespace: "kyverno-system"}, deployment)
		if err != nil {
			return false
		}
		if deployment.Status.ReadyReplicas < 1 {
			return false
		}
		if deployment.Status.ReadyReplicas < 1 {
			return false
		}

		return true
	}, 120*time.Second, 2*time.Second, "kyverno deployments not ready")
	if !ok {
		return errors.New("kyverno deployments not ready")
	}

	time.Sleep(25 * time.Second)

	return ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/istio-gateway/istio-gateway.yaml", s.client, make(map[string]string))
}

// SetupSuite sets up the Kind cluster and deploys the operator for testing.
func (s *KindTestSuite) SetupSuite() {
	ctx := context.Background()
	var err error

	if err = s.createLogger(); err != nil {
		s.logger.Error().Err(err).Msg("Failed to create logger")
		s.T().FailNow()
	}
	if err = s.createKindCluster(); err != nil {
		s.logger.Error().Err(err).Msg("Failed to create Kind cluster")
		s.T().FailNow()
	}
	if err = s.InstallCRDs(ctx); err != nil {
		s.logger.Error().Err(err).Msg("Failed to install CRDs")
		s.T().FailNow()
	}
	var dirRootPath []byte
	if dirRootPath, err = s.createCerts(); err != nil {
		s.logger.Error().Err(err).Msg("Failed to create certificates")
		s.T().FailNow()
	}
	if err = ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/namespaces.yaml", s.client, make(map[string]string)); err != nil {
		s.logger.Error().Err(err).Msg("Failed to apply namespaces.yaml manifest")
		s.T().FailNow()
	}
	if err = s.createSecrets(ctx, dirRootPath); err != nil {
		s.logger.Error().Err(err).Msg("Failed to create secrets")
		s.T().FailNow()
	}
	if err = s.createReleases(ctx); err != nil {
		s.logger.Error().Err(err).Msg("Failed to create releases")
		s.T().FailNow()
	}
	if err = s.applyKustomize(ctx); err != nil {
		s.FailNow("Failed to apply kustomize manifests")
	}
	if err = s.applyOCM(ctx); err != nil {
		s.FailNow("Failed to apply OCM manifests")
	}

	// add Platform Mesh resource
	if err = ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/openmfp-resource/platform-mesh.yaml", s.client, make(map[string]string)); err != nil {
		s.FailNow("Failed to apply PlatformMesh resource manifest", err)
	}

	avail := s.Eventually(func() bool {
		pm := v1alpha1.PlatformMesh{}
		err := s.client.Get(ctx, client.ObjectKey{
			Name:      "platform-mesh",
			Namespace: "platform-mesh-system",
		}, &pm)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to get PlatformMesh resource")
			return false
		}
		return true
	}, 15*time.Second, 2*time.Second, "PlatformMesh resource did not become available")

	if !avail {
		s.logger.Error().Msg("PlatformMesh resource is not available")
		s.T().FailNow()
	}

	// Run the PlatformMesh operator
	s.logger.Info().Msg("starting operator...")
	s.runOperator(ctx)

}

// applyOCM applies the OCM component and repository to the cluster.
func (s *KindTestSuite) applyOCM(ctx context.Context) error {
	clients, err := kapply.NewClients(s.config)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to create kapply clients")
		return err
	}

	return kapply.ApplyDir(ctx, "../../../test/e2e/kind/kustomize/components/ocm", clients)
}

func (s *KindTestSuite) applyKustomize(ctx context.Context) error {

	clients, err := kapply.NewClients(s.config)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to create kapply clients")
		return err
	}

	err = kapply.ApplyDir(ctx, "../../../test/e2e/kind/kustomize/base", clients)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to apply base kustomize manifests")
		return err
	}

	err = kapply.ApplyDir(ctx, "../../../test/e2e/kind/kustomize/components/policies", clients)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to apply components/policies kustomize manifests")
		return err
	}

	policyNames := []string{
		"git-repos",
		"helm-releases",
		"helm-repos",
		"oci-repos",
	}
	res := s.Eventually(func() bool {
		for _, policyName := range policyNames {
			clusterPolicy := &unstructured.Unstructured{}
			clusterPolicy.SetGroupVersionKind(schema.GroupVersionKind{Group: "kyverno.io", Version: "v1", Kind: "ClusterPolicy"})
			// Wait for root shard to be ready
			err = s.client.Get(ctx, types.NamespacedName{Name: policyName}, clusterPolicy)
			if err != nil || !subroutines.MatchesCondition(clusterPolicy, "Ready") {
				log.Info().Msg("ClusterPolicy is not ready.. Retry in 5 seconds")
				return false
			}
		}
		return true
	}, 180*time.Second, 5*time.Second, "policies not ready")
	if !res {
		return fmt.Errorf("policies are not ready")
	}
	s.logger.Info().Msg("All kyverno policies are ready")

	s.logger.Info().Msg("kapply finished successfully")
	return nil
}

func (s *KindTestSuite) TearDownSuite() {
}

func (s *KindTestSuite) InstallCRDs(ctx context.Context) error {
	err := ApplyManifestFromFile(ctx, "../../../config/crd/core.platform-mesh.io_platformmeshes.yaml", s.client, make(map[string]string))
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to apply PlatformMesh CRD manifest")
		return err
	}

	// Add more CRD installations here if needed
	return nil
}

func (s *KindTestSuite) runOperator(ctx context.Context) {

	appConfig := config.OperatorConfig{}
	appConfig.Subroutines.Deployment.Enabled = true
	appConfig.Subroutines.KcpSetup.Enabled = true
	appConfig.Subroutines.ProviderSecret.Enabled = true
	appConfig.WorkspaceDir = "../../../"
	appConfig.KCP.Url = "https://kcp.api.portal.dev.local:8443"
	appConfig.KCP.RootShardName = "root"
	appConfig.KCP.Namespace = "platform-mesh-system"
	appConfig.KCP.FrontProxyName = "frontproxy"
	appConfig.KCP.FrontProxyPort = "6443"
	appConfig.KCP.ClusterAdminSecretName = "kcp-cluster-admin-client-cert"

	commonConfig := &pmconfig.CommonServiceConfig{}
	commonConfig.IsLocal = true

	ctx = context.WithValue(ctx, keys.ConfigCtxKey, appConfig)

	options := ctrl.Options{
		Scheme:      s.scheme,
		BaseContext: func() context.Context { return ctx },
	}
	mgr, err := ctrl.NewManager(s.config, options)

	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to create manager")
		return
	}

	s.kubernetesManager = mgr

	pmReconciler := controller.NewPlatformMeshReconciler(s.logger, s.kubernetesManager, &appConfig, commonConfig, "../../../")
	err = pmReconciler.SetupWithManager(s.kubernetesManager, commonConfig, s.logger)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to setup PlatformMesh reconciler with manager")
		return
	}

	go s.startController()
	s.logger.Info().Msg("PlatformMesh operator started")
}

func (suite *KindTestSuite) startController() {
	var controllerContext context.Context
	controllerContext, suite.cancel = context.WithCancel(context.Background())
	err := suite.kubernetesManager.Start(controllerContext)
	suite.Nil(err)
}
