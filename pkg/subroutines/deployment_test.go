package subroutines

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
)

type DeploymentProcessTestSuite struct {
	suite.Suite
	scheme *runtime.Scheme
	log    *logger.Logger
	tmpDir string
}

func TestDeploymentProcessTestSuite(t *testing.T) {
	suite.Run(t, new(DeploymentProcessTestSuite))
}

func (s *DeploymentProcessTestSuite) SetupSuite() {
	s.scheme = runtime.NewScheme()
	s.Require().NoError(clientgoscheme.AddToScheme(s.scheme))
	s.Require().NoError(corev1alpha1.AddToScheme(s.scheme))
	logCfg := logger.DefaultConfig()
	logCfg.Level = "debug"
	logCfg.NoJSON = true
	logCfg.Name = "DeploymentProcessTest"
	s.log, _ = logger.New(logCfg)
}

func (s *DeploymentProcessTestSuite) SetupTest() {
	s.tmpDir = s.T().TempDir()
	s.setupGotemplates()
}

// setupGotemplates creates a minimal gotemplates directory structure with simple
// templates that render valid YAML objects the fake client can handle.
func (s *DeploymentProcessTestSuite) setupGotemplates() {
	dirs := []string{
		"gotemplates/infra/infra/cert-manager",
		"gotemplates/infra/runtime/cert-manager",
		"gotemplates/components/infra",
		"gotemplates/components/runtime",
		"manifests/k8s/rebac-auth-webhook",
	}
	for _, d := range dirs {
		s.Require().NoError(os.MkdirAll(filepath.Join(s.tmpDir, d), 0o755))
	}

	// Infra template: renders a ConfigMap (FluxCD path — file starts with "helmrelease")
	infraHelmRelease := `{{- if .certManager.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: cert-manager-rendered
  namespace: {{ .helmReleaseNamespace }}
data:
  rendered: "true"
{{- end }}
`
	s.writeFile("gotemplates/infra/infra/cert-manager/helmrelease.yaml", infraHelmRelease)

	// ArgoCD template for infra (file starts with "application")
	infraArgoApp := `{{- if .certManager.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: cert-manager-argo-rendered
  namespace: {{ .helmReleaseNamespace }}
data:
  rendered: "true"
{{- end }}
`
	s.writeFile("gotemplates/infra/infra/cert-manager/application.yaml", infraArgoApp)

	// Runtime template: renders an OCM Resource-like ConfigMap
	runtimeTemplate := `{{- if .certManager.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: cert-manager-runtime-rendered
  namespace: {{ .releaseNamespace }}
data:
  rendered: "true"
{{- end }}
`
	s.writeFile("gotemplates/infra/runtime/cert-manager/resource.yaml", runtimeTemplate)

	// Components infra template (empty conditional to avoid errors)
	componentsInfra := `{{- if .values }}
{{- end }}
`
	s.writeFile("gotemplates/components/infra/helmreleases.yaml", componentsInfra)

	// Components runtime template (empty conditional)
	componentsRuntime := `{{- if .values }}
{{- end }}
`
	s.writeFile("gotemplates/components/runtime/ocm-chart-resources.yaml", componentsRuntime)

	// Webhook manifests required by manageAuthorizationWebhookSecrets
	caIssuer := `apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: rebac-authz-webhook-issuer
  namespace: {{ .namespace }}
spec:
  selfSigned: {}
`
	s.writeFile("manifests/k8s/rebac-auth-webhook/ca-issuer.yaml", caIssuer)

	webhookCert := `apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: rebac-authz-webhook-cert
  namespace: {{ .namespace }}
spec:
  secretName: rebac-authz-webhook-cert
  issuerRef:
    name: rebac-authz-webhook-issuer
`
	s.writeFile("manifests/k8s/rebac-auth-webhook/webhook-cert.yaml", webhookCert)

	kcpWebhookSecret := `apiVersion: v1
kind: Secret
metadata:
  name: kcp-webhook-secret
  namespace: {{ .namespace }}
type: Opaque
stringData:
  kubeconfig: "placeholder"
`
	s.writeFile("manifests/k8s/rebac-auth-webhook/kcp-webhook-secret.yaml", kcpWebhookSecret)
}

