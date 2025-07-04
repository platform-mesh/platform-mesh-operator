package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	goruntime "runtime"

	certmanager "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openmfp/golang-commons/logger"
	"github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/stretchr/testify/suite"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	helmv2beta "github.com/fluxcd/helm-controller/api/v2beta1"
	fluxcdv1 "github.com/fluxcd/source-controller/api/v1beta2"
	openmfpconfig "github.com/openmfp/golang-commons/config"
	"github.com/openmfp/openmfp-operator/internal/config"
	"github.com/openmfp/openmfp-operator/internal/controller"
	"k8s.io/client-go/rest"
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

var clusterName = "openmfp"

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

func (s *KindTestSuite) CreateLogger() {
	logConfig := logger.DefaultConfig()
	logConfig.NoJSON = true
	logConfig.Level = "debug"
	logConfig.Name = "KindTestSuite"
	log, err := logger.New(logConfig)
	if err != nil {
		println("Error creating logger:", err)
		os.Exit(1)
	}
	s.logger = log
	ctrl.SetLogger(s.logger.Logr())

}

func (s *KindTestSuite) CreateKindCluster() {
	// Check if Kind cluster already exists if not create it
	s.logger.Info().Msg("Checking if Kind cluster exists...")
	clusterExists, err := checkClusterExists(clusterName)
	s.Require().NoError(err, "Error checking Kind cluster existence")

	if clusterExists {
		s.logger.Info().Msg("Kind cluster already exists, using existing cluster...")
	} else {
		out, err := runCommand("docker", "system", "prune", "-f")
		if err != nil {
			s.logger.Error().Err(err).Msg("Error pruning Docker system")
			s.T().FailNow()
		}
		s.logger.Info().Str("out", string(out)).Msg("Pruning Docker system...")
		out, err = runCommand("docker", "ps", "-a")
		if err != nil {
			s.logger.Error().Err(err).Msg("Error listing Docker containers")
			s.T().FailNow()
		}
		s.logger.Info().Str("out", string(out)).Msg("Listing Docker containers...")
		out, err = runCommand("kind", "--version")
		if err != nil {
			pwd, errOs := os.Getwd()
			s.logger.Error().Err(err).Err(errOs).Str("pwd", pwd).Msg("KIND version error")
			s.T().FailNow()

		}
		s.logger.Info().Str("out", string(out)).Msg("KIND  version")

		s.logger.Info().Msg("Creating Kind cluster...")
		_, err = runCommand("kind", "create", "cluster", "--config", "../../../kind-config.yaml", "--name", clusterName, "--image=kindest/node:v1.30.2")
		s.Require().NoError(err, "Error creating Kind cluster")
		if err != nil {
			s.logger.Error().Err(err).Msg("Failed to create Kind cluster")
			s.T().FailNow()
		}
	}

	// Get kubeconfig for the Kind cluster
	s.logger.Info().Msg("Retrieving kubeconfig for Kind cluster...")
	kubeconfig, err := runCommand("kind", "get", "kubeconfig", "--name", clusterName)
	s.Require().NoError(err, "Error retrieving kubeconfig")

	// register scheme
	s.scheme = k8sruntime.NewScheme()
	utilruntime.Must(v1alpha1.AddToScheme(s.scheme))
	utilruntime.Must(helmv2.AddToScheme(s.scheme))
	utilruntime.Must(helmv2beta.AddToScheme(s.scheme))
	utilruntime.Must(corev1.AddToScheme(s.scheme))
	utilruntime.Must(appsv1.AddToScheme(s.scheme))
	utilruntime.Must(certmanager.AddToScheme(s.scheme))
	utilruntime.Must(fluxcdv1.AddToScheme(s.scheme))

	gvk := helmv2.GroupVersion.WithKind("HelmRelease")
	s.logger.Info().Msgf("Registering GVK: %s", gvk.String())

	_, err = s.scheme.New(gvk)
	s.Require().NoError(err, "HelmRelease GVK not registered in scheme")

	for gvk := range s.scheme.AllKnownTypes() {
		if gvk.Kind == "HelmRelease" {
			s.logger.Info().Msgf("Found HelmRelease GVK: %s", gvk.String())
		}
	}

	// Pass kubeconfig directly to the Kubernetes client
	s.logger.Info().Msg("Creating Kubernetes client using kubeconfig...")
	cl, configClient, err := createKubernetesClient(kubeconfig, s.scheme)
	s.Require().NoError(err, "Error creating Kubernetes client")
	s.client = cl
	s.config = configClient

	pods := &corev1.PodList{}
	err = s.client.List(context.TODO(), pods, &client.ListOptions{
		Namespace: "kube-system",
	})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to list pods in openmfp-system namespace")
		s.T().FailNow()
	}
	if len(pods.Items) == 0 {
		s.logger.Error().Msg("No pods found in kube-system namespace, this might be an issue")
		s.T().FailNow()
	}
	s.logger.Info().Msgf("Found %d pods in openmfp-system namespace", len(pods.Items))
}

