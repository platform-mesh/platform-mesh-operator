package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
)

func TestKindSuite(t *testing.T) {
	suite.Run(t, new(KindTestSuite))
}

func (s *KindTestSuite) TestResourceReady() {
	ctx := context.TODO()
	s.Eventually(func() bool {
		pm := corev1alpha1.PlatformMesh{}
		err := s.client.Get(ctx, client.ObjectKey{
			Name:      "platform-mesh",
			Namespace: "platform-mesh-system",
		}, &pm)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to get Platform Mesh resource")
			return false
		}

		for _, condition := range pm.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				s.logger.Info().Msg("PlatformMesh resource is ready")
				return true
			}
		}
		return false
	}, 20*time.Minute, 10*time.Second)

}
