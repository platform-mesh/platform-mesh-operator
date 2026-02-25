package subroutines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

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
	imageVersionStore        *ImageVersionStore
}

const (
	profileConfigMapKey           = "profile.yaml"
	defaultProfileConfigMapSuffix = "-profile"
)

func NewDeploymentSubroutine(clientRuntime client.Client, clientInfra client.Client, cfg *pmconfig.CommonServiceConfig, operatorCfg *config.OperatorConfig) *DeploymentSubroutine {
	workspaceDir := filepath.Join(operatorCfg.WorkspaceDir, "/manifests/k8s/")
	// gotemplates is at the root level, relative to WorkspaceDir
	gotemplatesInfraDir := filepath.Join(operatorCfg.WorkspaceDir, "gotemplates/infra")
	gotemplatesComponentsDir := filepath.Join(operatorCfg.WorkspaceDir, "gotemplates/components")

	sub := &DeploymentSubroutine{
		cfg:                      cfg,
		clientInfra:              clientInfra,
		clientRuntime:            clientRuntime,
		workspaceDirectory:       workspaceDir,
		gotemplatesInfraDir:      gotemplatesInfraDir,
		gotemplatesComponentsDir: gotemplatesComponentsDir,
		cfgOperator:              operatorCfg,
	}

	return sub
}

// SetImageVersionStore sets the shared ImageVersionStore used to merge
// Resource-managed image versions into ArgoCD Application helm values.
func (r *DeploymentSubroutine) SetImageVersionStore(store *ImageVersionStore) {
	r.imageVersionStore = store
}

// getProfileConfigMap ensures the profile ConfigMap exists, creating a default one if needed.
func (r *DeploymentSubroutine) getProfileConfigMap(ctx context.Context, inst *v1alpha1.PlatformMesh) (*corev1.ConfigMap, error) {
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
		return configMap, nil
	}

	return nil, err
}

