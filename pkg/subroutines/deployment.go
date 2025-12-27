package subroutines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"text/template"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	_ "embed"

	"github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/merge"
)

const DeploymentSubroutineName = "DeploymentSubroutine"

type DeploymentSubroutine struct {
	clientInfra              client.Client
	clientRuntime            client.Client
	cfg                      *pmconfig.CommonServiceConfig
	workspaceDirectory       string
	gotemplatesInfraDir      string
	gotemplatesComponentsDir string
	cfgOperator              *config.OperatorConfig
	restConfig               *rest.Config
}

//go:embed profile-infra.yaml
var profileInfraEmbedded []byte

//go:embed profile-components.yaml
var profileComponentsEmbedded []byte

const (
	profileConfigMapKey           = "profile.yaml"
	defaultProfileConfigMapSuffix = "-profile"
)

func NewDeploymentSubroutine(clientRuntime client.Client, clientInfra client.Client, cfg *pmconfig.CommonServiceConfig, operatorCfg *config.OperatorConfig) *DeploymentSubroutine {
	workspaceDir := filepath.Join(operatorCfg.WorkspaceDir, "/manifests/k8s/")
	// gotemplates is at the root level, relative to WorkspaceDir
	gotemplatesInfraDir := filepath.Join(operatorCfg.WorkspaceDir, "gotemplates/infra")
	gotemplatesComponentsDir := filepath.Join(operatorCfg.WorkspaceDir, "gotemplates/components")

	// REST config will be set via SetRestConfig() from the manager
	// Initialize with nil - it will be set by the controller
	sub := &DeploymentSubroutine{
		cfg:                      cfg,
		clientInfra:              clientInfra,
		clientRuntime:            clientRuntime,
		workspaceDirectory:       workspaceDir,
		gotemplatesInfraDir:      gotemplatesInfraDir,
		gotemplatesComponentsDir: gotemplatesComponentsDir,
		cfgOperator:              operatorCfg,
		restConfig:               nil, // Will be set via SetRestConfig()
	}

	return sub
}

// SetRestConfig sets the REST config for the dynamic client provider.
// This should be called after creation to ensure the correct REST config is used.
func (r *DeploymentSubroutine) SetRestConfig(restConfig *rest.Config) error {
	if restConfig == nil {
		return fmt.Errorf("rest config cannot be nil")
	}
	r.restConfig = restConfig
	return nil
}

// getOrCreateProfileConfigMap ensures the profile ConfigMap exists, creating a default one if needed.
func (r *DeploymentSubroutine) getOrCreateProfileConfigMap(ctx context.Context, inst *v1alpha1.PlatformMesh) (*corev1.ConfigMap, error) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	var configMapName, configMapNamespace string
	if inst.Spec.ProfileConfigMap != nil {
		configMapName = inst.Spec.ProfileConfigMap.Name
		configMapNamespace = inst.Spec.ProfileConfigMap.Namespace
		if configMapNamespace == "" {
			configMapNamespace = inst.Namespace
		}
	} else {
		// Use default ConfigMap name
		configMapName = inst.Name + defaultProfileConfigMapSuffix
		configMapNamespace = inst.Namespace
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: configMapNamespace,
		},
	}

	// Try to get existing ConfigMap
	err := r.clientRuntime.Get(ctx, client.ObjectKeyFromObject(configMap), configMap)
	if err == nil {
		// ConfigMap exists, verify it has the required key
		if _, ok := configMap.Data[profileConfigMapKey]; !ok {
			return nil, fmt.Errorf("configMap %s/%s exists but does not contain key %s", configMapNamespace, configMapName, profileConfigMapKey)
		}
		log.Debug().Str("configmap", configMapName).Str("namespace", configMapNamespace).Msg("Using existing profile ConfigMap")
		return configMap, nil
	}

	if !kerrors.IsNotFound(err) {
		return nil, errors.Wrap(err, "failed to get profile ConfigMap")
	}

	// ConfigMap doesn't exist, create default one
	log.Info().Str("configmap", configMapName).Str("namespace", configMapNamespace).Msg("Creating default profile ConfigMap")

	// Create unified profile from embedded files
	unifiedProfile, err := r.createUnifiedProfile()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create unified profile")
	}

	configMap.Data = map[string]string{
		profileConfigMapKey: unifiedProfile,
	}

	// Set owner reference to the PlatformMesh instance
	if err := controllerutil.SetControllerReference(inst, configMap, r.clientRuntime.Scheme()); err != nil {
		return nil, errors.Wrap(err, "failed to set controller reference")
	}

	if err := r.clientRuntime.Create(ctx, configMap); err != nil {
		return nil, errors.Wrap(err, "failed to create default profile ConfigMap")
	}

	log.Info().Str("configmap", configMapName).Str("namespace", configMapNamespace).Msg("Created default profile ConfigMap")
	return configMap, nil
}

// createUnifiedProfile creates a unified profile YAML combining infra and components sections.
func (r *DeploymentSubroutine) createUnifiedProfile() (string, error) {
	// Parse infra profile
	var infraData map[string]interface{}
	if err := yaml.Unmarshal(profileInfraEmbedded, &infraData); err != nil {
		return "", errors.Wrap(err, "Failed to parse embedded profile-infra.yaml")
	}

	// Parse components profile
	var componentsData map[string]interface{}
	if err := yaml.Unmarshal(profileComponentsEmbedded, &componentsData); err != nil {
		return "", errors.Wrap(err, "Failed to parse embedded profile-components.yaml")
	}

	// Create unified structure
	unified := map[string]interface{}{
		"infra":      infraData,
		"components": componentsData,
	}

	// Marshal to YAML
	unifiedYAML, err := yaml.Marshal(unified)
	if err != nil {
		return "", errors.Wrap(err, "Failed to marshal unified profile")
	}

	return string(unifiedYAML), nil
}

