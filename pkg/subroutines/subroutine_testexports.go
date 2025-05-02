package subroutines

import (
	"context"

	"github.com/openmfp/golang-commons/logger"
	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// these are needed to allow testing private functions in the subroutines_test namespace

func (r *KcpsetupSubroutine) GetCaBundle(ctx context.Context, webhookConfig *corev1alpha1.WebhookConfiguration) ([]byte, error) {
	return r.getCaBundle(ctx, webhookConfig)
}

func (r *KcpsetupSubroutine) GetCABundleInventory(ctx context.Context) (map[string]string, error) {
	return r.getCABundleInventory(ctx)
}

func (r *KcpsetupSubroutine) CreateKcpResources(ctx context.Context, secret corev1.Secret, secretKey string, dir DirectoryStructure, instance *corev1alpha1.OpenMFP) error {
	return r.createKcpResources(ctx, secret, secretKey, dir, instance)
}

func (r *KcpsetupSubroutine) GetAPIExportHashInventory(ctx context.Context, config *rest.Config) (map[string]string, error) {
	return r.getAPIExportHashInventory(ctx, config)
}

func (r *KcpsetupSubroutine) ApplyDirStructure(
	ctx context.Context, dir DirectoryStructure, config *rest.Config, inventory map[string]string,
) error {
	return r.applyDirStructure(ctx, dir, config, inventory)
}

func (r *KcpsetupSubroutine) WaitForWorkspace(
	ctx context.Context,
	config *rest.Config, name string, log *logger.Logger,
) error {
	return r.waitForWorkspace(ctx, config, name, log)
}

func (r *KcpsetupSubroutine) ApplyManifestFromFile(
	ctx context.Context,
	path string,
	k8sClient client.Client,
	templateData map[string]string,
) error {
	return r.applyManifestFromFile(ctx, path, k8sClient, templateData)
}