// loadProfileSections  returns infra and components profile sections as separate YAML strings
func (r *DeploymentSubroutine) loadProfileSections(ctx context.Context, inst *v1alpha1.PlatformMesh) (infraProfile string, componentsProfile string, err error) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	configMap, err := r.getProfileConfigMap(ctx, inst)
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
	inst, ok := runtimeObj.(*v1alpha1.PlatformMesh)
	if !ok {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unexpected runtime object type %T", runtimeObj), false, true)
	}
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	operatorCfg, ok := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
	if !ok {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unexpected config type from context"), false, true)
	}

	// Create DeploymentComponents Version
	templateVars, err := TemplateVars(ctx, inst, r.clientRuntime)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Render and apply infra templates directly from gotemplates/infra/infra using profile
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

	// Get deploymentTechnology from template vars or config (needed for checking resource readiness)
	tmplVars, err := r.templateVarsFromProfileInfra(ctx, inst, templateVars, r.cfgOperator)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get template vars for deploymentTechnology check")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	deploymentTech, _ := tmplVars["deploymentTechnology"].(string)
	if deploymentTech == "" {
		deploymentTech = "fluxcd" // default to fluxcd if not in profile
	}
	deploymentTech = strings.ToLower(deploymentTech)

	// Wait for cert-manager to be ready

	rel, err := getDeploymentResource(ctx, r.clientInfra, "cert-manager", inst.Namespace, deploymentTech)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get cert-manager resource")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	if deploymentTech == "argocd" {
		// For ArgoCD Applications, check status.sync.status and status.health.status directly
		// ArgoCD Applications may not have conditions initially, so check status fields directly
		syncStatus, found, _ := unstructured.NestedString(rel.Object, "status", "sync", "status")
		healthStatus, healthFound, _ := unstructured.NestedString(rel.Object, "status", "health", "status")

		if !found || syncStatus != "Synced" {
			log.Info().Str("deploymentTechnology", deploymentTech).
				Str("syncStatus", syncStatus).Msg("cert-manager Application is not synced..")
			return ctrl.Result{}, errors.NewOperatorError(errors.New("cert-manager Application is not synced"), true, false)
		}
		if !healthFound || healthStatus != "Healthy" {
			log.Info().Str("deploymentTechnology", deploymentTech).
				Str("healthStatus", healthStatus).Msg("cert-manager Application is not healthy..")
			return ctrl.Result{}, errors.NewOperatorError(errors.New("cert-manager Application is not healthy"), true, false)
		}
	} else {
		// For FluxCD HelmReleases, check Ready condition
		if !matchesConditionWithStatus(rel, "Ready", "True") {
			log.Info().Str("deploymentTechnology", deploymentTech).Msg("cert-manager Release is not ready..")
			return ctrl.Result{}, errors.NewOperatorError(errors.New("cert-manager Release is not ready"), true, false)
		}
	}

	// Render and apply components templates (HelmReleases + OCM Resources) using profile
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
		rel, err := getDeploymentResource(ctx, r.clientInfra, "istio-istiod", inst.Namespace, deploymentTech)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get istio-istiod resource")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
		if deploymentTech == "argocd" {
			// For ArgoCD Applications, check status.sync.status and status.health.status directly
			syncStatus, found, _ := unstructured.NestedString(rel.Object, "status", "sync", "status")
			healthStatus, healthFound, _ := unstructured.NestedString(rel.Object, "status", "health", "status")

			if !found || syncStatus != "Synced" {
				log.Info().Str("deploymentTechnology", deploymentTech).
					Str("syncStatus", syncStatus).Msg("istio-istiod Application is not synced..")
				return ctrl.Result{}, errors.NewOperatorError(errors.New("istio-istiod Application is not synced"), true, false)
			}
			if !healthFound || healthStatus != "Healthy" {
				log.Info().Str("deploymentTechnology", deploymentTech).
					Str("healthStatus", healthStatus).Msg("istio-istiod Application is not healthy..")
				return ctrl.Result{}, errors.NewOperatorError(errors.New("istio-istiod Application is not healthy"), true, false)
			}
		} else {
			// For FluxCD HelmReleases, check Ready condition
			if !matchesConditionWithStatus(rel, "Ready", "True") {
				log.Info().Str("deploymentTechnology", deploymentTech).Msg("istio-istiod Release is not ready..")
				return ctrl.Result{}, errors.NewOperatorError(errors.New("istio-istiod Release is not ready"), true, false)
			}
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

// templateVarsFromProfileInfra parses the infra profile and merges it with templateVars for rendering gotemplates/infra
func (r *DeploymentSubroutine) templateVarsFromProfileInfra(ctx context.Context, inst *v1alpha1.PlatformMesh, templateVars apiextensionsv1.JSON, config *config.OperatorConfig) (map[string]interface{}, error) {
	// Load profile from ConfigMap
	infraProfileYaml, _, err := r.loadProfileSections(ctx, inst)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load profile from ConfigMap")
	}

	// Parse profile YAML to map
	var infraProfileMap map[string]interface{}
	if err := yaml.Unmarshal([]byte(infraProfileYaml), &infraProfileMap); err != nil {
		return nil, errors.Wrap(err, "Failed to parse profile yaml")
	}

	// Parse templateVars JSON to map
	var templateVarsMap map[string]interface{}
	if len(templateVars.Raw) > 0 {
		if err := json.Unmarshal(templateVars.Raw, &templateVarsMap); err != nil {
			return nil, errors.Wrap(err, "Failed to parse templateVars")
		}
	} else {
		templateVarsMap = make(map[string]interface{})
	}

	// Add instance-specific fields
	infraProfileMap["releaseNamespace"] = inst.Namespace
	infraProfileMap["kubeConfigEnabled"] = config.RemoteRuntime.IsEnabled()
	if config.RemoteRuntime.IsEnabled() {
		infraProfileMap["kubeConfigSecretName"] = config.RemoteRuntime.InfraSecretName
		infraProfileMap["kubeConfigSecretKey"] = config.RemoteRuntime.InfraSecretKey
	}

	// Add deploymentTechnology from profile or templateVars (defaults to fluxcd if not specified)
	deploymentTech := "fluxcd" // default
	if deploymentTechFromProfile, ok := infraProfileMap["deploymentTechnology"].(string); ok && deploymentTechFromProfile != "" {
		deploymentTech = deploymentTechFromProfile
	}
	if deploymentTechFromTemplateVars, ok := templateVarsMap["deploymentTechnology"].(string); ok && deploymentTechFromTemplateVars != "" {
		deploymentTech = deploymentTechFromTemplateVars
	}
	// Normalize to lowercase
	deploymentTech = strings.ToLower(deploymentTech)
	if deploymentTech != "fluxcd" && deploymentTech != "argocd" {
		deploymentTech = "fluxcd" // default to fluxcd if invalid
	}
	infraProfileMap["deploymentTechnology"] = deploymentTech

	// destinationServer from infra profile (for AppProject destinations.server) will be available
	// in templateVars automatically since infraProfileMap is merged with templateVarsMap

	// Merge infra profile (base) with templateVars (overrides)
	// templateVars take precedence over profile values
	log, err := logger.New(logger.DefaultConfig())
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create logger")
	}
	tmplVars, err := merge.MergeMaps(infraProfileMap, templateVarsMap, log)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to merge infra profile with templateVars")
	}

	// Ensure helmReleaseNamespace is set (from templateVars or use releaseNamespace)
	if _, ok := tmplVars["helmReleaseNamespace"]; !ok {
		tmplVars["helmReleaseNamespace"] = inst.Namespace
	}

	return tmplVars, nil
}

