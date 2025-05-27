package subroutines

import (
	"context"
	"fmt"
	"os"
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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

const DeploymentSubroutineName = "DeploymentSubroutine"

type DeploymentSubroutine struct {
	client             client.Client
	cfg                *openmfpconfig.CommonServiceConfig
	workspaceDirectory string
}

func NewDeploymentSubroutine(client client.Client, cfg *openmfpconfig.CommonServiceConfig, operatorCfg *config.OperatorConfig) *DeploymentSubroutine {

	sub := &DeploymentSubroutine{
		cfg:                cfg,
		client:             client,
		workspaceDirectory: operatorCfg.WorkspaceDir + "/manifests/k8s/",
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
	values, err := TemplateVars(ctx, inst, r.client)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	services := apiextensionsv1.JSON{}
	services.Raw = inst.Spec.Values.Raw

	mergedValues, err := MergeValuesAndServices(values, services)
	if err != nil {
		log.Error().Err(err).Msg("Failed to merge values and services")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	log.Debug().Msgf("Merged values: %s", string(mergedValues.Raw))

	// apply repository
	path := r.workspaceDirectory + "openmfp-operator-components/repository.yaml"
	tplValues := map[string]string{
		"chartVersion":     inst.Spec.ChartVersion,
		"componentVersion": inst.Spec.ComponentVersion,
	}
	err = applyManifestFromFileWithMergedValues(ctx, path, r.client, tplValues)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}
	log.Debug().Str("path", path).Msgf("Applied repository path: %s", path)

	// apply release and merge values from spec.values
	path = r.workspaceDirectory + "openmfp-operator-components/release.yaml"
	err = applyReleaseWithValues(ctx, path, r.client, mergedValues)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}
	log.Debug().Str("path", path).Msgf("Applied release path: %s", path)

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
	obj, err := unstructuredFromFile(fmt.Sprintf("%s/iam-authorization-webhook-cert.yaml", r.workspaceDirectory), map[string]string{}, log)
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
		return err
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

func applyManifestFromFileWithMergedValues(ctx context.Context, path string, k8sClient client.Client, templateData map[string]string) error {
	log := logger.LoadLoggerFromContext(ctx)

	obj, err := unstructuredFromFile(path, templateData, log)
	if err != nil {
		return err
	}

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("openmfp-operator"))
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

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("openmfp-operator"))
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}
	return nil
}
