package subroutines

import (
	"context"

	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// these are needed to allow testing private functions in the subroutines_test namespace

func (r *KcpsetupSubroutine) CreateKcpResources(ctx context.Context, secret corev1.Secret, secretKey string, dir DirectoryStructure) error {
	return r.createKcpResources(ctx, secret, secretKey, dir)
}

func (r *KcpsetupSubroutine) GetAPIExportHashInventory(ctx context.Context, config *rest.Config) (APIExportInventory, error) {
	return r.getAPIExportHashInventory(ctx, config)
}

func (r *KcpsetupSubroutine) ApplyDirStructure(
	ctx context.Context, dir DirectoryStructure, config *rest.Config, hashes APIExportInventory,
) error {
	return r.applyDirStructure(ctx, dir, config, hashes)
}

func (r *KcpsetupSubroutine) WaitForWorkspace(
	ctx context.Context,
	config *rest.Config, name string, log *logger.Logger,
) error {
	return r.waitForWorkspace(ctx, config, name, log)
}

func (r *KcpsetupSubroutine) ApplyManifestFromFile(
	ctx context.Context,
	path string, k8sClient client.Client, hashes APIExportInventory,
) error {
	return r.applyManifestFromFile(ctx, path, k8sClient, hashes)
}

func (r *WebhooksSubroutine) HandleWebhookConfig(
	ctx context.Context, instance *corev1alpha1.OpenMFP, webhookConfig corev1alpha1.WebhookConfiguration,
) (ctrl.Result, errors.OperatorError) {
	return r.handleWebhookConfig(ctx, instance, webhookConfig)
}
