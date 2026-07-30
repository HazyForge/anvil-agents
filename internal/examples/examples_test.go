package examples

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestExampleManifestsParseAndIdentifyObjects(t *testing.T) {
	t.Parallel()

	for _, root := range []string{"../../examples", "../../config/samples"} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || strings.HasSuffix(path, "-values.yaml") || (!strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml")) {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(contents), 4096)
			for document := 1; ; document++ {
				object := &unstructured.Unstructured{}
				if err := decoder.Decode(object); err != nil {
					if err == io.EOF {
						break
					}
					t.Fatalf("decode %s document %d: %v", path, document, err)
				}
				if len(object.Object) == 0 {
					continue
				}
				if object.GetAPIVersion() == "" || object.GetKind() == "" || object.GetName() == "" {
					t.Fatalf("%s document %d must set apiVersion, kind, and metadata.name", path, document)
				}
				if object.GetKind() == "AgentRun" {
					kind, _, _ := unstructured.NestedString(object.Object, "spec", "sourceRef", "kind")
					name, _, _ := unstructured.NestedString(object.Object, "spec", "sourceRef", "name")
					if strings.TrimSpace(kind) == "" || strings.TrimSpace(name) == "" {
						t.Fatalf("%s document %d AgentRun must set spec.sourceRef.kind and name", path, document)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}
