package subroutines

import (
	"context"
	"net/url"

	"github.com/openmfp/golang-commons/controller/lifecycle"
	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func NewProvidersecretSubroutine(
	client client.Client,
	helper KcpHelperInterface,
) *ProvidersecretSubroutine {
	sub := &ProvidersecretSubroutine{
		client: client,
	}
	if helper == nil {
		sub.kcpHelper = &KcpHelper{}
	} else {
		sub.kcpHelper = helper
	}
	return sub
}

type ProvidersecretSubroutine struct {
	client    client.Client
	kcpHelper KcpHelperInterface
}

const (
	ProvidersecretSubroutineName      = "ProvidersecretSubroutine"
	ProvidersecretSubroutineFinalizer = "openmfp.core.openmfp.org/finalizer"
)

// TODO: Implement the following methods
func (r *ProvidersecretSubroutine) Finalize(
	ctx context.Context, runtimeObj lifecycle.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil // TODO: Implement
}

func (r *ProvidersecretSubroutine) Process(
	ctx context.Context, runtimeObj lifecycle.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {

	instance := runtimeObj.(*corev1alpha1.OpenMFP)

	log := logger.LoadLoggerFromContext(ctx)

	secret, err := r.kcpHelper.GetSecret(
		r.client, instance.Spec.Kcp.AdminSecretRef.Name, instance.Namespace,
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	for _, pc := range instance.Spec.Kcp.ProviderConnections {

		kcpConfig, err := clientcmd.Load(secret.Data["kubeconfig"])
		if err != nil {
			log.Error().Err(err).Msg("Failed to load kubeconfig")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
		u, err := url.Parse(kcpConfig.Clusters["root"].Server)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse KCP host")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
		kcpConfig.Clusters["root"].Server = u.Scheme + "://" + u.Host + "/clusters/" + pc.Path

		kcpConfigBytes, err := clientcmd.Write(*kcpConfig)
		if err != nil {
			log.Error().Err(err).Msg("Failed to write kubeconfig")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
		providerSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pc.Secret,
				Namespace: instance.Namespace,
			},
			Data: map[string][]byte{
				"kubeconfig": kcpConfigBytes,
			},
		}
		err = r.client.Create(ctx, providerSecret)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create secret")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
		if err := controllerutil.SetOwnerReference(instance, providerSecret, r.client.Scheme()); err != nil {
			log.Error().Err(err).Msg("Failed to set owner reference")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
	}

	return ctrl.Result{}, nil
}

// TODO: Implement the following methods
func (r *ProvidersecretSubroutine) Finalizers() []string { // coverage-ignore
	return []string{ProvidersecretSubroutineFinalizer}
}

func (r *ProvidersecretSubroutine) GetName() string {
	return KcpsetupSubroutineName
}
