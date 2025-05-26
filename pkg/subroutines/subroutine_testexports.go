package subroutines

import (
	"context"

	"github.com/openmfp/golang-commons/logger"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
)

// these are needed to allow testing private functions in the subroutines_test namespace

func (r *KcpsetupSubroutine) GetCaBundle(ctx context.Context, webhookConfig *corev1alpha1.WebhookConfiguration) ([]byte, error) {
	return r.getCaBundle(ctx, webhookConfig)
}

func (r *KcpsetupSubroutine) GetCABundleInventory(ctx context.Context) (map[string]string, error) {
	return r.getCABundleInventory(ctx)
}

func (r *KcpsetupSubroutine) CreateKcpResources(ctx context.Context, config *rest.Config, dir string, inst *corev1alpha1.OpenMFP) error {
	return r.createKcpResources(ctx, config, dir, inst)
}

func (r *KcpsetupSubroutine) GetAPIExportHashInventory(ctx context.Context, config *rest.Config) (map[string]string, error) {
	return r.getAPIExportHashInventory(ctx, config)
}

func (r *KcpsetupSubroutine) ApplyDirStructure(
	ctx context.Context, dir string, kcpPath string, config *rest.Config, inventory map[string]string, inst *corev1alpha1.OpenMFP,
) error {
	return r.applyDirStructure(ctx, dir, kcpPath, config, inventory, inst)
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
	templateData map[string]string, wsPath string, inst *corev1alpha1.OpenMFP,
) error {
	return applyManifestFromFile(ctx, path, k8sClient, templateData, wsPath, inst)
}
