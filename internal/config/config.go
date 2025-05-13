package config

// OperatorConfig struct to hold the app config
type OperatorConfig struct {
	WorkspaceDir string `mapstructure:"workspace-dir"`
	Subroutines  struct {
		Deployment struct {
			Enabled bool `mapstructure:"subroutines-deployment-enabled"`
		} `mapstructure:",squash"`
		KcpSetup struct {
			Enabled bool `mapstructure:"subroutines-kcp-setup-enabled"`
		} `mapstructure:",squash"`
		ProviderSecret struct {
			Enabled bool `mapstructure:"subroutines-provider-secret-enabled"`
		} `mapstructure:",squash"`
	} `mapstructure:",squash"`
}
