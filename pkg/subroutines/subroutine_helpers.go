package subroutines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"text/template"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcpcorev1alpha "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/openmfp/golang-commons/errors"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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

func GetWorkspaceDirs(dir string) []string {
	workspaces := []string{}
	// find all subdirectories named "dd-name", e.g. "01-openmfp-system"
	dirs, err := os.ReadDir(dir)
	if err != nil {
		// TODO: print error
		return workspaces
	}
	for _, d := range dirs {
		// check if d.Name() match the regex ^[0-9]{2}-[a-zA-Z0-9-]+$
		if d.IsDir() {
			if IsWorkspace(d.Name()) {
				workspaces = append(workspaces, d.Name())
			}
			if err != nil {
				return workspaces
			}
			workspaces = append(workspaces, d.Name())
		}
	}
	return workspaces
}

func GetWorkspaceName(dir string) (string, error) {
	validWorkspaceName := regexp.MustCompile(`.*[0-9]{2}-([a-zA-Z0-9-]+)$`)
	matches := validWorkspaceName.FindAllSubmatch([]byte(dir), -1)
	if matches == nil {
		return "", fmt.Errorf("Invalid workspace name: %s", dir)
	}
	last := matches[len(matches)-1]
	return string(last[1]), nil
}

func IsWorkspace(dir string) bool {
	pattern := `^[0-9]{2}-[a-zA-Z0-9-]+$`
	match, err := regexp.Match(pattern, []byte(dir))
	if err != nil {
		return false
	}
	return match
}

func ListFiles(dir string) ([]string, error) {
	files := []string{}
	// find all files in the directory
	dirs, err := os.ReadDir(dir)
	if err != nil {
		return files, errors.Wrap(err, "Failed to read directory")
	}
	for _, d := range dirs {
		if d.IsDir() {
			continue
		}
		files = append(files, d.Name())
	}
	return files, nil
}

func MergeJSON(a, b apiextensionsv1.JSON) (apiextensionsv1.JSON, error) {
	// Unmarshal 'a'
	var mapA map[string]interface{}
	if len(a.Raw) > 0 {
		if err := json.Unmarshal(a.Raw, &mapA); err != nil {
			return apiextensionsv1.JSON{}, err
		}
	} else {
		mapA = map[string]interface{}{}
	}

	// Unmarshal 'b'
	var mapB map[string]interface{}
	if len(b.Raw) > 0 {
		if err := json.Unmarshal(b.Raw, &mapB); err != nil {
			return apiextensionsv1.JSON{}, err
		}
	} else {
		mapB = map[string]interface{}{}
	}

	// Merge mapB into mapA (b overwrites a on conflict)
	for k, v := range mapB {
		mapA[k] = v
	}

	// Marshal back to apiextensionsv1.JSON
	mergedRaw, err := json.Marshal(mapA)
	if err != nil {
		return apiextensionsv1.JSON{}, err
	}
	return apiextensionsv1.JSON{Raw: mergedRaw}, nil
}

func MergeValuesAndServices(values, services apiextensionsv1.JSON) (apiextensionsv1.JSON, error) {
	// Unmarshal 'values'
	var mapValues map[string]interface{}
	if len(values.Raw) > 0 {
		if err := json.Unmarshal(values.Raw, &mapValues); err != nil {
			return apiextensionsv1.JSON{}, err
		}
	} else {
		mapValues = map[string]interface{}{}
	}
	// Unmarshal 'services'
	var mapServices map[string]interface{}
	if len(services.Raw) > 0 {
		if err := json.Unmarshal(services.Raw, &mapServices); err != nil {
			return apiextensionsv1.JSON{}, err
		}
	} else {
		mapServices = map[string]interface{}{}
	}

	// Create 'services' key in 'values' if it doesn't exist
	if _, ok := mapValues["services"]; !ok {
		mapValues["services"] = map[string]interface{}{}
	}

	// add 'services' to mapValues["services"]
	if _, ok := mapValues["services"].(map[string]interface{}); !ok {
		return apiextensionsv1.JSON{}, fmt.Errorf("services is not a map")
	}
	for k, v := range mapServices {
		mapValues["services"].(map[string]interface{})[k] = v
	}
	// Marshal back to apiextensionsv1.JSON
	mergedRaw, err := json.Marshal(mapValues)
	if err != nil {
		return apiextensionsv1.JSON{}, err
	}
	return apiextensionsv1.JSON{Raw: mergedRaw}, nil

}
