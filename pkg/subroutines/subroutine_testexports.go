package subroutines

import (
	"context"

	"github.com/platform-mesh/golang-commons/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
)

// these are needed to allow testing private functions in the subroutines_test namespace

func (r *KcpsetupSubroutine) GetCaBundle(ctx context.Context, webhookConfig *corev1alpha1.WebhookConfiguration) ([]byte, error) {
	return r.getCaBundle(ctx, webhookConfig)
}

func (r *KcpsetupSubroutine) GetCABundleInventory(ctx context.Context) (map[string]string, error) {
	return r.getCABundleInventory(ctx)
}

func (r *KcpsetupSubroutine) CreateKcpResources(ctx context.Context, config *rest.Config, dir string, inst *corev1alpha1.PlatformMesh) error {
	return r.createKcpResources(ctx, config, dir, inst)
}

func (r *KcpsetupSubroutine) GetAPIExportHashInventory(ctx context.Context, config *rest.Config) (map[string]string, error) {
	return r.getAPIExportHashInventory(ctx, config)
}

func (s *DeploymentSubroutine) ApplyManifestFromFileWithMergedValues(ctx context.Context, path string, k8sClient client.Client, templateData map[string]any) error {
	return applyManifestFromFileWithMergedValues(ctx, path, k8sClient, templateData)
}

func (s *KcpsetupSubroutine) UnstructuredFromFile(path string, templateData map[string]any, log *logger.Logger) (unstructured.Unstructured, error) {
	return unstructuredFromFile(path, templateData, log)
}

func (r *KcpsetupSubroutine) ApplyExtraWorkspaces(ctx context.Context, config *rest.Config, inst *corev1alpha1.PlatformMesh) error {
	return r.applyExtraWorkspaces(ctx, config, inst)
}
