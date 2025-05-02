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
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	kcpapisv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcpcorev1alpha "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/openmfp/openmfp-operator/pkg/subroutines"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

const kcpIoApiExport = "kcp.io"

func TestOpenmfpSuite(t *testing.T) {
	suite.Run(t, new(OpenmfpTestSuite))
}

func (suite *OpenmfpTestSuite) TestSecretsCreated() {
	// Given
	instance := &v1alpha1.OpenMFP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secrets-created",
			Namespace: "default",
		},
		Spec: v1alpha1.OpenMFPSpec{
			Kcp: v1alpha1.Kcp{
				AdminSecretRef: &v1alpha1.AdminSecretRef{
					SecretRef: v1alpha1.SecretReference{
						Name:      "openmfp-operator-kubeconfig",
						Namespace: "default",
					},
					Key: "kubeconfig",
				},
				ProviderConnections: []v1alpha1.ProviderConnection{
					{
						EndpointSliceName: "core.openmfp.org",
						Path:              "root:openmfp-system",
						Secret:            "openmfp-system-kubeconfig",
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
					suite.logger.Debug().Msg("Secret does not exist yet")
					return false
				}
				// connect to kcp with secret
				config, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["kubeconfig"])
				if err != nil {
					suite.logger.Debug().Err(err).Msg("Error building config from kubeconfig string")
					return false
				}
				helper := &subroutines.Helper{}
				kcpClient, err := helper.NewKcpClient(config, pc.Path)
				if err != nil {
					suite.logger.Error().Err(err).Msg("Error creating kcp client")
					return false
				}
				list := &kcpapisv1alpha.APIExportList{}
				err = kcpClient.List(context.Background(), list)
				if err != nil {
					suite.logger.Debug().Err(err).Msg("Error listing APIExports")
					return false
				}
			}
			return true
		},
		40*time.Second, // timeout
		5*time.Second,  // polling interval
	)

	suite.logger.Info().Msg("Secret created")

	// get the created provider secret
	providerSecret := &corev1.Secret{}
	err = suite.kubernetesClient.Get(
		testContext, types.NamespacedName{Name: instance.Spec.Kcp.ProviderConnections[0].Secret, Namespace: instance.Namespace}, providerSecret)
	suite.Nil(err)
	origSecretValue := string(providerSecret.Data["kubeconfig"])

	// mangle the created provider secret
	providerSecret.Data["kubeconfig"] = []byte("UnViYmlzaA==") // "Rubbish" base64 encoded
	err = suite.kubernetesClient.Update(testContext, providerSecret)
	suite.Nil(err)
	suite.logger.Debug().Msg("Secret mangled")

	// Check if labels map exists, if not initialize it
	if instance.Labels == nil {
		instance.Labels = make(map[string]string)
	}
	instance.Labels["openmfp.io/refresh-reconcile"] = "True"
	err = suite.kubernetesClient.Update(testContext, instance)
	suite.Nil(err)

	// wait for the secret to be reconciled
	suite.Assert().Eventually(
		func() bool {
			// verify the secret was restored by the operator
			updatedSecret := &corev1.Secret{}
			err = suite.kubernetesClient.Get(testContext, types.NamespacedName{Name: providerSecret.Name, Namespace: providerSecret.Namespace}, updatedSecret)
			suite.Nil(err)
			return origSecretValue == string(updatedSecret.Data["kubeconfig"])
		},
		30*time.Second, // timeout
		5*time.Second,  // polling interval
	)

	suite.logger.Info().Msg("Secret restored by the operator")
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
			Name:      "test-workspace-creation",
			Namespace: "default",
		},
		Spec: v1alpha1.OpenMFPSpec{
			Kcp: v1alpha1.Kcp{
				AdminSecretRef: &v1alpha1.AdminSecretRef{
					SecretRef: v1alpha1.SecretReference{
						Namespace: "default",
						Name:      "openmfp-operator-kubeconfig",
					},
					Key: "kubeconfig",
				},
				ProviderConnections: []v1alpha1.ProviderConnection{
					{
						EndpointSliceName: "core.openmfp.org",
						Path:              "root:openmfp-system",
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
		30*time.Second, // timeout
		5*time.Second,  // polling interval
	)

	suite.logger.Info().Msg("Workspace created")
}

func (suite *OpenmfpTestSuite) TestWorkspaceCreationDefaults() {
	// Given
	instance := &v1alpha1.OpenMFP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-workspace-creation-defaults",
			Namespace: "default",
		},
		Spec: v1alpha1.OpenMFPSpec{
			Kcp: v1alpha1.Kcp{},
		},
	}

	// When
	testContext := context.Background()
	err := suite.kubernetesClient.Create(testContext, instance)
	suite.Nil(err)

	kcpHelper := &subroutines.Helper{}
	openmfpSystemClient, cerr := kcpHelper.NewKcpClient(suite.config, "root:openmfp-system")
	suite.Require().Nil(cerr)

	// Then
	apiexport := &kcpapisv1alpha.APIExport{}
	suite.Assert().Eventually(
		func() bool {
			err = openmfpSystemClient.Get(
				testContext, types.NamespacedName{Name: kcpIoApiExport}, apiexport)

			return err == nil
		},
		1*time.Minute,
		5*time.Second,
	)

	// Check API binding in root:orgs workspace

	orgsClient, err := kcpHelper.NewKcpClient(suite.config, "root:orgs")
	suite.Nil(err)

	orgsBindingList := &kcpapisv1alpha.APIBindingList{}
	suite.Assert().Eventually(
		func() bool {
			err = orgsClient.List(testContext, orgsBindingList)
			if err != nil {
				return false
			}

			for _, binding := range orgsBindingList.Items {
				if strings.HasPrefix(binding.Name, kcpIoApiExport) {
					return true
				}
			}

			return false
		},
		1*time.Minute,
		5*time.Second,
	)

	testOrgName := "test-org"

	// Create test org workspace under orgs
	testOrg := &kcptenancyv1alpha.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testOrgName,
		},
		Spec: kcptenancyv1alpha.WorkspaceSpec{
			Type: kcptenancyv1alpha.WorkspaceTypeReference{
				Name: "org",
				Path: "root",
			},
		},
	}
	err = orgsClient.Create(testContext, testOrg)
	suite.Nil(err)

	// // Wait for org workspace to be ready
	// suite.Assert().Eventually(
	// 	func() bool {
	// 		err = orgsClient.Get(testContext, types.NamespacedName{Name: testOrgName}, testOrg)
	// 		if err != nil {
	// 			return false
	// 		}

	// 		return testOrg.Status.Phase == kcpcorev1alpha.LogicalClusterPhaseReady
	// 	},
	// 	1*time.Minute,
	// 	5*time.Second,
	// )

	// Create client for the org workspace
	orgClient, err := kcpHelper.NewKcpClient(suite.config, "root:orgs:test-org")
	suite.Nil(err)

	// Check API binding in root:orgs:test-org workspace
	testOrgBindingList := &kcpapisv1alpha.APIBindingList{}
	suite.Assert().Eventually(
		func() bool {
			err = orgClient.List(testContext, testOrgBindingList)
			if err != nil {
				suite.logger.Error().Err(err).Msg("Error listing APIBindings in account workspace")
				return false
			}

			for _, binding := range testOrgBindingList.Items {
				if strings.HasPrefix(binding.Name, kcpIoApiExport) {
					suite.logger.Info().Str("binding", binding.Name).Msg("Found API binding in account workspace")
					return true
				}
			}

			suite.logger.Info().Int("count", len(testOrgBindingList.Items)).Msg("No matching API bindings found")
			return false
		},
		2*time.Minute,
		5*time.Second,
	)

	// Create test account workspace under the org
	testAccount := &kcptenancyv1alpha.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-account",
		},
		Spec: kcptenancyv1alpha.WorkspaceSpec{
			Type: kcptenancyv1alpha.WorkspaceTypeReference{
				Name: "account",
				Path: "root",
			},
		},
	}
	err = orgClient.Create(testContext, testAccount)
	suite.Nil(err)

	// Wait for account workspace to be ready
	suite.Assert().Eventually(
		func() bool {
			err = orgClient.Get(testContext, types.NamespacedName{Name: "test-account"}, testAccount)
			if err != nil {
				return false
			}
			return testAccount.Status.Phase == kcpcorev1alpha.LogicalClusterPhaseReady
		},
		2*time.Minute,
		5*time.Second,
	)

	// Check API binding in account workspace
	accountClient, err := kcpHelper.NewKcpClient(suite.config, "root:orgs:test-org:test-account")
	suite.Nil(err)

	accountBindingList := &kcpapisv1alpha.APIBindingList{}
	suite.Assert().Eventually(
		func() bool {
			err = accountClient.List(testContext, accountBindingList)
			if err != nil {
				suite.logger.Error().Err(err).Msg("Error listing APIBindings in account workspace")
				return false
			}

			for _, binding := range accountBindingList.Items {
				if strings.HasPrefix(binding.Name, kcpIoApiExport) {
					suite.logger.Info().Str("binding", binding.Name).Msg("Found API binding in account workspace")
					return true
				}
			}

			suite.logger.Info().Int("count", len(accountBindingList.Items)).Msg("No matching API bindings found")
			return false
		},
		2*time.Minute,
		5*time.Second,
	)

	suite.logger.Info().Msg("APIExport propagated through the entire workspace hierarchy")

}

