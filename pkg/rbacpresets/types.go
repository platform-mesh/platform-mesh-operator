package rbacpresets

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	GroupVersion           = "rbacpresets.platform-mesh.io/v1alpha1"
	KindProviderRBACPreset = "ProviderRBACPreset"
	AnnotationWorkspace    = "rbacpresets.platform-mesh.io/workspace"
)

type ServerTargetType string

const (
	ServerTargetWorkspaceCluster              ServerTargetType = "workspaceCluster"
	ServerTargetRawPath                       ServerTargetType = "rawPath"
	ServerTargetWorkspaceTypeVirtualWorkspace ServerTargetType = "workspaceTypeVirtualWorkspace"
	ServerTargetPathRawPath                   ServerTargetType = "pathRawPath"
)

type ProviderRBACPreset struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ProviderRBACPresetSpec `json:"spec"`
}

type ProviderRBACPresetSpec struct {
	ServerTarget ServerTarget `json:"serverTarget"`
	// Defaulted to the provider connection path when empty.
	ServiceAccountWorkspace string `json:"serviceAccountWorkspace,omitempty"`
	// Defaulted to "platform-mesh-provider-<provider secret>" when empty.
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

type ServerTarget struct {
	Type ServerTargetType `json:"type"`
	// For rawPath: optional preset-declared rawPath that overrides pc.RawPath when set.
	RawPath string `json:"rawPath,omitempty"`
	// For workspaceTypeVirtualWorkspace.
	WorkspaceTypeName string `json:"workspaceTypeName,omitempty"`
	WorkspaceTypePath string `json:"workspaceTypePath,omitempty"`
}

type PresetTemplateData struct {
	ProviderPath string
	RawPath      string
	SAName       string
	Suffix       string
}

type RenderedPreset struct {
	Spec        ProviderRBACPresetSpec
	ByWorkspace []WorkspaceManifests
}

type WorkspaceManifests struct {
	Workspace string
	Manifests []unstructured.Unstructured
}