// loadProfileFromConfigMap loads the profile from the ConfigMap and returns infra and components sections.
func (r *DeploymentSubroutine) loadProfileFromConfigMap(ctx context.Context, inst *v1alpha1.PlatformMesh) (infraProfile string, componentsProfile string, err error) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	configMap, err := r.getOrCreateProfileConfigMap(ctx, inst)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to get or create profile ConfigMap")
	}

	profileYAML, ok := configMap.Data[profileConfigMapKey]
	if !ok {
		return "", "", fmt.Errorf("configMap %s/%s does not contain key %s", configMap.Namespace, configMap.Name, profileConfigMapKey)
	}

	// Parse unified profile
	var unifiedProfile map[string]interface{}
	if err := yaml.Unmarshal([]byte(profileYAML), &unifiedProfile); err != nil {
		return "", "", errors.Wrap(err, "failed to parse profile YAML from ConfigMap")
	}

	// Extract infra section
	infraData, ok := unifiedProfile["infra"]
	if !ok {
		return "", "", fmt.Errorf("profile ConfigMap does not contain 'infra' section")
	}
	infraYAML, err := yaml.Marshal(infraData)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to marshal infra profile")
	}

	// Extract components section
	componentsData, ok := unifiedProfile["components"]
	if !ok {
		return "", "", fmt.Errorf("profile ConfigMap does not contain 'components' section")
	}
	componentsYAML, err := yaml.Marshal(componentsData)
	if err != nil {
		return "", "", errors.Wrap(err, "Failed to marshal components profile")
	}

	log.Debug().Str("configmap", configMap.Name).Str("namespace", configMap.Namespace).Msg("Loaded profile from ConfigMap")
	return string(infraYAML), string(componentsYAML), nil
}

func (r *DeploymentSubroutine) GetName() string {
	return DeploymentSubroutineName
}

func (r *DeploymentSubroutine) Finalize(_ context.Context, _ runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func (r *DeploymentSubroutine) Finalizers(instance runtimeobject.RuntimeObject) []string { // coverage-ignore
	return []string{}
}

func (r *DeploymentSubroutine) Process(ctx context.Context, runtimeObj runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	inst := runtimeObj.(*v1alpha1.PlatformMesh)
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)

	// Create DeploymentComponents Version
	templateVars, err := TemplateVars(ctx, inst, r.clientRuntime)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Render and apply infra templates directly from gotemplates/infra/infra using profile-infra.yaml
	oErr := r.renderAndApplyInfraTemplates(ctx, inst, templateVars)
	if oErr != nil {
		log.Error().Err(oErr.Err()).Msg("Failed to render and apply infra templates")
		return ctrl.Result{}, oErr
	}
	log.Debug().Msg("Successfully rendered and applied infra templates")

	oErr = r.renderAndApplyRuntimeTemplates(ctx, inst, templateVars)
	if oErr != nil {
		log.Error().Err(oErr.Err()).Msg("Failed to render and apply runtime templates")
		return ctrl.Result{}, oErr
	}
	log.Debug().Msg("Successfully rendered and applied runtime templates")

	// Wait for cert-manager to be ready
	rel, err := getHelmRelease(ctx, r.clientInfra, "cert-manager", inst.Namespace)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get cert-manager Release")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	if !matchesConditionWithStatus(rel, "Ready", "True") {
		log.Info().Msg("cert-manager Release is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("cert-manager Release is not ready"), true, false)
	}

	// Render and apply components templates (HelmReleases + OCM Resources) using profile-components.yaml
	oErr = r.renderAndApplyComponentsInfraTemplates(ctx, inst, templateVars)
	if oErr != nil {
		log.Error().Err(oErr.Err()).Msg("Failed to render and apply components infra templates")
		return ctrl.Result{}, oErr
	}
	log.Debug().Msg("Successfully rendered and applied components infra templates")

	oErr = r.renderAndApplyComponentsRuntimeTemplates(ctx, inst, templateVars)
	if oErr != nil {
		log.Error().Err(oErr.Err()).Msg("Failed to render and apply components runtime templates")
		return ctrl.Result{}, oErr
	}
	log.Debug().Msg("Successfully rendered and applied components runtime templates")

	_, oErr = r.manageAuthorizationWebhookSecrets(ctx, inst)
	if oErr != nil {
		log.Info().Msg("Failed to manage authorization webhook secrets")
		return ctrl.Result{}, oErr
	}

	// Check if istio-proxy is injected
	// At he boostrap time of the cluster the operator will install istio. Later in the Process the operator needs
	// to communicate via the proxy with KCP. Once Istio is up and running the operator will be restarted to ensure
	// this communication will work
	if r.cfgOperator.Subroutines.Deployment.EnableIstio {

		// Wait for istiod release to be ready before continuing
		rel, err := getHelmRelease(ctx, r.clientInfra, "istio-istiod", inst.Namespace)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get istio-istiod Release")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}

		if !matchesConditionWithStatus(rel, "Ready", "True") {
			log.Info().Msg("istio-istiod Release is not ready..")
			return ctrl.Result{}, errors.NewOperatorError(errors.New("istio-istiod Release is not ready"), true, false)
		}

		hasProxy, pod, err := r.hasIstioProxyInjected(ctx, "platform-mesh-operator", "platform-mesh-system")
		if err != nil {
			log.Error().Err(err).Msg("Failed to check if istio-proxy is injected")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
		// When running the operator locally there will never be a proxy
		if !r.cfg.IsLocal && !hasProxy {
			log.Info().Msg("Restarting operator to ensure istio-proxy is injected")
			err := r.clientInfra.Delete(ctx, pod)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete istio-proxy pod")
				return ctrl.Result{}, errors.NewOperatorError(err, false, false)
			}
			// Forcing a pod restart
			os.Exit(0)
		}
	}

	// Wait for kcp release to be ready before continuing
	rootShard := &unstructured.Unstructured{}
	rootShard.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "RootShard"})
	// Wait for root shard to be ready
	err = r.clientRuntime.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.RootShardName, Namespace: operatorCfg.KCP.Namespace}, rootShard)
	if err != nil || !matchesConditionWithStatus(rootShard, "Available", "True") {
		log.Info().Msg("RootShard is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("RootShard is not ready"), true, false)
	}

	frontProxy := &unstructured.Unstructured{}
	frontProxy.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "FrontProxy"})
	// Wait for root shard to be ready
	err = r.clientRuntime.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.FrontProxyName, Namespace: operatorCfg.KCP.Namespace}, frontProxy)
	if err != nil || !matchesConditionWithStatus(frontProxy, "Available", "True") {
		log.Info().Msg("FrontProxy is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("FrontProxy is not ready"), true, false)
	}
	return ctrl.Result{}, nil
}

