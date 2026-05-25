// Package rbacpresets defines the in-tree provider RBAC preset format. Preset
// files use a ProviderRBACPreset header document (kind only) followed by RBAC
// manifests; this is not a Kubernetes CRD and is not registered with the
// controller-runtime scheme. ProviderConnection.providerRBACPreset (in
// api/v1alpha1) references a preset by name only.
package rbacpresets

const (
	KindProviderRBACPreset = "ProviderRBACPreset"
	AnnotationWorkspace    = "rbacpresets.platform-mesh.io/workspace"
	LabelPreset            = "rbacpresets.platform-mesh.io/preset"
	LabelProviderSecret    = "rbacpresets.platform-mesh.io/provider-secret"
	LabelManagedBy         = "app.kubernetes.io/managed-by"
	ManagedByPlatformMesh  = "platform-mesh-operator"
)

type ServerTargetType string

const (
	ServerTargetWorkspaceCluster              ServerTargetType = "workspaceCluster"
	ServerTargetRawPath                       ServerTargetType = "rawPath"
	ServerTargetWorkspaceTypeVirtualWorkspace ServerTargetType = "workspaceTypeVirtualWorkspace"
	ServerTargetPathRawPath                   ServerTargetType = "pathRawPath"
)

type ProviderRBACPresetSpec struct {
	ServerTarget ServerTarget `yaml:"serverTarget" json:"serverTarget"`
	// Defaulted to the provider connection path when empty.
	ServiceAccountWorkspace string `yaml:"serviceAccountWorkspace,omitempty" json:"serviceAccountWorkspace,omitempty"`
	// Defaulted to "platform-mesh-provider-<provider secret>" when empty.
	ServiceAccountName string `yaml:"serviceAccountName,omitempty" json:"serviceAccountName,omitempty"`
}

type ServerTarget struct {
	Type ServerTargetType `yaml:"type" json:"type"`
	// For rawPath: optional preset-declared rawPath that overrides pc.RawPath when set.
	RawPath string `yaml:"rawPath,omitempty" json:"rawPath,omitempty"`
	// For workspaceTypeVirtualWorkspace.
	WorkspaceTypeName string `yaml:"workspaceTypeName,omitempty" json:"workspaceTypeName,omitempty"`
	WorkspaceTypePath string `yaml:"workspaceTypePath,omitempty" json:"workspaceTypePath,omitempty"`
}

type presetDocument struct {
	Spec ProviderRBACPresetSpec `yaml:"spec"`
}

var allowedManifestKinds = map[string]struct{}{
	"ServiceAccount":     {},
	"ClusterRole":        {},
	"ClusterRoleBinding": {},
	"RoleBinding":        {},
}