func (s *KindTestSuite) CreateCerts() []byte {
	// mkcert
	_, err := runCommand("mkdir", "-p", "certs")
	s.Require().NoError(err, "Error creating certs directory")
	_, err = runCommand("mkcert", "-cert-file=certs/cert.crt", "-key-file=certs/cert.key", "*.dev.local", "*.portal.dev.local")
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate certificates with mkcert")
		s.T().FailNow()
	}
	dirRootPath, err := runCommand("mkcert", "-CAROOT")
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to get mkcert CAROOT")
		s.T().FailNow()
	}

	// generate webhook certificates
	outGenCerts, err := runCommand("scripts/gen-certs.sh")
	s.Require().NoError(err, "Error generating webhook certificates")
	if err != nil {
		s.logger.Error().Err(err).Str("output", string(outGenCerts)).Msg("Failed to generate webhook certificates")
		s.T().FailNow()
		return []byte{}
	}

	return dirRootPath
}

func (s *KindTestSuite) CreateSecrets(ctx context.Context, dirRootPath []byte) {
	carootPath := fmt.Sprintf("%s/rootCA.pem", strings.TrimSuffix(string(dirRootPath), "\n"))
	caRootBytes, err := os.ReadFile(carootPath)
	s.Require().NoError(err, "Error reading mkcert root CA file")
	certBytes, err := os.ReadFile("certs/cert.crt")
	s.Require().NoError(err, "Error reading mkcert cert file")
	keyBytes, err := os.ReadFile("certs/cert.key")
	s.Require().NoError(err, "Error reading mkcert key file")

	caIamRootCABytes, err := os.ReadFile("webhook-config/ca.crt")
	s.Require().NoError(err, "Error reading webhook ca.crt file")
	iamCertBytes, err := os.ReadFile("webhook-config/tls.crt")
	s.Require().NoError(err, "Error reading webhook tls.crt file")
	iamKeyBytes, err := os.ReadFile("webhook-config/tls.key")
	s.Require().NoError(err, "Error reading webhook tls.key file")

	s.logger.Debug().Str("gh-token", os.Getenv("GH_TOKEN")).Msg("Using GitHub token for Docker secrets")

	// create docker secrets
	dockerCfg := map[string]interface{}{
		"auths": map[string]interface{}{
			"ghcr.io/openmfp": map[string]string{
				"auth": base64.StdEncoding.EncodeToString([]byte("openmfp-technical-user:" + os.Getenv("GH_TOKEN"))),
			},
		},
	}
	jsonBytes, _ := json.Marshal(dockerCfg)

	secretGithub := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "github",
			Namespace: "default",
		},
		Data: map[string][]byte{
			".dockerconfigjson": jsonBytes,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
	secretGithub2 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "github",
			Namespace: "openmfp-system",
		},
		Data: map[string][]byte{
			".dockerconfigjson": jsonBytes,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
	secretKeycloakAdmin := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "keycloak-admin",
			Namespace: "openmfp-system",
		},
		Data: map[string][]byte{
			"secret": []byte("admin"),
		},
		Type: corev1.SecretTypeOpaque,
	}
	secretOcm := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ocm-oci-github-pull",
			Namespace: "default",
		},
		Data: map[string][]byte{
			".dockerconfigjson": jsonBytes,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
	domainCertSecret := &corev1.Secret{
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

	iamWebhookSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "iam-authorization-webhook-webhook-ca",
			Namespace: "openmfp-system",
		},
		Data: map[string][]byte{
			"ca.crt":  caIamRootCABytes,
			"tls.crt": iamCertBytes,
			"tls.key": iamKeyBytes,
		},
		Type: corev1.SecretTypeTLS,
	}

	err = s.client.Create(ctx, secretGithub)
	if err != nil {
		if !s.Assert().EqualError(err, "secrets \"github\" already exists") {
			s.FailNow("unexpected error")
		}
	}
	err = s.client.Create(ctx, secretGithub2)
	if err != nil {
		if !s.Assert().EqualError(err, "secrets \"github\" already exists") {
			s.FailNow("unexpected error")
		}
	}
	err = s.client.Create(ctx, secretOcm)
	if err != nil {
		if !s.Assert().EqualError(err, "secrets \"ocm-oci-github-pull\" already exists") {
			s.FailNow("unexpected error")
		}
	}
	err = s.client.Create(ctx, secretKeycloakAdmin)
	if err != nil {
		if !s.Assert().EqualError(err, "secrets \"keycloak-admin\" already exists") {
			s.FailNow("unexpected error")
		}
	}
	err = s.client.Create(ctx, domainCertSecret)
	if err != nil {
		if !s.Assert().EqualError(err, "secrets \"domain-certificate\" already exists") {
			s.FailNow("unexpected error")
		}
	}
	err = s.client.Create(ctx, iamWebhookSecret)
	if err != nil {
		if !s.Assert().EqualError(err, "secrets \"iam-authorization-webhook-webhook-ca\" already exists") {
			s.FailNow("unexpected error")
		}
	}

}