// templateVarsFromProfileInfra parses profile-infra.yaml and merges it with templateVars for rendering gotemplates/infra
func (r *DeploymentSubroutine) templateVarsFromProfileInfra(ctx context.Context, inst *v1alpha1.PlatformMesh, templateVars apiextensionsv1.JSON, config *config.OperatorConfig) (map[string]interface{}, error) {
	// Load profile from ConfigMap
	infraProfile, _, err := r.loadProfileFromConfigMap(ctx, inst)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load profile from ConfigMap")
	}

	// Parse profile-infra.yaml
	var profileData map[string]interface{}
	if err := yaml.Unmarshal([]byte(infraProfile), &profileData); err != nil {
		return nil, errors.Wrap(err, "Failed to parse profile-infra.yaml")
	}

	// Parse templateVars JSON
	var templateVarsMap map[string]interface{}
	if len(templateVars.Raw) > 0 {
		if err := json.Unmarshal(templateVars.Raw, &templateVarsMap); err != nil {
			return nil, errors.Wrap(err, "Failed to parse templateVars")
		}
	} else {
		templateVarsMap = make(map[string]interface{})
	}

	// Add instance-specific fields
	profileData["releaseNamespace"] = inst.Namespace
	profileData["kubeConfigEnabled"] = config.RemoteRuntime.Enabled
	if config.RemoteRuntime.Enabled {
		profileData["kubeConfigSecretName"] = config.RemoteRuntime.InfraSecretName
		profileData["kubeConfigSecretKey"] = config.RemoteRuntime.InfraSecretKey
	}

	// Merge profile-infra.yaml (base) with templateVars (overrides)
	// templateVars take precedence over profile values
	log, err := logger.New(logger.DefaultConfig())
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create logger")
	}
	tmplVars, err := merge.MergeMaps(profileData, templateVarsMap, log)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to merge profile-infra.yaml with templateVars")
	}

	// Convert certManager.values from map to indented YAML string for template insertion
	if certManager, ok := tmplVars["certManager"].(map[string]interface{}); ok {
		if values, ok := certManager["values"].(map[string]interface{}); ok {
			valuesBytes, err := yaml.Marshal(values)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to marshal cert-manager values")
			}
			// Indent the values YAML for embedding in HelmRelease (4 spaces)
			indented := strings.TrimSpace(string(valuesBytes))
			lines := strings.Split(indented, "\n")
			indentedLines := make([]string, len(lines))
			for i, line := range lines {
				indentedLines[i] = "    " + line
			}
			certManager["values"] = strings.Join(indentedLines, "\n")
			tmplVars["certManager"] = certManager
		}
	}

	// Convert traefik.values from map to indented YAML string for template insertion
	if traefik, ok := tmplVars["traefik"].(map[string]interface{}); ok {
		if values, ok := traefik["values"].(map[string]interface{}); ok {
			valuesBytes, err := yaml.Marshal(values)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to marshal traefik values")
			}
			// Indent the values YAML for embedding in HelmRelease (4 spaces)
			indented := strings.TrimSpace(string(valuesBytes))
			lines := strings.Split(indented, "\n")
			indentedLines := make([]string, len(lines))
			for i, line := range lines {
				indentedLines[i] = "    " + line
			}
			tmplVars["traefikValues"] = strings.Join(indentedLines, "\n")
		}
	}

	// Ensure helmReleaseNamespace is set (from templateVars or use releaseNamespace)
	if _, ok := tmplVars["helmReleaseNamespace"]; !ok {
		tmplVars["helmReleaseNamespace"] = inst.Namespace
	}

	return tmplVars, nil
}

// buildRuntimeTemplateVars merges profile-infra.yaml, templateVars, PlatformMesh.spec, and profile-components.yaml services
// for rendering gotemplates/infra/runtime templates
func (r *DeploymentSubroutine) buildRuntimeTemplateVars(ctx context.Context, inst *v1alpha1.PlatformMesh, templateVars apiextensionsv1.JSON) (map[string]interface{}, error) {
	log := logger.LoadLoggerFromContext(ctx)

	// Load profile from ConfigMap
	infraProfile, componentsProfile, err := r.loadProfileFromConfigMap(ctx, inst)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load profile from ConfigMap")
	}

	// Start with profile-infra.yaml as base (runtime templates need infra profile data)
	var profileData map[string]interface{}
	if err := yaml.Unmarshal([]byte(infraProfile), &profileData); err != nil {
		return nil, errors.Wrap(err, "Failed to parse profile-infra.yaml for runtime templates")
	}

	// Parse templateVars JSON
	var templateVarsMap map[string]interface{}
	if len(templateVars.Raw) > 0 {
		if err := json.Unmarshal(templateVars.Raw, &templateVarsMap); err != nil {
			return nil, errors.Wrap(err, "Failed to parse templateVars")
		}
	} else {
		templateVarsMap = make(map[string]interface{})
	}

	// Merge profile-infra.yaml (base) with templateVars (overrides)
	baseVars, err := merge.MergeMaps(profileData, templateVarsMap, log)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to merge profile-infra.yaml with templateVars")
	}

	// Merge PlatformMesh.spec.Values
	var specValues map[string]interface{}
	if len(inst.Spec.Values.Raw) > 0 {
		if err := json.Unmarshal(inst.Spec.Values.Raw, &specValues); err != nil {
			return nil, errors.Wrap(err, "Failed to parse PlatformMesh.spec.Values")
		}
		var err error
		baseVars, err = merge.MergeMaps(baseVars, specValues, log)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to merge PlatformMesh.spec.Values")
		}
	}

	// Merge PlatformMesh.spec.OCM config
	if inst.Spec.OCM != nil {
		ocmConfig := make(map[string]interface{})
		if inst.Spec.OCM.Repo != nil {
			ocmConfig["repo"] = map[string]interface{}{
				"name": inst.Spec.OCM.Repo.Name,
			}
		}
		if inst.Spec.OCM.Component != nil {
			ocmConfig["component"] = map[string]interface{}{
				"name": inst.Spec.OCM.Component.Name,
			}
		}
		if len(inst.Spec.OCM.ReferencePath) > 0 {
			refPath := make([]interface{}, len(inst.Spec.OCM.ReferencePath))
			for i, el := range inst.Spec.OCM.ReferencePath {
				refPath[i] = map[string]interface{}{"name": el.Name}
			}
			ocmConfig["referencePath"] = refPath
		}
		if len(ocmConfig) > 0 {
			// Merge OCM config into existing ocm key if present
			if existingOcm, ok := baseVars["ocm"].(map[string]interface{}); ok {
				var err error
				ocmConfig, err = merge.MergeMaps(existingOcm, ocmConfig, log)
				if err != nil {
					return nil, errors.Wrap(err, "Failed to merge OCM config")
				}
			}
			baseVars["ocm"] = ocmConfig
		}
	}

	// Get profile-components.yaml services
	// Render profile-components.yaml as a Go template with templateVars
	tmpl, err := template.New("profile-components").Funcs(templateFuncMap()).Parse(componentsProfile)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to parse profile-components.yaml template")
	}

	var buf bytes.Buffer
	// Use templateVars as Values for rendering profile-components.yaml
	if err := tmpl.Execute(&buf, map[string]interface{}{"Values": baseVars}); err != nil {
		return nil, errors.Wrap(err, "Failed to execute profile-components.yaml template")
	}

	// Parse the rendered YAML
	var profileComponentsData map[string]interface{}
	if err := yaml.Unmarshal(buf.Bytes(), &profileComponentsData); err != nil {
		return nil, errors.Wrap(err, "Failed to unmarshal rendered profile-components.yaml")
	}

	// Extract services from profile-components.yaml
	if services, ok := profileComponentsData["services"].(map[string]interface{}); ok {
		// Merge services into baseVars
		if existingServices, ok := baseVars["services"].(map[string]interface{}); ok {
			// Merge services from profile into existing services
			mergedServices, err := merge.MergeMaps(existingServices, services, log)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to merge services from profile-components.yaml")
			}
			baseVars["services"] = mergedServices
		} else {
			baseVars["services"] = services
		}
	}

	// Add instance-specific fields
	baseVars["releaseNamespace"] = inst.Namespace
	baseVars["helmReleaseNamespace"] = inst.Namespace // Some templates use this
	baseVars["kubeConfigEnabled"] = r.cfgOperator.RemoteRuntime.Enabled
	if r.cfgOperator.RemoteRuntime.Enabled {
		baseVars["kubeConfigSecretName"] = r.cfgOperator.RemoteRuntime.InfraSecretName
		baseVars["kubeConfigSecretKey"] = r.cfgOperator.RemoteRuntime.InfraSecretKey
	}

	return baseVars, nil
}

