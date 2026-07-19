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

func TestHazyForgeArtifactBuildUsesCatalogSourceContexts(t *testing.T) {
	t.Parallel()

	applicationPath := "../../.hazyforge/clusters/anvil-primaris/namespace/anvil-agents-system/manifests/application.yaml"
	contents, err := os.ReadFile(applicationPath)
	if err != nil {
		t.Fatal(err)
	}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(contents), 4096)
	var repository, application *unstructured.Unstructured
	for {
		object := &unstructured.Unstructured{}
		if err := decoder.Decode(object); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode %s: %v", applicationPath, err)
		}
		switch object.GetKind() {
		case "Repository":
			repository = object
		case "Application":
			application = object
		}
	}
	if repository == nil || application == nil {
		t.Fatalf("%s must contain Repository and Application objects", applicationPath)
	}
	checkoutPath, found, err := unstructured.NestedString(repository.Object, "spec", "defaultCheckoutPath")
	if err != nil || !found || strings.TrimSpace(checkoutPath) == "" {
		t.Fatalf("read Repository defaultCheckoutPath: found=%t err=%v", found, err)
	}
	components, found, err := unstructured.NestedSlice(application.Object, "spec", "components")
	if err != nil || !found {
		t.Fatalf("read Application components: found=%t err=%v", found, err)
	}
	type componentSource struct {
		repository string
		path       string
	}
	componentSources := map[string]componentSource{}
	for _, raw := range components {
		component, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("Application component has type %T", raw)
		}
		name, _, _ := unstructured.NestedString(component, "name")
		repository, _, _ := unstructured.NestedString(component, "source", "repository")
		sourcePath, _, _ := unstructured.NestedString(component, "source", "path")
		componentSources[name] = componentSource{repository: repository, path: sourcePath}
	}

	buildPath := "../../.hazyforge/artifact-build.yaml"
	contents, err = os.ReadFile(buildPath)
	if err != nil {
		t.Fatal(err)
	}
	build := &unstructured.Unstructured{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(contents), 4096).Decode(build); err != nil {
		t.Fatalf("decode %s: %v", buildPath, err)
	}
	sources, found, err := unstructured.NestedSlice(build.Object, "spec", "sources")
	if err != nil || !found {
		t.Fatalf("read ArtifactBuild sources: found=%t err=%v", found, err)
	}
	buildSourcePaths := map[string]string{}
	for _, raw := range sources {
		source, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("ArtifactBuild source has type %T", raw)
		}
		name, _, _ := unstructured.NestedString(source, "name")
		sourcePath, _, _ := unstructured.NestedString(source, "path")
		buildSourcePaths[name] = sourcePath
	}
	nodes, found, err := unstructured.NestedSlice(build.Object, "spec", "actionPlan", "nodes")
	if err != nil || !found {
		t.Fatalf("read ArtifactBuild nodes: found=%t err=%v", found, err)
	}
	for _, raw := range nodes {
		node, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("ArtifactBuild node has type %T", raw)
		}
		componentName, _, _ := unstructured.NestedString(node, "invocation", "inputs", "component", "name")
		workdir, _, _ := unstructured.NestedString(node, "invocation", "inputs", "workdir")
		contextPath, _, _ := unstructured.NestedString(node, "invocation", "inputs", "context")
		componentSource, found := componentSources[componentName]
		if !found {
			t.Fatalf("ArtifactBuild component %q is missing from Application", componentName)
		}
		sourcePath := componentSource.path
		if sourcePath == "" || sourcePath == "." {
			var sourceFound bool
			sourcePath, sourceFound = buildSourcePaths[componentSource.repository]
			if !sourceFound {
				t.Fatalf("ArtifactBuild component %q source %q is missing", componentName, componentSource.repository)
			}
		}
		want := filepath.Clean(filepath.Join(checkoutPath, sourcePath))
		actual := filepath.Clean(filepath.Join(workdir, contextPath))
		if actual != want {
			t.Fatalf("ArtifactBuild component %q build context = %q, want catalog source context %q", componentName, actual, want)
		}
	}
}