// buildRuntimeTemplateVars merges infra profile, templateVars, PlatformMesh.spec, and profile-components.yaml services
// for rendering gotemplates/infra/runtime templates
func (r *DeploymentSubroutine) buildRuntimeTemplateVars(ctx context.Context, inst *v1alpha1.PlatformMesh, templateVars apiextensionsv1.JSON) (map[string]interface{}, error) {
	log := logger.LoadLoggerFromContext(ctx)

	// Load profile from ConfigMap
	infraProfile, componentsProfile, err := r.loadProfileSections(ctx, inst)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load profile from ConfigMap")
	}

	// Start with infra profile as base (runtime templates need infra profile data)
	var profileData map[string]interface{}
	if err := yaml.Unmarshal([]byte(infraProfile), &profileData); err != nil {
		return nil, errors.Wrap(err, "Failed to parse infra profile for runtime templates")
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

	// Merge infra profile (base) with templateVars (overrides)
	baseVars, err := merge.MergeMaps(profileData, templateVarsMap, log)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to merge infra profile with templateVars")
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
	// Render profile-components.yaml template with baseVars directly (not wrapped in Values)
	// This allows templates to use {{ .baseDomain }} instead of {{ .Values.baseDomain }}
	if err := tmpl.Execute(&buf, baseVars); err != nil {
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
	baseVars["kubeConfigEnabled"] = r.cfgOperator.RemoteRuntime.IsEnabled()
	if r.cfgOperator.RemoteRuntime.IsEnabled() {
		baseVars["kubeConfigSecretName"] = r.cfgOperator.RemoteRuntime.InfraSecretName
		baseVars["kubeConfigSecretKey"] = r.cfgOperator.RemoteRuntime.InfraSecretKey
	}

	return baseVars, nil
}

// buildComponentsTemplateVars parses components profile using TemplateVars and produces the data
// structure expected by gotemplates/components (root keys: values, releaseNamespace).
func (r *DeploymentSubroutine) buildComponentsTemplateVars(ctx context.Context, inst *v1alpha1.PlatformMesh, templateVars apiextensionsv1.JSON) (map[string]interface{}, error) {
	log, err := logger.New(logger.DefaultConfig())
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create logger")
	}

	// Load components profile from ConfigMap
	_, componentsProfileYaml, err := r.loadProfileSections(ctx, inst)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load profile from ConfigMap")
	}

	// Parse components profile as YAML to get the base structure
	var componentsProfileMap map[string]interface{}
	if err := yaml.Unmarshal([]byte(componentsProfileYaml), &componentsProfileMap); err != nil {
		return nil, errors.Wrap(err, "Failed to parse components profile as YAML")
	}

	// Parse templateVars JSON into a map
	var templateVarsMap map[string]interface{}
	if len(templateVars.Raw) > 0 {
		if err := json.Unmarshal(templateVars.Raw, &templateVarsMap); err != nil {
			return nil, errors.Wrap(err, "Failed to unmarshal templateVars for components profile")
		}
	} else {
		templateVarsMap = make(map[string]interface{})
	}

	// Merge components profile (base) with templateVars (overrides)
	// templateVars take precedence over profile values
	templateVarsMap, err = merge.MergeMaps(componentsProfileMap, templateVarsMap, log)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to merge profile-components.yaml with templateVars")
	}

	// Render profile-components.yaml as a Go template with tv directly (merged values)
	// Templates can use {{ .baseDomain }} instead of {{ .Values.baseDomain }}
	tmpl, err := template.New("profile-components").Funcs(templateFuncMap()).Parse(componentsProfileYaml)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to parse profile-components.yaml template")
	}

	var buf bytes.Buffer
	// Render profile-components.yaml template with tv directly (not wrapped in Values)
	// This allows templates to use {{ .baseDomain }} instead of {{ .Values.baseDomain }}
	if err := tmpl.Execute(&buf, templateVarsMap); err != nil {
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
		// Wrap templateData in Values key to support {{ .Values.* }} syntax in spec.Values
		wrappedTemplateData := map[string]interface{}{
			"Values": templateData,
		}
		// Also add top-level keys for backward compatibility with {{ .baseDomain }} syntax
		for k, v := range templateData {
			wrappedTemplateData[k] = v
		}
		renderedServices, err := renderTemplatesInValue(specServices, wrappedTemplateData)
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
	data["kubeConfigEnabled"] = r.cfgOperator.RemoteRuntime.IsEnabled()
	if r.cfgOperator.RemoteRuntime.IsEnabled() {
		data["kubeConfigSecretName"] = r.cfgOperator.RemoteRuntime.InfraSecretName
		data["kubeConfigSecretKey"] = r.cfgOperator.RemoteRuntime.InfraSecretKey
	}

	// Add deploymentTechnology from profile or templateVars (defaults to fluxcd if not specified)
	deploymentTech := "fluxcd" // default
	if deploymentTechFromProfile, ok := values["deploymentTechnology"].(string); ok && deploymentTechFromProfile != "" {
		deploymentTech = deploymentTechFromProfile
	}
	if deploymentTechFromTemplateVars, ok := templateVarsMap["deploymentTechnology"].(string); ok && deploymentTechFromTemplateVars != "" {
		deploymentTech = deploymentTechFromTemplateVars
	}
	// Normalize to lowercase
	deploymentTech = strings.ToLower(deploymentTech)
	if deploymentTech != "fluxcd" && deploymentTech != "argocd" {
		deploymentTech = "fluxcd" // default to fluxcd if invalid
	}
	data["deploymentTechnology"] = deploymentTech

	// Calculate sync waves for ArgoCD Applications based on dependsOn
	if deploymentTech == "argocd" {
		if err := calculateSyncWaves(mergedServices, inst.Namespace); err != nil {
			log.Warn().Err(err).Msg("Failed to calculate sync waves, continuing without sync wave annotations")
		}
	}

	// Extract destinationServer from components profile and add it to root level for template access
	if destinationServer, ok := values["destinationServer"].(string); ok && destinationServer != "" {
		data["destinationServer"] = destinationServer
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
		return "portal.localhost"
	}
	return inst.Spec.Exposure.BaseDomain
}

