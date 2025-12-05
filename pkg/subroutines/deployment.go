package subroutines

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/rs/zerolog/log"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
)

const DeploymentSubroutineName = "DeploymentSubroutine"

type DeploymentSubroutine struct {
	clientDeploy       client.Client
	clientPlatformMesh client.Client
	cfg                *pmconfig.CommonServiceConfig
	workspaceDirectory string
	cfgOperator        *config.OperatorConfig
}

func NewDeploymentSubroutine(clientPlatformMesh, clientDeploy client.Client, cfg *pmconfig.CommonServiceConfig, operatorCfg *config.OperatorConfig) *DeploymentSubroutine {
	sub := &DeploymentSubroutine{
		cfg:                cfg,
		clientDeploy:       clientDeploy,
		clientPlatformMesh: clientPlatformMesh,
		workspaceDirectory: filepath.Join(operatorCfg.WorkspaceDir, "/manifests/k8s/"),
		cfgOperator:        operatorCfg,
	}

	return sub
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
	templateVars, err := TemplateVars(ctx, inst, r.clientPlatformMesh)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	mergedInfraValues, err := MergeValuesAndInfraValues(inst, templateVars)
	if err != nil {
		log.Error().Err(err).Msg("Failed to merge templateVars and infra values")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	// apply infra resource
	path := filepath.Join(r.workspaceDirectory, "platform-mesh-operator-infra-components/resource.yaml")
	tplValues := map[string]string{
		"componentName": inst.Spec.OCM.Component.Name,
		"repoName":      inst.Spec.OCM.Repo.Name,
		"referencePath": func() string {
			if inst.Spec.OCM == nil || inst.Spec.OCM.ReferencePath == nil {
				return ""
			}
			out := ""
			for _, rp := range inst.Spec.OCM.ReferencePath {
				if rp.Name == "" {
					continue
				}
				out += "\n        - name: " + rp.Name
			}
			return out
		}(),
	}
	err = applyManifestFromFileWithMergedValues(ctx, path, r.clientDeploy, tplValues)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}
	log.Debug().Str("path", path).Msgf("Applied path: %s", path)

	// apply infra release
	path = filepath.Join(r.workspaceDirectory, "platform-mesh-operator-infra-components/release.yaml")
	err = applyReleaseWithValues(ctx, path, r.clientDeploy, mergedInfraValues)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}
	log.Debug().Str("path", path).Msgf("Applied release path: %s", path)

	// Wait for infra-components release to be ready before continuing
	rel, err := getHelmRelease(ctx, r.clientDeploy, "platform-mesh-operator-infra-components", "default")
	if err != nil {
		log.Error().Err(err).Msg("Failed to get platform-mesh-operator-infra-components Release")
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	if !matchesConditionWithStatus(rel, "Ready", "True") {
		log.Info().Msg("platform-mesh-operator-infra-components Release is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("platform-mesh-operator-infra-components Release is not ready"), true, false)
	}

	// Wait for cert-manager to be ready
	rel, err = getHelmRelease(ctx, r.clientDeploy, "cert-manager", "default")
	if err != nil {
		log.Error().Err(err).Msg("Failed to get cert-manager Release")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	if !matchesConditionWithStatus(rel, "Ready", "True") {
		log.Info().Msg("cert-manager Release is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("cert-manager Release is not ready"), true, false)
	}

	mergedValues, err := MergeValuesAndServices(inst, templateVars)
	if err != nil {
		log.Error().Err(err).Msg("Failed to merge templateVars and services")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	log.Debug().Msgf("Merged templateVars: %s", string(mergedValues.Raw))

	// apply resource
	path = filepath.Join(r.workspaceDirectory, "platform-mesh-operator-components/resource.yaml")
	err = applyManifestFromFileWithMergedValues(ctx, path, r.clientDeploy, tplValues)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}
	log.Debug().Str("path", path).Msgf("Applied path: %s", path)

	// apply release and merge templateVars from spec.templateVars
	path = filepath.Join(r.workspaceDirectory, "platform-mesh-operator-components/release.yaml")
	err = applyReleaseWithValues(ctx, path, r.clientDeploy, mergedValues)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}
	log.Debug().Str("path", path).Msgf("Applied release path: %s", path)

	_, oErr := r.manageAuthorizationWebhookSecrets(ctx, inst)
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
		rel, err := getHelmRelease(ctx, r.clientDeploy, "istio-istiod", "default")
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
			err := r.clientDeploy.Delete(ctx, pod)
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
	err = r.clientDeploy.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.RootShardName, Namespace: operatorCfg.KCP.Namespace}, rootShard)
	if err != nil || !matchesConditionWithStatus(rootShard, "Available", "True") {
		log.Info().Msg("RootShard is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("RootShard is not ready"), true, false)
	}

	frontProxy := &unstructured.Unstructured{}
	frontProxy.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "FrontProxy"})
	// Wait for root shard to be ready
	err = r.clientDeploy.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.FrontProxyName, Namespace: operatorCfg.KCP.Namespace}, frontProxy)
	if err != nil || !matchesConditionWithStatus(frontProxy, "Available", "True") {
		log.Info().Msg("FrontProxy is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("FrontProxy is not ready"), true, false)
	}
	return ctrl.Result{}, nil
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
	_, err := GetSecret(r.clientDeploy, webhookSecret, inst.Namespace)
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
	if err := r.clientDeploy.Create(ctx, &obj); err != nil {
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
	webhookCertSecret, err := GetSecret(r.clientDeploy, caSecretName, inst.Namespace)
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
	kcpWebhookSecret, err := GetSecret(r.clientDeploy, webhookSecret, inst.Namespace)
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

	err = r.clientDeploy.Update(ctx, kcpWebhookSecret)
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
	return kcpRelease, err
}

func (r *DeploymentSubroutine) hasIstioProxyInjected(ctx context.Context, labelSelector, namespace string) (bool, *unstructured.Unstructured, error) {
	pods := &unstructured.UnstructuredList{}
	pods.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})
	err := r.clientDeploy.List(ctx, pods, &client.ListOptions{
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
	err := r.ApplyManifestFromFileWithMergedValues(ctx, caIssuerPath, r.clientDeploy, map[string]string{})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	// Create Certificate
	certPath := fmt.Sprintf("%s/rebac-auth-webhook/webhook-cert.yaml", r.workspaceDirectory)
	err = r.ApplyManifestFromFileWithMergedValues(ctx, certPath, r.clientDeploy, map[string]string{})
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
