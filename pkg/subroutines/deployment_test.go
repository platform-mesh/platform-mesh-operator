package subroutines_test

import (
	"testing"

	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/suite"

	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"

	pmconfig "github.com/platform-mesh/golang-commons/config"

	"github.com/platform-mesh/platform-mesh-operator/internal/config"
)

type DeployTestSuite struct {
	suite.Suite
	clientMock     *mocks.Client
	helperMock     *mocks.KcpHelper
	testObj        *subroutines.DeploymentSubroutine
	log            *logger.Logger
	operatorConfig *config.OperatorConfig
}

func TestDeployTestSuite(t *testing.T) {
	suite.Run(t, new(DeployTestSuite))
}

func (s *DeployTestSuite) SetupTest() {
	s.clientMock = new(mocks.Client)
	s.helperMock = new(mocks.KcpHelper)
	cfgLog := logger.DefaultConfig()
	cfgLog.Level = "debug"
	cfgLog.NoJSON = true
	cfgLog.Name = "DeployTestSuite"
	s.log, _ = logger.New(cfgLog)

	cfg := pmconfig.CommonServiceConfig{}
	operatorCfg := config.OperatorConfig{
		WorkspaceDir: "../../",
	}
	operatorCfg.KCP.RootShardName = "root"
	operatorCfg.KCP.Namespace = "platform-mesh-system"
	operatorCfg.KCP.FrontProxyName = "frontproxy"
	operatorCfg.KCP.FrontProxyPort = "6443"
	operatorCfg.KCP.ClusterAdminSecretName = "kcp-cluster-admin-client-cert"
	operatorCfg.RemoteInfra.Enabled = true
	operatorCfg.RemoteInfra.Kubeconfig = "platform-mesh-kubeconfig"

	s.operatorConfig = &operatorCfg

	s.testObj = subroutines.NewDeploymentSubroutine(s.clientMock, nil, &cfg, &operatorCfg, nil)
}