// helper: functions for Helm-like templates in components gotemplates
func isZeroValue(v interface{}) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return rv.Len() == 0
	case reflect.Slice, reflect.Map:
		return rv.Len() == 0
	}
	return rv.IsZero()
}

func templateFuncMap() template.FuncMap {
	return template.FuncMap{
		"default": func(d, v interface{}) interface{} {
			if isZeroValue(v) {
				return d
			}
			return v
		},
		"toYaml": func(v interface{}) (string, error) {
			b, err := yaml.Marshal(v)
			return string(b), err
		},
		"nindent": func(spaces int, s string) string {
			if s == "" {
				return ""
			}
			pad := strings.Repeat(" ", spaces)
			lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
			// Filter out empty lines at the start
			startIdx := 0
			for startIdx < len(lines) && strings.TrimSpace(lines[startIdx]) == "" {
				startIdx++
			}
			if startIdx >= len(lines) {
				return ""
			}
			// Filter out empty lines at the end
			endIdx := len(lines)
			for endIdx > startIdx && strings.TrimSpace(lines[endIdx-1]) == "" {
				endIdx--
			}
			// Indent non-empty lines
			for i := startIdx; i < endIdx; i++ {
				if strings.TrimSpace(lines[i]) != "" {
					lines[i] = pad + lines[i]
				}
			}
			result := strings.Join(lines[startIdx:endIdx], "\n")
			if result != "" {
				result += "\n"
			}
			return result
		},
		"or": func(a, b interface{}) interface{} {
			if !isZeroValue(a) {
				return a
			}
			return b
		},
		"and": func(a, b interface{}) bool {
			return !isZeroValue(a) && !isZeroValue(b)
		},
		"not": func(v interface{}) bool {
			return isZeroValue(v)
		},
	}
}

