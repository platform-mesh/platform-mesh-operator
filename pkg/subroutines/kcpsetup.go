package subroutines

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openmfp/golang-commons/controller/lifecycle"
	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
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
	kcpDirectory DirectoryStructure
	wave         string
}

const (
	KcpsetupSubroutineName      = "KcpsetupSubroutine"
	KcpsetupSubroutineFinalizer = "openmfp.core.openmfp.org/finalizer"
)

func NewKcpsetupSubroutine(client client.Client, helper KcpHelper, kcpdir DirectoryStructure, wave string) *KcpsetupSubroutine {
	sub := &KcpsetupSubroutine{
		client:       client,
		kcpDirectory: kcpdir,
		kcpHelper:    helper,
		wave:         wave,
	}
	return sub
}

func (r *KcpsetupSubroutine) GetName() string {
	return KcpsetupSubroutineName + "." + r.wave
}

// TODO: Implement the following methods
func (r *KcpsetupSubroutine) Finalize(
	ctx context.Context, runtimeObj lifecycle.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	instance := runtimeObj.(*corev1alpha1.OpenMFP)
	_ = instance

	return ctrl.Result{}, nil // TODO: Implement
}

// TODO: Implement the following methods
func (r *KcpsetupSubroutine) Finalizers() []string { // coverage-ignore
	return []string{KcpsetupSubroutineFinalizer}
}

func (r *KcpsetupSubroutine) Process(ctx context.Context, runtimeObj lifecycle.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	instance := runtimeObj.(*corev1alpha1.OpenMFP)
	log.Debug().Str("subroutine", r.GetName()).Str("name", instance.Name).Msg("Processing OpenMFP resource")

	// Get the secret
	secret, err := GetSecret(
		r.client, instance.GetAdminSecretName(), instance.GetAdminSecretNamespace(),
	)
	if err != nil {
		log.Error().Str("subroutine", r.GetName()).Err(err).Msg("Failed to get secret")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to get secret"), false, false)
	}

	err = r.createKcpResources(ctx, *secret, instance.GetAdminSecretKey(), r.kcpDirectory, instance)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create kcp workspaces")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to create kcp workspaces"), true, false)
	}

	// update workspace status
	instance.Status.KcpWorkspaces = []corev1alpha1.KcpWorkspace{
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

func (r *KcpsetupSubroutine) createKcpResources(
	ctx context.Context,
	secret corev1.Secret,
	secretKey string,
	dir DirectoryStructure,
	instance *corev1alpha1.OpenMFP,
) error {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	// kcp kubernetes client
	config, err := clientcmd.RESTConfigFromKubeConfig(secret.Data[secretKey])
	if err != nil {
		log.Error().Err(err).Msg("Failed to build config from kubeconfig string")
		return errors.Wrap(err, "Failed to build config from kubeconfig string")
	}

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

	// TODO: check if already applied
	err = r.applyDirStructure(ctx, dir, config, templateData)
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

	caBundles := make(map[string]string)

	webhookConfig := DEFAULT_WEBHOOK_CONFIGURATION
	caData, err := r.getCaBundle(ctx, &webhookConfig)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get CA bundle")
		return nil, errors.Wrap(err, "Failed to get CA bundle")
	}

	key := fmt.Sprintf("%s.ca-bundle", webhookConfig.WebhookRef.Name)
	caBundles[key] = string(caData)

	return caBundles, nil
}

func (r *KcpsetupSubroutine) getCaBundle(
	ctx context.Context,
	webhookConfig *corev1alpha1.WebhookConfiguration,
) ([]byte, error) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

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
	// decodedCaData := base64.StdEncoding.EncodeToString(caData)
	// if err != nil {
	// 	log.Error().Str("subroutine", r.GetName()).Err(err).Msg("Failed to decode caData")
	// 	return nil, errors.Wrap(err, "Failed to decode caData from secret: %s/%s, key: %s", webhookConfig.SecretRef.Namespace, webhookConfig.SecretRef.Name, webhookConfig.SecretData)
	// }

	return []byte(decodedCaData), nil
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
	dir DirectoryStructure,
	config *rest.Config,
	templateData map[string]string,
) error {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	for _, workspace := range dir.Workspaces {
		if workspace.Name != "root" {
			wsName, _ := strings.CutPrefix(workspace.Name, "root:")
			err := r.waitForWorkspace(ctx, config, wsName, log)
			if err != nil {
				return err
			}
		}

		k8sClient, err := r.kcpHelper.NewKcpClient(config, workspace.Name)
		if err != nil {
			return err
		}
		for _, file := range workspace.Files {
			err := r.applyManifestFromFile(ctx, file, k8sClient, templateData)
			if err != nil {
				return err
			}
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

func (r *KcpsetupSubroutine) applyManifestFromFile(
	ctx context.Context,
	path string, k8sClient client.Client, templateData map[string]string,
) error {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	manifestBytes, err := os.ReadFile(path)
	if err != nil {
		pwdir, _ := os.Getwd()
		return errors.Wrap(err, "Failed to read file, pwd: %s", pwdir)
	}

	res, err := ReplaceTemplate(templateData, manifestBytes)
	if err != nil {
		return errors.Wrap(err, "Failed to replace template with path: %s", path)
	}

	var objMap map[string]interface{}
	if err := yaml.Unmarshal(res, &objMap); err != nil {
		return errors.Wrap(err, "Failed to unmarshal YAML from template %s. Output:\n%s", path, string(res))
	}

	obj := unstructured.Unstructured{Object: objMap}

	log.Debug().Str("file", path).Str("kind", obj.GetKind()).Str("name", obj.GetName()).Str("namespace", obj.GetNamespace()).Msg("Applying manifest")

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("openmfp-operator"))
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}

	return nil
}