func (s *KindTestSuite) CreateNamespaces(ctx context.Context) {
	// create namespaces
	err := ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/namespaces.yaml", s.client, make(map[string]string))
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to apply namespaces.yaml manifest")
		s.T().FailNow()
	}
}

func (s *KindTestSuite) CreateReleases(ctx context.Context) {
	// install flux2 controllers
	err := ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/flux2/kustomizebuild/install.yaml", s.client, make(map[string]string))
	s.Assert().NoError(err, "Error applying flux2 labels manifest")

	// install cert-manager
	err = ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/cert-manager/namespace.yaml", s.client, make(map[string]string))
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to apply cert-manager namespace manifest")
		s.T().FailNow()
	}
	err = ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/cert-manager/repository.yaml", s.client, make(map[string]string))
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to apply cert-manager repository manifest")
		s.T().FailNow()
	}
	err = ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/cert-manager/release.yaml", s.client, make(map[string]string))
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to apply cert-manager release manifest")
		s.T().FailNow()
	}

	time.Sleep(10 * time.Second) // Wait for HelmRelease to be created

	avail := s.Eventually(func() bool {
		helmRelease := helmv2.HelmRelease{
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
		s.logger.Error().Msg("cert-manager HelmRelease is not available")
		s.T().FailNow()
	}

	s.logger.Info().Msg("cert-manager, fluxcd helmreleases ready")

	err = ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/ocm-controller/certificates.yaml", s.client, make(map[string]string))
	s.Assert().NoError(err, "Error applying ocm-controller/certificates.yaml manifest")
	err = ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/ocm-controller/ocm-controller.yaml", s.client, make(map[string]string))
	s.Assert().NoError(err, "Error applying ocm-controller/ocm-controller.yaml manifest")

	avail = s.Eventually(func() bool {
		deployment := &appsv1.Deployment{}

		err = s.client.Get(ctx, client.ObjectKey{
			Name:      "ocm-controller",
			Namespace: "ocm-system",
		}, deployment)
		if err != nil {
			s.logger.Warn().Msg("Not getting ocm-controller deployment")
			return false
		}
		ocmControllerReady := (deployment.Status.ReadyReplicas > 0)

		err = s.client.Get(ctx, client.ObjectKey{
			Name:      "registry",
			Namespace: "ocm-system",
		}, deployment)
		if err != nil {
			s.logger.Warn().Msg("Not getting registry deployment")
			return false
		}
		registryReady := (deployment.Status.ReadyReplicas > 0)

		return ocmControllerReady && registryReady
	}, 180*time.Second, 5*time.Second, "ocm-controller and registry deployments did not become ready")

	if !avail {
		s.logger.Error().Msg("ocm-controller and registry deployments are not available")
		s.T().FailNow()
	}

	s.logger.Info().Msg("ocm-controller deployments ready")

	time.Sleep(10 * time.Second) // Wait for HelmRelease to be created

	// Install the OpenMFP operator CRDs
	err = ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/openmfp-operator-crds/repository.yaml", s.client, make(map[string]string))
	s.Assert().NoError(err, "Error applying openmfp-operator-crds/repository.yaml manifest")
	err = ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/openmfp-operator-crds/release.yaml", s.client, make(map[string]string))
	s.Assert().NoError(err, "Error applying openmfp-operator-crds/release.yaml manifest")

	time.Sleep(10 * time.Second) // Wait for HelmRelease to be created

	avail = s.Eventually(func() bool {
		helmRelease := helmv2.HelmRelease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "openmfp-operator-crds",
				Namespace: "default",
			},
		}
		err := s.client.Get(ctx, client.ObjectKeyFromObject(&helmRelease), &helmRelease)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Not getting openmfp-operator-crds HelmRelease")
			return false
		}
		for _, cond := range helmRelease.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				s.logger.Info().Msg("openmfp-operator-crds HelmRelease ready")
				return true
			} else {
				s.logger.Debug().Any("helmRelease", helmRelease).Msgf("openmfp-operator-crds HelmRelease not ready yet")
			}
		}
		return false
	}, 240*time.Second, 10*time.Second, "openmfp-operator-crds HelmRelease did not become ready")

	if !avail {
		s.logger.Error().Msg("openmfp-operator-crds HelmRelease is not available")
		s.T().FailNow()
	}

}

