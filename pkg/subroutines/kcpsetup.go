package subroutines

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/google/go-cmp/cmp"
	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/rs/zerolog"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"k8s.io/client-go/rest"
)

type KcpsetupSubroutine struct {
	client       client.Client
	kcpHelper    KcpHelper
	kcpUrl       string
	helm         HelmGetter
	kcpDirectory string
	// Cache for CA bundles to avoid redundant secret lookups
	caBundleCache map[string]string
	cfg           *config.OperatorConfig
}

const (
	KcpsetupSubroutineName      = "KcpsetupSubroutine"
	KcpsetupSubroutineFinalizer = "platform-mesh.core.platform-mesh.io/finalizer"
)

func NewKcpsetupSubroutine(client client.Client, helper KcpHelper, cfg *config.OperatorConfig, kcpdir string, kcpUrl string) *KcpsetupSubroutine {
	return &KcpsetupSubroutine{
		client:        client,
		kcpDirectory:  kcpdir,
		kcpUrl:        kcpUrl,
		kcpHelper:     helper,
		helm:          DefaultHelmGetter{},
		caBundleCache: make(map[string]string),
		cfg:           cfg,
	}
}

func (r *KcpsetupSubroutine) GetName() string {
	return KcpsetupSubroutineName
}

func (r *KcpsetupSubroutine) Finalize(
	ctx context.Context, runtimeObj runtimeobject.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil // TODO: Implement
}

func (r *KcpsetupSubroutine) Finalizers(instance runtimeobject.RuntimeObject) []string { // coverage-ignore
	return []string{KcpsetupSubroutineFinalizer}
}

func (r *KcpsetupSubroutine) Process(ctx context.Context, runtimeObj runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)

	inst := runtimeObj.(*corev1alpha1.PlatformMesh)
	log.Debug().Str("subroutine", r.GetName()).Str("name", inst.Name).Msg("Processing Platform Mesh resource")

	rootShard := &unstructured.Unstructured{}
	rootShard.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "RootShard"})
	// Wait for root shard to be ready
	err := r.client.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.RootShardName, Namespace: operatorCfg.KCP.Namespace}, rootShard)
	if err != nil || !matchesConditionWithStatus(rootShard, "Available", "True") {
		log.Info().Msg("RootShard is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("RootShard is not ready"), true, true)
	}

	frontProxy := &unstructured.Unstructured{}
	frontProxy.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "FrontProxy"})
	// Wait for root shard to be ready
	err = r.client.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.FrontProxyName, Namespace: operatorCfg.KCP.Namespace}, frontProxy)
	if err != nil || !matchesConditionWithStatus(frontProxy, "Available", "True") {
		log.Info().Msg("FrontProxy is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("FrontProxy is not ready"), true, true)
	}

	// Build kcp kubeonfig
	cfg, err := buildKubeconfig(ctx, r.client, r.kcpUrl)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to build kubeconfig"), true, false)
	}

	// Create kcp workspaces recursively
	err = r.createKcpResources(ctx, cfg, r.kcpDirectory, inst)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create kcp workspaces")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to create kcp workspaces"), true, false)
	}

	// apply extra workspaces
	err = r.applyExtraWorkspaces(ctx, cfg, inst)
	if err != nil {
		log.Error().Err(err).Msg("Failed to apply extra workspaces")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to apply extra workspaces"), true, false)
	}

	// update workspace status
	inst.Status.KcpWorkspaces = []corev1alpha1.KcpWorkspace{
		{
			Name:  "root:platform-mesh-system",
			Phase: "Ready",
		},
		{
			Name:  "root:orgs",
			Phase: "Ready",
		},
	}

	log.Debug().Msg("Successful kcp setup")

	return ctrl.Result{}, nil

}

func (r *KcpsetupSubroutine) createKcpResources(ctx context.Context, config *rest.Config, dir string, inst *corev1alpha1.PlatformMesh) error {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	// Get API export hashes
	apiExportHashes, err := r.getAPIExportHashInventory(ctx, config)
	if err != nil {
		log.Err(err).Msg("Failed to get APIExport hash inventory")
		return errors.Wrap(err, "Failed to get APIExport hash inventory")
	}

	// Get CA bundle data
	templateData, err := r.getCABundleInventory(ctx)
	if err != nil {
		log.Err(err).Msg("Failed to get CA bundle inventory")
		return errors.Wrap(err, "Failed to get CA bundle inventory")
	}

	// Merge the api export hashes with the CA bundle data
	for k, v := range apiExportHashes {
		templateData[k] = v
	}

	baseDomain, baseDomainPort, port, protocol := baseDomainPortProtocol(inst)
	templateData["baseDomain"] = baseDomain
	templateData["baseDomainPort"] = baseDomainPort
	templateData["port"] = fmt.Sprintf("%d", port)
	templateData["protocol"] = protocol
	templateData["featureDisableEmailVerification"] = HasFeatureToggle(inst, "feature-disable-email-verification")

	err = ApplyDirStructure(ctx, dir, "root", config, templateData, inst, r.kcpHelper)
	if err != nil {
		log.Err(err).Msg("Failed to apply dir structure")
		return errors.Wrap(err, "Failed to apply dir structure")
	}

	return nil
}

