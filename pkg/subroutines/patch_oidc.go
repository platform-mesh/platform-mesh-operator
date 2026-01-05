package subroutines

import (
	"context"
	"fmt"
	"slices"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	apiserverv1beta1 "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
	"k8s.io/utils/ptr"
)

type PatchOIDCSubroutine struct {
	cl             client.Client
	baseDomain     string
	configMapName  string
	namespace      string
	domainCALookup bool
}

func NewPatchOIDCSubroutine(cl client.Client, configMapName, namespace, baseDomain string, domainCALookup bool) *PatchOIDCSubroutine {
	return &PatchOIDCSubroutine{
		cl:             cl,
		baseDomain:     baseDomain,
		configMapName:  configMapName,
		namespace:      namespace,
		domainCALookup: domainCALookup,
	}
}

// Finalize implements subroutine.Subroutine.
func (p *PatchOIDCSubroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	name := instance.GetName()

	var oidcCM corev1.ConfigMap
	oidcCM.SetName(p.configMapName)
	oidcCM.SetNamespace(p.namespace)

	// Get existing ConfigMap to preserve other data
	if err := p.cl.Get(ctx, client.ObjectKeyFromObject(&oidcCM), &oidcCM); err != nil && !kerrors.IsNotFound(err) {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Initialize Data if it doesn't exist
	if oidcCM.Data == nil {
		oidcCM.Data = map[string]string{}
	}

	configYaml, ok := oidcCM.Data["config.yaml"]
	if !ok {
		configYaml = ""
	}

	var structuredAuth apiserverv1beta1.AuthenticationConfiguration
	if configYaml != "" {
		if err := yaml.Unmarshal([]byte(configYaml), &structuredAuth); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(err, true, true)
		}
	}

	structuredAuth.JWT = slices.DeleteFunc(structuredAuth.JWT, func(j apiserverv1beta1.JWTAuthenticator) bool {
		return j.Issuer.URL == fmt.Sprintf("https://%s/keycloak/realms/%s", p.baseDomain, name)
	})

	rawYaml, err := yaml.Marshal(&structuredAuth)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	oidcCM.Data["config.yaml"] = string(rawYaml)

	// Set GVK for SSA (required when object might be fresh)
	oidcCM.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"})
	// Clear managedFields before applying with SSA (required for SSA)
	oidcCM.SetManagedFields(nil)

	// Apply using SSA
	if err := p.cl.Patch(ctx, &oidcCM, client.Apply, client.FieldOwner("platform-mesh-patch-oidc"), client.ForceOwnership); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	return ctrl.Result{}, nil
}

// Finalizers implements subroutine.Subroutine.
func (p *PatchOIDCSubroutine) Finalizers(instance runtimeobject.RuntimeObject) []string {
	return []string{"platform-mesh.io/patch-oidc"}
}

// GetName implements subroutine.Subroutine.
func (p *PatchOIDCSubroutine) GetName() string { return "PatchOIDCSubroutine" }

// Process implements subroutine.Subroutine.
func (p *PatchOIDCSubroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	name := instance.GetName()

	var oidcCM corev1.ConfigMap
	oidcCM.SetName(p.configMapName)
	oidcCM.SetNamespace(p.namespace)

	var domainCA string
	if p.domainCALookup {
		var domainCASecret corev1.Secret
		if err := p.cl.Get(ctx, client.ObjectKey{Name: "domain-certificate-ca", Namespace: p.namespace}, &domainCASecret); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(err, true, true)
		}

		domainCA = string(domainCASecret.Data["tls.crt"])
	}

	// Get existing ConfigMap to preserve other data
	if err := p.cl.Get(ctx, client.ObjectKeyFromObject(&oidcCM), &oidcCM); err != nil && !kerrors.IsNotFound(err) {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Initialize Data if it doesn't exist
	if oidcCM.Data == nil {
		oidcCM.Data = map[string]string{}
	}

	configYaml, ok := oidcCM.Data["config.yaml"]
	if !ok {
		configYaml = ""
	}

	var structuredAuth apiserverv1beta1.AuthenticationConfiguration
	if configYaml != "" {
		if err := yaml.Unmarshal([]byte(configYaml), &structuredAuth); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(err, true, true)
		}
	}

	oidcConfig := apiserverv1beta1.JWTAuthenticator{
		Issuer: apiserverv1beta1.Issuer{
			URL:                 fmt.Sprintf("https://%s/keycloak/realms/%s", p.baseDomain, name),
			Audiences:           []string{name},
			AudienceMatchPolicy: apiserverv1beta1.AudienceMatchPolicyMatchAny,
		},
		ClaimMappings: apiserverv1beta1.ClaimMappings{
			Username: apiserverv1beta1.PrefixedClaimOrExpression{
				Claim:  "email",
				Prefix: ptr.To(""),
			},
			Groups: apiserverv1beta1.PrefixedClaimOrExpression{
				Claim:  "groups",
				Prefix: ptr.To(""),
			},
		},
	}

	if p.domainCALookup {
		oidcConfig.Issuer.CertificateAuthority = domainCA
	}

	idx := slices.IndexFunc(structuredAuth.JWT, func(j apiserverv1beta1.JWTAuthenticator) bool {
		return j.Issuer.URL == fmt.Sprintf("https://%s/keycloak/realms/%s", p.baseDomain, name)
	})

	if idx != -1 {
		structuredAuth.JWT[idx] = oidcConfig
	} else {
		structuredAuth.JWT = append(structuredAuth.JWT, oidcConfig)
	}

	if structuredAuth.GroupVersionKind().Empty() {
		structuredAuth.SetGroupVersionKind(apiserverv1beta1.ConfigSchemeGroupVersion.WithKind("AuthenticationConfiguration"))
	}

	rawYaml, err := yaml.Marshal(&structuredAuth)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	oidcCM.Data["config.yaml"] = string(rawYaml)

	// Set GVK for SSA (required when object might be fresh)
	oidcCM.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"})
	// Clear managedFields before applying with SSA (required for SSA)
	oidcCM.SetManagedFields(nil)

	// Apply using SSA
	if err := p.cl.Patch(ctx, &oidcCM, client.Apply, client.FieldOwner("platform-mesh-patch-oidc"), client.ForceOwnership); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	return ctrl.Result{}, nil
}

var _ subroutine.Subroutine = &PatchOIDCSubroutine{}