// buildComponentsTemplateData parses profile-components.yaml using TemplateVars and produces the data
// structure expected by gotemplates/components (root keys: values, releaseNamespace).
func (r *DeploymentSubroutine) buildComponentsTemplateData(ctx context.Context, inst *v1alpha1.PlatformMesh, templateVars apiextensionsv1.JSON) (map[string]interface{}, error) {
	log, err := logger.New(logger.DefaultConfig())
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create logger")
	}

	// Load profile from ConfigMap
	_, componentsProfile, err := r.loadProfileFromConfigMap(ctx, inst)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load profile from ConfigMap")
	}

	// Parse profile-components.yaml as YAML to get the base structure
	var profileData map[string]interface{}
	if err := yaml.Unmarshal([]byte(componentsProfile), &profileData); err != nil {
		return nil, errors.Wrap(err, "Failed to parse profile-components.yaml")
	}

	// Unmarshal templateVars JSON
	var tv map[string]interface{}
	if len(templateVars.Raw) > 0 {
		if err := json.Unmarshal(templateVars.Raw, &tv); err != nil {
			return nil, errors.Wrap(err, "Failed to unmarshal templateVars for components profile")
		}
	} else {
		tv = make(map[string]interface{})
	}

	// Merge profileData (base) with templateVars (overrides)
	// templateVars take precedence over profile values
	tv, err = merge.MergeMaps(profileData, tv, log)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to merge profile-components.yaml with templateVars")
	}

	// Render profile-components.yaml as a Go template with .Values = tv (merged values)
	tmpl, err := template.New("profile-components").Funcs(templateFuncMap()).Parse(componentsProfile)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to parse profile-components.yaml template")
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]interface{}{"Values": tv}); err != nil {
		return nil, errors.Wrap(err, "Failed to execute profile-components.yaml template")
	}

	// Now parse the rendered YAML into a generic values map
	values := map[string]interface{}{}
	if err := yaml.Unmarshal(buf.Bytes(), &values); err != nil {
		return nil, errors.Wrap(err, "Failed to unmarshal rendered profile-components.yaml")
	}

	// Extract services from the rendered profile-components.yaml
	var baseServices map[string]interface{}
	if services, ok := values["services"].(map[string]interface{}); ok {
		baseServices = services
	} else {
		baseServices = make(map[string]interface{})
	}

	// Build template data for rendering templates in spec.Values
	templateData := make(map[string]interface{})
	_, baseDomainPort, _, _ := baseDomainPortProtocol(inst)

	templateData["baseDomain"] = getBaseDomainFromInstance(inst)
	templateData["baseDomainPort"] = baseDomainPort
	templateData["port"] = "443"
	if inst.Spec.Exposure != nil && inst.Spec.Exposure.Port != 0 {
		templateData["port"] = fmt.Sprintf("%d", inst.Spec.Exposure.Port)
	}
	if templateData["port"] != "443" {
		templateData["baseDomainWithPort"] = fmt.Sprintf("%s:%s", templateData["baseDomain"], templateData["port"])
	} else {
		templateData["baseDomainWithPort"] = templateData["baseDomain"]
	}

	// Extract services from PlatformMesh.spec.Values
	// spec.Values can either have services under a "services" key, or the entire spec.Values can be services
	var specServices map[string]interface{}
	if len(inst.Spec.Values.Raw) > 0 {
		var specValues map[string]interface{}
		if err := json.Unmarshal(inst.Spec.Values.Raw, &specValues); err != nil {
			return nil, errors.Wrap(err, "Failed to parse PlatformMesh.spec.Values")
		}
		// Check if services are under a "services" key
		if services, ok := specValues["services"].(map[string]interface{}); ok {
			specServices = services
		} else {
			// If no "services" key, treat the entire specValues as services (flat structure)
			// This matches the behavior in MergeValuesAndServices
			specServices = specValues
		}

		// Render any template syntax in specServices before merging
		renderedServices, err := renderTemplatesInValue(specServices, templateData)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to render templates in PlatformMesh.spec.Values services")
		}
		if renderedMap, ok := renderedServices.(map[string]interface{}); ok {
			specServices = renderedMap
		}
	}

	// Deep merge specServices into baseServices (specServices takes precedence)
	mergedServices, err := merge.MergeMaps(baseServices, specServices, log)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to merge services from PlatformMesh.spec.Values with profile-components.yaml services")
	}

	// Put the merged services back into values
	values["services"] = mergedServices

	// Root data passed to component gotemplates
	data := map[string]interface{}{
		"values":           values,
		"releaseNamespace": inst.Namespace,
	}

	// Add kubeConfig fields for remote PlatformMesh support
	data["kubeConfigEnabled"] = r.cfgOperator.RemoteRuntime.Enabled
	if r.cfgOperator.RemoteRuntime.Enabled {
		data["kubeConfigSecretName"] = r.cfgOperator.RemoteRuntime.InfraSecretName
		data["kubeConfigSecretKey"] = r.cfgOperator.RemoteRuntime.InfraSecretKey
	}

	data["baseDomain"] = getBaseDomainFromInstance(inst)
	data["port"] = "443"
	if inst.Spec.Exposure != nil && inst.Spec.Exposure.Port != 0 {
		data["port"] = fmt.Sprintf("%d", inst.Spec.Exposure.Port)
	}
	if data["port"] != "443" {
		data["baseDomainWithPort"] = fmt.Sprintf("%s:%s", data["baseDomain"], data["port"])
	} else {
		data["baseDomainWithPort"] = data["baseDomain"]
	}

	return data, nil
}

// getBaseDomainFromInstance extracts the base domain from PlatformMesh instance
func getBaseDomainFromInstance(inst *v1alpha1.PlatformMesh) string {
	if inst.Spec.Exposure == nil || inst.Spec.Exposure.BaseDomain == "" {
		return "portal.dev.local"
	}
	return inst.Spec.Exposure.BaseDomain
}

// renderTemplatesInValue renders templates in a value and returns the rendered result
func renderTemplatesInValue(v interface{}, templateData map[string]interface{}) (interface{}, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		// Create a copy to avoid modifying the original during iteration
		result := make(map[string]interface{})
		for k, item := range val {
			rendered, err := renderTemplatesInValue(item, templateData)
			if err != nil {
				return nil, err
			}
			result[k] = rendered
		}
		return result, nil
	case []interface{}:
		// Create a new slice with rendered values
		result := make([]interface{}, len(val))
		for i, item := range val {
			rendered, err := renderTemplatesInValue(item, templateData)
			if err != nil {
				return nil, err
			}
			result[i] = rendered
		}
		return result, nil
	case string:
		// Check if the string contains template syntax
		if strings.Contains(val, "{{") && strings.Contains(val, "}}") {
			// Parse and render the template
			parsed, err := template.New("value").Funcs(templateFuncMap()).Parse(val)
			if err != nil {
				// If parsing fails, it might not be a valid template, so return the original value
				return val, nil
			}
			var buf bytes.Buffer
			if err := parsed.Execute(&buf, templateData); err != nil {
				// If execution fails, return the original value (don't error, might be intentional)
				return val, nil
			}
			return buf.String(), nil
		}
		return val, nil
	default:
		return val, nil
	}
}

// renderAndApplyInfraTemplates renders all templates in gotemplates/infra/infra and applies them.
func (r *DeploymentSubroutine) renderAndApplyInfraTemplates(ctx context.Context, inst *v1alpha1.PlatformMesh, templateVars apiextensionsv1.JSON) errors.OperatorError {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	tmplVars, err := r.templateVarsFromProfileInfra(ctx, inst, templateVars, r.cfgOperator)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build template variables from profile")
		return errors.NewOperatorError(err, true, true)
	}

	return r.renderAndApplyTemplates(ctx, r.gotemplatesInfraDir+"/infra", tmplVars, r.clientInfra, log, "infra")
}

// renderAndApplyRuntimeTemplates renders all templates in gotemplates/infra/runtime and applies them to the runtime cluster.
func (r *DeploymentSubroutine) renderAndApplyRuntimeTemplates(ctx context.Context, inst *v1alpha1.PlatformMesh, templateVars apiextensionsv1.JSON) errors.OperatorError {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	tmplVars, err := r.buildRuntimeTemplateVars(ctx, inst, templateVars)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build template variables for runtime templates")
		return errors.NewOperatorError(err, true, true)
	}

	return r.renderAndApplyTemplates(ctx, r.gotemplatesInfraDir+"/runtime", tmplVars, r.clientRuntime, log, "runtime")
}

