package rbacpresets

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderPresetGroupsTemplatedManifestsByWorkspace(t *testing.T) {
	t.Parallel()

	raw := []byte(`kind: ProviderRBACPreset
metadata:
  name: test
spec:
  serverTarget:
    type: workspaceCluster
  serviceAccountWorkspace: "{{ .ProviderPath }}"
  serviceAccountName: "platform-mesh-provider-{{ .Suffix }}"
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: "{{ .SAName }}"
  namespace: default
  annotations:
    rbacpresets.platform-mesh.io/workspace: "{{ .ProviderPath }}"
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: "{{ .SAName }}-extra"
  annotations:
    rbacpresets.platform-mesh.io/workspace: root
subjects: []
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: anything
`)

	preset, err := RenderPreset("test", raw, PresetTemplateData{
		ProviderPath: "root:platform-mesh-system",
		Suffix:       "init-agent-kubeconfig",
	})
	require.NoError(t, err)
	require.Equal(t, ServerTargetWorkspaceCluster, preset.Spec.ServerTarget.Type)
	require.Equal(t, "root:platform-mesh-system", preset.Spec.ServiceAccountWorkspace)
	require.Equal(t, "platform-mesh-provider-init-agent-kubeconfig", preset.Spec.ServiceAccountName)
	require.Len(t, preset.ByWorkspace, 2)

	require.Equal(t, "root", preset.ByWorkspace[0].Workspace)
	require.Equal(t, "ClusterRoleBinding", preset.ByWorkspace[0].Manifests[0].GetKind())
	require.NotContains(t, preset.ByWorkspace[0].Manifests[0].GetAnnotations(), AnnotationWorkspace)

	require.Equal(t, "root:platform-mesh-system", preset.ByWorkspace[1].Workspace)
	require.Equal(t, "ServiceAccount", preset.ByWorkspace[1].Manifests[0].GetKind())
	require.Equal(t, "platform-mesh-provider-init-agent-kubeconfig", preset.ByWorkspace[1].Manifests[0].GetName())
	require.NotContains(t, preset.ByWorkspace[1].Manifests[0].GetAnnotations(), AnnotationWorkspace)
}

func TestRenderPresetErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{
			name: "missing header",
			raw: `apiVersion: v1
kind: ServiceAccount
metadata:
  name: test
`,
			wantErr: "missing ProviderRBACPreset header document",
		},
		{
			name: "multiple headers",
			raw: `kind: ProviderRBACPreset
metadata:
  name: one
spec:
  serverTarget:
    type: workspaceCluster
---
kind: ProviderRBACPreset
metadata:
  name: two
spec:
  serverTarget:
    type: workspaceCluster
`,
			wantErr: "multiple ProviderRBACPreset documents",
		},
		{
			name: "unsupported kind",
			raw: `kind: ProviderRBACPreset
metadata:
  name: test
spec:
  serverTarget:
    type: workspaceCluster
  serviceAccountWorkspace: root
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`,
			wantErr: "unsupported manifest kind",
		},
		{
			name: "missing workspace",
			raw: `kind: ProviderRBACPreset
metadata:
  name: test
spec:
  serverTarget:
    type: workspaceCluster
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: test
  namespace: default
`,
			wantErr: "has no rbacpresets.platform-mesh.io/workspace annotation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := RenderPreset("test", []byte(tt.raw), PresetTemplateData{})
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
