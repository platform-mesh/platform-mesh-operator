package e2e

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"os"
	"strings"

	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

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
		err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("openmfp-operator"))
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
