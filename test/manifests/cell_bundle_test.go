package manifests_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// Infra reconciles the namespace, downstream CRDs and cell controllers
// separately. Rendering each successfully is insufficient: their inventories
// must also be disjoint after the deployment namespace has been applied.
func TestCellBundleOwnership(t *testing.T) {
	for _, namespace := range []string{"network-services-operator-system", "network-services-system"} {
		t.Run(namespace, func(t *testing.T) {
			downstream := renderBundle(t, "crd/downstream", "")
			cell := renderBundle(t, "cell", namespace)
			owners := map[string]string{
				fmt.Sprintf("/Namespace /%s", namespace): "platform namespace",
			}
			for _, bundle := range []struct {
				name    string
				objects []unstructured.Unstructured
			}{
				{"crd/downstream", downstream},
				{"cell", cell},
			} {
				for _, object := range bundle.objects {
					id := fmt.Sprintf("%s/%s %s/%s", object.GroupVersionKind().Group,
						object.GetKind(), object.GetNamespace(), object.GetName())
					if owner, found := owners[id]; found {
						t.Errorf("%s is owned by both %s and %s", id, owner, bundle.name)
					}
					owners[id] = bundle.name
				}
			}

			// These are the NSO APIs the cell watches locally. ServingLocation
			// comes from the locations service; it must not be vendored here.
			for _, plural := range []string{"networkinterfaceclaims", "networkinterfaces", "networkcontexts", "subnets"} {
				id := "apiextensions.k8s.io/CustomResourceDefinition /" + plural + ".networking.datumapis.com"
				if owners[id] != "crd/downstream" {
					t.Errorf("cell prerequisite %s must be owned by crd/downstream", plural)
				}
			}
			for _, object := range downstream {
				group, _, err := unstructured.NestedString(object.Object, "spec", "group")
				if err != nil {
					t.Fatal(err)
				}
				if object.GetKind() != "CustomResourceDefinition" || group != "networking.datumapis.com" {
					t.Errorf("downstream bundle contains a resource outside NSO's CRD ownership: %s", object.GetName())
				}
			}

			foundDeployment := false
			for _, object := range cell {
				if object.GetKind() == "CustomResourceDefinition" || object.GetKind() == "Namespace" {
					t.Errorf("cell controllers must not own prerequisite %s %s", object.GetKind(), object.GetName())
				}
				if ns := object.GetNamespace(); ns != "" && ns != namespace {
					t.Errorf("%s %s targets namespace %s, want %s", object.GetKind(), object.GetName(), ns, namespace)
				}
				if object.GetKind() == "Deployment" && object.GetName() == "cell-controller-manager" {
					foundDeployment = true
					sa, _, err := unstructured.NestedString(object.Object, "spec", "template", "spec", "serviceAccountName")
					if err != nil {
						t.Fatal(err)
					}
					if owners[fmt.Sprintf("/ServiceAccount %s/%s", namespace, sa)] != "cell" {
						t.Errorf("cell deployment's service account %s/%s is missing", namespace, sa)
					}
				}
			}
			if !foundDeployment {
				t.Fatal("cell bundle has no cell-controller-manager Deployment")
			}
		})
	}
}

func renderBundle(t *testing.T, bundle, namespace string) []unstructured.Unstructured {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	kustomize := os.Getenv("KUSTOMIZE")
	if kustomize == "" {
		kustomize = filepath.Join(root, "bin", "kustomize")
	}
	overlay, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bundlePath, err := filepath.Rel(overlay, filepath.Join(root, "config", bundle))
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{
		"apiVersion": "kustomize.config.k8s.io/v1beta1",
		"kind":       "Kustomization",
		"resources":  []string{bundlePath},
	}
	if namespace != "" {
		config["namespace"] = namespace
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlay, "kustomization.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(kustomize, "build", "--load-restrictor", "LoadRestrictionsNone", overlay)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err = cmd.Output()
	if err != nil {
		t.Fatalf("render %s (run make test-manifests to install kustomize): %v\n%s", bundle, err, stderr.String())
	}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	objects := make([]unstructured.Unstructured, 0)
	for {
		var object unstructured.Unstructured
		if err := decoder.Decode(&object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if len(object.Object) != 0 {
			objects = append(objects, object)
		}
	}
	if len(objects) == 0 {
		t.Fatalf("%s rendered no resources", bundle)
	}
	return objects
}
