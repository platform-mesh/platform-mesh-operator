package subroutines

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"text/template"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcpcorev1alpha "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/openmfp/golang-commons/errors"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	admissionv1 "k8s.io/api/admissionregistration/v1"

	"github.com/openmfp/openmfp-operator/api/v1alpha1"
)

type KcpHelper interface {
	NewKcpClient(config *rest.Config, workspacePath string) (client.Client, error)
}

type Helper struct {
}

func (h *Helper) NewKcpClient(config *rest.Config, workspacePath string) (client.Client, error) {
	config.QPS = 1000.0
	config.Burst = 2000.0
	u, err := url.Parse(config.Host)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to parse kcp host: %s", config.Host)
	}
	config.Host = u.Scheme + "://" + u.Host + "/clusters/" + workspacePath
	scheme := runtime.NewScheme()
	utilruntime.Must(v1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcpapiv1alpha.AddToScheme(scheme))
	utilruntime.Must(kcptenancyv1alpha.AddToScheme(scheme))
	utilruntime.Must(kcpcorev1alpha.AddToScheme(scheme))
	utilruntime.Must(admissionv1.AddToScheme(scheme))

	cl, err := client.New(config, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create KCP client: %w", err)
	}
	return cl, nil
}

func GetSecret(client client.Client, name string, namespace string) (*corev1.Secret, error) {
	secret := corev1.Secret{}
	err := client.Get(context.Background(), types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, &secret)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get secret")
	}
	return &secret, nil
}

func ReplacePaths(dirs DirectoryStructure, relativePath string, isLocal bool) {
	if isLocal {
		for wi, w := range dirs.Workspaces {
			filteredFiles := []string{}
			for _, f := range w.Files {
				filteredFiles = append(filteredFiles, strings.ReplaceAll(f, "/operator/setup/", relativePath))
			}
			dirs.Workspaces[wi].Files = filteredFiles
		}
	}
}

func ReplaceTemplate(templateData map[string]string, templateBytes []byte) ([]byte, error) {
	tmpl, err := template.New("manifest").Parse(string(templateBytes))
	if err != nil {
		return []byte{}, errors.Wrap(err, "Failed to parse template")
	}
	var result bytes.Buffer
	err = tmpl.Execute(&result, templateData)
	if err != nil {
		keys := make([]string, 0, len(templateData))
		for k := range templateData {
			keys = append(keys, k)
		}
		return []byte{}, errors.Wrap(err, "Failed to execute template with keys %v", keys)
	}
	if result.Len() == 0 {
		return []byte{}, nil
	}
	return result.Bytes(), nil
}

func ConvertToUnstructured(webhook admissionv1.MutatingWebhookConfiguration) (*unstructured.Unstructured, error) {
	// Convert the structured object to a map
	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&webhook)
	if err != nil {
		return nil, err
	}
	// Create an unstructured object and assign the map
	unstructuredObj := &unstructured.Unstructured{Object: objMap}
	unstructuredObj.SetKind("MutatingWebhookConfiguration")
	unstructuredObj.SetAPIVersion("admissionregistration.k8s.io/v1")
	unstructuredObj.SetManagedFields(nil)
	return unstructuredObj, nil
}