// renderAndApplyComponentsInfraTemplates renders gotemplates/components/infra with profile-components.yaml
// and applies the resulting manifests to the infra cluster.
func (r *DeploymentSubroutine) renderAndApplyComponentsInfraTemplates(ctx context.Context, inst *v1alpha1.PlatformMesh, templateVars apiextensionsv1.JSON) errors.OperatorError {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	data, err := r.buildComponentsTemplateData(ctx, inst, templateVars)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build components template data for infra")
		return errors.NewOperatorError(err, true, true)
	}

	err = filepath.WalkDir(r.gotemplatesComponentsDir+"/infra", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}

		log.Debug().Str("path", path).Msg("Rendering components infra template")

		tplBytes, err := os.ReadFile(path)
		if err != nil {
			return errors.Wrap(err, "Failed to read components infra template file: %s", path)
		}

		tpl, err := template.New(filepath.Base(path)).Funcs(templateFuncMap()).Parse(string(tplBytes))
		if err != nil {
			return errors.Wrap(err, "Failed to parse components infra template: %s", path)
		}

		var rendered bytes.Buffer
		if err := tpl.Execute(&rendered, data); err != nil {
			return errors.Wrap(err, "Failed to execute components infra template: %s", path)
		}

		renderedStr := strings.TrimSpace(rendered.String())
		if renderedStr == "" {
			log.Debug().Str("path", path).Msg("Components infra template rendered empty, skipping")
			return nil
		}

		// Split multi-doc YAML
		docs := strings.Split(renderedStr, "\n---")
		for _, doc := range docs {
			doc = strings.TrimSpace(doc)
			if doc == "" {
				continue
			}
			var objMap map[string]interface{}
			if err := yaml.Unmarshal([]byte(doc), &objMap); err != nil {
				return errors.Wrap(err, "Failed to unmarshal rendered components infra YAML from template %s. Output:\n%s", path, doc)
			}
			obj := unstructured.Unstructured{Object: objMap}

			// Apply the rendered manifest using Server-Side Apply with field manager via dynamic client
			// This bypasses client-side schema validation and uses server-side validation instead
			// This allows Kubernetes to merge fields managed by other subroutines (e.g., Resource subroutine)
			if err := r.applyWithDynamicClient(ctx, &obj, r.clientInfra, fieldManagerDeployment, log); err != nil {
				return errors.Wrap(err, "Failed to apply rendered components infra manifest from template: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
			}
			log.Debug().Str("path", path).Str("kind", obj.GetKind()).Str("name", obj.GetName()).Msg("Applied rendered components infra template")
		}

		return nil
	})

	if err != nil {
		log.Error().Err(err).Msg("Failed to render and apply components infra templates")
		return errors.NewOperatorError(err, false, true)
	}

	return nil
}

// renderAndApplyComponentsRuntimeTemplates renders gotemplates/components/runtime with profile-components.yaml
// and applies the resulting manifests to the infra cluster (OCM Resources).
func (r *DeploymentSubroutine) renderAndApplyComponentsRuntimeTemplates(ctx context.Context, inst *v1alpha1.PlatformMesh, templateVars apiextensionsv1.JSON) errors.OperatorError {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	data, err := r.buildComponentsTemplateData(ctx, inst, templateVars)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build components template data for runtime")
		return errors.NewOperatorError(err, true, true)
	}

	err = filepath.WalkDir(r.gotemplatesComponentsDir+"/runtime", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}

		log.Debug().Str("path", path).Msg("Rendering components runtime template")

		tplBytes, err := os.ReadFile(path)
		if err != nil {
			return errors.Wrap(err, "Failed to read components runtime template file: %s", path)
		}

		tpl, err := template.New(filepath.Base(path)).Funcs(templateFuncMap()).Parse(string(tplBytes))
		if err != nil {
			return errors.Wrap(err, "Failed to parse components runtime template: %s", path)
		}

		var rendered bytes.Buffer
		if err := tpl.Execute(&rendered, data); err != nil {
			return errors.Wrap(err, "Failed to execute components runtime template: %s", path)
		}

		renderedStr := strings.TrimSpace(rendered.String())
		if renderedStr == "" {
			log.Debug().Str("path", path).Msg("Components runtime template rendered empty, skipping")
			return nil
		}

		// Split multi-doc YAML
		docs := strings.Split(renderedStr, "\n---")
		for _, doc := range docs {
			doc = strings.TrimSpace(doc)
			if doc == "" {
				continue
			}
			var objMap map[string]interface{}
			if err := yaml.Unmarshal([]byte(doc), &objMap); err != nil {
				return errors.Wrap(err, "Failed to unmarshal rendered components runtime YAML from template %s. Output:\n%s", path, doc)
			}
			obj := unstructured.Unstructured{Object: objMap}

			// Apply the rendered manifest using Server-Side Apply with field manager via dynamic client
			// This bypasses client-side schema validation and uses server-side validation instead
			// This allows Kubernetes to merge fields managed by other subroutines (e.g., Resource subroutine)
			if err := r.applyWithDynamicClient(ctx, &obj, r.clientRuntime, fieldManagerDeployment, log); err != nil {
				return errors.Wrap(err, "Failed to apply rendered components runtime manifest from template: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
			}
			log.Debug().Str("path", path).Str("kind", obj.GetKind()).Str("name", obj.GetName()).Msg("Applied rendered components runtime template")
		}

		return nil
	})

	if err != nil {
		log.Error().Err(err).Msg("Failed to render and apply components runtime templates")
		return errors.NewOperatorError(err, false, true)
	}

	return nil
}

// applyWithDynamicClient applies an object using Server-Side Apply via the dynamic client.
// This bypasses client-side schema validation, allowing fields that exist in the server's CRD
// but may not be in the client's cached schema (e.g., kubeConfig in HelmRelease).
// If SSA fails due to schema validation errors or conflicts, it falls back to Update with merge logic.
func (r *DeploymentSubroutine) applyWithDynamicClient(ctx context.Context, obj *unstructured.Unstructured, k8sClient client.Client, fieldManager string, log *logger.Logger) error {
	// For Resource objects, check if it already exists before removing interval
	// interval is required for creation, but managed by OCM controller after creation
	if obj.GetKind() == kindResource {
		existingResource := &unstructured.Unstructured{}
		existingResource.SetGroupVersionKind(obj.GroupVersionKind())
		existingResource.SetName(obj.GetName())
		existingResource.SetNamespace(obj.GetNamespace())

		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), existingResource)
		if err == nil {
			// Resource exists - remove interval to avoid conflict with OCM controller
			if spec, found, _ := unstructured.NestedMap(obj.Object, "spec"); found && spec != nil {
				if _, hasInterval := spec[specFieldInterval]; hasInterval {
					delete(spec, specFieldInterval)
					if setErr := unstructured.SetNestedField(obj.Object, spec, "spec"); setErr != nil {
						log.Debug().Err(setErr).Msg("Failed to remove interval from Resource spec")
					}
				}
			}
		} else if !kerrors.IsNotFound(err) {
			// If there's an error other than "not found", return it
			return errors.Wrap(err, "Failed to check if Resource exists")
		}
		// If resource doesn't exist (IsNotFound), keep interval (it's required for creation)
	}

	provider, err := newDynamicClientProvider(r.restConfig)
	if err != nil {
		return err
	}

	// Attempt Server-Side Apply
	err = applyWithSSA(ctx, provider, obj, fieldManager)
	if err == nil {
		return nil
	}

	// Check if error requires fallback
	isSchemaError, isConflictError := isSSAError(err)
	if isSchemaError || isConflictError {
		log.Debug().
			Str("kind", obj.GetKind()).
			Str("name", obj.GetName()).
			Bool("schema_error", isSchemaError).
			Bool("conflict_error", isConflictError).
			Err(err).
			Msg("SSA failed, falling back to Update with merge")

		return r.applyWithUpdateFallback(ctx, obj, k8sClient, log)
	}

	return errors.Wrap(err, "Failed to apply object using dynamic client")
}

