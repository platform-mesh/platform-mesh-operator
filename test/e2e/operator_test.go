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
	"testing"
	"time"

	"github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/openmfp/openmfp-operator/pkg/subroutines"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/stretchr/testify/suite"

	kcpcorev1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
)

func TestOpenmfpSuite(t *testing.T) {
	suite.Run(t, new(OpenmfpTestSuite))
}

func (suite *OpenmfpTestSuite) TestSecretsCreated() {
	// Given
	instance := &v1alpha1.OpenMFP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test2",
			Namespace: "default",
		},
		Spec: v1alpha1.OpenMFPSpec{
			Kcp: v1alpha1.Kcp{
				AdminSecretRef: v1alpha1.AdminSecretRef{
					Name: "kcp-admin",
				},
				ProviderConnections: []v1alpha1.ProviderConnection{
					{
						EndpointSliceName: "openmfp.org",
						Path:              "root:openmfp-system",
						Secret:            "core-openmfp-system-kubeconfig",
					},
				},
			},
		},
	}

	// When
	testContext := context.Background()
	err := suite.kubernetesClient.Create(testContext, instance)
	suite.Nil(err)

	// Then
	suite.Assert().Eventually(
		func() bool {
			// get reconciled instance
			err := suite.kubernetesClient.Get(
				testContext, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, instance)
			if err != nil {
				return false
			}

			// check if all secrets are created
			for _, pc := range instance.Spec.Kcp.ProviderConnections {
				secret := &corev1.Secret{}
				err := suite.kubernetesClient.Get(
					testContext, types.NamespacedName{Name: pc.Secret, Namespace: instance.Namespace}, secret)
				if err != nil {
					suite.logger.Error().Err(err).Msg("Error getting secret")
					return false
				}
				// connect to kcp with secret
				config, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["kubeconfig"])
				if err != nil {
					suite.logger.Error().Err(err).Msg("Error building config from kubeconfig string")
					return false
				}
				helper := &subroutines.Helper{}
				kcpClient, err := helper.NewKcpClient(config, pc.Path)
				if err != nil {
					suite.logger.Error().Err(err).Msg("Error creating kcp client")
					return false
				}
				list := &kcpcorev1alpha.APIExportList{}
				err = kcpClient.List(context.Background(), list)
				if err != nil {
					suite.logger.Error().Err(err).Msg("Error listing APIExports")
					return false
				}
			}
			return true
		},
		2*time.Minute, // timeout
		5*time.Second, // polling interval
	)

	suite.logger.Info().Msg("Workspace created")
}

func (suite *OpenmfpTestSuite) AfterTest(suiteName, testName string) {
	// Clean up: delete OpenMFP instances
	OpenMFPList := &v1alpha1.OpenMFPList{}
	err := suite.kubernetesClient.List(context.Background(), OpenMFPList)
	if err != nil {
		suite.T().Fatal(err)
		return
	}
	for _, item := range OpenMFPList.Items {
		err = suite.kubernetesClient.Delete(context.Background(), &item)
		if err != nil {
			suite.T().Fatal(err)
			return
		}
	}
}

func (suite *OpenmfpTestSuite) TestWorkspaceCreation() {
	// Given
	instance := &v1alpha1.OpenMFP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-openmfp",
			Namespace: "default",
		},
		Spec: v1alpha1.OpenMFPSpec{
			Kcp: v1alpha1.Kcp{
				AdminSecretRef: v1alpha1.AdminSecretRef{
					Name: "kcp-admin",
				},
				ProviderConnections: []v1alpha1.ProviderConnection{
					{
						EndpointSliceName: "test-endpoint-slice",
						Path:              "root",
						Secret:            "test-secret",
					},
				},
			},
		},
	}

	// When
	testContext := context.Background()
	err := suite.kubernetesClient.Create(testContext, instance)
	suite.Nil(err)

	// Then
	workspace := &kcptenancyv1alpha.Workspace{}
	suite.Assert().Eventually(
		func() bool {
			err := suite.kcpKubernetesClient.Get(
				testContext, types.NamespacedName{Name: "orgs", Namespace: "default"}, workspace)

			return err == nil
		},
		2*time.Minute, // timeout
		5*time.Second, // polling interval
	)

	suite.logger.Info().Msg("Workspace created")
}