func (suite *OpenmfpTestSuite) TestWebhookConfigurations() {
	// 1: create OpenMFP instance with WebhookConfigurations (override) pointing to test webhook (it doesn't crevent account creation)

	// Given
	instance := &v1alpha1.OpenMFP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-webhook-creation",
			Namespace: "default",
		},
		Spec: v1alpha1.OpenMFPSpec{
			Kcp: v1alpha1.Kcp{
				AdminSecretRef: &v1alpha1.AdminSecretRef{
					SecretRef: v1alpha1.SecretReference{
						Namespace: "default",
						Name:      "openmfp-operator-kubeconfig",
					},
					Key: "kubeconfig",
				},
				ProviderConnections: []v1alpha1.ProviderConnection{
					{
						EndpointSliceName: "core.openmfp.org",
						Path:              "root:openmfp-system",
						Secret:            "test-secret",
					},
				},
			},
		},
	}

	// When
	testContext := context.Background()
	suite.logger.Info().Msg("CA secret created")
	err := suite.kubernetesClient.Create(testContext, instance)
	suite.Nil(err)

	// 2: check that webhook has been created with replaced CABundle
	// Then
	kcpHelper := &subroutines.Helper{}
	kcpWebhookClient, err := kcpHelper.NewKcpClient(suite.config, subroutines.DEFAULT_WEBHOOK_CONFIGURATION.WebhookRef.Path)
	suite.Nil(err)
	suite.Assert().Eventually(
		func() bool {
			webhookCertSecret := corev1.Secret{}
			err := suite.kubernetesClient.Get(
				testContext, types.NamespacedName{Name: subroutines.AccountOperatorMutatingWebhookSecretName, Namespace: subroutines.AccountOperatorMutatingWebhookSecretNamespace}, &webhookCertSecret)
			if err != nil {
				suite.logger.Error().Err(err).Msg("Error getting secret")
				return false
			}

			// List all webhook configurations
			webhookList := &admissionregistrationv1.MutatingWebhookConfigurationList{}
			err = kcpWebhookClient.List(testContext, webhookList)
			if err != nil {
				suite.logger.Error().Err(err).Msg("Error listing webhook configurations")
				return false
			}

			for _, webhook := range webhookList.Items {
				if webhook.Name == "account-operator.webhooks.core.openmfp.org" {
					suite.logger.Debug().Str("webhook.CABundle", string(webhook.Webhooks[0].ClientConfig.CABundle)).Str("secret.data", string(webhookCertSecret.Data[subroutines.DefaultCASecretKey])).Msg("Comparing CABundle")
					// Decode the base64 value from the secret
					decodedSecretData, _ := base64.StdEncoding.DecodeString(string(webhookCertSecret.Data[subroutines.DefaultCASecretKey]))
					return bytes.Equal(webhook.Webhooks[0].ClientConfig.CABundle, decodedSecretData)
				}
			}
			suite.logger.Debug().Msg("Webhook not found")
			return false
		},
		30*time.Second, // timeout
		5*time.Second,  // polling interval
	)

}