func (r *KcpsetupSubroutine) getCABundleInventory(
	ctx context.Context,
) (map[string]string, error) {
	log := logger.LoadLoggerFromContext(ctx)

	// If we already have cached results, return them
	if len(r.caBundleCache) > 0 {
		return r.caBundleCache, nil
	}

	caBundles := make(map[string]string)

	// Get default webhook CA bundle
	webhookConfig := DEFAULT_WEBHOOK_CONFIGURATION
	caData, err := r.getCaBundle(ctx, &webhookConfig)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get CA bundle")
		return nil, errors.Wrap(err, "Failed to get CA bundle")
	}

	key := fmt.Sprintf("%s.ca-bundle", webhookConfig.WebhookRef.Name)
	b64Data := base64.StdEncoding.EncodeToString(caData)
	caBundles[key] = b64Data

	// Get validating webhook CA bundle
	validatingWebhookConfig := DEFAULT_VALIDATING_WEBHOOK_CONFIGURATION
	validatingCaData, err := r.getCaBundle(ctx, &validatingWebhookConfig)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get ValidatingWebhook CA bundle")
		return nil, errors.Wrap(err, "Failed to get ValidatingWebhook CA bundle")
	}

	validatingKey := fmt.Sprintf("%s.ca-bundle", validatingWebhookConfig.WebhookRef.Name)
	validatingB64Data := base64.StdEncoding.EncodeToString(validatingCaData)
	caBundles[validatingKey] = validatingB64Data

	domainCA, err := r.getCaBundle(ctx, &corev1alpha1.WebhookConfiguration{
		SecretData: "tls.crt",
		SecretRef: corev1alpha1.SecretReference{
			Name:      "domain-certificate-ca",
			Namespace: "platform-mesh-system",
		},
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to get Domain CA bundle")
		return nil, errors.Wrap(err, "Failed to get Domain CA bundle")
	}

	caBundles["domainCA"] = base64.StdEncoding.EncodeToString(domainCA)
	caBundles["domainCADec"] = string(domainCA)

	// Cache the results
	r.caBundleCache = caBundles

	return caBundles, nil
}

func (r *KcpsetupSubroutine) getCaBundle(
	ctx context.Context,
	webhookConfig *corev1alpha1.WebhookConfiguration,
) ([]byte, error) {
	log := logger.LoadLoggerFromContext(ctx)

	caSecret := corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{
		Name:      webhookConfig.SecretRef.Name,
		Namespace: webhookConfig.SecretRef.Namespace,
	}, &caSecret)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get ca secret")
		return nil, errors.Wrap(err, "Failed to get ca secret: %s/%s", webhookConfig.SecretRef.Namespace, webhookConfig.SecretRef.Name)
	}

	caData, ok := caSecret.Data[webhookConfig.SecretData]
	if !ok {
		log.Error().Msg("Failed to get caData from secret")
		return nil, errors.New("Failed to get caData from secret: %s/%s, key: %s", webhookConfig.SecretRef.Namespace, webhookConfig.SecretRef.Name, webhookConfig.SecretData)
	}

	decodedCaData := caData
	return decodedCaData, nil
}

func (r *KcpsetupSubroutine) getAPIExportHashInventory(ctx context.Context, config *rest.Config) (map[string]string, error) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	inventory := map[string]string{}

	cs, err := r.kcpHelper.NewKcpClient(config, "root")
	if err != nil {
		return inventory, err
	}

	apiExport := kcpapiv1alpha.APIExport{}
	err = cs.Get(ctx, types.NamespacedName{Name: "tenancy.kcp.io"}, &apiExport)
	if err != nil {
		log.Err(err).Msg("Failed to get APIExport for tenancy.kcp.io")
		return inventory, errors.Wrap(err, "Failed to get APIExport for tenancy.kcp.io")
	}
	inventory["apiExportRootTenancyKcpIoIdentityHash"] = apiExport.Status.IdentityHash

	err = cs.Get(ctx, types.NamespacedName{Name: "shards.core.kcp.io"}, &apiExport)
	if err != nil {
		log.Err(err).Msg("Failed to get APIExport for shards.core.kcp.io")
		return inventory, errors.Wrap(err, "Failed to get APIExport for shards.core.kcp.io")
	}
	inventory["apiExportRootShardsKcpIoIdentityHash"] = apiExport.Status.IdentityHash

	err = cs.Get(ctx, types.NamespacedName{Name: "topology.kcp.io"}, &apiExport)
	if err != nil {
		log.Err(err).Msg("Failed to get APIExport for topology.kcp.io")
		return inventory, errors.Wrap(err, "Failed to get APIExport for topology.kcp.io")
	}
	inventory["apiExportRootTopologyKcpIoIdentityHash"] = apiExport.Status.IdentityHash

	return inventory, nil
}

