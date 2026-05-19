package e2e

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// e2eScopedKubeconfigProvider1Path matches kind_scoped_kubeconfig_test.go fixtures (provider1 workspace cluster).
const e2eScopedKubeconfigProvider1Path = "root:providers:provider1"

func dynamicClientForKubeconfig(kubeconfigBytes []byte) (dynamic.Interface, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return nil, err
	}
	cfg.Timeout = 60 * time.Second
	return dynamic.NewForConfig(cfg)
}

func ApplyManifestFromFile(
	ctx context.Context,
	path string, k8sClient client.Client, templateData map[string]string,
) error {
	log := logger.LoadLoggerFromContext(ctx)

	objs, err := unstructuredsFromFile(path, templateData, log)
	if err != nil {
		return err
	}

	var errRet error = nil
	for _, obj := range objs {
		if obj.Object == nil {
			continue
		}
		err = k8sClient.Apply(ctx, client.ApplyConfigurationFromUnstructured(&obj),
			client.FieldOwner("platform-mesh-operator"))
		if err != nil {
			errRet = errors.Wrap(errRet, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
		}
	}
	return errRet
}

func unstructuredsFromFile(path string, templateData map[string]string, log *logger.Logger) ([]unstructured.Unstructured, error) {
	manifestBytes, err := os.ReadFile(path)
	if err != nil {
		return []unstructured.Unstructured{}, errors.Wrap(err, "Failed to read file, pwd: %s", path)
	}
	log.Debug().Str("file", path).Str("template", string(manifestBytes)).Str("templateData", fmt.Sprintf("%+v", templateData)).Msg("Replacing template")

	res, err := ReplaceTemplate(templateData, manifestBytes)
	if err != nil {
		return []unstructured.Unstructured{}, errors.Wrap(err, "Failed to replace template with path: %s", path)
	}

	// split the result into multiple YAML objects
	objects := strings.Split(string(res), "---\n")
	var unstructuredObjs []unstructured.Unstructured
	for _, obj := range objects {
		var objMap map[string]interface{}
		if err := yaml.Unmarshal([]byte(obj), &objMap); err != nil {
			return []unstructured.Unstructured{}, errors.Wrap(err, "Failed to unmarshal YAML from template %s. Output:\n%s", path, string(res))
		}

		log.Debug().Str("obj", fmt.Sprintf("%+v", objMap)).Msg("Unmarshalled object")

		obj := unstructured.Unstructured{Object: objMap}

		log.Debug().Str("file", path).Str("kind", obj.GetKind()).Str("name", obj.GetName()).Str("namespace", obj.GetNamespace()).Msg("Applying manifest")
		unstructuredObjs = append(unstructuredObjs, obj)
	}
	return unstructuredObjs, nil
}

func ReplaceTemplate(templateData map[string]string, templateBytes []byte) ([]byte, error) {
	// If no template data is provided, return the raw bytes unchanged.
	// This avoids the template engine interpreting {{ }} expressions that are meant to be
	// preserved as-is in the output (e.g. Go template expressions inside ConfigMap values
	// that the operator renders at runtime).
	if len(templateData) == 0 {
		return templateBytes, nil
	}
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

// runKubectlAuthCanI runs `kubectl auth can-i <verb> <resource>` with a kubeconfig backed by kubeconfigBytes
// (after normalizeScopedKubeconfigServerForLocalRun). Returns whether kubectl answered "yes".
func runKubectlAuthCanI(kubeconfigBytes []byte, verb, resource string) (bool, error) {
	normalizedKubeconfigBytes, err := normalizeScopedKubeconfigServerForLocalRun(kubeconfigBytes)
	if err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp("", "preset-kubeconfig-auth-can-i-*.yaml")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(normalizedKubeconfigBytes); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	args := []string{"--kubeconfig", tmp.Name(), "auth", "can-i", verb, resource}
	cmd := exec.Command("kubectl", args...)
	env := os.Environ()
	if goruntime.GOOS == "darwin" {
		env = append(env, "DOCKER_HOST=unix:///var/run/docker.sock")
	} else {
		env = append(env, "DOCKER_HOST=unix:///run/docker.sock")
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	raw := string(out)
	var lastLine string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Warning:") {
			continue
		}
		lastLine = line
	}
	switch lastLine {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("kubectl auth can-i: %w, output: %s", err, strings.TrimSpace(raw))
	}
	return false, fmt.Errorf("kubectl auth can-i: unexpected output: %q", strings.TrimSpace(raw))
}

// normalizeScopedKubeconfigServerForLocalRun handles scoped e2e cases.
// This is test-only behavior for host-run kubectl in local/CI e2e, not generic production kubeconfig rewriting.
func normalizeScopedKubeconfigServerForLocalRun(kubeconfigBytes []byte) ([]byte, error) {
	cfg, err := clientcmd.Load(kubeconfigBytes)
	if err != nil {
		return nil, err
	}

	currentContext := cfg.Contexts[cfg.CurrentContext]
	cluster := cfg.Clusters[currentContext.Cluster]

	server := cluster.Server

	// In-cluster front-proxy DNS is not resolvable from host-run kubectl.
	server = strings.Replace(server, "frontproxy-front-proxy.platform-mesh-system:8443", "localhost:8443", 1)

	// provider1: virtual workspace URL from endpoint slice is flaky for create/get in host-run kubectl.
	if strings.Contains(server, "/services/apiexport/") {
		server = "https://localhost:8443/clusters/" + e2eScopedKubeconfigProvider1Path
	}

	cluster.Server = server
	out, err := clientcmd.Write(*cfg)
	if err != nil {
		return nil, err
	}
	return out, nil
}
