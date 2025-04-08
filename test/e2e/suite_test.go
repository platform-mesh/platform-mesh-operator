/*
Copyright 2025.

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

package e2e

import (
	"context"
	"fmt"
	"k8s.io/client-go/rest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	kcpcorev1alpha "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	openmfpconfig "github.com/openmfp/golang-commons/config"

	openmfpcontext "github.com/openmfp/golang-commons/context"
	"github.com/openmfp/golang-commons/logger"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/apps/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/openmfp/openmfp-operator/api/v1alpha1"
	cachev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/openmfp/openmfp-operator/internal/config"
	"github.com/openmfp/openmfp-operator/internal/controller"
	"github.com/openmfp/openmfp-operator/pkg/subroutines"
	"github.com/openmfp/openmfp-operator/pkg/testing/kcpenvtest"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:scaffold:imports

const (
	defaultTestTimeout  = 10 * time.Second
	defaultTickInterval = 250 * time.Millisecond
	defaultNamespace    = "default"
)

var testDirs = subroutines.DirectoryStructure{
	Workspaces: []subroutines.WorkspaceDirectory{
		{
			Name: "root",
			Files: []string{
				"../../setup/workspace-openmfp-system.yaml",
				"../../setup/workspacetype-org.yaml",
				"../../setup/workspace-type-orgs.yaml",
				"../../setup/workspace-type-account.yaml",
				"../../setup/workspace-orgs.yaml",
				"../../setup/apiexport-kcp.io.yaml",
			},
		},
		{
			Name: "root:openmfp-system",
			Files: []string{
				"../../setup/01-openmfp-system/apiexport-core.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiexportendpointslice-core.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiresourceschema-accountinfos.core.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiresourceschema-accounts.core.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiresourceschema-authorizationmodels.core.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiresourceschema-stores.core.openmfp.org.yaml",
			},
		},
		{
			Name: "root:orgs",
			Files: []string{
				"../../setup/02-orgs/account-root-org.yaml",
				"../../setup/02-orgs/workspace-root-org.yaml",
			},
		},
	},
}

type OpenmfpTestSuite struct {
	suite.Suite

	kubernetesClient    client.Client
	kcpKubernetesClient client.Client
	kubernetesManager   ctrl.Manager
	testEnv             *envtest.Environment
	logger              *logger.Logger
	kcpTestenv          *kcpenvtest.Environment
	config              *rest.Config

	cancel context.CancelFunc
}

func (suite *OpenmfpTestSuite) SetupSuite() {
	logConfig := logger.DefaultConfig()
	logConfig.NoJSON = true
	logConfig.Level = "debug"
	logConfig.Name = "OpenmfpTestSuite"
	log, err := logger.New(logConfig)
	suite.logger = log
	suite.Nil(err)
	// Disable color logging as vs-code does not support color logging in the test output
	log = logger.NewFromZerolog(log.Output(&zerolog.ConsoleWriter{Out: os.Stdout, NoColor: true}))

	defaultConfig := &openmfpconfig.CommonServiceConfig{}
	appConfig := &config.OperatorConfig{}
	appConfig.Subroutines.KcpSetup.Enabled = true
	appConfig.Subroutines.ProviderSecret.Enabled = true
	testContext, _, _ := openmfpcontext.StartContext(log, appConfig, defaultConfig.ShutdownTimeout)

	testContext = logger.SetLoggerInContext(testContext, log.ComponentLogger("TestSuite"))

	suite.testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd")},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: filepath.Join(
			"..", "..", "bin", "k8s", fmt.Sprintf("1.29.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	cfg, err := suite.testEnv.Start()
	suite.Nil(err)

	utilruntime.Must(cachev1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(v1.AddToScheme(scheme.Scheme))

	// +kubebuilder:scaffold:scheme

	suite.kubernetesClient, err = client.New(cfg, client.Options{
		Scheme: scheme.Scheme,
	})
	suite.Nil(err)
	ctrl.SetLogger(log.Logr())
	suite.kubernetesManager, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme:      scheme.Scheme,
		BaseContext: func() context.Context { return testContext },
	})
	suite.Nil(err)

	openmfpReconciler := controller.NewOpenmfpReconciler(log, suite.kubernetesManager, appConfig, testDirs)
	err = openmfpReconciler.SetupWithManager(suite.kubernetesManager, defaultConfig, log)
	suite.Nil(err)

	// setup KCP test environment
	testEnvLogger := log.ComponentLogger("kcpenvtest")

	useExistingCluster := true
	if envValue, err := strconv.ParseBool(os.Getenv("USE_EXISTING_CLUSTER")); err != nil {
		useExistingCluster = envValue
	}
	suite.kcpTestenv = kcpenvtest.NewEnvironment(
		"openmfp.org", "openmfp-system", "../../", "bin", "setup", useExistingCluster, testEnvLogger)
	k8sCfg, _, err := suite.kcpTestenv.Start()
	suite.Require().NoError(err)
	if err != nil {
		stopErr := suite.kcpTestenv.Stop(useExistingCluster)
		suite.Require().NoError(stopErr)
	}
	suite.Require().NotNil(k8sCfg)

	scheme := k8sruntime.NewScheme()
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(v1.AddToScheme(scheme))
	utilruntime.Must(kcpcorev1alpha.AddToScheme(scheme))
	utilruntime.Must(kcptenancyv1alpha.AddToScheme(scheme))

	// store k8sCfg as a secret
	kubeconfigData, err := os.ReadFile("../../.kcp/admin.kubeconfig")
	if err != nil {
		suite.logger.Error().Err(err).Msg("Failed to read kubeconfig file")
		return
	}
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kcp-admin",
			Namespace: defaultNamespace,
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	err = suite.kubernetesClient.Create(context.Background(), &secret)
	if err != nil {
		suite.logger.Error().Err(err).Msg("Failed to create secret")
	}

	// create KCP client
	config, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["kubeconfig"])
	if err != nil {
		log.Error().Err(err).Msg("Failed to build config from kubeconfig string")
		return
	}

	config.QPS = 1000.0
	config.Burst = 2000.0
	client, err := client.New(config, client.Options{})
	if err != nil {
		log.Error().Err(err).Msg("Failed to create client")
		return
	}
	suite.kcpKubernetesClient = client
	suite.config = config

	go suite.startController()
}

func (suite *OpenmfpTestSuite) startController() {
	var controllerContext context.Context
	controllerContext, suite.cancel = context.WithCancel(context.Background())
	err := suite.kubernetesManager.Start(controllerContext)
	suite.Nil(err)
}

func (suite *OpenmfpTestSuite) TearDownSuite() {
	suite.cancel()
	err := suite.kcpTestenv.Stop(false)
	suite.Nil(err)
	err = suite.testEnv.Stop()
	suite.Nil(err)
}
