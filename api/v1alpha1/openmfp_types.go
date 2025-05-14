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

// OpenMFPSpec defines the desired state of OpenMFP
type OpenMFPSpec struct {
	Exposure   *ExposureConfig  `json:"exposure,omitempty"`
	Components ComponentsConfig `json:"components,omitempty"`
	Kcp        Kcp              `json:"kcp,omitempty"`
	Version    string           `json:"version,omitempty"`
}

type ComponentsConfig struct {
	AccountOperator          Component `json:"accountOperator,omitempty"`
	AccountUI                Component `json:"accountUI,omitempty"`
	Crossplane               Component `json:"crossplane,omitempty"`
	Kcp                      Component `json:"kcp,omitempty"`
	Keycloak                 Component `json:"keycloak,omitempty"`
	KubernetesGraphqlGateway Component `json:"kubernetesGraphqlGateway,omitempty"`
	ApeiroExampleContent     Component `json:"apeiroExampleContent,omitempty"`
	ApeiroPortal             Component `json:"apeiroPortal,omitempty"`
	Portal                   Component `json:"portal,omitempty"`
	ExtensionManagerOperator Component `json:"extensionManagerOperator,omitempty"`
	ExampleResources         Component `json:"exampleResources,omitempty"`
	FgaOperator              Component `json:"fgaOperator,omitempty"`
	IamAuthorizationWebhook  Component `json:"iamAuthorizationWebhook,omitempty"`
	IamService               Component `json:"iamService,omitempty"`
	Infra                    Component `json:"infra,omitempty"`
	IstioBase                Component `json:"istioBase,omitempty"`
	IstioD                   Component `json:"istioD,omitempty"`
	IstioGateway             Component `json:"istioGateway,omitempty"`
	OpenFGA                  Component `json:"openFGA,omitempty"`
}

type Component struct {
	Values  apiextensionsv1.JSON `json:"values,omitempty"`
	Enabled *bool                `json:"enabled,omitempty"`
	Version string               `json:"version,omitempty"`
}

type ExposureConfig struct {
	BaseDomain string `json:"baseDomain,omitempty"`
	Port       int    `json:"port,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
}

type Kcp struct {
	ProviderConnections         []ProviderConnection             `json:"providerConnections,omitempty"`
	ExtraProviderConnections    []ProviderConnection             `json:"extraProviderConnections,omitempty"`
	InitializerConnections      []InitializerConnection          `json:"initializerConnections,omitempty"`
	ExtraInitializerConnections []InitializerConnection          `json:"extraInitializerConnections,omitempty"`
	ExtraDefaultAPIBindings     []DefaultAPIBindingConfiguration `json:"extraDefaultAPIBindings,omitempty"`
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
	External          bool   `json:"external,omitempty"`
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
