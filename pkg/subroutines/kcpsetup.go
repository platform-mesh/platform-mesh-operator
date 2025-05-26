package subroutines

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"github.com/openmfp/golang-commons/controller/lifecycle"
	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type KcpsetupSubroutine struct {
	client       client.Client
	kcpHelper    KcpHelper
	kcpUrl       string
	helm         HelmGetter
	kcpDirectory string
	// Cache for CA bundles to avoid redundant secret lookups
	caBundleCache map[string]string
}

const (
	KcpsetupSubroutineName      = "KcpsetupSubroutine"
	KcpsetupSubroutineFinalizer = "openmfp.core.openmfp.org/finalizer"
)

func NewKcpsetupSubroutine(client client.Client, helper KcpHelper, kcpdir string, kcpUrl string) *KcpsetupSubroutine {
	return &KcpsetupSubroutine{
		client:        client,
		kcpDirectory:  kcpdir,
		kcpUrl:        kcpUrl,
		kcpHelper:     helper,
		helm:          DefaultHelmGetter{},
		caBundleCache: make(map[string]string),
	}
}

func (r *KcpsetupSubroutine) GetName() string {
	return KcpsetupSubroutineName
}

func (r *KcpsetupSubroutine) Finalize(
	ctx context.Context, runtimeObj lifecycle.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	instance := runtimeObj.(*corev1alpha1.OpenMFP)
	_ = instance

	return ctrl.Result{}, nil // TODO: Implement
}

func (r *KcpsetupSubroutine) Finalizers() []string { // coverage-ignore
	return []string{KcpsetupSubroutineFinalizer}
}

