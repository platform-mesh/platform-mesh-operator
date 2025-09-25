package config

// OperatorConfig struct to hold the app config
type OperatorConfig struct {
	WorkspaceDir               string `mapstructure:"workspace-dir" default:"/operator/"`
	PatchOIDCControllerEnabled bool   `mapstructure:"patch-oidc-controller-enabled" default:"false"`
	KCP                        struct {
		Url                    string `mapstructure:"kcp-url"`
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
		} `mapstructure:",squash"`
		KcpSetup struct {
			Enabled bool `mapstructure:"subroutines-kcp-setup-enabled" default:"true"`
		} `mapstructure:",squash"`
		ProviderSecret struct {
			Enabled bool `mapstructure:"subroutines-provider-secret-enabled" default:"true"`
		} `mapstructure:",squash"`
		PatchOIDC struct {
			ConfigMapName string `mapstructure:"subroutines-patch-oidc-configmap-name" default:"oidc-authentication-config"`
			Namespace     string `mapstructure:"subroutines-patch-oidc-namespace" default:"platform-mesh-system"`
			BaseDomain    string `mapstructure:"subroutines-patch-oidc-basedomain" default:"portal.dev.local:8443"`
		} `mapstructure:",squash"`
	} `mapstructure:",squash"`
}
