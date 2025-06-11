package config

// OperatorConfig struct to hold the app config
type OperatorConfig struct {
	WorkspaceDir string `mapstructure:"workspace-dir" default:"/operator/"`
	KCPUrl       string `mapstructure:"kcp-url"`
	Subroutines  struct {
		Deployment struct {
			Enabled bool `mapstructure:"subroutines-deployment-enabled" default:"true"`
		} `mapstructure:",squash"`
		KcpSetup struct {
			Enabled bool `mapstructure:"subroutines-kcp-setup-enabled" default:"true"`
		} `mapstructure:",squash"`
		ProviderSecret struct {
			Enabled bool `mapstructure:"subroutines-provider-secret-enabled" default:"true"`
		} `mapstructure:",squash"`
	} `mapstructure:",squash"`
}
