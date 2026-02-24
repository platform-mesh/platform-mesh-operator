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
		ClusterAdminSecretName string `mapstructure:"kcp-cluster-admin-secret-name" default:"kcp-cluster-admin-client-cert"`
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
			Enabled  bool `mapstructure:"subroutines-wait-enabled" default:"true"`
			Resource struct {
				Enabled bool `mapstructure:"subroutines-resource-enabled" default:"true"`
			} `mapstructure:",squash"`
		} `mapstructure:",squash"`
	} `mapstructure:",squash"`
	RemoteInfra   RemoteInfraConfig   `mapstructure:",squash"`
	RemoteRuntime RemoteRuntimeConfig `mapstructure:",squash"`
}

// RemoteInfraConfig holds configuration for remote infrastructure cluster
type RemoteInfraConfig struct {
	Kubeconfig string `mapstructure:"remote-infra-kubeconfig"`
}

// IsEnabled returns true if remote infra is enabled (kubeconfig is set)
func (c *RemoteInfraConfig) IsEnabled() bool {
	return c.Kubeconfig != ""
}

// RemoteRuntimeConfig holds configuration for remote runtime cluster
type RemoteRuntimeConfig struct {
	Kubeconfig      string `mapstructure:"remote-runtime-kubeconfig"`
	InfraSecretName string `mapstructure:"remote-runtime-infra-secret-name" default:"infra-kubeconfig"`
	InfraSecretKey  string `mapstructure:"remote-runtime-infra-secret-key" default:"kubeconfig"`
}

// IsEnabled returns true if remote runtime is enabled (kubeconfig is set)
func (c *RemoteRuntimeConfig) IsEnabled() bool {
	return c.Kubeconfig != ""
}