func (r *KcpsetupSubroutine) Process(ctx context.Context, runtimeObj lifecycle.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	inst := runtimeObj.(*corev1alpha1.OpenMFP)
	log.Debug().Str("subroutine", r.GetName()).Str("name", inst.Name).Msg("Processing OpenMFP resource")

	// Wait for kcp release to be ready before continuing
	rel, err := r.helm.GetRelease(ctx, r.client, "kcp", inst.Namespace)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get KCP Release")
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	if !isReady(rel) {
		log.Info().Msg("KCP Release is not ready.. Retry in 5 seconds")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Build kcp kubeonfig
	cfg, err := buildKubeconfig(r.client, r.kcpUrl, "kcp-cluster-admin-client-cert")
	if err != nil {
		log.Error().Err(err).Msg("Failed to build kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to build kubeconfig"), true, false)
	}

	// generate kcp secret
	err = r.createKcpResources(ctx, cfg, r.kcpDirectory, inst)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create kcp workspaces")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to create kcp workspaces"), true, false)
	}

	// update workspace status
	inst.Status.KcpWorkspaces = []corev1alpha1.KcpWorkspace{
		{
			Name:  "root:openmfp-system",
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

// isReady checks the Ready Condition if it has status true
func isReady(release *unstructured.Unstructured) bool {
	if release == nil {
		return false
	}
	conditions, found, err := unstructured.NestedSlice(release.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}

	for _, condition := range conditions {
		c := condition.(map[string]interface{})
		if c["type"] == "Ready" && c["status"] == "True" {
			return true
		}
	}

	return false
}

func buildKubeconfig(client client.Client, kcpUrl string, secretName string) (*rest.Config, error) {
	secret, err := GetSecret(client, secretName, "openmfp-system")
	if err != nil {
		return nil, fmt.Errorf("getting secret %s/openmfp-system: %w", secretName, err)
	}
	if secret == nil {
		return nil, fmt.Errorf("secret %s/openmfp-system is nil", secretName)
	}
	if secret.Data == nil {
		return nil, fmt.Errorf("secret %s/openmfp-system has no Data", secretName)
	}

	caData, ok := secret.Data["ca.crt"]
	if !ok || len(caData) == 0 {
		return nil, fmt.Errorf("secret %s/openmfp-system missing or empty key \"ca.crt\"", secretName)
	}

	tlsCrt, ok := secret.Data["tls.crt"]
	if !ok || len(tlsCrt) == 0 {
		return nil, fmt.Errorf("secret %s/openmfp-system missing or empty key \"tls.crt\"", secretName)
	}

	tlsKey, ok := secret.Data["tls.key"]
	if !ok || len(tlsKey) == 0 {
		return nil, fmt.Errorf("secret %s/openmfp-system missing or empty key \"tls.key\"", secretName)
	}

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters = map[string]*clientcmdapi.Cluster{
		"kcp": {
			Server:                   kcpUrl,
			CertificateAuthorityData: secret.Data["ca.crt"],
		},
	}
	cfg.Contexts = map[string]*clientcmdapi.Context{
		"admin": {
			Cluster:  "kcp",
			AuthInfo: "admin",
		},
	}
	cfg.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		"admin": {
			ClientCertificateData: secret.Data["tls.crt"],
			ClientKeyData:         secret.Data["tls.key"],
		},
	}
	cfg.CurrentContext = "admin"
	return clientcmd.NewDefaultClientConfig(*cfg, nil).ClientConfig()
}

func (r *KcpsetupSubroutine) createKcpResources(ctx context.Context, config *rest.Config, dir string, inst *corev1alpha1.OpenMFP) error {
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

	err = r.applyDirStructure(ctx, dir, "root", config, templateData, inst)
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

func (r *KcpsetupSubroutine) applyDirStructure(
	ctx context.Context,
	dir string,
	kcpPath string,
	config *rest.Config,
	templateData map[string]string,
	inst *corev1alpha1.OpenMFP,
) error {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	k8sClient, err := r.kcpHelper.NewKcpClient(config, kcpPath)
	if err != nil {
		return err
	}

	// apply all manifest files in the current directory first
	files, err := ListFiles(dir)
	if err != nil {
		return errors.Wrap(err, "Failed to list files in workspace")
	}
	var errApplyManifests error = nil
	for _, file := range files {
		path := dir + "/" + file
		err := applyManifestFromFile(ctx, path, k8sClient, templateData, kcpPath, inst)
		if err != nil {
			log.Warn().Err(err).Str("file", path).Msg("Failed to apply manifest file, continueing to next file in directory")
			errApplyManifests = err
		}
	}
	if errApplyManifests != nil {
		return errApplyManifests
	}

	for _, wsDir := range GetWorkspaceDirs(dir) {
		wsName, err := GetWorkspaceName(wsDir)
		if err != nil {
			log.Warn().Err(err).Str("Directory", dir).Str("wsName", wsName).Msg("Failed to get workspace path, skipping")
			continue
		}
		err = r.waitForWorkspace(ctx, config, wsName, log)
		if err != nil {
			return err
		}
		err = r.applyDirStructure(ctx, dir+"/"+wsDir, fmt.Sprintf("%s:%s", kcpPath, wsName), config, templateData, inst)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *KcpsetupSubroutine) waitForWorkspace(
	ctx context.Context,
	config *rest.Config, name string, log *logger.Logger,
) error {
	client, err := r.kcpHelper.NewKcpClient(config, "root")
	if err != nil {
		return err
	}

	err = wait.PollUntilContextTimeout(
		ctx, time.Second, time.Second*15, true,
		func(ctx context.Context) (bool, error) {
			ws := &kcptenancyv1alpha.Workspace{}
			if err := client.Get(ctx, types.NamespacedName{Name: name}, ws); err != nil {
				return false, nil //nolint:nilerr
			}
			ready := ws.Status.Phase == "Ready"
			log.Info().Str("workspace", name).Bool("ready", ready).Msg("waiting for workspace to be ready")
			return ready, nil
		})

	if err != nil {
		return fmt.Errorf("workspace %s did not become ready: %w", name, err)
	}
	return err
}

func applyManifestFromFile(
	ctx context.Context,
	path string, k8sClient client.Client, templateData map[string]string, wsPath string, inst *corev1alpha1.OpenMFP,
) error {
	log := logger.LoadLoggerFromContext(ctx)

	obj, err := unstructuredFromFile(path, templateData, log)
	if err != nil {
		return err
	}

	if obj.GetKind() == "WorkspaceType" && obj.GetAPIVersion() == "tenancy.kcp.io/v1alpha1" {
		extraDefaultApiBindings := getExtraDefaultApiBindings(obj, wsPath, inst)
		currentDefAPiBindings, found, err := unstructured.NestedSlice(obj.Object, "spec", "defaultAPIBindings")
		if err != nil || !found {
			currentDefAPiBindings = []interface{}{}
		}
		for _, v := range extraDefaultApiBindings {
			newExport := kcptenancyv1alpha.APIExportReference{Path: v.Path, Export: v.Export}
			var m map[string]interface{}
			b, marshalErr := yaml.Marshal(newExport)
			if marshalErr != nil {
				return errors.Wrap(marshalErr, "Failed to marshal APIExportReference")
			}
			if unmarshalErr := yaml.Unmarshal(b, &m); unmarshalErr != nil {
				return errors.Wrap(unmarshalErr, "Failed to unmarshal APIExportReference")
			}
			currentDefAPiBindings = append(currentDefAPiBindings, m)
		}
		err = unstructured.SetNestedSlice(obj.Object, currentDefAPiBindings, "spec", "defaultAPIBindings")
		if err != nil {
			return errors.Wrap(err, "Failed to set defaultAPIBindings")
		}
	}

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("openmfp-operator"))
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}
	return nil
}

func getExtraDefaultApiBindings(obj unstructured.Unstructured, workspacePath string, inst *corev1alpha1.OpenMFP) []corev1alpha1.DefaultAPIBindingConfiguration {
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

func unstructuredFromFile(path string, templateData map[string]string, log *logger.Logger) (unstructured.Unstructured, error) {
	manifestBytes, err := os.ReadFile(path)
	if err != nil {
		return unstructured.Unstructured{}, errors.Wrap(err, "Failed to read file, pwd: %s", path)
	}
	log.Debug().Str("file", path).Str("template", string(manifestBytes)).Str("templateData", fmt.Sprintf("%+v", templateData)).Msg("Replacing template")

	res, err := ReplaceTemplate(templateData, manifestBytes)
	if err != nil {
		return unstructured.Unstructured{}, errors.Wrap(err, "Failed to replace template with path: %s", path)
	}

	var objMap map[string]interface{}
	if err := yaml.Unmarshal(res, &objMap); err != nil {
		return unstructured.Unstructured{}, errors.Wrap(err, "Failed to unmarshal YAML from template %s. Output:\n%s", path, string(res))
	}

	log.Debug().Str("obj", fmt.Sprintf("%+v", objMap)).Msg("Unmarshalled object")

	obj := unstructured.Unstructured{Object: objMap}

	log.Debug().Str("file", path).Str("kind", obj.GetKind()).Str("name", obj.GetName()).Str("namespace", obj.GetNamespace()).Msg("Applying manifest")
	return obj, err
}
