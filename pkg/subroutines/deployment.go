package subroutines

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"time"

	openmfpconfig "github.com/openmfp/golang-commons/config"
	"github.com/openmfp/golang-commons/controller/lifecycle"
	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/openmfp/openmfp-operator/internal/config"
	"github.com/openmfp/openmfp-operator/pkg/merge"
)

const DeploymentSubroutineName = "DeploymentSubroutine"

type DeploymentSubroutine struct {
	client             client.Client
	component          DeploymentComponents
	cfg                *openmfpconfig.CommonServiceConfig
	workspaceDirectory string
}

type DeploymentComponents struct {
	name         string
	ManifestFile string
	Resources    []Resource
}
type Resource struct {
	name string
}

func NewDeploymentSubroutine(client client.Client, cfg *openmfpconfig.CommonServiceConfig, operatorCfg *config.OperatorConfig) *DeploymentSubroutine {

	sub := &DeploymentSubroutine{
		cfg:                cfg,
		client:             client,
		workspaceDirectory: operatorCfg.WorkspaceDir,
		component: DeploymentComponents{
			name:         "openmfp",
			ManifestFile: "deployment/component-version.yaml",
			Resources: []Resource{
				{name: "IstioBase"},
				{name: "IstioD"},
				{name: "IstioGateway"},
				{name: "Crossplane"},
				{name: "AccountOperator"},
				{name: "AccountUI"},
				{name: "ApeiroExampleContent"},
				//{name: "ApeiroPortal"},
				{name: "Portal"},
				{name: "ExampleResources"},
				{name: "ExtensionManagerOperator"},
				{name: "FgaOperator"},
				{name: "IamAuthorizationWebhook"},
				//{name: "IamService"},
				{name: "Infra"},
				{name: "OpenFGA"},
				{name: "Kcp"},
				{name: "Keycloak"},
				{name: "KubernetesGraphqlGateway"},
			},
		},
	}

	return sub
}

func (r *DeploymentSubroutine) GetName() string {
	return DeploymentSubroutineName
}

