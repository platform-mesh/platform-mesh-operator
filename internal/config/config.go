package config

// OperatorConfig struct to hold the app config
type OperatorConfig struct {
	WorkspaceDir               string `mapstructure:"workspace-dir" default:"/operator/"`
	PatchOIDCControllerEnabled bool   `mapstructure:"patch-oidc-controller-enabled" default:"false"`
	KCP                        struct {
		Namespace              string `mapstructure:"kcp-namespace" default:"platform-mesh-system"`
		RootShardName          string `mapstructure:"kcp-root-shard-name" default:"root"`
		FrontProxyName         string `mapstructure:"kcp-front-proxy-name" default:"frontproxy"`
		FrontProxyPort         string `mapstructure:"kcp-front-proxy-port" default:"6443"`
		ClusterAdminSecretName string `mapstructure:"kcp-cluster-admin-secret-name" default:"kcp-cluster-admin-client-cert"`
	} `mapstructure:",squash"`
	Subroutines struct {
		Deployment struct {
			Enabled                          bool   `mapstructure:"subroutines-deployment-enabled" default:"true"`
			AuthorizationWebhookSecretName   string `mapstructure:"authorization-webhook-secret-name" default:"kcp-webhook-secret"`
			AuthorizationWebhookSecretCAName string `mapstructure:"authorization-webhook-secret-ca-name" default:"rebac-authz-webhook-cert"`
			EnableIstio                      bool   `mapstructure:"subroutines-deployment-enable-istio" default:"true"`
		} `mapstructure:",squash"`
		KcpSetup struct {
			Enabled bool `mapstructure:"subroutines-kcp-setup-enabled" default:"true"`
		} `mapstructure:",squash"`
		ProviderSecret struct {
			Enabled bool `mapstructure:"subroutines-provider-secret-enabled" default:"true"`
		} `mapstructure:",squash"`
		PatchOIDC struct {
			ConfigMapName  string `mapstructure:"subroutines-patch-oidc-configmap-name" default:"oidc-authentication-config"`
			Namespace      string `mapstructure:"subroutines-patch-oidc-namespace" default:"platform-mesh-system"`
			BaseDomain     string `mapstructure:"subroutines-patch-oidc-basedomain" default:"portal.dev.local:8443"`
			DomainCALookup bool   `mapstructure:"subroutines-patch-oidc-domain-ca-lookup" default:"false"`
		} `mapstructure:",squash"`
		FeatureToggles struct {
			Enabled bool `mapstructure:"subroutines-feature-toggles-enabled" default:"false"`
		} `mapstructure:",squash"`
		Wait struct {
			Enabled  bool `mapstructure:"subroutines-wait-enabled" default:"true"`
			Resource struct {
				Enabled bool `mapstructure:"subroutines-resource-enabled" default:"true"`
			} `mapstructure:",squash"`
		} `mapstructure:",squash"`
	}
	RemoteInfra struct {
		Enabled    bool   `mapstructure:"remote-infra-enabled" default:"false"`
		Kubeconfig string `mapstructure:"remote-infra-kubeconfig" default:"/operator/infra-kubeconfig"`
	} `mapstructure:",squash"`
	RemoteRuntime struct {
		Enabled         bool   `mapstructure:"remote-runtime-enabled" default:"false"`
		Kubeconfig      string `mapstructure:"remote-runtime-kubeconfig" default:"/operator/runtime-kubeconfig"`
		InfraSecretName string `mapstructure:"remote-runtime-infra-secret-name" default:"infra-kubeconfig"`
		InfraSecretKey  string `mapstructure:"remote-runtime-infra-secret-key" default:"kubeconfig"`
	} `mapstructure:",squash"`
}
