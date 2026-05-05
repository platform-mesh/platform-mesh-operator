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

package providers

import (
	"context"
	"fmt"

	gcerrors "github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/subroutines"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
)

const (
	ScopedKubeconfigSubroutineName = "ScopedKubeconfigSubroutine"
	scopedKubeconfigFinalizer      = "providers.platform-mesh.io/scoped-kubeconfig-finalizer"

	providerSANamespace = "default"
)

func providerKubeconfigSecretName(provider *providersv1alpha1.Provider) string {
	return "platform-mesh-provider-kubeconfig-" + provider.Name
}

func providerServiceAccountName(provider *providersv1alpha1.Provider) string {
	return "platform-mesh-provider-" + provider.Name
}

func providerServiceAccountTokenSecretName(provider *providersv1alpha1.Provider) string {
	return "platform-mesh-provider-token-" + provider.Name
}

func providerRoleName(provider *providersv1alpha1.Provider) string {
	return "platform-mesh-provider-" + provider.Name
}

// ScopedKubeconfigSubroutine creates the ServiceAccount, RBAC, static SA token
// Secret, and kubeconfig Secret inside the provider workspace in a single
// reconciliation step. Runs in the kcp workspace via the VW-aware client.
type ScopedKubeconfigSubroutine struct {
	kcpUrl string

	getClusterClientFromContext func(context.Context) (client.Client, error)
}

func NewScopedKubeconfigSubroutine(kcpUrl string, getClusterClientFromContext func(context.Context) (client.Client, error)) *ScopedKubeconfigSubroutine {
	return &ScopedKubeconfigSubroutine{
		kcpUrl:                      kcpUrl,
		getClusterClientFromContext: getClusterClientFromContext,
	}
}

func (r *ScopedKubeconfigSubroutine) GetName() string {
	return ScopedKubeconfigSubroutineName
}

func (r *ScopedKubeconfigSubroutine) Process(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	inst := obj.(*providersv1alpha1.Provider)

	saName := providerServiceAccountName(inst)
	tokenSecretName := providerServiceAccountTokenSecretName(inst)
	kubeconfigSecretName := providerKubeconfigSecretName(inst)
	roleName := providerRoleName(inst)

	cl, err := r.getClusterClientFromContext(ctx)
	if err != nil {
		return subroutines.OK(), err
	}
	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return subroutines.OK(), fmt.Errorf("failed to get cluster from context")
	}

	// Ensure the default namespace exists in the workspace.
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: providerSANamespace}}
	if err := cl.Create(ctx, ns); err != nil && !kerrors.IsAlreadyExists(err) {
		return subroutines.OK(), gcerrors.Wrap(err, "ensure namespace %s in provider workspace", providerSANamespace)
	}

	// Ensure ServiceAccount.
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: providerSANamespace}}
	if err := cl.Create(ctx, sa); err != nil && !kerrors.IsAlreadyExists(err) {
		return subroutines.OK(), gcerrors.Wrap(err, "create ServiceAccount %s", saName)
	}

	// Ensure Role.
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: providerSANamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, cl, role, func() error {
		role.Rules = []rbacv1.PolicyRule{
			// TODO: define exact permission claims required by the provider. ManagedProvider.Spec.PermissionClaims?
			{ // Until we figure this out. 🤩
				APIGroups: []string{"*"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
		}
		return nil
	}); err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "create or update Role %s", roleName)
	}

	// Ensure RoleBinding for the provider role.
	roleBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: providerSANamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, cl, roleBinding, func() error {
		roleBinding.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: roleName}
		roleBinding.Subjects = []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Namespace: providerSANamespace, Name: saName}}
		return nil
	}); err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "create or update RoleBinding %s", roleName)
	}

	// Ensure a static long-lived SA token Secret.
	tokenSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: tokenSecretName, Namespace: providerSANamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, cl, tokenSecret, func() error {
		tokenSecret.Type = corev1.SecretTypeServiceAccountToken
		if tokenSecret.Annotations == nil {
			tokenSecret.Annotations = map[string]string{}
		}
		tokenSecret.Annotations[corev1.ServiceAccountNameKey] = saName
		return nil
	}); err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "create or update token Secret %s", tokenSecretName)
	}
	if len(tokenSecret.Data["token"]) == 0 || len(tokenSecret.Data["ca.crt"]) == 0 {
		log.Info().Str("secret", tokenSecretName).Msg("SA token not yet populated, requeuing")
		return subroutines.StopWithRequeue(waitProviderRequeueDuration, "waiting for SA token to be populated"), nil
	}

	token := string(tokenSecret.Data["token"])
	caData := tokenSecret.Data["ca.crt"]
	hostURL := inst.Spec.HostOverride
	if hostURL == "" {
		hostURL = r.kcpUrl
	}
	hostURL += fmt.Sprintf("/clusters/%s", clusterName)

	kubeconfigBytes, err := clientcmd.Write(buildProviderScopedKubeconfig(hostURL, token, caData))
	if err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "serialize kubeconfig")
	}

	kubeconfigSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: kubeconfigSecretName, Namespace: providerSANamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, cl, kubeconfigSecret, func() error {
		kubeconfigSecret.Data = map[string][]byte{"kubeconfig": kubeconfigBytes}
		return nil
	}); err != nil {
		return subroutines.OK(), gcerrors.Wrap(err, "write kubeconfig Secret %s", kubeconfigSecretName)
	}

	inst.Status.KubeconfigSecretRef = &providersv1alpha1.SecretReference{
		Name:      kubeconfigSecretName,
		Namespace: providerSANamespace,
	}

	inst.Status.Phase = "Ready"

	log.Info().Str("provider", inst.Name).Str("secret", kubeconfigSecretName).Msg("Ensured scoped kubeconfig in provider workspace")
	return subroutines.OK(), nil
}

func (r *ScopedKubeconfigSubroutine) Finalize(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	inst := obj.(*providersv1alpha1.Provider)
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	cl, err := r.getClusterClientFromContext(ctx)
	if err != nil {
		return subroutines.OK(), err
	}

	saName := providerServiceAccountName(inst)
	tokenSecretName := providerServiceAccountTokenSecretName(inst)
	kubeconfigSecretName := providerKubeconfigSecretName(inst)
	roleName := providerRoleName(inst)

	for _, res := range []client.Object{
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: providerSANamespace}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: providerSANamespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: tokenSecretName, Namespace: providerSANamespace}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: providerSANamespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: kubeconfigSecretName, Namespace: providerSANamespace}},
	} {
		if err := client.IgnoreNotFound(cl.Delete(ctx, res)); err != nil {
			return subroutines.OK(), gcerrors.Wrap(err, "delete %T %s", res, res.GetName())
		}
	}

	log.Info().Str("provider", inst.Name).Msg("Deleted scoped kubeconfig resources from provider workspace")
	return subroutines.OK(), nil
}

func (r *ScopedKubeconfigSubroutine) Finalizers(_ client.Object) []string {
	return []string{scopedKubeconfigFinalizer}
}

func buildProviderScopedKubeconfig(hostURL, token string, caData []byte) clientcmdapi.Config {
	return clientcmdapi.Config{
		Clusters:       map[string]*clientcmdapi.Cluster{"default-cluster": {Server: hostURL, CertificateAuthorityData: caData}},
		AuthInfos:      map[string]*clientcmdapi.AuthInfo{"default-auth": {Token: token}},
		Contexts:       map[string]*clientcmdapi.Context{"default-context": {Cluster: "default-cluster", AuthInfo: "default-auth"}},
		CurrentContext: "default-context",
	}
}