// calculateSyncWaves calculates ArgoCD sync waves based on dependsOn relationships
// Services with no dependencies get wave 0, services depending on wave N get wave N+1
func calculateSyncWaves(services map[string]interface{}, defaultNamespace string) error {
	if services == nil {
		return nil
	}

	// Build dependency graph: service -> list of dependencies
	dependencies := make(map[string][]string)
	serviceNames := make([]string, 0)

	// First pass: collect all services and their dependencies
	for serviceName, serviceConfig := range services {
		serviceStr := serviceName
		serviceNames = append(serviceNames, serviceStr)
		dependencies[serviceStr] = []string{}

		config, ok := serviceConfig.(map[string]interface{})
		if !ok {
			continue
		}

		// Check if service has dependsOn
		dependsOn, found := config["dependsOn"]
		if !found {
			continue
		}

		// dependsOn can be a slice of maps with "name" and optional "namespace"
		dependsOnSlice, ok := dependsOn.([]interface{})
		if !ok {
			continue
		}

		for _, dep := range dependsOnSlice {
			depMap, ok := dep.(map[string]interface{})
			if !ok {
				continue
			}

			depName, ok := depMap["name"].(string)
			if !ok || depName == "" {
				continue
			}

			// Add dependency (using service name as-is, assuming same namespace unless specified)
			dependencies[serviceStr] = append(dependencies[serviceStr], depName)
		}
	}

	// First, collect user-configured syncWave values (from profile or PlatformMesh.spec.Values)
	// These should be preserved and not overwritten by automatic calculation
	userConfiguredSyncWaves := make(map[string]int)
	for serviceName, serviceConfig := range services {
		config, ok := serviceConfig.(map[string]interface{})
		if !ok {
			continue
		}

		// Check if syncWave is already configured by the user
		if syncWaveVal, found := config["syncWave"]; found {
			switch v := syncWaveVal.(type) {
			case int:
				userConfiguredSyncWaves[serviceName] = v
			case int64:
				userConfiguredSyncWaves[serviceName] = int(v)
			case float64:
				// JSON numbers unmarshal as float64
				userConfiguredSyncWaves[serviceName] = int(v)
			}
		}
	}

	// Calculate sync waves using iterative approach
	// Services with no dependencies get wave 0
	// Services depending on wave N services get wave N+1
	syncWaves := make(map[string]int)

	// Initialize all services to wave 0, or use user-configured value if present
	for _, serviceName := range serviceNames {
		if userWave, exists := userConfiguredSyncWaves[serviceName]; exists {
			syncWaves[serviceName] = userWave
		} else {
			syncWaves[serviceName] = 0
		}
	}

	// Calculate waves iteratively until no changes (handles dependencies)
	// Maximum iterations to prevent infinite loops (should be <= number of services)
	maxIterations := len(serviceNames) + 1
	for iteration := 0; iteration < maxIterations; iteration++ {
		changed := false
		for _, serviceName := range serviceNames {
			deps := dependencies[serviceName]
			if len(deps) == 0 {
				// No dependencies, stays at wave 0
				continue
			}

			// Find max wave of all valid dependencies
			maxDepWave := -1
			hasValidDeps := false
			for _, depName := range deps {
				depWave, depExists := syncWaves[depName]
				if !depExists {
					// Dependency not found in services, ignore it
					continue
				}
				hasValidDeps = true
				if depWave > maxDepWave {
					maxDepWave = depWave
				}
			}

			if hasValidDeps {
				// Set this service's wave to max dependency wave + 1
				newWave := maxDepWave + 1
				// Only update if not user-configured (preserve user-configured values)
				// Dependencies will still be respected because we use syncWaves (which includes user values) for dependency calculation
				if _, isUserConfigured := userConfiguredSyncWaves[serviceName]; !isUserConfigured {
					currentWave := syncWaves[serviceName]
					if currentWave < newWave {
						syncWaves[serviceName] = newWave
						changed = true
					}
				}
			}
		}

		if !changed {
			// No more changes, we're done
			break
		}
	}

	// Add sync wave to each service config
	// Only overwrite if not user-configured, otherwise preserve user value
	// Note: ignoreDifferences is also preserved from the profile components section
	// and will be available in the config for use in templates
	for serviceName, serviceConfig := range services {
		config, ok := serviceConfig.(map[string]interface{})
		if !ok {
			continue
		}

		// If user configured syncWave, preserve it; otherwise use calculated value
		if _, isUserConfigured := userConfiguredSyncWaves[serviceName]; isUserConfigured {
			// Keep user-configured value, but still respect dependencies if needed
			// For user-configured values, we keep them as-is
			continue
		}

		wave, exists := syncWaves[serviceName]
		if !exists {
			// Service not in syncWaves map, default to wave 0
			wave = 0
		}

		// Set syncWave field (only if not user-configured)
		config["syncWave"] = wave

		// ignoreDifferences is preserved from the profile components section
		// It is already in the config map from the merged services, so no explicit
		// handling is needed here - it will be available in templates via $config.ignoreDifferences
	}

	return nil
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

// mergeImageVersionsIntoHelmValues reads the ImageVersionStore for the given Application and
// merges any Resource-managed image versions into the object's spec.source.helm.values YAML string.
func (r *DeploymentSubroutine) mergeImageVersionsIntoHelmValues(obj map[string]interface{}, appName, namespace string, log *logger.Logger) {
	if r.imageVersionStore == nil {
		return
	}
	versions := r.imageVersionStore.Get(namespace, appName)
	if len(versions) == 0 {
		return
	}

	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return
	}
	source, ok := spec["source"].(map[string]interface{})
	if !ok {
		return
	}
	helm, ok := source["helm"].(map[string]interface{})
	if !ok {
		return
	}
	valuesStr, _ := helm["values"].(string)

	updatedYAML, err := SetHelmValues(valuesStr, versions)
	if err != nil {
		log.Warn().Err(err).Str("application", appName).Msg("Failed to merge image versions into helm values")
		return
	}

	for _, iv := range versions {
		log.Debug().Str("application", appName).Str("path", iv.Path).Str("version", iv.Version).Msg("Merged Resource image version into helm values")
	}
	helm["values"] = updatedYAML
}

