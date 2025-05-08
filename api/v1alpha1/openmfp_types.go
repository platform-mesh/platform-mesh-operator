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
	// Important: Run "make" to regenerate code after modifying this file
	Kcp Kcp `json:"kcp"`
}

type Kcp struct {
	AdminSecretRef           *AdminSecretRef      `json:"adminSecretRef,omitempty"` // TODO: change to v1.SecretReference
	ProviderConnections      []ProviderConnection `json:"providerConnections,omitempty"`
	ExtraProviderConnections []ProviderConnection `json:"extraProviderConnections,omitempty"`
	InitializerConnections      []InitializerConnection `json:"initializerConnections,omitempty"`
	ExtraInitializerConnections []InitializerConnection `json:"extraInitializerConnections,omitempty"`
}

type InitializerConnection struct {
	WorkspaceTypeName string `json:"workspaceTypeName"`
	Path              string `json:"path"`
	Secret            string `json:"secret,omitempty"`
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
// +kubebuilder:printcolumn:JSONPath=".status.conditions[?(@.type=='KcpsetupSubroutine_Ready')].status",name="KCP",type=string
// +kubebuilder:printcolumn:JSONPath=".status.conditions[?(@.type=='ProvidersecretSubroutine_Ready')].status",name="SECRET",type=string

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

var DEFAULT_ADMIN_SECRET_KEY = "kubeconfig"
var DEFAULT_ADMIN_SECRET_NAME = "openmfp-operator-kubeconfig"

func (i *OpenMFP) GetAdminSecretName() string {
	if i.Spec.Kcp.AdminSecretRef == nil {
		return DEFAULT_ADMIN_SECRET_NAME
	}
	return i.Spec.Kcp.AdminSecretRef.SecretRef.Name
}

func (i *OpenMFP) GetAdminSecretNamespace() string {
	if i.Spec.Kcp.AdminSecretRef == nil {
		return "default"
	}
	return i.Spec.Kcp.AdminSecretRef.SecretRef.Namespace
}

func (i *OpenMFP) GetAdminSecretKey() string {
	if i.Spec.Kcp.AdminSecretRef == nil {
		return DEFAULT_ADMIN_SECRET_KEY
	}
	return i.Spec.Kcp.AdminSecretRef.Key
}
