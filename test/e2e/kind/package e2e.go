package e2e

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apixclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	corev1 "k8s.io/api/core/v1"

	"github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
)

// newTestLogger creates a simple console logger for the tests.
func newTestLogger(t *testing.T) *logger.Logger {
	t.Helper()
	cfg := logger.DefaultConfig()
	cfg.NoJSON = true
	cfg.Level = "debug"
	cfg.Name = "runOperatorTest"
	l, err := logger.New(cfg)
	require.NoError(t, err)
	return l
}

// startEnvTest starts a local control plane and returns its config and a stop function.
func startEnvTest(t *testing.T) (*envtest.Environment, *rest.Config) {
	t.Helper()

	// Point to your CRDs. If your CRDs are in config/crd/bases, change accordingly.
	crdDir := filepath.Join("..", "..", "..", "config", "crd")

	testEnv := &envtest.Environment{
		// For older controller-runtime versions use CRDDirectoryPaths.
		CRDDirectoryPaths: []string{crdDir},
	}

	cfg, err := testEnv.Start()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	return testEnv, cfg
}

func Test_runOperator_StartsManagerAndInstallsCRD(t *testing.T) {
	// 1) Start envtest and install CRDs
	testEnv, cfg := startEnvTest(t)
	defer func() { require.NoError(t, testEnv.Stop()) }()

	// 2) Build a scheme needed by the manager
	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	// 3) Create the test suite instance with envtest config
	s := &KindTestSuite{
		scheme: scheme,
		config: cfg,
		logger: newTestLogger(t),
	}

	// Make controller-runtime use our test logger
	ctrl.SetLogger(s.logger.Logr())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 4) Invoke the method under test
	s.runOperator(ctx)

	// 5) Validate: manager created
	require.NotNil(t, s.kubernetesManager, "manager not created")

	// 6) Wait for cache sync to prove the manager actually started
	cacheCtx, cacheCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cacheCancel()
	ok := s.kubernetesManager.GetCache().WaitForCacheSync(cacheCtx)
	require.True(t, ok, "manager cache failed to sync")

	// 7) Validate CRD is present in the API server (envtest installed it)
	apix, err := apixclientset.NewForConfig(cfg)
	require.NoError(t, err)
	// CRD name follows the pattern "<plural>.<group>"
	_, err = apix.ApiextensionsV1().CustomResourceDefinitions().Get(context.Background(), "platformmeshes.core.platform-mesh.io", metav1.GetOptions{})
	require.NoError(t, err, "PlatformMesh CRD not found; ensure CRDDirectoryPaths points to your CRDs")

	// 8) Cleanup: stop the running manager
	if s.cancel != nil {
		s.cancel()
	}
}

// Optional: example of installing CRDs programmatically at runtime (not recommended inside runOperator).
// If you absolutely need to do it, embed the CRD YAML and create it via the apiextensions client.
// This is only an example snippet and not executed by tests.
/*
import (
  _ "embed"
  apixv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
  apixclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
  "sigs.k8s.io/yaml"
)

//go:embed ../../../config/crd/core.platform-mesh.io_platform-mesh.yaml
var platformMeshCRDBytes []byte

func installPlatformMeshCRD(ctx context.Context, cfg *rest.Config) error {
  apix, err := apixclientset.NewForConfig(cfg)
  if err != nil {
    return err
  }
  var crd apixv1.CustomResourceDefinition
  if err := yaml.Unmarshal(platformMeshCRDBytes, &crd); err != nil {
    return err
  }
  // Create or update
  _, err = apix.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, &crd, metav1.CreateOptions{})
  if err == nil {
    return nil
  }
  // Try update if already exists
  _, err = apix.ApiextensionsV1().CustomResourceDefinitions().Update(ctx, &crd, metav1.UpdateOptions{})
  return err
}
*/