func (r *KcpsetupSubroutine) applyExtraWorkspaces(ctx context.Context, config *rest.Config, inst *corev1alpha1.PlatformMesh) error {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	if inst.Spec.Kcp.ExtraWorkspaces == nil {
		return nil
	}

	for _, wsDecl := range inst.Spec.Kcp.ExtraWorkspaces {
		lastColon := strings.LastIndex(wsDecl.Path, ":")
		if lastColon == -1 {
			log.Warn().Str("path", wsDecl.Path).Msg("Invalid workspace path format for extraWorkspace, skipping. Must be 'parent:name'.")
			continue
		}
		parentPath := wsDecl.Path[:lastColon]
		workspaceName := wsDecl.Path[lastColon+1:]

		log.Debug().Str("parentPath", parentPath).Str("workspaceName", workspaceName).Msg("Processing extra workspace")

		k8sClient, err := r.kcpHelper.NewKcpClient(config, parentPath)
		if err != nil {
			return errors.Wrap(err, "Failed to create kcp client for parent workspace %s", parentPath)
		}

		ws := &kcptenancyv1alpha.Workspace{}
		ws.APIVersion = kcptenancyv1alpha.SchemeGroupVersion.String()
		ws.Kind = "Workspace"
		ws.Name = workspaceName
		ws.Spec.Type = &kcptenancyv1alpha.WorkspaceTypeReference{
			Name: kcptenancyv1alpha.WorkspaceTypeName(wsDecl.Type.Name),
			Path: wsDecl.Type.Path,
		}

		unstructuredWs, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ws)
		if err != nil {
			return errors.Wrap(err, "failed to convert workspace to unstructured")
		}
		obj := unstructured.Unstructured{Object: unstructuredWs}

		err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("platform-mesh-operator"))
		if err != nil {
			return errors.Wrap(err, "Failed to apply extra workspace: %s", obj.GetName())
		}
		log.Info().Str("workspace", wsDecl.Path).Msg("Applied extra workspace")

	}
	return nil
}

func needsPatch(existingObj, obj unstructured.Unstructured, log *logger.Logger) bool {
	sanitize := func(u *unstructured.Unstructured, expected *unstructured.Unstructured) {
		if u == nil {
			return
		}
		// Remove system-generated fields
		meta, ok := u.Object["metadata"].(map[string]interface{})
		if ok {
			delete(meta, "resourceVersion")
			delete(meta, "generation")
			delete(meta, "creationTimestamp")
			delete(meta, "uid")
			delete(meta, "managedFields")
			delete(meta, "selfLink")
			delete(meta, "finalizers")
			delete(meta, "ownerReferences")

			// Remove extra labels/annotations not present in expected
			if expected != nil {
				expectedMeta, ok2 := expected.Object["metadata"].(map[string]interface{})
				if ok2 {
					for _, field := range []string{"labels", "annotations"} {
						existingField, _ := meta[field].(map[string]interface{})
						expectedField, _ := expectedMeta[field].(map[string]interface{})
						if existingField != nil && expectedField != nil {
							for k := range existingField {
								if _, found := expectedField[k]; !found {
									delete(existingField, k)
								}
							}
							meta[field] = existingField
						}
						if existingField != nil && expectedField == nil {
							// Remove all if expected has none
							delete(meta, field)
						}
					}
				}
			}
			u.Object["metadata"] = meta
		}
		delete(u.Object, "status")
	}

	existingCopy := existingObj.DeepCopy()
	desiredCopy := obj.DeepCopy()
	sanitize(existingCopy, desiredCopy)
	sanitize(desiredCopy, desiredCopy)

	if !equality.Semantic.DeepEqual(existingCopy.Object, desiredCopy.Object) {
		// Log the diff if there is a difference and debug is enabled
		if log.GetLevel() <= zerolog.DebugLevel {
			diff := cmp.Diff(desiredCopy.Object, existingCopy.Object)
			log.Debug().Msgf("Resource difference detected:\n%s", diff)
		}
		return true
	}
	return false
}

func getExtraDefaultApiBindings(obj unstructured.Unstructured, workspacePath string, inst *corev1alpha1.PlatformMesh) []corev1alpha1.DefaultAPIBindingConfiguration {
	if inst.Spec.Kcp.ExtraDefaultAPIBindings == nil {
		return nil
	}
	res := []corev1alpha1.DefaultAPIBindingConfiguration{}
	for _, binding := range inst.Spec.Kcp.ExtraDefaultAPIBindings {
		workspaceTypePath := fmt.Sprintf("%s:%s", workspacePath, obj.GetName())
		if binding.WorkspaceTypePath == workspaceTypePath {
			found := binding
			res = append(res, found)
		}
	}

	return res
}

func HasFeatureToggle(inst *corev1alpha1.PlatformMesh, name string) string {
	for _, ft := range inst.Spec.FeatureToggles {
		if ft.Name == name {
			return "true"
		}
	}
	return "false"
}
