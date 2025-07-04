package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestKindSuite(t *testing.T) {
	suite.Run(t, new(KindTestSuite))
}

func (s *KindTestSuite) TestResourceReady() {
	ctx := context.TODO()
	s.Eventually(func() bool {
		openmfp := corev1alpha1.OpenMFP{}
		err := s.client.Get(ctx, client.ObjectKey{
			Name:      "openmfp",
			Namespace: "openmfp-system",
		}, &openmfp)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to get OpenMFP resource")
			return false
		}

		for _, condition := range openmfp.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				s.logger.Info().Msg("OpenMFP resource is ready")
				return true
			}
		}
		return false
	}, 25*time.Minute, 10*time.Second)

}
