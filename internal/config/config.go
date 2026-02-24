package config

// OperatorConfig struct to hold the app config
type OperatorConfig struct {
	WorkspaceDir string `mapstructure:"workspace-dir" default:"/operator/"`
	KCP          struct {
		Url                    string `mapstructure:"kcp-url"`
		Namespace              string `mapstructure:"kcp-namespace" default:"platform-mesh-system"`
		RootShardName          string `mapstructure:"kcp-root-shard-name" default:"root"`
		FrontProxyName         string `mapstructure:"kcp-front-proxy-name" default:"frontproxy"`
		FrontProxyPort         string `mapstructure:"kcp-front-proxy-port" default:"6443"`
		ClusterAdminSecretName string `mapstructure:"kcp-cluster-admin-secret-name" default:"kcp-cluster-admin-kubeconfig"`
	} `mapstructure:",squash"`
	IDP struct {
		RegistrationAllowed bool `mapstructure:"idp-registration-allowed" default:"false"`
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
		FeatureToggles struct {
			Enabled bool `mapstructure:"subroutines-feature-toggles-enabled" default:"false"`
		} `mapstructure:",squash"`
		Wait struct {
			Enabled bool `mapstructure:"subroutines-wait-enabled" default:"true"`
		} `mapstructure:",squash"`
	} `mapstructure:",squash"`
}
