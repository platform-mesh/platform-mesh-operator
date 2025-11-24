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
	}, 25*time.Minute, 10*time.Second)

}

func (s *KindTestSuite) TestRunFor30Minutes() {
	s.Eventually(func() bool {
		return false
	}, 30*time.Minute, 10*time.Second)
}

// func (s *KindTestSuite) TestExtraWorkspaces() {
// 	ctx := context.TODO()

// 	pm := corev1alpha1.PlatformMesh{}
// 	err := s.client.Get(ctx, client.ObjectKey{
// 		Name:      "platform-mesh",
// 		Namespace: "platform-mesh-system",
// 	}, &pm)
// 	s.Assert().NoError(err, "Failed to get Platform Mesh resource")

// 	pm.Spec.Kcp.ExtraWorkspaces = []corev1alpha1.WorkspaceDeclaration{
// 		{
// 			Path: "root:orgs:extra1",
// 			Type: corev1alpha1.WorkspaceTypeReference{
// 				Name: "org",
// 				Path: "root",
// 			},
// 		},
// 	}
// 	pm.Spec.Kcp.ExtraProviderConnections = append(pm.Spec.Kcp.ExtraProviderConnections, corev1alpha1.ProviderConnection{
// 		Path:     "root:orgs:extra1",
// 		Secret:   "extra1-kubeconfig",
// 		External: true,
// 	})
// 	s.logger.Info().Str("platformmesh", fmt.Sprintf("%+v", pm)).Msg("Updating Platform Mesh resource to add extra workspace and provider connection")
// 	err = s.client.Update(ctx, &pm)
// 	s.Assert().NoError(err, "Failed to update Platform Mesh resource")

// 	s.Eventually(func() bool {
// 		updatedPM := corev1alpha1.PlatformMesh{}
// 		err := s.client.Get(ctx, client.ObjectKey{
// 			Name:      "platform-mesh",
// 			Namespace: "platform-mesh-system",
// 		}, &updatedPM)
// 		if err != nil {
// 			s.logger.Warn().Err(err).Msg("Failed to get Platform Mesh resource")
// 			return false
// 		}

// 		for _, condition := range updatedPM.Status.Conditions {
// 			if condition.Status != "True" {
// 				s.logger.Info().Msg("PlatformMesh resource is not ready")
// 				return false
// 			}
// 		}

// 		// get extra1 secret
// 		secret := &corev1.Secret{}
// 		err = s.client.Get(ctx, client.ObjectKey{
// 			Name:      "extra1-kubeconfig",
// 			Namespace: "platform-mesh-system",
// 		}, secret)
// 		if err != nil {
// 			s.logger.Warn().Err(err).Msg("Failed to get extra1-kubeconfig secret")
// 			return false
// 		}

// 		s.logger.Info().Msg("PlatformMesh resource is ready and extra1-kubeconfig secret exists")
// 		return true

// 	}, 20*time.Minute, 10*time.Second)

// }
