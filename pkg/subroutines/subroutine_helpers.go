package subroutines

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	certmanager "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	fluxcdv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxcdv1 "github.com/fluxcd/source-controller/api/v1beta2"
	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcpcorev1alpha "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/rs/zerolog/log"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	admissionv1 "k8s.io/api/admissionregistration/v1"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
)

type KcpHelper interface {
	NewKcpClient(config *rest.Config, workspacePath string) (client.Client, error)
}

type Helper struct {
}

func (h *Helper) NewKcpClient(config *rest.Config, workspacePath string) (client.Client, error) {
	config.QPS = 1000.0
	config.Burst = 2000.0
	u, err := url.Parse(config.Host)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to parse kcp host: %s", config.Host)
	}
	config.Host = u.Scheme + "://" + u.Host + "/clusters/" + workspacePath
	scheme := runtime.NewScheme()
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcpapiv1alpha.AddToScheme(scheme))
	utilruntime.Must(kcptenancyv1alpha.AddToScheme(scheme))
	utilruntime.Must(kcpcorev1alpha.AddToScheme(scheme))
	utilruntime.Must(admissionv1.AddToScheme(scheme))

	cl, err := client.New(config, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create KCP client: %w", err)
	}
	return cl, nil
}

func GetSecret(client client.Client, name string, namespace string) (*corev1.Secret, error) {
	secret := corev1.Secret{}
	err := client.Get(context.Background(), types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, &secret)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get secret")
	}
	return &secret, nil
}

func ReplaceTemplate(templateData map[string]string, templateBytes []byte) ([]byte, error) {
	funcMap := template.FuncMap{
		"indent": func(spaces int, s string) string {
			pad := strings.Repeat(" ", spaces)
			lines := strings.Split(s, "\n")
			for i, line := range lines {
				if line != "" {
					lines[i] = pad + line
				}
			}
			return strings.Join(lines, "\n")
		},
	}

	tmpl, err := template.New("manifest").Funcs(funcMap).Parse(string(templateBytes))
	if err != nil {
		return []byte{}, errors.Wrap(err, "Failed to parse template")
	}
	var result bytes.Buffer
	err = tmpl.Execute(&result, templateData)
	if err != nil {
		keys := make([]string, 0, len(templateData))
		for k := range templateData {
			keys = append(keys, k)
		}
		return []byte{}, errors.Wrap(err, "Failed to execute template with keys %v", keys)
	}
	if result.Len() == 0 {
		return []byte{}, nil
	}
	return result.Bytes(), nil
}

func ConvertToUnstructured(webhook admissionv1.MutatingWebhookConfiguration) (*unstructured.Unstructured, error) {
	// Convert the structured object to a map
	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&webhook)
	if err != nil {
		return nil, err
	}
	// Create an unstructured object and assign the map
	unstructuredObj := &unstructured.Unstructured{Object: objMap}
	unstructuredObj.SetKind("MutatingWebhookConfiguration")
	unstructuredObj.SetAPIVersion("admissionregistration.k8s.io/v1")
	unstructuredObj.SetManagedFields(nil)
	return unstructuredObj, nil
}

func GetWorkspaceDirs(dir string) []string {
	workspaces := []string{}
	// find all subdirectories named "dd-name", e.g. "01-platform-mesh-system"
	dirs, err := os.ReadDir(dir)
	if err != nil {
		// TODO: print error
		return workspaces
	}
	for _, d := range dirs {
		// check if d.Name() match the regex ^[0-9]{2}-[a-zA-Z0-9-]+$
		if d.IsDir() {
			if IsWorkspace(d.Name()) {
				workspaces = append(workspaces, d.Name())
			}
		}
	}
	return workspaces
}

func GetWorkspaceName(dir string) (string, error) {
	validWorkspaceName := regexp.MustCompile(`.*[0-9]{2}-([a-zA-Z0-9-]+)$`)
	matches := validWorkspaceName.FindAllSubmatch([]byte(dir), -1)
	if matches == nil {
		return "", fmt.Errorf("invalid workspace name: %s", dir)
	}
	last := matches[len(matches)-1]
	return string(last[1]), nil
}

