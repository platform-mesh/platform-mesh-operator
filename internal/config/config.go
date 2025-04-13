package config

// OperatorConfig struct to hold the app config
type OperatorConfig struct {
	Subroutines struct {
		KcpSetup struct {
			Enabled bool `mapstructure:"subroutines-kcp-setup-enabled"`
		} `mapstructure:",squash"`
		ProviderSecret struct {
			Enabled bool `mapstructure:"subroutines-provider-secret-enabled"`
		} `mapstructure:",squash"`
		Webhook struct {
			Enabled bool `mapstructure:"subroutines-webhook-enabled"`
		} `mapstructure:",squash"`
	} `mapstructure:",squash"`
}