func (r *DeploymentSubroutine) Finalize(_ context.Context, _ lifecycle.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func (r *DeploymentSubroutine) Finalizers() []string { // coverage-ignore
	return []string{}
}

func (r *DeploymentSubroutine) Process(ctx context.Context, runtimeObj lifecycle.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	inst := runtimeObj.(*v1alpha1.OpenMFP)
	log := logger.LoadLoggerFromContext(ctx)
	// Create DeploymentComponents Version
	templateVars, err := templateVars(ctx, inst, r.client)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	err = applyComponentVersionManifest(ctx, fmt.Sprintf("%s%s", r.workspaceDirectory, r.component.ManifestFile), r.client, templateVars, inst)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}
	for _, resource := range r.component.Resources {
		// lookup version
		path := fmt.Sprintf("%sdeployment/%s/resource.yaml", r.workspaceDirectory, resource.name)
		err := applyResourceManifest(ctx, path, r.client, templateVars, resource.name, inst)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(err, false, true)
		}
	}
	for _, resource := range r.component.Resources {
		path := fmt.Sprintf("%sdeployment/%s/deployer.yaml", r.workspaceDirectory, resource.name)
		err := applyManifestFromFileWithMergedValues(ctx, inst, resource.name, path, r.client, templateVars)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(err, false, true)
		}
	}

	// Wait for kcp release to be ready before continuing
	rel, err := getHelmRelease(ctx, r.client, "istio-istiod", "default")
	if err != nil {
		log.Error().Err(err).Msg("Failed to get istio-istiod Release")
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	if !isReady(rel) {
		log.Info().Msg("istio-istiod Release is not ready.. Retry in 5 seconds")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Check if istio-proxy is injected
	// At he boostrap time of the cluster the operator will install istio. Later in the Process the operator needs
	// to communicate via the proxy with KCP. Once Istio is up and running the operator will be restarted to ensure
	// this communication will work
	hasProxy, pod, err := r.hasIstioProxyInjected(ctx, "openmfp-operator", "openmfp-system")
	if err != nil {
		log.Error().Err(err).Msg("Failed to check if istio-proxy is injected")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	// When running the operator locally there will never be a proxy
	if !r.cfg.IsLocal && !hasProxy {
		err := r.client.Delete(ctx, pod)
		if err != nil {
			log.Error().Err(err).Msg("Failed to delete istio-proxy pod")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
		// Forcing a pod restart
		os.Exit(0)
	}

	// Wait for kcp release to be ready before continuing
	rel, err = getHelmRelease(ctx, r.client, "kcp", "default")
	if err != nil {
		log.Error().Err(err).Msg("Failed to get KCP Release")
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	if !isReady(rel) {
		log.Info().Msg("KCP Release is not ready.. Retry in 5 seconds")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Create IAM Webhook secret
	result, operatorError := r.createIAMAuthzWebhookSecret(ctx, inst)
	return result, operatorError
}

func (r *DeploymentSubroutine) createIAMAuthzWebhookSecret(ctx context.Context, inst *v1alpha1.OpenMFP) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	obj, err := unstructuredFromFile(fmt.Sprintf("%sdeployment/iam-authorization-webhook-cert.yaml", r.workspaceDirectory), map[string]string{}, log)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// create system masters secret
	err = r.client.Patch(ctx, &obj, client.Apply, client.FieldOwner("openmfp-operator"))
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Select Secret
	secret := &corev1.Secret{}
	err = r.client.Get(ctx, types.NamespacedName{Name: "kcp-system-masters-client-cert-iam-authorization-webhook", Namespace: "openmfp-system"}, secret)

	if err != nil {
		if kerrors.IsNotFound(err) {
			log.Info().Msg("IAM secret not found, waiting for it to be created")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
	}

	// Create kubeconfig and store secret
	url := "https://kcp.openmfp-system:6443/clusters/root"
	newConfig, err := buildKubeconfig(r.client, url, "kcp-system-masters-client-cert-iam-authorization-webhook")
	if err != nil {
		log.Error().Err(err).Msg("Failed to build kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to build kubeconfig"), true, false)
	}
	apiConfig := restConfigToAPIConfig(newConfig)
	kcpConfigBytes, err := clientcmd.Write(*apiConfig)
	if err != nil {
		log.Error().Err(err).Msg("Failed to write kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	iamWebhookSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "iam-authorization-webhook-kubeconfig",
			Namespace: "openmfp-system",
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.client, iamWebhookSecret, func() error {
		iamWebhookSecret.Data = map[string][]byte{
			"kubeconfig": kcpConfigBytes,
		}
		return controllerutil.SetOwnerReference(inst, iamWebhookSecret, r.client.Scheme())
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to create or update secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
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
	return kcpRelease, err
}

func (r *DeploymentSubroutine) hasIstioProxyInjected(ctx context.Context, labelSelector, namespace string) (bool, *unstructured.Unstructured, error) {
	pods := &unstructured.UnstructuredList{}
	pods.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})
	err := r.client.List(ctx, pods, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{"app": labelSelector}),
		Namespace:     namespace,
	})
	if err != nil {
		return false, nil, err
	}

	if len(pods.Items) > 0 {
		pod := pods.Items[0]
		containers := pod.Object["spec"].(map[string]interface{})["containers"].([]interface{})
		for _, container := range containers {
			containerMap := container.(map[string]interface{})
			if containerMap["name"] == "istio-proxy" {
				return true, &pod, nil
			}
		}
		return false, &pod, nil
	}

	return false, nil, errors.New("pod not found")
}

func applyManifestFromFileWithMergedValues(ctx context.Context, inst *v1alpha1.OpenMFP, name string, path string, k8sClient client.Client, templateData map[string]string) error {
	log := logger.LoadLoggerFromContext(ctx)

	obj, err := unstructuredFromFile(path, templateData, log)
	if err != nil {
		return err
	}

	err = mergeValues(inst, name, obj, log)
	if err != nil {
		return errors.Wrap(err, "Failed to merge values for %s/%s", obj.GetKind(), obj.GetName())
	}

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("openmfp-operator"))
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}
	return nil
}

func mergeValues(inst *v1alpha1.OpenMFP, name string, obj unstructured.Unstructured, log *logger.Logger) error {
	componentInt, err := getFieldValueByName(inst.Spec.Components, name)
	if err != nil {
		return errors.Wrap(err, "Failed to get component name for %s/%s", obj.GetKind(), obj.GetName())
	}

	component, ok := componentInt.(v1alpha1.Component)
	if !ok {
		return errors.New("Failed to cast component to v1alpha1.DeploymentComponents")
	}

	data, err := component.Values.MarshalJSON()
	if err != nil {
		return errors.Wrap(err, "Failed to marshal values for %s/%s", obj.GetKind(), obj.GetName())
	}

	overwriteValues := map[string]any{}
	err = json.Unmarshal(data, &overwriteValues)
	if err != nil {
		return errors.Wrap(err, "Failed to unmarshal values for %s/%s", obj.GetKind(), obj.GetName())
	}

	var values map[string]any
	baseValues, found, err := unstructured.NestedMap(obj.Object, "spec", "helmReleaseTemplate", "values")
	if err != nil || !found {
		values = overwriteValues
	} else {
		// Overwrite base values with the values from the component
		values, err = merge.MergeMaps(baseValues, overwriteValues, log)
		if err != nil {
			return errors.Wrap(err, "Failed to merge values for %s/%s", obj.GetKind(), obj.GetName())
		}
	}

	err = unstructured.SetNestedMap(obj.Object, values, "spec", "helmReleaseTemplate", "values")
	if err != nil {
		return errors.Wrap(err, "Failed to set values for %s/%s", obj.GetKind(), obj.GetName())
	}
	return err
}

func templateVars(ctx context.Context, inst *v1alpha1.OpenMFP, cl client.Client) (map[string]string, error) {
	port := 8443
	baseDomain := "portal.dev.local"
	protocol := "https"

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

	var secret corev1.Secret
	err := cl.Get(ctx, client.ObjectKey{
		Name:      "iam-authorization-webhook-cert",
		Namespace: inst.Namespace,
	}, &secret)
	if err != nil && !kerrors.IsNotFound(err) {
		return nil, errors.Wrap(err, "Failed to get secret iam-authorization-webhook-cert")
	}

	result := map[string]string{
		"IAM_WEBHOOK_CA": base64.StdEncoding.EncodeToString(secret.Data["ca.crt"]),
		"COLON_PORT":     fmt.Sprintf(":%d", port),
		"BASE_DOMAIN":    baseDomain,
		"VERSION":        inst.Spec.Version,
		"PROTOCOL":       protocol,
		"PORT":           fmt.Sprintf("%d", port),
	}

	return result, nil
}

func getFieldValueByName(obj interface{}, fieldName string) (interface{}, error) {
	v := reflect.ValueOf(obj)

	// Ensure the object is a struct or a pointer to a struct
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected a struct or pointer to struct")
	}

	// Get the field by name
	field := v.FieldByName(fieldName)
	if !field.IsValid() {
		return nil, fmt.Errorf("no such field: %s", fieldName)
	}

	return field.Interface(), nil
}

