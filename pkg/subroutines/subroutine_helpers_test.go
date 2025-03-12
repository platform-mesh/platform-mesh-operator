package subroutines

import (
	"testing"

	"github.com/stretchr/testify/suite"
	"k8s.io/client-go/rest"
)

type HelperTestSuite struct {
	suite.Suite

	KcpHelper
}

func TestHelperTestSuite(t *testing.T) {
	suite.Run(t, new(HelperTestSuite))
}

func (suite *HelperTestSuite) SetupTest() {
	suite.KcpHelper = &Helper{}
}

func (s *HelperTestSuite) TestConstructorError() {
	client, err := s.KcpHelper.NewKcpClient(&rest.Config{}, "")
	s.Assert().Error(err)
	s.Assert().Nil(client)
}

func (s *HelperTestSuite) TestConstructorOK() {
	client, err := s.KcpHelper.NewKcpClient(&rest.Config{
		Host: "http://server:1234",
	}, "")
	s.Assert().NoError(err)
	s.Assert().NotNil(client)
}
