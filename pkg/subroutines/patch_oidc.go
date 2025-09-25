package subroutines

import (
	"context"
	"fmt"
	"slices"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"gopkg.in/yaml.v3"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiserver/pkg/apis/apiserver"
	"k8s.io/utils/ptr"
)

type PatchOIDCSubroutine struct {
	cl            client.Client
	baseDomain    string
	configMapName string
	namespace     string
}

func NewPatchOIDCSubroutine(cl client.Client, configMapName, namespace, baseDomain string) *PatchOIDCSubroutine {
	return &PatchOIDCSubroutine{
		cl:            cl,
		baseDomain:    baseDomain,
		configMapName: configMapName,
		namespace:     namespace,
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

		var structuredAuth apiserver.AuthenticationConfiguration
		err := yaml.Unmarshal([]byte(configYaml), &structuredAuth)
		if err != nil {
			return err
		}

		structuredAuth.JWT = slices.DeleteFunc(structuredAuth.JWT, func(j apiserver.JWTAuthenticator) bool {
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

	_, err := controllerutil.CreateOrPatch(ctx, p.cl, &oidcCM, func() error {

		configYaml, ok := oidcCM.Data["config.yaml"]
		if !ok {
			oidcCM.Data = map[string]string{}
		}

		var structuredAuth apiserver.AuthenticationConfiguration
		err := yaml.Unmarshal([]byte(configYaml), &structuredAuth)
		if err != nil {
			return err
		}

		structuredAuth.JWT = append(structuredAuth.JWT, apiserver.JWTAuthenticator{
			Issuer: apiserver.Issuer{
				URL:                 fmt.Sprintf("https://%s/keycloak/realms/%s", p.baseDomain, name),
				Audiences:           []string{name},
				AudienceMatchPolicy: apiserver.AudienceMatchPolicyMatchAny,
			},
			ClaimMappings: apiserver.ClaimMappings{
				Username: apiserver.PrefixedClaimOrExpression{
					Claim:  "email",
					Prefix: ptr.To(""),
				},
				Groups: apiserver.PrefixedClaimOrExpression{
					Claim:  "groups",
					Prefix: ptr.To(""),
				},
			},
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

var _ subroutine.Subroutine = &PatchOIDCSubroutine{}
