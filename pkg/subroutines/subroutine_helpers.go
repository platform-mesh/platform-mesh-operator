package subroutines

import (
	"context"
	"fmt"
	"net/url"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/openmfp/golang-commons/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type APIExportInventory struct {
	ApiExportRootTenancyKcpIoIdentityHash  string `yaml:"apiExportRootTenancyKcpIoIdentityHash"`
	ApiExportRootShardsKcpIoIdentityHash   string `yaml:"apiExportRootShardsKcpIoIdentityHash"`
	ApiExportRootTopologyKcpIoIdentityHash string `yaml:"apiExportRootTopologyKcpIoIdentityHash"`
}

type DirectoryStructure struct {
	Workspaces []WorkspaceDirectory `yaml:"workspaces"`
}

type WorkspaceDirectory struct {
	Name  string   `yaml:"name"`
	Files []string `yaml:"files"`
}

// apply manifests for folder dir to KCP
var DirManifestStructure = DirectoryStructure{
	Workspaces: []WorkspaceDirectory{
		{
			Name: "root",
			Files: []string{
				"/operator/setup/workspace-openmfp-system.yaml",
				"/operator/setup/workspace-type-provider.yaml",
				"/operator/setup/workspace-type-providers.yaml",
				"/operator/setup/workspace-type-org.yaml",
				"/operator/setup/workspace-type-orgs.yaml",
				"/operator/setup/workspace-type-account.yaml",
			},
		},
		{
			Name: "root:openmfp-system",
			Files: []string{
				"/operator/setup/01-openmfp-system/apiexport-core.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiexport-fga.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiexport-kcp.io.yaml",
				"/operator/setup/01-openmfp-system/apiexportendpointslice-core.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiexportendpointslice-fga.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiresourceschema-accountinfos.core.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiresourceschema-accounts.core.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiresourceschema-authorizationmodels.core.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiresourceschema-stores.core.openmfp.org.yaml",
			},
		},
		{
			Name: "root",
			Files: []string{
				"/operator/setup/workspace-orgs.yaml",
			},
		},
		{
			Name: "root:orgs",
			Files: []string{
				"/operator/setup/02-orgs/account-root-org.yaml",
				"/operator/setup/02-orgs/workspace-root-org.yaml",
			},
		},
	},
}

type KcpHelper interface {
	NewKcpClient(config *rest.Config, workspacePath string) (client.Client, error)
	GetSecret(client client.Client, name string, namespace string) (*corev1.Secret, error)
}

type Helper struct {
}

func (h *Helper) NewKcpClient(config *rest.Config, workspacePath string) (client.Client, error) {
	config.QPS = 1000.0
	config.Burst = 2000.0
	u, err := url.Parse(config.Host)
	if err != nil {
		return nil, fmt.Errorf("Unable to parse KCP host: %w", err)
	}
	config.Host = u.Scheme + "://" + u.Host + "/clusters/" + workspacePath
	scheme := runtime.NewScheme()
	utilruntime.Must(kcpapiv1alpha.AddToScheme(scheme))
	utilruntime.Must(kcptenancyv1alpha.AddToScheme(scheme))

	client, err := client.New(config, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("Unable to create KCP client: %w", err)
	}
	return client, nil
}

func (h *Helper) GetSecret(client client.Client, name string, namespace string) (*corev1.Secret, error) {
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

var DEFAULT_KCP_SECRET_KEY = "kubeconfig"
var DEFAULT_KCP_SECRET_NAME = "openmfp-operator-kubeconfig"
