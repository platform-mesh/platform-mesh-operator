package subroutines

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openmfp/golang-commons/controller/lifecycle"
	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/yaml"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"text/template"
)

type KcpsetupSubroutine struct {
	client       client.Client
	kcpHelper    KcpHelper
	kcpDirectory DirectoryStructure
}

const (
	KcpsetupSubroutineName      = "KcpsetupSubroutine"
	KcpsetupSubroutineFinalizer = "openmfp.core.openmfp.org/finalizer"
)

func NewKcpsetupSubroutine(client client.Client, helper KcpHelper, kcpdir DirectoryStructure) *KcpsetupSubroutine {
	sub := &KcpsetupSubroutine{
		client:       client,
		kcpDirectory: kcpdir,
	}
	if helper == nil {
		sub.kcpHelper = &Helper{}
	} else {
		sub.kcpHelper = helper
	}
	return sub
}

func (r *KcpsetupSubroutine) GetName() string {
	return KcpsetupSubroutineName
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

// TODO: Implement the following methods
func (r *KcpsetupSubroutine) Process(
	ctx context.Context, runtimeObj lifecycle.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	instance := runtimeObj.(*corev1alpha1.OpenMFP)
	log.Debug().Str("name", instance.Name).Msg("Processing OpenMFP instance")

	log.Debug().Str("name", instance.Name).Str(
		"kcp-secret-name", instance.Spec.Kcp.AdminSecretRef.Name).Msg("Processing kcp secrect")

	// Get the secret
	secret, err := r.kcpHelper.GetSecret(
		r.client, instance.Spec.Kcp.AdminSecretRef.Name, instance.Namespace,
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	secretKey := DEFAULT_KCP_SECRET_KEY
	if instance.Spec.Kcp.AdminSecretRef.Key != nil {
		secretKey = *instance.Spec.Kcp.AdminSecretRef.Key
	}
	err = r.createKcpWorkspaces(ctx, *secret, secretKey, r.kcpDirectory)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create kcp workspaces")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
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

func (r *KcpsetupSubroutine) createKcpWorkspaces(ctx context.Context, secret corev1.Secret, secretKey string, dir DirectoryStructure) error {

	// kcp kubernetes client
	config, err := clientcmd.RESTConfigFromKubeConfig(secret.Data[secretKey])
	if err != nil {
		log.Error().Err(err).Msg("Failed to build config from kubeconfig string")
		return errors.Wrap(err, "Failed to build config from kubeconfig string")
	}

	inventory, err := r.getAPIExportHashInventory(ctx, config)
	if err != nil {
		log.Err(err).Msg("Failed to get APIExport hash inventory")
		return errors.Wrap(err, "Failed to get APIExport hash inventory")
	}

	// TODO: check if already applied
	err = r.applyDirStructure(ctx, dir, config, inventory)
	if err != nil {
		log.Err(err).Msg("Failed to apply dir structure")
		return errors.Wrap(err, "Failed to apply dir structure")
	}

	return nil

}

func (r *KcpsetupSubroutine) getAPIExportHashInventory(ctx context.Context, config *rest.Config) (APIExportInventory, error) {
	inventory := APIExportInventory{}

	cs, err := r.kcpHelper.NewKcpClient(config, "root")
	if err != nil {
		return inventory, err
	}

	apiExport := kcpapiv1alpha.APIExport{}
	err = cs.Get(ctx, types.NamespacedName{Name: "tenancy.kcp.io"}, &apiExport)
	if err != nil {
		return inventory, err
	}
	inventory.ApiExportRootTenancyKcpIoIdentityHash = apiExport.Status.IdentityHash

	err = cs.Get(ctx, types.NamespacedName{Name: "shards.core.kcp.io"}, &apiExport)
	if err != nil {
		return inventory, err
	}
	inventory.ApiExportRootShardsKcpIoIdentityHash = apiExport.Status.IdentityHash

	err = cs.Get(ctx, types.NamespacedName{Name: "topology.kcp.io"}, &apiExport)
	if err != nil {
		return inventory, err
	}
	inventory.ApiExportRootTopologyKcpIoIdentityHash = apiExport.Status.IdentityHash

	return inventory, nil
}

func (r *KcpsetupSubroutine) applyDirStructure(
	ctx context.Context, dir DirectoryStructure, config *rest.Config, hashes APIExportInventory,
) error {
	log := logger.LoadLoggerFromContext(ctx)
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
			err := r.applyManifestFromFile(ctx, file, k8sClient, hashes)
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

	// It shouldn't take longer than 5s for the default namespace to be brought up in etcd
	err = wait.PollUntilContextTimeout(
		ctx, time.Millisecond*500, time.Second*15, true,
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
	path string, k8sClient client.Client, hashes APIExportInventory,
) error {
	manifestBytes, err := os.ReadFile(path)
	if err != nil {
		return errors.Wrap(err, "Failed to read file")
	}

	tmpl, err := template.New("manifest").Parse(string(manifestBytes))
	if err != nil {
		return errors.Wrap(err, "Failed to parse template")
	}
	var result bytes.Buffer
	err = tmpl.Execute(&result, hashes)
	if err != nil {
		return errors.Wrap(err, "Failed to execute template")
	}

	var objMap map[string]interface{}
	if err := yaml.Unmarshal(result.Bytes(), &objMap); err != nil {
		return errors.Wrap(err, "Failed to unmarshal YAML")
	}

	obj := unstructured.Unstructured{Object: objMap}
	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("controller-runtime"))
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest")
	}

	return nil
}