func IsWorkspace(dir string) bool {
	pattern := `^[0-9]{2}-[a-zA-Z0-9-]+$`
	match, err := regexp.Match(pattern, []byte(dir))
	if err != nil {
		return false
	}
	return match
}

func ListFiles(dir string) ([]string, error) {
	files := []string{}
	// find all files in the directory
	dirs, err := os.ReadDir(dir)
	if err != nil {
		return files, errors.Wrap(err, "Failed to read directory")
	}
	for _, d := range dirs {
		if d.IsDir() {
			continue
		}
		files = append(files, d.Name())
	}
	return files, nil
}

func MergeValuesAndServices(inst *v1alpha1.PlatformMesh, templateVars apiextensionsv1.JSON, config config.OperatorConfig) (apiextensionsv1.JSON, error) {
	services := inst.Spec.Values
	var mapValues map[string]interface{}
	if len(templateVars.Raw) > 0 {
		if err := json.Unmarshal(templateVars.Raw, &mapValues); err != nil {
			return apiextensionsv1.JSON{}, err
		}
	} else {
		mapValues = map[string]interface{}{}
	}
	// Unmarshal 'services'
	var mapServices map[string]interface{}
	if len(services.Raw) > 0 {
		if err := json.Unmarshal(services.Raw, &mapServices); err != nil {
			return apiextensionsv1.JSON{}, err
		}
	} else {
		mapServices = map[string]interface{}{}
	}

	// Create 'services' key in 'values' if it doesn't exist
	if _, ok := mapValues["services"]; !ok {
		mapValues["services"] = map[string]interface{}{}
	}

	// add 'services' to mapValues["services"]
	if _, ok := mapValues["services"].(map[string]interface{}); !ok {
		return apiextensionsv1.JSON{}, fmt.Errorf("services is not a map")
	}
	for k, v := range mapServices {
		mapValues["services"].(map[string]interface{})[k] = v
	}

	mergeOCMConfig(mapValues, inst)

	mapValues["kubeConfigEnabled"] = config.RemoteRuntime.Enabled
	if config.RemoteRuntime.Enabled {
		mapValues["kubeConfigSecretName"] = config.RemoteRuntime.InfraSecretName
		mapValues["kubeConfigSecretKey"] = config.RemoteRuntime.InfraSecretKey
	}

	// Marshal back to apiextensionsv1.JSON
	mergedRaw, err := json.Marshal(mapValues)
	if err != nil {
		return apiextensionsv1.JSON{}, err
	}
	return apiextensionsv1.JSON{Raw: mergedRaw}, nil

}

func baseDomainPortProtocol(inst *v1alpha1.PlatformMesh) (string, string, int, string) {
	port := 8443
	baseDomain := "portal.localhost"
	protocol := "https"
	baseDomainPort := ""

	if inst.Spec.Exposure != nil {
		if inst.Spec.Exposure.Port != 0 {
			port = inst.Spec.Exposure.Port
		}
		if inst.Spec.Exposure.BaseDomain != "" {
			baseDomain = inst.Spec.Exposure.BaseDomain
		}
		if inst.Spec.Exposure.Protocol != "" {
			protocol = inst.Spec.Exposure.Protocol
		}
	}

	if port == 80 || port == 443 {
		baseDomainPort = baseDomain
	} else {
		baseDomainPort = fmt.Sprintf("%s:%d", baseDomain, port)
	}
	return baseDomain, baseDomainPort, port, protocol
}

