package config_test

import (
	"testing"

	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestRemoteInfraConfig_IsEnabled(t *testing.T) {
	assert.False(t, (&config.RemoteInfraConfig{}).IsEnabled(), "empty kubeconfig should not be enabled")
	assert.True(t, (&config.RemoteInfraConfig{Kubeconfig: "/path/to/kubeconfig"}).IsEnabled(), "non-empty kubeconfig should be enabled")
}

func TestRemoteRuntimeConfig_IsEnabled(t *testing.T) {
	assert.False(t, (&config.RemoteRuntimeConfig{}).IsEnabled(), "empty kubeconfig should not be enabled")
	assert.True(t, (&config.RemoteRuntimeConfig{Kubeconfig: "/path/to/kubeconfig"}).IsEnabled(), "non-empty kubeconfig should be enabled")
}