func applyComponentVersionManifest(
	ctx context.Context,
	path string, k8sClient client.Client, templateData map[string]string, inst *v1alpha1.OpenMFP,
) error {
	log := logger.LoadLoggerFromContext(ctx)

	obj, err := unstructuredFromFile(path, templateData, log)
	if err != nil {
		return err
	}

	if inst.Spec.Version != "" {
		err = unstructured.SetNestedField(obj.Object, inst.Spec.Version, "spec", "version", "semver")
		if err != nil {
			return errors.Wrap(err, "Failed to set semver for %s/%s", obj.GetKind(), obj.GetName())
		}
	}

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("openmfp-operator"))
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}
	return nil
}

func applyResourceManifest(
	ctx context.Context,
	path string, k8sClient client.Client, templateData map[string]string, name string, inst *v1alpha1.OpenMFP,
) error {
	log := logger.LoadLoggerFromContext(ctx)

	// lookup version
	componentInt, err := getFieldValueByName(inst.Spec.Components, name)
	if err != nil {
		return errors.Wrap(err, "Failed to get component by name for OpenMFP/%s", inst.GetName())
	}

	component, ok := componentInt.(v1alpha1.Component)
	if !ok {
		return errors.New("Failed to cast component to v1alpha1.DeploymentComponents")
	}

	obj, err := unstructuredFromFile(path, templateData, log)
	if err != nil {
		return err
	}

	if component.Version != "" {
		err = unstructured.SetNestedField(obj.Object, component.Version, "spec", "sourceRef", "resourceRef", "version")
		if err != nil {
			return errors.Wrap(err, "Failed to set semver for %s/%s", obj.GetKind(), obj.GetName())
		}
	}

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("openmfp-operator"))
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}
	return nil
}