func TemplateVars(ctx context.Context, inst *v1alpha1.PlatformMesh, cl client.Client) (apiextensionsv1.JSON, error) {
	baseDomain, baseDomainPort, port, protocol := baseDomainPortProtocol(inst)

	var secret corev1.Secret
	err := cl.Get(ctx, client.ObjectKey{
		Name:      "rebac-authz-webhook-cert",
		Namespace: inst.Namespace,
	}, &secret)
	if err != nil && !kerrors.IsNotFound(err) {
		return apiextensionsv1.JSON{}, errors.Wrap(err, "Failed to get secret rebac-authz-webhook-cert")
	}

	values := map[string]interface{}{
		"iamWebhookCA":         base64.StdEncoding.EncodeToString(secret.Data["ca.crt"]),
		"baseDomain":           baseDomain,
		"protocol":             protocol,
		"port":                 fmt.Sprintf("%d", port),
		"baseDomainPort":       baseDomainPort,
		"helmReleaseNamespace": inst.Namespace,
	}

	result := apiextensionsv1.JSON{}
	result.Raw, _ = json.Marshal(values)
	raw, err := json.Marshal(values)
	if err != nil {
		return apiextensionsv1.JSON{}, errors.Wrap(err, "Failed to marshal template vars")
	}
	result.Raw = raw

	return result, nil
}

func buildKubeconfig(ctx context.Context, client client.Client, kcpUrl string) (*rest.Config, error) {
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
	return buildKubeconfigFromConfig(client, &operatorCfg, kcpUrl)
}