func (s *DeploymentProcessTestSuite) writeFile(relPath, content string) {
	fullPath := filepath.Join(s.tmpDir, relPath)
	s.Require().NoError(os.WriteFile(fullPath, []byte(content), 0o644))
}

func (s *DeploymentProcessTestSuite) newContext(operatorCfg config.OperatorConfig) context.Context {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg)
	return ctx
}

func (s *DeploymentProcessTestSuite) newOperatorConfig() config.OperatorConfig {
	return config.OperatorConfig{
		WorkspaceDir: s.tmpDir,
		KCP: config.KCPConfig{
			Namespace:      "platform-mesh-system",
			RootShardName:  "root",
			FrontProxyName: "frontproxy",
			FrontProxyPort: "8443",
		},
		Subroutines: config.SubroutinesConfig{
			Deployment: config.DeploymentSubroutineConfig{
				Enabled:     true,
				EnableIstio: false,
			},
		},
	}
}

const testProfileFluxCD = `
infra:
  deploymentTechnology: fluxcd
  certManager:
    enabled: true
    name: cert-manager
    targetNamespace: cert-manager
components:
  services: {}
`

const testProfileArgoCD = `
infra:
  deploymentTechnology: argocd
  certManager:
    enabled: true
    name: cert-manager
    targetNamespace: cert-manager
components:
  services: {}
`

func (s *DeploymentProcessTestSuite) newFluxCDReadyCertManager(namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "helm.toolkit.fluxcd.io", Version: "v2", Kind: "HelmRelease"})
	obj.SetName("cert-manager")
	obj.SetNamespace(namespace)
	_ = unstructured.SetNestedSlice(obj.Object, []interface{}{
		map[string]interface{}{"type": "Ready", "status": "True"},
	}, "status", "conditions")
	return obj
}

func (s *DeploymentProcessTestSuite) newArgoCDReadyCertManager(namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"})
	obj.SetName("cert-manager")
	obj.SetNamespace(namespace)
	_ = unstructured.SetNestedField(obj.Object, "Synced", "status", "sync", "status")
	_ = unstructured.SetNestedField(obj.Object, "Healthy", "status", "health", "status")
	return obj
}

func (s *DeploymentProcessTestSuite) newReadyRootShard(namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "RootShard"})
	obj.SetName("root")
	obj.SetNamespace(namespace)
	_ = unstructured.SetNestedSlice(obj.Object, []interface{}{
		map[string]interface{}{"type": "Available", "status": "True"},
	}, "status", "conditions")
	return obj
}

func (s *DeploymentProcessTestSuite) newReadyFrontProxy(namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "FrontProxy"})
	obj.SetName("frontproxy")
	obj.SetNamespace(namespace)
	_ = unstructured.SetNestedSlice(obj.Object, []interface{}{
		map[string]interface{}{"type": "Available", "status": "True"},
	}, "status", "conditions")
	return obj
}

func (s *DeploymentProcessTestSuite) newEstablishedCRD(name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"})
	obj.SetName(name)
	_ = unstructured.SetNestedSlice(obj.Object, []interface{}{
		map[string]interface{}{"type": "Established", "status": "True"},
	}, "status", "conditions")
	return obj
}

func (s *DeploymentProcessTestSuite) seedCertManagerCRDs(ctx context.Context, cl client.Client) {
	s.Require().NoError(cl.Create(ctx, s.newEstablishedCRD("issuers.cert-manager.io")))
	s.Require().NoError(cl.Create(ctx, s.newEstablishedCRD("certificates.cert-manager.io")))
}

