package subroutines

import (
	"context"
	"net/url"

	"github.com/openmfp/golang-commons/controller/lifecycle"
	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
)

func NewProvidersecretSubroutine(
	client client.Client,
	helper KcpHelper,
) *ProvidersecretSubroutine {
	sub := &ProvidersecretSubroutine{
		client: client,
	}
	if helper == nil {
		sub.kcpHelper = &Helper{}
	} else {
		sub.kcpHelper = helper
	}
	return sub
}

type ProvidersecretSubroutine struct {
	client    client.Client
	kcpHelper KcpHelper
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

	secretName := DEFAULT_KCP_SECRET_NAME
	secretNamespace := instance.Namespace
	if instance.Spec.Kcp.AdminSecretRef != nil {
		secretName = instance.Spec.Kcp.AdminSecretRef.SecretRef.Name
		secretNamespace = instance.Spec.Kcp.AdminSecretRef.SecretRef.Namespace
	}

	secret, err := r.kcpHelper.GetSecret(
		r.client, secretName, secretNamespace,
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	for _, pc := range instance.Spec.Kcp.ProviderConnections {
		_, errOp := r.handleProviderConnection(ctx, instance, pc, secret)
		if errOp != nil {
			log.Error().Err(errOp.Err()).Msg("Failed to handle provider connection")
			return ctrl.Result{}, errOp
		}
	}
	for _, pc := range instance.Spec.Kcp.ExtraProviderConnections {
		_, errOp := r.handleProviderConnection(ctx, instance, pc, secret)
		if errOp != nil {
			log.Error().Err(errOp.Err()).Msg("Failed to handle extra provider connection")
			return ctrl.Result{}, errOp
		}
	}

	return ctrl.Result{}, nil
}

// TODO: Implement the following methods
func (r *ProvidersecretSubroutine) Finalizers() []string { // coverage-ignore
	return []string{ProvidersecretSubroutineFinalizer}
}

func (r *ProvidersecretSubroutine) GetName() string {
	return ProvidersecretSubroutineName
}

func (r *ProvidersecretSubroutine) handleProviderConnection(
	ctx context.Context, instance *corev1alpha1.OpenMFP, pc corev1alpha1.ProviderConnection, secret *corev1.Secret,
) (ctrl.Result, errors.OperatorError) {
	secretKey := DEFAULT_KCP_SECRET_KEY
	if instance.Spec.Kcp.AdminSecretRef != nil && instance.Spec.Kcp.AdminSecretRef.Key != "" {
		secretKey = instance.Spec.Kcp.AdminSecretRef.Key
	}
	kcpConfig, err := clientcmd.Load(secret.Data[secretKey])
	if err != nil {
		log.Error().Err(err).Msg("Failed to load kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	for _, cluster := range kcpConfig.Clusters {
		u, err := url.Parse(cluster.Server)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse KCP host")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
		cluster.Server = u.Scheme + "://" + u.Host + "/clusters/" + pc.Path
		break
	}

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
	_, err = controllerutil.CreateOrUpdate(ctx, r.client, providerSecret, func() error {
		err = controllerutil.SetOwnerReference(instance, providerSecret, r.client.Scheme())
		return err
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to create secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	return ctrl.Result{}, nil
}
