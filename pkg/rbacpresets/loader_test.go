package rbacpresets

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadPresetLoadsEmbeddedPreset(t *testing.T) {
	t.Parallel()

	preset, err := NewLoader(EmbeddedProvidersFS()).LoadPreset("sample", PresetTemplateData{
		ProviderPath: "root:platform-mesh-system",
		Suffix:       "sample-kubeconfig",
	})
	require.NoError(t, err)
	require.Equal(t, ServerTargetWorkspaceCluster, preset.Spec.ServerTarget.Type)
	require.Equal(t, "platform-mesh-provider-sample-kubeconfig", preset.Spec.ServiceAccountName)
	require.Len(t, preset.ByWorkspace, 1)
	require.Len(t, preset.ByWorkspace[0].Manifests, 3)
}

func TestLoadPresetRejectsUnsafeNames(t *testing.T) {
	t.Parallel()

	_, err := NewLoader(EmbeddedProvidersFS()).LoadPreset("../init-agent", PresetTemplateData{})
	require.ErrorContains(t, err, "invalid preset name")
}