func (s *DeploymentProcessTestSuite) Test_Process_FluxCD_HappyPath() {
	ns := "platform-mesh-system"
	operatorCfg := s.newOperatorConfig()
	ctx := s.newContext(operatorCfg)

	inst := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh", Namespace: ns},
		Spec: corev1alpha1.PlatformMeshSpec{
			Exposure: &corev1alpha1.ExposureConfig{
				BaseDomain: "localhost",
				Port:       8443,
				Protocol:   "https",
			},
		},
	}

	profileCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh-profile", Namespace: ns},
		Data:       map[string]string{profileConfigMapKey: testProfileFluxCD},
	}

	cl := fake.NewClientBuilder().
		WithScheme(s.scheme).
		WithObjects(inst, profileCM).
		WithStatusSubresource(inst).
		Build()

	// Pre-create the unstructured resources the fake client needs to return on Get
	s.Require().NoError(cl.Create(ctx, s.newFluxCDReadyCertManager(ns)))
	s.Require().NoError(cl.Create(ctx, s.newReadyRootShard(ns)))
	s.Require().NoError(cl.Create(ctx, s.newReadyFrontProxy(ns)))
	s.seedCertManagerCRDs(ctx, cl)

	sub := &DeploymentSubroutine{
		clientRuntime:            cl,
		clientInfra:              cl,
		cfg:                      &pmconfig.CommonServiceConfig{IsLocal: true},
		cfgOperator:              &operatorCfg,
		gotemplatesInfraDir:      filepath.Join(s.tmpDir, "gotemplates/infra"),
		gotemplatesComponentsDir: filepath.Join(s.tmpDir, "gotemplates/components"),
		workspaceDirectory:       filepath.Join(s.tmpDir, "manifests/k8s"),
	}

	result, err := sub.Process(ctx, inst)

	s.NoError(err)
	s.True(result.IsContinue(), "expected OK/continue result, got stop")
}

func (s *DeploymentProcessTestSuite) Test_Process_ArgoCD_HappyPath() {
	ns := "platform-mesh-system"
	operatorCfg := s.newOperatorConfig()
	ctx := s.newContext(operatorCfg)

	inst := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh", Namespace: ns},
		Spec: corev1alpha1.PlatformMeshSpec{
			Exposure: &corev1alpha1.ExposureConfig{
				BaseDomain: "localhost",
				Port:       8443,
				Protocol:   "https",
			},
		},
	}

	profileCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh-profile", Namespace: ns},
		Data:       map[string]string{profileConfigMapKey: testProfileArgoCD},
	}

	cl := fake.NewClientBuilder().
		WithScheme(s.scheme).
		WithObjects(inst, profileCM).
		WithStatusSubresource(inst).
		Build()

	s.Require().NoError(cl.Create(ctx, s.newArgoCDReadyCertManager(ns)))
	s.Require().NoError(cl.Create(ctx, s.newReadyRootShard(ns)))
	s.Require().NoError(cl.Create(ctx, s.newReadyFrontProxy(ns)))
	s.seedCertManagerCRDs(ctx, cl)

	sub := &DeploymentSubroutine{
		clientRuntime:            cl,
		clientInfra:              cl,
		cfg:                      &pmconfig.CommonServiceConfig{IsLocal: true},
		cfgOperator:              &operatorCfg,
		gotemplatesInfraDir:      filepath.Join(s.tmpDir, "gotemplates/infra"),
		gotemplatesComponentsDir: filepath.Join(s.tmpDir, "gotemplates/components"),
		workspaceDirectory:       filepath.Join(s.tmpDir, "manifests/k8s"),
	}

	result, err := sub.Process(ctx, inst)

	s.NoError(err)
	s.True(result.IsContinue(), "expected OK/continue result, got stop")
}

