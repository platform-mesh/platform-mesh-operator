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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProviderSpec defines the desired state of Provider.
// Provider is a kcp-level resource that handles kcp-side bootstrap only
// (ServiceAccount, RBAC, kubeconfig Secret). It has no runtime-side effects.
type ProviderSpec struct {
	// hostOverride overrides the kcp front-proxy host written into the
	// generated kubeconfig Secret. Optional; defaults to the operator-configured
	// front-proxy URL.
	// +optional
	HostOverride string `json:"hostOverride,omitempty"`
}

// ProviderStatus defines the observed state of Provider.
type ProviderStatus struct {
	// phase summarises the bootstrap state of the Provider (e.g. "Pending", "Ready").
	// +optional
	Phase string `json:"phase,omitempty"`

	// kubeconfigSecretRef points to the Secret created in the provider workspace
	// that contains the scoped kubeconfig.
	// +optional
	KubeconfigSecretRef *SecretReference `json:"kubeconfigSecretRef,omitempty"`

	// conditions represent the current state of the Provider resource.
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
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:JSONPath=".status.phase",name="Phase",type=string,description="Bootstrap phase"
// +kubebuilder:printcolumn:JSONPath=".status.kubeconfigSecretRef.name",name="KubeconfigSecret",type=string,description="Name of the generated kubeconfig Secret"
// +kubebuilder:printcolumn:JSONPath=".status.conditions[?(@.type=='Ready')].status",name="Ready",type=string,description="Shows if resource is ready"

// Provider is the Schema for the providers API.
// It is a kcp-level resource that bootstraps provider RBAC and credentials.
type Provider struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of Provider
	// +required
	Spec ProviderSpec `json:"spec"`

	// status defines the observed state of Provider
	// +optional
	Status ProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProviderList contains a list of Provider
type ProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Provider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Provider{}, &ProviderList{})
}

func (i *Provider) GetConditions() []metav1.Condition {
	return i.Status.Conditions
}

func (i *Provider) SetConditions(conditions []metav1.Condition) {
	i.Status.Conditions = conditions
}
