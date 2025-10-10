package subroutines

import (
	"context"
	"fmt"
	"slices"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

	_, err := controllerutil.CreateOrPatch(ctx, p.cl, &oidcCM, func() error {

		configYaml, ok := oidcCM.Data["config.yaml"]
		if !ok {
			oidcCM.Data = map[string]string{}
		}

		var structuredAuth apiserverv1beta1.AuthenticationConfiguration
		err := yaml.Unmarshal([]byte(configYaml), &structuredAuth)
		if err != nil {
			return err
		}

		structuredAuth.JWT = slices.DeleteFunc(structuredAuth.JWT, func(j apiserverv1beta1.JWTAuthenticator) bool {
			return j.Issuer.URL == fmt.Sprintf("https://%s/keycloak/realms/%s", p.baseDomain, name)
		})

		rawYaml, err := yaml.Marshal(&structuredAuth)
		if err != nil {
			return err
		}

		oidcCM.Data["config.yaml"] = string(rawYaml)

		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	return ctrl.Result{}, nil
}

// Finalizers implements subroutine.Subroutine.
func (p *PatchOIDCSubroutine) Finalizers() []string {
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
		err := p.cl.Get(ctx, client.ObjectKey{Name: "domain-certificate-ca", Namespace: p.namespace}, &domainCASecret)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(err, true, true)
		}

		domainCA = string(domainCASecret.Data["tls.crt"])
	}

	_, err := controllerutil.CreateOrPatch(ctx, p.cl, &oidcCM, func() error {

		configYaml, ok := oidcCM.Data["config.yaml"]
		if !ok {
			oidcCM.Data = map[string]string{}
		}

		var structuredAuth apiserverv1beta1.AuthenticationConfiguration
		err := yaml.Unmarshal([]byte(configYaml), &structuredAuth)
		if err != nil {
			return err
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
			return err
		}

		oidcCM.Data["config.yaml"] = string(rawYaml)

		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	return ctrl.Result{}, nil
}

var _ subroutine.Subroutine = &PatchOIDCSubroutine{}
