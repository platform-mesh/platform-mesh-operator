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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// OpenMFPSpec defines the desired state of OpenMFP
type OpenMFPSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of OpenMFP. Edit openmfp_types.go to remove/update
	// Foo string `json:"foo,omitempty"`
	Kcp Kcp `json:"kcp"`
}

type Kcp struct {
	AdminSecretRef           *AdminSecretRef      `json:"adminSecretRef,omitempty"`
	ProviderConnections      []ProviderConnection `json:"providerConnections,omitempty"`
	ExtraProviderConnections []ProviderConnection `json:"extraProviderConnections,omitempty"`
}

type AdminSecretRef struct {
	SecretRef SecretReference `json:"secret,omitempty"`
	Key       string          `json:"key,omitempty"`
}

type SecretReference struct {
	// name is unique within a namespace to reference a secret resource.
	// +optional
	Name string `json:"name,omitempty" protobuf:"bytes,1,opt,name=name"`
	// namespace defines the space within which the secret name must be unique.
	// +optional
	Namespace string `json:"namespace,omitempty" protobuf:"bytes,2,opt,name=namespace"`
}

type ProviderConnection struct {
	EndpointSliceName string `json:"endpointSliceName"`
	Path              string `json:"path"`
	Secret            string `json:"secret,omitempty"`
}

// OpenMFPStatus defines the observed state of OpenMFP
type OpenMFPStatus struct {
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

// OpenMFP is the Schema for the openmfps API
type OpenMFP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OpenMFPSpec   `json:"spec,omitempty"`
	Status OpenMFPStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OpenMFPList contains a list of OpenMFP
type OpenMFPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenMFP `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OpenMFP{}, &OpenMFPList{})
}

func (i *OpenMFP) GetConditions() []metav1.Condition           { return i.Status.Conditions }
func (i *OpenMFP) SetConditions(conditions []metav1.Condition) { i.Status.Conditions = conditions }