// SetupSuite sets up the Kind cluster and deploys the operator for testing.
func (s *KindTestSuite) SetupSuite() {
	ctx := context.Background()

	s.CreateLogger()
	s.CreateKindCluster()
	dirRootPath := s.CreateCerts()
	s.CreateNamespaces(ctx)
	s.CreateSecrets(ctx, dirRootPath)
	s.CreateReleases(ctx)

	// add OpenMFP resource
	err := ApplyManifestFromFile(ctx, "../../../test/e2e/kind/yaml/openmfp-resource/openmfp.yaml", s.client, make(map[string]string))
	s.Assert().NoError(err, "Error applying openmfp-resource/openmfp.yaml manifest")
	if err != nil {
		s.FailNow("Failed to apply OpenMFP resource manifest")
	} else {
		s.logger.Info().Msg("Added OpenMFP resource")
	}

	avail := s.Eventually(func() bool {
		openmfp := v1alpha1.OpenMFP{}
		err := s.client.Get(ctx, client.ObjectKey{
			Name:      "openmfp",
			Namespace: "openmfp-system",
		}, &openmfp)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to get OpenMFP resource")
			return false
		}
		return true
	}, 15*time.Second, 2*time.Second, "OpenMFP resource did not become available")

	if !avail {
		s.logger.Error().Msg("OpenMFP resource is not available")
		s.T().FailNow()
	}

	s.logger.Info().Msg("OpenMFP resource is available")

	// Run the OpenMFP operator
	s.logger.Info().Msg("starting operator ...")
	s.runOperator(ctx)

}

func (s *KindTestSuite) TearDownSuite() {
}

func (s *KindTestSuite) runOperator(ctx context.Context) {

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

	appConfig := config.OperatorConfig{}
	appConfig.Subroutines.Deployment.Enabled = true
	appConfig.Subroutines.KcpSetup.Enabled = true
	appConfig.Subroutines.ProviderSecret.Enabled = true
	appConfig.WorkspaceDir = "../../../"
	appConfig.KCPUrl = "https://kcp.api.portal.dev.local:8443"

	commonConfig := &openmfpconfig.CommonServiceConfig{}
	commonConfig.IsLocal = true

	openmfpReconciler := controller.NewOpenmfpReconciler(s.logger, s.kubernetesManager, &appConfig, commonConfig, "../../../")
	err = openmfpReconciler.SetupWithManager(s.kubernetesManager, commonConfig, s.logger)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to setup OpenMFP reconciler with manager")
		return
	}

	go s.startController()
	s.logger.Info().Msg("OpenMFP operator started")
}

func (suite *KindTestSuite) startController() {
	var controllerContext context.Context
	controllerContext, suite.cancel = context.WithCancel(context.Background())
	err := suite.kubernetesManager.Start(controllerContext)
	suite.Nil(err)
}
