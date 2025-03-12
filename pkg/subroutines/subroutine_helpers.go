package subroutines

import (
	"context"
	"fmt"
	"net/url"

	"github.com/openmfp/golang-commons/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
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
var manifestStructure = DirectoryStructure{
	Workspaces: []WorkspaceDirectory{
		{
			Name: "root",
			Files: []string{
				"../../test/setup/workspace-openmfp-system.yaml",
				"../../test/setup/workspacetype-org.yaml",
				"../../test/setup/workspace-type-orgs.yaml",
				"../../test/setup/workspace-type-account.yaml",
				"../../test/setup/workspace-orgs.yaml",
			},
		},
		{
			Name: "root:openmfp-system",
			Files: []string{
				"../../test/setup/01-openmfp-system/apiexport-core.openmfp.org.yaml",
				"../../test/setup/01-openmfp-system/apiexportendpointslice-core.openmfp.org.yaml",
				"../../test/setup/01-openmfp-system/apiresourceschema-accountinfos.core.openmfp.org.yaml",
				"../../test/setup/01-openmfp-system/apiresourceschema-accounts.core.openmfp.org.yaml",
				"../../test/setup/01-openmfp-system/apiresourceschema-authorizationmodels.core.openmfp.org.yaml",
				"../../test/setup/01-openmfp-system/apiresourceschema-stores.core.openmfp.org.yaml",
			},
		},
		{
			Name: "root:orgs",
			Files: []string{
				"../../test/setup/02-orgs/account-root-org.yaml",
				"../../test/setup/02-orgs/workspace-root-org.yaml",
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
	client, err := client.New(config, client.Options{})
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
