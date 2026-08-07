package downstreamclient

import (
	"context"
	"testing"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestProjectNameFromClusterName(t *testing.T) {
	for _, tc := range []struct {
		name        string
		clusterName string
		want        string
	}{
		{"project", "my-project", "my-project"},
		{"legacy leading slash", "/my-project", "my-project"},
		{"single cluster provider", "single", ""},
		{"empty", "", ""},
		{"invalid label value", "org/my-project", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProjectNameFromClusterName(tc.clusterName); got != tc.want {
				t.Errorf("ProjectNameFromClusterName(%q) = %q, want %q", tc.clusterName, got, tc.want)
			}
		})
	}
}

func newTestStrategy(t *testing.T, upstreamClusterName string, downstreamObjects ...client.Object) (ResourceStrategy, client.Client) {
	t.Helper()

	testScheme := runtime.NewScheme()
	if err := scheme.AddToScheme(testScheme); err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}

	downstreamClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(downstreamObjects...).
		Build()

	upstreamClient := fake.NewClientBuilder().WithScheme(testScheme).Build()

	return NewMappedNamespaceResourceStrategy(upstreamClusterName, upstreamClient, downstreamClient), downstreamClient
}

func downstreamNamespaceLabels(t *testing.T, c client.Client, name string) map[string]string {
	t.Helper()

	var namespace corev1.Namespace
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, &namespace); err != nil {
		t.Fatalf("failed to get downstream namespace %q: %v", name, err)
	}

	return namespace.Labels
}

func TestEnsureDownstreamNamespaceLabelsProject(t *testing.T) {
	strategy, downstreamClient := newTestStrategy(t, "my-project")

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "anchor", Namespace: "ns-1234"},
	}
	if err := strategy.GetClient().Create(context.Background(), configMap); err != nil {
		t.Fatalf("failed to create downstream object: %v", err)
	}

	labels := downstreamNamespaceLabels(t, downstreamClient, "ns-1234")
	if got := labels[resourcemanagerv1alpha1.ProjectNameLabel]; got != "my-project" {
		t.Errorf("project label = %q, want %q", got, "my-project")
	}
	if got := labels[UpstreamOwnerClusterNameLabel]; got != "cluster-my-project" {
		t.Errorf("cluster label = %q, want %q", got, "cluster-my-project")
	}
}

func TestEnsureDownstreamNamespaceSkipsProjectForSingleCluster(t *testing.T) {
	strategy, downstreamClient := newTestStrategy(t, "single")

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "anchor", Namespace: "ns-1234"},
	}
	if err := strategy.GetClient().Create(context.Background(), configMap); err != nil {
		t.Fatalf("failed to create downstream object: %v", err)
	}

	labels := downstreamNamespaceLabels(t, downstreamClient, "ns-1234")
	if _, ok := labels[resourcemanagerv1alpha1.ProjectNameLabel]; ok {
		t.Errorf("project label present for single cluster provider: %v", labels)
	}
}

func TestUpdateBackfillsProjectLabel(t *testing.T) {
	existing := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns-1234",
			Labels: map[string]string{
				UpstreamOwnerClusterNameLabel: "cluster-my-project",
			},
		},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "anchor", Namespace: "ns-1234"},
	}

	strategy, downstreamClient := newTestStrategy(t, "my-project", existing, configMap)

	configMap.Data = map[string]string{"key": "value"}
	if err := strategy.GetClient().Update(context.Background(), configMap); err != nil {
		t.Fatalf("failed to update downstream object: %v", err)
	}

	labels := downstreamNamespaceLabels(t, downstreamClient, "ns-1234")
	if got := labels[resourcemanagerv1alpha1.ProjectNameLabel]; got != "my-project" {
		t.Errorf("project label = %q, want %q", got, "my-project")
	}
}