// applyWithUpdateFallback applies an object using Update with merge logic to preserve
// fields managed by other subroutines (e.g., Resource subroutine).
func (r *DeploymentSubroutine) applyWithUpdateFallback(ctx context.Context, obj *unstructured.Unstructured, k8sClient client.Client, log *logger.Logger) error {
	existing, err := getOrCreateObject(ctx, k8sClient, obj)
	if err != nil {
		return err
	}

	// If object was just created, no merge needed
	if existing == obj {
		return nil
	}

	// Merge spec based on resource type
	if obj.GetKind() == kindHelmRelease {
		if err := mergeHelmReleaseSpec(existing, obj, log); err != nil {
			return errors.Wrap(err, "Failed to merge HelmRelease spec")
		}
	} else if obj.GetKind() == kindResource {
		if err := mergeResourceSpec(existing, obj, log); err != nil {
			return errors.Wrap(err, "Failed to merge Resource spec")
		}
	} else {
		if err := mergeGenericSpec(existing, obj, log); err != nil {
			return errors.Wrap(err, "Failed to merge spec")
		}
	}

	// Update metadata from desired
	updateObjectMetadata(existing, obj)

	return k8sClient.Update(ctx, existing)
}

func mergeOCMConfig(mapValues map[string]interface{}, inst *v1alpha1.PlatformMesh) {
	if inst.Spec.OCM != nil {
		repoConfig := map[string]interface{}{}
		compConfig := map[string]interface{}{}

		if inst.Spec.OCM.Repo != nil {
			repoConfig = map[string]interface{}{
				"name": inst.Spec.OCM.Repo.Name,
			}
		}

		if inst.Spec.OCM.Component != nil {
			compConfig = map[string]interface{}{
				"name": inst.Spec.OCM.Component.Name,
			}
		}
		var referencePath []interface{}
		for _, element := range inst.Spec.OCM.ReferencePath {
			referencePath = append(referencePath, map[string]interface{}{"name": element.Name})
		}
		ocmConfig := map[string]interface{}{
			"repo":          repoConfig,
			"component":     compConfig,
			"referencePath": referencePath,
		}
		mapValues["ocm"] = ocmConfig
	}
}

func (r *DeploymentSubroutine) createKCPWebhookSecret(ctx context.Context, inst *v1alpha1.PlatformMesh) errors.OperatorError {
	log := logger.LoadLoggerFromContext(ctx)
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
	webhookSecret := operatorCfg.Subroutines.Deployment.AuthorizationWebhookSecretName
	_, err := GetSecret(r.clientRuntime, webhookSecret, inst.Namespace)
	if err != nil && !kerrors.IsNotFound(err) {
		log.Error().Err(err).Str("secret", webhookSecret).Str("namespace", inst.Namespace).Msg("Failed to get kcp webhook secret")
		return errors.NewOperatorError(err, true, true)
	}
	if err == nil {
		return nil
	}

	// Continue to create the secret
	obj, err := unstructuredFromFile(fmt.Sprintf("%s/rebac-auth-webhook/kcp-webhook-secret.yaml", r.workspaceDirectory), map[string]string{}, log)
	if err != nil {
		return errors.NewOperatorError(err, true, true)
	}
	obj.SetNamespace(inst.Namespace)

	// create system masters secret (idempotent)
	if err := r.clientRuntime.Create(ctx, &obj); err != nil {
		if kerrors.IsAlreadyExists(err) {
			log.Info().Str("name", obj.GetName()).Str("namespace", obj.GetNamespace()).Msg("KCP webhook secret already exists, skipping create")
			return nil
		}
		return errors.NewOperatorError(err, true, true)
	}
	return nil
}

