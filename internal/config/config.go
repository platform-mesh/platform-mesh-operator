package config

import "github.com/spf13/pflag"

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
			Enabled                       bool   `mapstructure:"subroutines-kcp-setup-enabled" default:"true"`
			DomainCertificateCASecretName string `mapstructure:"domain-certificate-ca-secret-name" default:"domain-certificate"`
			DomainCertificateCASecretKey  string `mapstructure:"domain-certificate-ca-secret-key" default:"tls.crt"`
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

func NewOperatorConfig() OperatorConfig {
	cfg := OperatorConfig{}
	cfg.WorkspaceDir = "/operator/"
	cfg.KCP.Namespace = "platform-mesh-system"
	cfg.KCP.RootShardName = "root"
	cfg.KCP.FrontProxyName = "frontproxy"
	cfg.KCP.FrontProxyPort = "6443"
	cfg.KCP.ClusterAdminSecretName = "kcp-cluster-admin-client-cert"
	cfg.Subroutines.Deployment.Enabled = true
	cfg.Subroutines.Deployment.AuthorizationWebhookSecretName = "kcp-webhook-secret"
	cfg.Subroutines.Deployment.AuthorizationWebhookSecretCAName = "rebac-authz-webhook-cert"
	cfg.Subroutines.Deployment.EnableIstio = true
	cfg.Subroutines.KcpSetup.Enabled = true
	cfg.Subroutines.KcpSetup.DomainCertificateCASecretName = "domain-certificate"
	cfg.Subroutines.KcpSetup.DomainCertificateCASecretKey = "tls.crt"
	cfg.Subroutines.ProviderSecret.Enabled = true
	cfg.Subroutines.FeatureToggles.Enabled = false
	cfg.Subroutines.Wait.Enabled = true
	return cfg
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
}
