package config

import "github.com/spf13/pflag"

type KCPConfig struct {
	Url                    string
	Namespace              string
	RootShardName          string
	FrontProxyName         string
	FrontProxyPort         string
	ClusterAdminSecretName string
}

type IDPConfig struct {
	RegistrationAllowed bool
}

type DeploymentSubroutineConfig struct {
	Enabled                          bool
	AuthorizationWebhookSecretName   string
	AuthorizationWebhookSecretCAName string
	EnableIstio                      bool
}

type KcpSetupSubroutineConfig struct {
	Enabled                       bool
	DomainCertificateCASecretName string
	DomainCertificateCASecretKey  string
}

type ProviderSecretSubroutineConfig struct {
	Enabled bool
}

type FeatureTogglesSubroutineConfig struct {
	Enabled bool
}

type WaitSubroutineConfig struct {
	Enabled bool
}

type RemoteClusterConfig struct {
	Kubeconfig      string
	InfraSecretName string
	InfraSecretKey  string
}

func (r *RemoteClusterConfig) IsEnabled() bool {
	return r.Kubeconfig != ""
}

type SubroutinesConfig struct {
	Deployment     DeploymentSubroutineConfig
	KcpSetup       KcpSetupSubroutineConfig
	ProviderSecret ProviderSecretSubroutineConfig
	FeatureToggles FeatureTogglesSubroutineConfig
	Wait           WaitSubroutineConfig
}

// OperatorConfig struct to hold the app config
type OperatorConfig struct {
	WorkspaceDir  string
	KCP           KCPConfig
	IDP           IDPConfig
	Subroutines   SubroutinesConfig
	RemoteRuntime RemoteClusterConfig
	RemoteInfra   RemoteClusterConfig
}

func NewOperatorConfig() OperatorConfig {
	return OperatorConfig{
		WorkspaceDir: "/operator/",
		KCP: KCPConfig{
			Namespace:              "platform-mesh-system",
			RootShardName:          "root",
			FrontProxyName:         "frontproxy",
			FrontProxyPort:         "6443",
			ClusterAdminSecretName: "kcp-cluster-admin-client-cert",
		},
		Subroutines: SubroutinesConfig{
			Deployment: DeploymentSubroutineConfig{
				Enabled:                          true,
				AuthorizationWebhookSecretName:   "kcp-webhook-secret",
				AuthorizationWebhookSecretCAName: "rebac-authz-webhook-cert",
				EnableIstio:                      true,
			},
			KcpSetup: KcpSetupSubroutineConfig{
				Enabled:                       true,
				DomainCertificateCASecretName: "domain-certificate",
				DomainCertificateCASecretKey:  "ca.crt",
			},
			ProviderSecret: ProviderSecretSubroutineConfig{
				Enabled: true,
			},
			FeatureToggles: FeatureTogglesSubroutineConfig{
				Enabled: false,
			},
			Wait: WaitSubroutineConfig{
				Enabled: true,
			},
		},
	}
}

func (c *OperatorConfig) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.WorkspaceDir, "workspace-dir", c.WorkspaceDir, "Set workspace directory")

	fs.StringVar(&c.KCP.Url, "kcp-url", c.KCP.Url, "Set KCP URL")
	fs.StringVar(&c.KCP.Namespace, "kcp-namespace", c.KCP.Namespace, "Set KCP namespace")
	fs.StringVar(&c.KCP.RootShardName, "kcp-root-shard-name", c.KCP.RootShardName, "Set KCP root shard name")
	fs.StringVar(&c.KCP.FrontProxyName, "kcp-front-proxy-name", c.KCP.FrontProxyName, "Set KCP front-proxy name")
	fs.StringVar(&c.KCP.FrontProxyPort, "kcp-front-proxy-port", c.KCP.FrontProxyPort, "Set KCP front-proxy port")
	fs.StringVar(&c.KCP.ClusterAdminSecretName, "kcp-cluster-admin-secret-name", c.KCP.ClusterAdminSecretName, "Set cluster-admin secret name")

	fs.BoolVar(&c.IDP.RegistrationAllowed, "idp-registration-allowed", c.IDP.RegistrationAllowed, "Allow IDP registration")

	fs.BoolVar(&c.Subroutines.Deployment.Enabled, "subroutines-deployment-enabled", c.Subroutines.Deployment.Enabled, "Enable deployment subroutine")
	fs.StringVar(&c.Subroutines.Deployment.AuthorizationWebhookSecretName, "authorization-webhook-secret-name", c.Subroutines.Deployment.AuthorizationWebhookSecretName, "Authorization webhook secret name")
	fs.StringVar(&c.Subroutines.Deployment.AuthorizationWebhookSecretCAName, "authorization-webhook-secret-ca-name", c.Subroutines.Deployment.AuthorizationWebhookSecretCAName, "Authorization webhook CA secret name")
	fs.BoolVar(&c.Subroutines.Deployment.EnableIstio, "subroutines-deployment-enable-istio", c.Subroutines.Deployment.EnableIstio, "Enable Istio integration in deployment subroutine")

	fs.BoolVar(&c.Subroutines.KcpSetup.Enabled, "subroutines-kcp-setup-enabled", c.Subroutines.KcpSetup.Enabled, "Enable KCP setup subroutine")
	fs.StringVar(&c.Subroutines.KcpSetup.DomainCertificateCASecretName, "domain-certificate-ca-secret-name", c.Subroutines.KcpSetup.DomainCertificateCASecretName, "Domain certificate secret name")
	fs.StringVar(&c.Subroutines.KcpSetup.DomainCertificateCASecretKey, "domain-certificate-ca-secret-key", c.Subroutines.KcpSetup.DomainCertificateCASecretKey, "Domain certificate secret key")

	fs.BoolVar(&c.Subroutines.ProviderSecret.Enabled, "subroutines-provider-secret-enabled", c.Subroutines.ProviderSecret.Enabled, "Enable provider secret subroutine")
	fs.BoolVar(&c.Subroutines.FeatureToggles.Enabled, "subroutines-feature-toggles-enabled", c.Subroutines.FeatureToggles.Enabled, "Enable feature toggles subroutine")
	fs.BoolVar(&c.Subroutines.Wait.Enabled, "subroutines-wait-enabled", c.Subroutines.Wait.Enabled, "Enable wait subroutine")

	fs.StringVar(&c.RemoteRuntime.Kubeconfig, "remote-runtime-kubeconfig", c.RemoteRuntime.Kubeconfig, "Kubeconfig for remote runtime cluster")
	fs.StringVar(&c.RemoteRuntime.InfraSecretName, "remote-runtime-infra-secret-name", c.RemoteRuntime.InfraSecretName, "Secret name for remote runtime infra kubeconfig")
	fs.StringVar(&c.RemoteRuntime.InfraSecretKey, "remote-runtime-infra-secret-key", c.RemoteRuntime.InfraSecretKey, "Secret key for remote runtime infra kubeconfig")

	fs.StringVar(&c.RemoteInfra.Kubeconfig, "remote-infra-kubeconfig", c.RemoteInfra.Kubeconfig, "Kubeconfig for remote infra cluster")
}