func (r *DeploymentSubroutine) udpateKcpWebhookSecret(ctx context.Context, inst *v1alpha1.PlatformMesh) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)

	// Retrieve the ca.crt from the rebac-authz-webhook-cert secret
	caSecretName := operatorCfg.Subroutines.Deployment.AuthorizationWebhookSecretCAName
	webhookCertSecret, err := GetSecret(r.clientRuntime, caSecretName, inst.Namespace)
	if err != nil {
		if kerrors.IsNotFound(err) {
			log.Info().Str("name", caSecretName).Msg("Webhook secret does not exist")
			return ctrl.Result{}, errors.NewOperatorError(errors.New("Webhook secret does not exist"), true, true)
		}
		log.Error().Err(err).Str("secret", caSecretName).Str("namespace", inst.Namespace).Msg("Failed to get webhook cert secret")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	caCrt, ok := webhookCertSecret.Data["ca.crt"]
	if !ok || len(caCrt) == 0 {
		err := fmt.Errorf("ca.crt not found or empty in secret %s/%s", inst.Namespace, caSecretName)
		log.Error().Err(err).Msg("ca.crt missing from webhook cert secret")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Get the kcp-webhook-secret
	webhookSecret := operatorCfg.Subroutines.Deployment.AuthorizationWebhookSecretName
	kcpWebhookSecret, err := GetSecret(r.clientRuntime, webhookSecret, inst.Namespace)
	if err != nil {
		log.Error().Err(err).Str("secret", webhookSecret).Str("namespace", inst.Namespace).Msg("Failed to get kcp webhook secret")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Get the kubeconfig from the secret
	kubeconfigData, ok := kcpWebhookSecret.Data["kubeconfig"]
	if !ok || len(kubeconfigData) == 0 {
		err := fmt.Errorf("kubeconfig not found or empty in secret %s/%s", inst.Namespace, webhookSecret)
		log.Error().Err(err).Msg("kubeconfig missing from kcp webhook secret")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Parse the kubeconfig using clientcmd utilities
	kubeconfig, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		log.Error().Err(err).Msg("Failed to load kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Update the certificate-authority-data in all clusters
	updated := false
	for clusterName, cluster := range kubeconfig.Clusters {
		if cluster != nil {
			// Update the certificate-authority-data with the new ca.crt
			cluster.CertificateAuthorityData = caCrt
			kubeconfig.Clusters[clusterName] = cluster
			updated = true
			log.Debug().Str("cluster", clusterName).Msg("Updated certificate-authority-data in cluster")
		}
	}

	if !updated {
		log.Info().Msg("No clusters found in kubeconfig to update")
		return ctrl.Result{}, nil
	}

	// Marshal the updated kubeconfig back to YAML using clientcmd
	updatedKubeconfigData, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		log.Error().Err(err).Msg("Failed to write updated kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Update the secret with the new kubeconfig
	kcpWebhookSecret.Data["kubeconfig"] = updatedKubeconfigData

	err = r.clientRuntime.Update(ctx, kcpWebhookSecret)
	if err != nil {
		log.Error().Err(err).Str("secret", webhookSecret).Str("namespace", operatorCfg.KCP.Namespace).Msg("Failed to update kcp webhook secret")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	log.Info().Str("secret", webhookSecret).Str("namespace", operatorCfg.KCP.Namespace).Msg("Successfully updated kcp webhook secret with new certificate-authority-data")

	return ctrl.Result{}, nil
}

func getHelmRelease(ctx context.Context, client client.Client, releaseName string, releaseNamespace string) (*unstructured.Unstructured, error) {
	kcpRelease := &unstructured.Unstructured{}
	kcpRelease.SetGroupVersionKind(schema.GroupVersionKind{Group: "helm.toolkit.fluxcd.io", Version: "v2", Kind: "HelmRelease"})
	err := client.Get(ctx, types.NamespacedName{Name: releaseName, Namespace: releaseNamespace}, kcpRelease)
	if err != nil {
		if kerrors.IsNotFound(err) {
			log.Info().Msgf("%s/%s Release not found, waiting for it to be created", releaseName, releaseNamespace)
			return nil, nil
		}
		log.Error().Err(err).Msgf("Failed to get %s/%s Release", releaseName, releaseNamespace)
		return nil, nil
	}
	return kcpRelease, nil
}

func (r *DeploymentSubroutine) hasIstioProxyInjected(ctx context.Context, labelSelector, namespace string) (bool, *unstructured.Unstructured, error) {
	pods := &unstructured.UnstructuredList{}
	pods.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})
	err := r.clientInfra.List(ctx, pods, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{"app": labelSelector}),
		Namespace:     namespace,
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to list pods with label selector: " + labelSelector)
		return false, nil, err
	}

	if len(pods.Items) > 0 {
		pod := pods.Items[0]
		spec := pod.Object["spec"].(map[string]interface{})
		// It is possible to have istio-proxy as an initContainer or a regular container
		if initContainersInt, ok := spec["initContainers"]; ok {
			initContainers := initContainersInt.([]interface{})
			log.Debug().Str("pod", pod.GetName()).Msgf("Found %d initContainers in pod", len(initContainers))
			for _, container := range initContainers {
				containerMap := container.(map[string]interface{})
				log.Debug().Msgf("Container name: %s", containerMap["name"].(string))
				if containerMap["name"] == "istio-proxy" {
					log.Info().Msgf("Found Istio proxy container: %s", containerMap["image"])
					return true, &pod, nil
				}
			}
		}
		if containersInt, ok := spec["containers"]; ok {
			containers := containersInt.([]interface{})
			log.Debug().Str("pod", pod.GetName()).Msgf("Found %d containers in pod", len(containers))
			for _, container := range containers {
				containerMap := container.(map[string]interface{})
				log.Debug().Msgf("Container name: %s", containerMap["name"].(string))
				if containerMap["name"] == "istio-proxy" {
					log.Info().Msgf("Found Istio proxy container: %s", containerMap["image"])
					return true, &pod, nil
				}
			}
		}
		log.Info().Msgf("Istio proxy containers not found")
		return false, &pod, nil
	}

	return false, nil, errors.New("pod not found")
}

func (r *DeploymentSubroutine) manageAuthorizationWebhookSecrets(ctx context.Context, inst *v1alpha1.PlatformMesh) (ctrl.Result, errors.OperatorError) {
	// Create Issuer
	caIssuerPath := fmt.Sprintf("%s/rebac-auth-webhook/ca-issuer.yaml", r.workspaceDirectory)
	err := r.ApplyManifestFromFileWithMergedValues(ctx, caIssuerPath, r.clientRuntime, map[string]string{})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	// Create Certificate
	certPath := fmt.Sprintf("%s/rebac-auth-webhook/webhook-cert.yaml", r.workspaceDirectory)
	err = r.ApplyManifestFromFileWithMergedValues(ctx, certPath, r.clientRuntime, map[string]string{})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	// Prepare KCP Webhook secret
	oErr := r.createKCPWebhookSecret(ctx, inst)
	if oErr != nil {
		return ctrl.Result{}, oErr
	}

	// Update KCP Webhook secret with the latest CA bundle
	return r.udpateKcpWebhookSecret(ctx, inst)
}

func applyManifestFromFileWithMergedValues(ctx context.Context, path string, k8sClient client.Client, templateData map[string]string) error {
	log := logger.LoadLoggerFromContext(ctx)

	obj, err := unstructuredFromFile(path, templateData, log)
	if err != nil {
		return err
	}

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("platform-mesh-operator"))
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}
	return nil
}

func applyReleaseWithValues(ctx context.Context, path string, k8sClient client.Client, values apiextensionsv1.JSON) error {
	log := logger.LoadLoggerFromContext(ctx)

	obj, err := unstructuredFromFile(path, map[string]string{}, log)
	if err != nil {
		return errors.Wrap(err, "Failed to get unstructuredFromFile")
	}
	obj.Object["spec"].(map[string]interface{})["values"] = values

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("platform-mesh-operator"))
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}
	return nil
}
