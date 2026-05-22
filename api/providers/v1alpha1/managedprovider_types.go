/*
Copyright 2026.

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
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ManagedProviderSpec defines the desired state of ManagedProvider.
// ManagedProvider is a runtime-cluster resource that orchestrates the full
// provider lifecycle: workspace creation, Provider bootstrap, secret copy,
// and workload deployment.
type ManagedProviderSpec struct {
	// workspacePath is the full kcp logical path for the provider workspace.
	// Defaults to root:providers:<name> when omitted.
	// When set, must be of the form root:providers:<name>
	//
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == '' || self.matches('^root:providers:[a-z][a-z0-9]*$')",message="workspacePath must be of the form root:providers:<Workspace name>"
	WorkspacePath string `json:"workspacePath,omitempty"`

	// platformMeshRef is a reference to the PlatformMesh object.
	// It must refer to the PlatformMesh instance this ManagedProvider
	// is associated with.
	PlatformMeshReference PlatformMeshReferenceSpec `json:"platformMeshRef"`

	// controller defines the OCM component to deploy as the provider controller.
	// +required
	Controller ProviderComponentSpec `json:"controller"`

	// portal defines the OCM component to deploy as the provider portal.
	// +optional
	Portal *ProviderComponentSpec `json:"portal,omitempty"`

	// cleanupOnDelete removes the kcp workspace when the ManagedProvider is deleted.
	// +optional
	CleanupOnDelete bool `json:"cleanupOnDelete,omitempty"`
}

// PlatformMeshReferenceSpec is a reference to a PlatformMesh object.
type PlatformMeshReferenceSpec struct {
	// name of the PlatformMesh object.
	// +required
	Name string `json:"name"`
}

// ProviderComponentSpec references an OCM component to deploy.
type ProviderComponentSpec struct {
	// ocm identifies the component in an OCM registry.
	// +required
	OCM OCMComponentSpec `json:"ocm"`
}

// OCMComponentSpec identifies a component in an OCM registry.
type OCMComponentSpec struct {
	// componentName is the fully-qualified OCM component name.
	// +required
	ComponentName string `json:"componentName"`

	// version is the component version to deploy.
	// +required
	Version string `json:"version"`

	// registry is the OCM registry host (e.g. ghcr.io/platform-mesh/ocm).
	// +required
	Registry string `json:"registry"`

	// values are Helm values passed to the deployed chart.
	// +optional
	Values apiextensionsv1.JSON `json:"values,omitempty"`
}

// ManagedProviderStatus defines the observed state of ManagedProvider.
type ManagedProviderStatus struct {
	// phase summarises the overall lifecycle state of the ManagedProvider.
	// +optional
	Phase string `json:"phase,omitempty"`

	// kubeconfigSecretRef points to the Secret in the runtime namespace that
	// contains the scoped kubeconfig copied from the provider kcp workspace.
	// +optional
	KubeconfigSecretRef *corev1.SecretReference `json:"kubeconfigSecretRef,omitempty"`

	// conditions represent the current state of the ManagedProvider resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// observedGeneration is the last generation reconciled by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".status.phase",name="Phase",type=string,description="Overall lifecycle phase"
// +kubebuilder:printcolumn:JSONPath=".status.conditions[?(@.type=='Ready')].status",name="Ready",type=string,description="Shows if resource is ready"

// ManagedProvider is the Schema for the managedproviders API.
// It orchestrates the full provider lifecycle from the runtime cluster side.
type ManagedProvider struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of ManagedProvider
	// +required
	Spec ManagedProviderSpec `json:"spec"`

	// status defines the observed state of ManagedProvider
	// +optional
	Status ManagedProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ManagedProviderList contains a list of ManagedProvider
type ManagedProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ManagedProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ManagedProvider{}, &ManagedProviderList{})
}

func (i *ManagedProvider) GetConditions() []metav1.Condition {
	return i.Status.Conditions
}

func (i *ManagedProvider) SetConditions(conditions []metav1.Condition) {
	i.Status.Conditions = conditions
}