func buildKubeconfigFromConfig(client client.Client, operatorCfg *config.OperatorConfig, kcpUrl string) (*rest.Config, error) {
	secretName := operatorCfg.KCP.ClusterAdminSecretName
	secret, err := GetSecret(client, secretName, operatorCfg.KCP.Namespace)
	if err != nil {
		return nil, fmt.Errorf("getting secret %s/platform-mesh-system: %w", secretName, err)
	}
	if secret == nil {
		return nil, fmt.Errorf("secret %s/platform-mesh-system is nil", secretName)
	}
	if secret.Data == nil {
		return nil, fmt.Errorf("secret %s/platform-mesh-system has no Data", secretName)
	}

	caData, ok := secret.Data["ca.crt"]
	if !ok || len(caData) == 0 {
		return nil, fmt.Errorf("secret %s/platform-mesh-system missing or empty key \"ca.crt\"", secretName)
	}

	tlsCrt, ok := secret.Data["tls.crt"]
	if !ok || len(tlsCrt) == 0 {
		return nil, fmt.Errorf("secret %s/platform-mesh-system missing or empty key \"tls.crt\"", secretName)
	}

	tlsKey, ok := secret.Data["tls.key"]
	if !ok || len(tlsKey) == 0 {
		return nil, fmt.Errorf("secret %s/platform-mesh-system missing or empty key \"tls.key\"", secretName)
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

func WaitForWorkspace(
	ctx context.Context,
	config *rest.Config, name string, log *logger.Logger,
	kcpHelper KcpHelper,
) error {
	client, err := kcpHelper.NewKcpClient(config, "root")
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

func ApplyManifestFromFile(
	ctx context.Context,
	path string, k8sClient client.Client, templateData map[string]string, wsPath string, inst *v1alpha1.PlatformMesh,
) error {
	log := logger.LoadLoggerFromContext(ctx)

	obj, err := unstructuredFromFile(path, templateData, log)
	if err != nil {
		return err
	}
	if obj.Object == nil {
		return nil
	}

	if obj.GetKind() == "ContentConfiguration" && obj.GetAPIVersion() == "ui.platform-mesh.io/v1alpha1" {
		if templateData["featureDisableContentConfigurations"] == "true" {
			log.Debug().Str("file", path).Str("kind", obj.GetKind()).Str("name", obj.GetName()).
				Msg("Skipping ContentConfiguration due to feature-disable-contentconfigurations toggle")
			return nil
		}
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

	existingObj := obj.DeepCopy()
	err = k8sClient.Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}, existingObj)
	if err != nil && !kerrors.IsNotFound(err) {
		return errors.Wrap(err, "Failed to get existing object: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}

	if kerrors.IsNotFound(err) || needsPatch(*existingObj, obj, log) {
		err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("platform-mesh-operator"))
		if err != nil {
			return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
		}
		log.Info().Str("file", path).Str("kind", obj.GetKind()).Str("name", obj.GetName()).Msg("Applied manifest file")
	}
	return nil
}

func ApplyDirStructure(
	ctx context.Context,
	dir string,
	kcpPath string,
	config *rest.Config,
	templateData map[string]string,
	inst *v1alpha1.PlatformMesh,
	kcpHelper KcpHelper,
) error {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", "")

	k8sClient, err := kcpHelper.NewKcpClient(config, kcpPath)
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
		log.Debug().Str("file", file).Msg("Applying file")
		path := filepath.Join(dir, file)
		err := ApplyManifestFromFile(ctx, path, k8sClient, templateData, kcpPath, inst)
		if err != nil {
			log.Warn().Err(err).Str("file", path).Msg("Failed to apply manifest file, continuing to next file in directory")
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
		err = WaitForWorkspace(ctx, config, wsName, log, kcpHelper)
		if err != nil {
			return err
		}
		err = ApplyDirStructure(ctx, dir+"/"+wsDir, fmt.Sprintf("%s:%s", kcpPath, wsName), config, templateData, inst, kcpHelper)
		if err != nil {
			return err
		}
	}

	return nil
}

func matchesConditionWithStatus(resource *unstructured.Unstructured, conditionType string, conditionStatus string) bool {
	if resource == nil {
		return false
	}
	conditions, found, err := unstructured.NestedSlice(resource.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}

	for _, condition := range conditions {
		c := condition.(map[string]interface{})
		if c["type"] == conditionType && c["status"] == conditionStatus {
			return true
		}
	}

	return false
}

// matchesStatusFieldValue checks if a resource has a specific value at a given nested field path.
// This is useful for resources like ArgoCD Applications that use nested status fields
// (e.g., status.sync.status) instead of the standard conditions array.
func matchesStatusFieldValue(resource *unstructured.Unstructured, fieldPath []string, expectedValue string) bool {
	if resource == nil || len(fieldPath) == 0 {
		return false
	}
	value, found, err := unstructured.NestedString(resource.Object, fieldPath...)
	if err != nil || !found {
		return false
	}
	return value == expectedValue
}

func unstructuredFromFile(path string, templateData map[string]string, log *logger.Logger) (unstructured.Unstructured, error) {
	manifestBytes, err := os.ReadFile(path)
	if err != nil {
		return unstructured.Unstructured{}, errors.Wrap(err, "Failed to read file, pwd: %s", path)
	}

	res, err := ReplaceTemplate(templateData, manifestBytes)
	if err != nil {
		return unstructured.Unstructured{}, errors.Wrap(err, "Failed to replace template with path: %s", path)
	}

	var objMap map[string]interface{}
	if err := yaml.Unmarshal(res, &objMap); err != nil {
		return unstructured.Unstructured{}, errors.Wrap(err, "Failed to unmarshal YAML from template %s. Output:\n%s", path, string(res))
	}

	obj := unstructured.Unstructured{Object: objMap}

	log.Debug().Str("file", path).Str("kind", obj.GetKind()).Str("name", obj.GetName()).Str("namespace", obj.GetNamespace()).Msg("Applying manifest")
	return obj, err
}

func GetClientAndRestConfig(kubeconfig string) (client.Client, *rest.Config, error) {
	if kubeconfig == "" {
		config, err := rest.InClusterConfig()
		if err != nil {
			log.Error().Err(err).Msg("unable to get in-cluster deployment kubeconfig")
			return nil, nil, err
		}
		deployClient, err := client.New(config, client.Options{Scheme: GetClientScheme()})
		if err != nil {
			log.Error().Err(err).Msg("unable to create in-cluster deployment client")
			return nil, nil, err
		}
		return deployClient, config, nil
	}

	config, err := clientcmd.LoadFromFile(kubeconfig)
	if err != nil {
		log.Error().Err(err).Msg("unable to build Config")
		return nil, nil, err
	}
	cfgBytes, err := clientcmd.Write(*config)
	if err != nil {
		log.Error().Err(err).Msg("unable to serialize config to bytes")
		return nil, nil, err
	}
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(cfgBytes)
	if err != nil {
		log.Error().Err(err).Msg("unable to build rest config from kubeconfig")
		return nil, nil, err
	}
	deployClient, err := client.New(restCfg, client.Options{Scheme: GetClientScheme()})
	if err != nil {
		log.Error().Err(err).Msg("unable to create client")
		return nil, nil, err
	}
	return deployClient, restCfg, nil

}

func GetClientScheme() *runtime.Scheme {

	var gvk = schema.GroupVersionKind{
		Group:   "delivery.ocm.software",
		Version: "v1alpha1",
		Kind:    "Resource",
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(certmanager.AddToScheme(scheme))
	utilruntime.Must(fluxcdv1.AddToScheme(scheme))
	utilruntime.Must(fluxcdv2.AddToScheme(scheme))

	scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(gvk.GroupVersion().WithKind(gvk.Kind+"List"), &unstructured.UnstructuredList{})

	return scheme
}

// GetDeploymentTechnologyFromProfile extracts deploymentTechnology from the profile ConfigMap for a PlatformMesh instance.
// Returns "fluxcd" as default if not found or on error.
func GetDeploymentTechnologyFromProfile(ctx context.Context, cl client.Client, inst *v1alpha1.PlatformMesh) string {
	// Get the profile ConfigMap
	var configMapName, configMapNamespace string
	if inst.Spec.ProfileConfigMap != nil {
		configMapName = inst.Spec.ProfileConfigMap.Name
		configMapNamespace = inst.Spec.ProfileConfigMap.Namespace
		if configMapNamespace == "" {
			configMapNamespace = inst.Namespace
		}
	} else {
		// Use default ConfigMap name
		configMapName = inst.Name + "-profile"
		configMapNamespace = inst.Namespace
	}

	configMap := &corev1.ConfigMap{}
	if err := cl.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: configMapNamespace}, configMap); err != nil {
		// Log error but return default - this is expected in some cases
		return "fluxcd" // default to fluxcd if ConfigMap not found
	}

	// Parse profile YAML
	profileYAML, ok := configMap.Data["profile.yaml"]
	if !ok {
		return "fluxcd" // default to fluxcd if profile key not found
	}

	var profile map[string]interface{}
	if err := yaml.Unmarshal([]byte(profileYAML), &profile); err != nil {
		return "fluxcd" // default to fluxcd if parsing fails
	}

	// Check infra section first
	if infra, ok := profile["infra"].(map[string]interface{}); ok {
		if dt, ok := infra["deploymentTechnology"].(string); ok && dt != "" {
			return strings.ToLower(dt)
		}
	}

	// Check components section
	if components, ok := profile["components"].(map[string]interface{}); ok {
		if dt, ok := components["deploymentTechnology"].(string); ok && dt != "" {
			return strings.ToLower(dt)
		}
	}

	// Default to fluxcd if not found
	return "fluxcd"
}

func getExternalKcpHost(inst *v1alpha1.PlatformMesh, cfg *config.OperatorConfig) string {
	// If kcp-url is explicitly configured, use it
	if cfg.KCP.Url != "" {
		return cfg.KCP.Url
	}
	if inst.Spec.Exposure == nil {
		return fmt.Sprintf("https://%s-front-proxy.%s:%s", cfg.KCP.FrontProxyName, cfg.KCP.Namespace, cfg.KCP.FrontProxyPort)
	}
	kcpUrl := inst.Spec.Exposure.Protocol + "://" + inst.Spec.Exposure.BaseDomain + ":" + fmt.Sprintf("%d", inst.Spec.Exposure.Port)
	return kcpUrl
}

func getInternalKcpHost(cfg *config.OperatorConfig) string {
	return fmt.Sprintf("https://%s-front-proxy.%s:%s", cfg.KCP.FrontProxyName, cfg.KCP.Namespace, cfg.KCP.FrontProxyPort)
}