// mergeImageVersionsIntoHelmReleaseValues reads the ImageVersionStore for the given HelmRelease and
// merges any Resource-managed image versions into the object's spec.values structured field.
func (r *DeploymentSubroutine) mergeImageVersionsIntoHelmReleaseValues(obj *unstructured.Unstructured, releaseName, namespace string, log *logger.Logger) {
	if r.imageVersionStore == nil {
		return
	}
	versions := r.imageVersionStore.Get(namespace, releaseName)
	if len(versions) == 0 {
		return
	}

	for _, iv := range versions {
		pathElems := SplitPath(iv.Path)
		fullPath := append([]string{"spec", "values"}, pathElems...)
		if err := unstructured.SetNestedField(obj.Object, iv.Version, fullPath...); err != nil {
			log.Warn().Err(err).Str("helmRelease", releaseName).Str("path", iv.Path).Msg("Failed to merge image version into HelmRelease spec.values")
			continue
		}
		log.Debug().Str("helmRelease", releaseName).Str("path", iv.Path).Str("version", iv.Version).Msg("Merged Resource image version into HelmRelease spec.values")
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

	// Determine which template to render based on deploymentTechnology from infra profile
	deploymentTech, ok := tmplVars["deploymentTechnology"].(string)
	if !ok {
		deploymentTech = "fluxcd" // default
	}
	deploymentTech = strings.ToLower(deploymentTech)

	err = filepath.WalkDir(r.gotemplatesInfraDir+"/infra", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}

		// Conditionally render templates based on deploymentTechnology
		fileName := d.Name()
		if deploymentTech == "argocd" && (strings.HasPrefix(fileName, "helmrelease") || strings.HasPrefix(fileName, "kustomization")) {
			log.Debug().Str("path", path).Str("deploymentTechnology", deploymentTech).Msg("Skipping FluxCD template, ArgoCD is enabled")
			return nil
		}
		if deploymentTech == "fluxcd" && strings.HasPrefix(fileName, "application") {
			log.Debug().Str("path", path).Str("deploymentTechnology", deploymentTech).Msg("Skipping ArgoCD template, FluxCD is enabled")
			return nil
		}

		log.Debug().Str("path", path).Str("deploymentTechnology", deploymentTech).Msg("Rendering infra template")

		// Read and render template
		obj, err := r.renderTemplateFile(path, tmplVars, log)
		if err != nil {
			return errors.Wrap(err, "Failed to render template: %s", path)
		}

		if obj == nil {
			// Template rendered empty, skip
			return nil
		}

		// For ArgoCD Applications, preserve existing repoURL and targetRevision if they're already set
		// (not placeholder values) to avoid overwriting values set by ResourceSubroutine
		if obj.GetKind() == "Application" && obj.GetAPIVersion() == "argoproj.io/v1alpha1" {
			existingApp := &unstructured.Unstructured{}
			existingApp.SetGroupVersionKind(obj.GroupVersionKind())
			if err := r.clientInfra.Get(ctx, client.ObjectKey{Name: obj.GetName(), Namespace: obj.GetNamespace()}, existingApp); err == nil {
				// Application exists - check if repoURL and targetRevision are already set (not placeholders)
				existingRepoURL, found, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "repoURL")
				existingTargetRevision, foundRev, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "targetRevision")

				// Check if the new object has repoURL/targetRevision before trying to preserve
				var newRepoURL string
				var newTargetRevision string
				if spec, ok := obj.Object["spec"].(map[string]interface{}); ok {
					if source, ok := spec["source"].(map[string]interface{}); ok {
						if url, ok := source["repoURL"].(string); ok {
							newRepoURL = url
						}
						if rev, ok := source["targetRevision"].(string); ok {
							newTargetRevision = rev
						}
					}
				}

				// Only preserve if:
				// 1. Existing value is set and not a placeholder
				// 2. New object has the field (so we don't remove required fields)
				// 3. Existing value is different from new value (to avoid unnecessary removals)
				if found && existingRepoURL != "" && existingRepoURL != "PLACEHOLDER_TO_BE_SET_BY_RESOURCE_SUBROUTINE" && newRepoURL != "" && existingRepoURL != newRepoURL {
					// Remove repoURL from the new object to preserve the existing value
					if spec, ok := obj.Object["spec"].(map[string]interface{}); ok {
						if source, ok := spec["source"].(map[string]interface{}); ok {
							delete(source, "repoURL")
						}
					}
				}
				if foundRev && existingTargetRevision != "" && existingTargetRevision != "PLACEHOLDER_TO_BE_SET_BY_RESOURCE_SUBROUTINE" && newTargetRevision != "" && existingTargetRevision != newTargetRevision {
					// Remove targetRevision from the new object to preserve the existing value
					if spec, ok := obj.Object["spec"].(map[string]interface{}); ok {
						if source, ok := spec["source"].(map[string]interface{}); ok {
							delete(source, "targetRevision")
						}
					}
				}
			}

			// Merge Resource-managed image versions into helm values
			r.mergeImageVersionsIntoHelmValues(obj.Object, obj.GetName(), obj.GetNamespace(), log)
		}

		// For FluxCD HelmReleases, merge Resource-managed image versions into spec.values
		if obj.GetKind() == "HelmRelease" && obj.GetAPIVersion() == "helm.toolkit.fluxcd.io/v2" {
			r.mergeImageVersionsIntoHelmReleaseValues(obj, obj.GetName(), obj.GetNamespace(), log)
		}

		// Apply the rendered manifest
		if err := r.clientInfra.Patch(ctx, obj, client.Apply, client.FieldOwner(fieldManagerDeployment), client.ForceOwnership); err != nil {
			return errors.Wrap(err, "Failed to apply rendered manifest from template: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
		}

		return nil
	})

	if err != nil {
		log.Error().Err(err).Msg("Failed to render and apply infra templates")
		return errors.NewOperatorError(err, false, true)
	}

	return nil
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

	tmplVars, err := r.buildComponentsTemplateVars(ctx, inst, templateVars)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build components template data for infra")
		return errors.NewOperatorError(err, true, true)
	}

	// Determine which template to render based on deploymentTechnology
	deploymentTech, ok := tmplVars["deploymentTechnology"].(string)
	if !ok {
		deploymentTech = "fluxcd" // default
	}
	deploymentTech = strings.ToLower(deploymentTech)

	err = filepath.WalkDir(r.gotemplatesComponentsDir+"/infra", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}

		// Conditionally render templates based on deploymentTechnology
		fileName := d.Name()
		if deploymentTech == "argocd" && (strings.HasPrefix(fileName, "helmrelease") || strings.HasPrefix(fileName, "kustomization")) {
			log.Debug().Str("path", path).Str("deploymentTechnology", deploymentTech).Msg("Skipping FluxCD template, ArgoCD is enabled")
			return nil
		}
		if deploymentTech == "fluxcd" && strings.HasPrefix(fileName, "application") {
			log.Debug().Str("path", path).Str("deploymentTechnology", deploymentTech).Msg("Skipping ArgoCD template, FluxCD is enabled")
			return nil
		}

		log.Debug().Str("path", path).Str("deploymentTechnology", deploymentTech).Msg("Rendering components infra template")

		tplBytes, err := os.ReadFile(path)
		if err != nil {
			return errors.Wrap(err, "Failed to read components infra template file: %s", path)
		}

		tpl, err := template.New(filepath.Base(path)).Funcs(templateFuncMap()).Parse(string(tplBytes))
		if err != nil {
			return errors.Wrap(err, "Failed to parse components infra template: %s", path)
		}

		var rendered bytes.Buffer
		if err := tpl.Execute(&rendered, tmplVars); err != nil {
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

			// For ArgoCD Applications, preserve existing repoURL and targetRevision if they're already set
			// (not placeholder values) to avoid overwriting values set by ResourceSubroutine
			if obj.GetKind() == "Application" && obj.GetAPIVersion() == "argoproj.io/v1alpha1" {
				existingApp := &unstructured.Unstructured{}
				existingApp.SetGroupVersionKind(obj.GroupVersionKind())
				if err := r.clientInfra.Get(ctx, client.ObjectKey{Name: obj.GetName(), Namespace: obj.GetNamespace()}, existingApp); err == nil {
					// Application exists - check if repoURL and targetRevision are already set (not placeholders)
					existingRepoURL, found, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "repoURL")
					existingTargetRevision, foundRev, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "targetRevision")

					// Check if the new object has repoURL/targetRevision before trying to preserve
					var newRepoURL string
					var newTargetRevision string
					if spec, ok := objMap["spec"].(map[string]interface{}); ok {
						if source, ok := spec["source"].(map[string]interface{}); ok {
							if url, ok := source["repoURL"].(string); ok {
								newRepoURL = url
							}
							if rev, ok := source["targetRevision"].(string); ok {
								newTargetRevision = rev
							}
						}
					}

					// Only preserve if:
					// 1. Existing value is set and not a placeholder
					// 2. New object has the field (so we don't remove required fields)
					// 3. Existing value is different from new value (to avoid unnecessary removals)
					if found && existingRepoURL != "" && existingRepoURL != "PLACEHOLDER_TO_BE_SET_BY_RESOURCE_SUBROUTINE" && newRepoURL != "" && existingRepoURL != newRepoURL {
						// Remove repoURL from the new object to preserve the existing value
						if spec, ok := objMap["spec"].(map[string]interface{}); ok {
							if source, ok := spec["source"].(map[string]interface{}); ok {
								delete(source, "repoURL")
							}
						}
					}
					if foundRev && existingTargetRevision != "" && existingTargetRevision != "PLACEHOLDER_TO_BE_SET_BY_RESOURCE_SUBROUTINE" && newTargetRevision != "" && existingTargetRevision != newTargetRevision {
						// Remove targetRevision from the new object to preserve the existing value
						if spec, ok := objMap["spec"].(map[string]interface{}); ok {
							if source, ok := spec["source"].(map[string]interface{}); ok {
								delete(source, "targetRevision")
							}
						}
					}
					// Update the unstructured object with the modified map
					obj = unstructured.Unstructured{Object: objMap}

					// Merge Resource-managed image versions into helm values
					r.mergeImageVersionsIntoHelmValues(obj.Object, obj.GetName(), obj.GetNamespace(), log)
				}
			}

			// For FluxCD HelmReleases, merge Resource-managed image versions into spec.values
			if obj.GetKind() == "HelmRelease" && obj.GetAPIVersion() == "helm.toolkit.fluxcd.io/v2" {
				r.mergeImageVersionsIntoHelmReleaseValues(&obj, obj.GetName(), obj.GetNamespace(), log)
			}

			// Apply the rendered manifest using Server-Side Apply with field manager via dynamic client
			// This bypasses client-side schema validation and uses server-side validation instead
			// This allows Kubernetes to merge fields managed by other subroutines (e.g., Resource subroutine)
			if err := r.clientInfra.Patch(ctx, &obj, client.Apply, client.FieldOwner(fieldManagerDeployment), client.ForceOwnership); err != nil {
				return errors.Wrap(err, "Failed to apply rendered components infra manifest from template: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
			}
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

	tmplVars, err := r.buildComponentsTemplateVars(ctx, inst, templateVars)
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
		if err := tpl.Execute(&rendered, tmplVars); err != nil {
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
			if err := r.clientRuntime.Patch(ctx, &obj, client.Apply, client.FieldOwner(fieldManagerDeployment), client.ForceOwnership); err != nil {
				return errors.Wrap(err, "Failed to apply rendered components runtime manifest from template: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
			}
		}

		return nil
	})

	if err != nil {
		log.Error().Err(err).Msg("Failed to render and apply components runtime templates")
		return errors.NewOperatorError(err, false, true)
	}

	return nil
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
	obj, err := unstructuredFromFile(fmt.Sprintf("%s/rebac-auth-webhook/kcp-webhook-secret.yaml", r.workspaceDirectory), map[string]any{}, log)
	if err != nil {
		return errors.NewOperatorError(err, true, true)
	}
	obj.SetNamespace(inst.Namespace)

	// Apply the secret using SSA (idempotent - creates if not exists, updates if exists)
	if err := r.clientRuntime.Patch(ctx, &obj, client.Apply, client.FieldOwner(fieldManagerDeployment), client.ForceOwnership); err != nil {
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

	// Update the certificate-authority-data in all clusters only if it actually changed
	updated := false
	for clusterName, cluster := range kubeconfig.Clusters {
		if cluster != nil && !bytes.Equal(cluster.CertificateAuthorityData, caCrt) {
			cluster.CertificateAuthorityData = caCrt
			kubeconfig.Clusters[clusterName] = cluster
			updated = true
			log.Debug().Str("cluster", clusterName).Msg("Updated certificate-authority-data in cluster")
		}
	}

	if !updated {
		log.Debug().Msg("certificate-authority-data is already up to date in kcp-webhook-secret, skipping update")
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

	// Clear managedFields before applying with SSA (required for SSA)
	kcpWebhookSecret.SetManagedFields(nil)

	// Apply the updated secret using SSA
	err = r.clientRuntime.Patch(ctx, kcpWebhookSecret, client.Apply, client.FieldOwner(fieldManagerDeployment), client.ForceOwnership)
	if err != nil {
		log.Error().Err(err).Str("secret", webhookSecret).Str("namespace", operatorCfg.KCP.Namespace).Msg("Failed to update kcp webhook secret")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	log.Info().Str("secret", webhookSecret).Str("namespace", operatorCfg.KCP.Namespace).Msg("Successfully updated kcp webhook secret with new certificate-authority-data")

	// Delete all kcp pods so they pick up the new webhook secret
	log.Info().Msg("kcp-webhook-secret was updated, deleting kcp pods to pick up new certificate-authority-data")
	if oErr := r.deleteKcpPods(ctx, operatorCfg.KCP.Namespace); oErr != nil {
		return ctrl.Result{}, oErr
	}

	return ctrl.Result{}, nil
}

// deleteKcpPods deletes all pods with label app.kubernetes.io/name=kcp in the given namespace
// so they restart and pick up updated secrets.
func (r *DeploymentSubroutine) deleteKcpPods(ctx context.Context, namespace string) errors.OperatorError {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	podList := &corev1.PodList{}
	labelSelector := labels.SelectorFromSet(labels.Set{"app.kubernetes.io/name": "kcp"})
	if err := r.clientRuntime.List(ctx, podList, &client.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     namespace,
	}); err != nil {
		log.Error().Err(err).Str("namespace", namespace).Msg("Failed to list kcp pods")
		return errors.NewOperatorError(err, true, true)
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		log.Info().Str("pod", pod.Name).Str("namespace", pod.Namespace).Msg("Deleting kcp pod")
		if err := r.clientRuntime.Delete(ctx, pod); err != nil {
			if !kerrors.IsNotFound(err) {
				log.Error().Err(err).Str("pod", pod.Name).Msg("Failed to delete kcp pod")
				return errors.NewOperatorError(err, true, true)
			}
		}
	}

	log.Info().Int("count", len(podList.Items)).Str("namespace", namespace).Msg("Deleted kcp pods")
	return nil
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

// getDeploymentResource gets either a FluxCD HelmRelease or ArgoCD Application based on deploymentTechnology
func getDeploymentResource(ctx context.Context, client client.Client, resourceName string, resourceNamespace string, deploymentTech string) (*unstructured.Unstructured, error) {
	deploymentTech = strings.ToLower(deploymentTech)
	obj := &unstructured.Unstructured{}

	if deploymentTech == "argocd" {
		obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"})
	} else {
		// Default to FluxCD
		obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "helm.toolkit.fluxcd.io", Version: "v2", Kind: "HelmRelease"})
	}

	err := client.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: resourceNamespace}, obj)
	if err != nil {
		if kerrors.IsNotFound(err) {
			log.Info().Str("deploymentTechnology", deploymentTech).Msgf("%s/%s resource not found, waiting for it to be created", resourceName, resourceNamespace)
			return nil, fmt.Errorf("%s/%s resource not found, waiting for it to be created", resourceName, resourceNamespace)
		}
		log.Error().Err(err).Str("deploymentTechnology", deploymentTech).Msgf("Failed to get %s/%s resource", resourceName, resourceNamespace)
		return nil, err
	}
	return obj, nil
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
		spec, ok := pod.Object["spec"].(map[string]interface{})
		if !ok {
			return false, &pod, fmt.Errorf("unexpected pod spec type for pod %s", pod.GetName())
		}
		// It is possible to have istio-proxy as an initContainer or a regular container
		if initContainersInt, ok := spec["initContainers"]; ok {
			initContainers, ok := initContainersInt.([]interface{})
			if !ok {
				return false, &pod, fmt.Errorf("unexpected initContainers type for pod %s", pod.GetName())
			}
			log.Debug().Str("pod", pod.GetName()).Msgf("Found %d initContainers in pod", len(initContainers))
			for _, container := range initContainers {
				containerMap, ok := container.(map[string]interface{})
				if !ok {
					continue
				}
				name, _ := containerMap["name"].(string)
				log.Debug().Msgf("Container name: %s", name)
				if name == "istio-proxy" {
					log.Info().Msgf("Found Istio proxy container: %s", containerMap["image"])
					return true, &pod, nil
				}
			}
		}
		if containersInt, ok := spec["containers"]; ok {
			containers, ok := containersInt.([]interface{})
			if !ok {
				return false, &pod, fmt.Errorf("unexpected containers type for pod %s", pod.GetName())
			}
			log.Debug().Str("pod", pod.GetName()).Msgf("Found %d containers in pod", len(containers))
			for _, container := range containers {
				containerMap, ok := container.(map[string]interface{})
				if !ok {
					continue
				}
				name, _ := containerMap["name"].(string)
				log.Debug().Msgf("Container name: %s", name)
				if name == "istio-proxy" {
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
	err := r.ApplyManifestFromFileWithMergedValues(ctx, caIssuerPath, r.clientRuntime, map[string]any{})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	// Create Certificate
	certPath := fmt.Sprintf("%s/rebac-auth-webhook/webhook-cert.yaml", r.workspaceDirectory)
	err = r.ApplyManifestFromFileWithMergedValues(ctx, certPath, r.clientRuntime, map[string]any{})
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

func applyManifestFromFileWithMergedValues(ctx context.Context, path string, k8sClient client.Client, templateData map[string]any) error {
	log := logger.LoadLoggerFromContext(ctx)

	obj, err := unstructuredFromFile(path, templateData, log)
	if err != nil {
		return err
	}

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner(fieldManagerDeployment), client.ForceOwnership)
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}
	return nil
}