func (s *DeploymentProcessTestSuite) Test_Process_CertManagerCRDsNotEstablished_FluxCD() {
	ns := "platform-mesh-system"
	operatorCfg := s.newOperatorConfig()
	ctx := s.newContext(operatorCfg)

	inst := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh", Namespace: ns},
		Spec: corev1alpha1.PlatformMeshSpec{
			Exposure: &corev1alpha1.ExposureConfig{BaseDomain: "localhost", Port: 8443, Protocol: "https"},
		},
	}

	profileCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh-profile", Namespace: ns},
		Data:       map[string]string{profileConfigMapKey: testProfileFluxCD},
	}

	cl := fake.NewClientBuilder().
		WithScheme(s.scheme).
		WithObjects(inst, profileCM).
		WithStatusSubresource(inst).
		Build()
	s.Require().NoError(cl.Create(ctx, s.newFluxCDReadyCertManager(ns)))
	s.Require().NoError(cl.Create(ctx, s.newReadyRootShard(ns)))
	s.Require().NoError(cl.Create(ctx, s.newReadyFrontProxy(ns)))
	// cert-manager CRDs are NOT seeded — Process must stop and requeue.

	sub := &DeploymentSubroutine{
		clientRuntime:            cl,
		clientInfra:              cl,
		cfg:                      &pmconfig.CommonServiceConfig{IsLocal: true},
		cfgOperator:              &operatorCfg,
		gotemplatesInfraDir:      filepath.Join(s.tmpDir, "gotemplates/infra"),
		gotemplatesComponentsDir: filepath.Join(s.tmpDir, "gotemplates/components"),
		workspaceDirectory:       filepath.Join(s.tmpDir, "manifests/k8s"),
	}

	result, err := sub.Process(ctx, inst)

	s.NoError(err)
	s.False(result.IsContinue(), "expected StopWithRequeue when cert-manager CRDs are not established")
}

func (s *DeploymentProcessTestSuite) Test_Process_MissingProfile() {
	ns := "platform-mesh-system"
	operatorCfg := s.newOperatorConfig()
	ctx := s.newContext(operatorCfg)

	inst := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh", Namespace: ns},
		Spec: corev1alpha1.PlatformMeshSpec{
			Exposure: &corev1alpha1.ExposureConfig{BaseDomain: "localhost", Port: 8443, Protocol: "https"},
		},
	}

	// No profile ConfigMap — should fail on template rendering
	cl := fake.NewClientBuilder().
		WithScheme(s.scheme).
		WithObjects(inst).
		WithStatusSubresource(inst).
		Build()

	sub := &DeploymentSubroutine{
		clientRuntime:            cl,
		clientInfra:              cl,
		cfg:                      &pmconfig.CommonServiceConfig{IsLocal: true},
		cfgOperator:              &operatorCfg,
		gotemplatesInfraDir:      filepath.Join(s.tmpDir, "gotemplates/infra"),
		gotemplatesComponentsDir: filepath.Join(s.tmpDir, "gotemplates/components"),
		workspaceDirectory:       filepath.Join(s.tmpDir, "manifests/k8s"),
	}

	result, err := sub.Process(ctx, inst)

	s.Error(err)
	s.NotNil(result)
}

func (s *DeploymentProcessTestSuite) Test_Process_RootShardNotReady() {
	ns := "platform-mesh-system"
	operatorCfg := s.newOperatorConfig()
	ctx := s.newContext(operatorCfg)

	inst := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh", Namespace: ns},
		Spec: corev1alpha1.PlatformMeshSpec{
			Exposure: &corev1alpha1.ExposureConfig{BaseDomain: "localhost", Port: 8443, Protocol: "https"},
		},
	}

	profileCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh-profile", Namespace: ns},
		Data:       map[string]string{profileConfigMapKey: testProfileFluxCD},
	}

	cl := fake.NewClientBuilder().
		WithScheme(s.scheme).
		WithObjects(inst, profileCM).
		WithStatusSubresource(inst).
		Build()

	// cert-manager ready but NO RootShard
	s.Require().NoError(cl.Create(ctx, s.newFluxCDReadyCertManager(ns)))

	sub := &DeploymentSubroutine{
		clientRuntime:            cl,
		clientInfra:              cl,
		cfg:                      &pmconfig.CommonServiceConfig{IsLocal: true},
		cfgOperator:              &operatorCfg,
		gotemplatesInfraDir:      filepath.Join(s.tmpDir, "gotemplates/infra"),
		gotemplatesComponentsDir: filepath.Join(s.tmpDir, "gotemplates/components"),
		workspaceDirectory:       filepath.Join(s.tmpDir, "manifests/k8s"),
	}

	result, err := sub.Process(ctx, inst)

	s.NoError(err)
	s.False(result.IsContinue(), "expected StopWithRequeue when RootShard not found")
}
