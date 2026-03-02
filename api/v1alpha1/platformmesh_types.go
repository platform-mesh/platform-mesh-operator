/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PlatformMeshSpec defines the desired state of PlatformMesh
type PlatformMeshSpec struct {
	Exposure         *ExposureConfig      `json:"exposure,omitempty"`
	Kcp              Kcp                  `json:"kcp,omitempty"`
	Values           apiextensionsv1.JSON `json:"values,omitempty"`
	OCM              *OCMConfig           `json:"ocm,omitempty"`
	FeatureToggles   []FeatureToggle      `json:"featureToggles,omitempty"`
	InfraValues      apiextensionsv1.JSON `json:"infraValues,omitempty"`
	Wait             *WaitConfig          `json:"wait,omitempty"`
	ProfileConfigMap *ConfigMapReference  `json:"profileConfigMap,omitempty"`
}

type ConfigMapReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

type WaitConfig struct {
	ResourceTypes []ResourceType `json:"resourceTypes,omitempty"` // e.g., apps/v1/Deployment, core/v1/
}

type ResourceType struct {
	Versions             []string `json:"versions,omitempty"`
	Group                string   `json:"group,omitempty"`
	Kind                 string   `json:"kind,omitempty"`
	Name                 string   `json:"name,omitempty"`
	Namespace            string   `json:"namespace,omitempty"`
	metav1.LabelSelector `json:",inline,omitempty"`
	ConditionStatus      metav1.ConditionStatus `json:"conditionStatus,omitempty"`
	ConditionType        string                 `json:"conditionType,omitempty"`
	// +optional
	StatusFieldPath []string `json:"statusFieldPath,omitempty"`
	// +optional
	StatusValue string `json:"statusValue,omitempty"`
}
type FeatureToggle struct {
	Name       string            `json:"name,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

type OCMConfig struct {
	Repo          *RepoConfig            `json:"repo,omitempty"`
	Component     *ComponentConfig       `json:"component,omitempty"`
	ReferencePath []ReferencePathElement `json:"referencePath,omitempty"`
}

type ReferencePathElement struct {
	Name string `json:"name"`
}

type RepoConfig struct {
	// +kubebuilder:default="platform-mesh"
	Name string `json:"name,omitempty"`
}

type ComponentConfig struct {
	// +kubebuilder:default="platform-mesh"
	Name string `json:"name,omitempty"`
}

type ExposureConfig struct {
	BaseDomain string `json:"baseDomain,omitempty"`
	Port       int    `json:"port,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
}

type Kcp struct {
	ProviderConnections      []ProviderConnection             `json:"providerConnections,omitempty"`
	ExtraProviderConnections []ProviderConnection             `json:"extraProviderConnections,omitempty"`
	ExtraDefaultAPIBindings  []DefaultAPIBindingConfiguration `json:"extraDefaultAPIBindings,omitempty"`
	// +optional
	ExtraWorkspaces []WorkspaceDeclaration `json:"extraWorkspaces,omitempty"`
}

type WorkspaceDeclaration struct {
	Path string                 `json:"path"`
	Type WorkspaceTypeReference `json:"type"`
}

type WorkspaceTypeReference struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type DefaultAPIBindingConfiguration struct {
	WorkspaceTypePath string `json:"workspaceTypePath"`
	Export            string `json:"export"`
	Path              string `json:"path"`
}

type InitializerConnection struct {
	WorkspaceTypeName string `json:"workspaceTypeName"`
	Path              string `json:"path"`
	Secret            string `json:"secret,omitempty"`
	Namespace         string `json:"namespace,omitempty"`
}

type WebhookConfiguration struct {
	SecretRef  SecretReference      `json:"secretRef,omitempty"`
	SecretData string               `json:"secretData,omitempty"`
	WebhookRef KCPAPIVersionKindRef `json:"webhookRef"`
}

type KCPAPIVersionKindRef struct {
	ApiVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Path       string `json:"path"`
}

type SecretReference struct {
	// +optional
	Name string `json:"name,omitempty" protobuf:"bytes,1,opt,name=name"`
	// +optional
	Namespace string `json:"namespace,omitempty" protobuf:"bytes,2,opt,name=namespace"`
}

type ProviderConnection struct {
	EndpointSliceName *string `json:"endpointSliceName,omitempty"`
	Path              string  `json:"path,omitempty"`
	RawPath           *string `json:"rawPath,omitempty"`
	Secret            string  `json:"secret"`
	External          bool    `json:"external,omitempty"`
	Namespace         *string `json:"namespace,omitempty"`
}

// PlatformMeshStatus defines the observed state of PlatformMesh
type PlatformMeshStatus struct {
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty" protobuf:"varint,3,opt,name=observedGeneration"`
	NextReconcileTime  metav1.Time        `json:"nextReconcileTime,omitempty"`
	KcpWorkspaces      []KcpWorkspace     `json:"kcpWorkspaces,omitempty"`
}

type KcpWorkspace struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="DEPLOYMENT",type=string,jsonPath=".status.conditions[?(@.type=='DeploymentSubroutine_Ready')].status",description="Deployment status (shows reason if Unknown)",priority=0
// +kubebuilder:printcolumn:name="KCP",type=string,jsonPath=".status.conditions[?(@.type=='KcpsetupSubroutine_Ready')].status",description="KCP status (shows reason if Unknown)",priority=0
// +kubebuilder:printcolumn:name="SECRET",type=string,jsonPath=".status.conditions[?(@.type=='ProvidersecretSubroutine_Ready')].status",description="Provider Secret status (shows reason if Unknown)",priority=0
// +kubebuilder:printcolumn:name="FEATURES",type=string,jsonPath=".status.conditions[?(@.type=='FeatureToggleSubroutine_Ready')].status",description="Feature toggles' status (shows reason if Unknown)",priority=0
// +kubebuilder:printcolumn:name="WAIT",type=string,jsonPath=".status.conditions[?(@.type=='WaitSubroutine_Ready')].status",description="Wait status (shows reason if Unknown)",priority=0
// +kubebuilder:printcolumn:name="Ready",type=string,jsonPath=".status.conditions[?(@.type=='Ready')].status",description="Shows if resource is ready",priority=0

// PlatformMesh is the Schema for the platform-mesh API
type PlatformMesh struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PlatformMeshSpec   `json:"spec,omitempty"`
	Status PlatformMeshStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PlatformMeshList contains a list of PlatformMesh
type PlatformMeshList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PlatformMesh `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PlatformMesh{}, &PlatformMeshList{})
}

func (i *PlatformMesh) GetConditions() []metav1.Condition           { return i.Status.Conditions }
func (i *PlatformMesh) SetConditions(conditions []metav1.Condition) { i.Status.Conditions = conditions }
