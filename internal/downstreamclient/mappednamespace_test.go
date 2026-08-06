package downstreamclient

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("failed building scheme: %v", err)
	}
	return s
}

// downstreamNamespace is what ensureDownstreamNamespace leaves behind: a
// namespace named after the upstream namespace UID, labelled with the cluster
// and the upstream namespace it belongs to.
func downstreamNamespace(name, cluster, upstreamNamespace string) *corev1.Namespace {
	labels := map[string]string{
		UpstreamOwnerNamespaceLabel: upstreamNamespace,
	}
	if cluster != "" {
		labels[UpstreamOwnerClusterNameLabel] = "cluster-" + cluster
	}
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}

func TestGetDownstreamNamespaceNameForUpstreamNamespace(t *testing.T) {
	scheme := testScheme(t)

	upstreamNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "demo",
			UID:  types.UID("11111111-2222-3333-4444-555555555555"),
		},
	}

	t.Run("resolves from the upstream namespace when it exists", func(t *testing.T) {
		strategy := NewMappedNamespaceResourceStrategy(
			"project-a",
			fake.NewClientBuilder().WithScheme(scheme).WithObjects(upstreamNamespace).Build(),
			fake.NewClientBuilder().WithScheme(scheme).Build(),
		)

		got, err := strategy.GetDownstreamNamespaceNameForUpstreamNamespace(context.Background(), "demo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := "ns-11111111-2222-3333-4444-555555555555"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	// The case a project purge creates: the upstream namespace is force-finalized
	// away while objects that still need cleaning up remain behind it.
	t.Run("falls back to the downstream namespace labels when the upstream namespace is gone", func(t *testing.T) {
		strategy := NewMappedNamespaceResourceStrategy(
			"project-a",
			fake.NewClientBuilder().WithScheme(scheme).Build(),
			fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				downstreamNamespace("ns-11111111-2222-3333-4444-555555555555", "project-a", "demo"),
				// Same upstream namespace name in a different project, and a
				// different namespace in the same project: neither may be picked.
				downstreamNamespace("ns-99999999-9999-9999-9999-999999999999", "project-b", "demo"),
				downstreamNamespace("ns-88888888-8888-8888-8888-888888888888", "project-a", "other"),
			).Build(),
		)

		got, err := strategy.GetDownstreamNamespaceNameForUpstreamNamespace(context.Background(), "demo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := "ns-11111111-2222-3333-4444-555555555555"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("falls back on the namespace name alone in single-cluster mode", func(t *testing.T) {
		strategy := NewMappedNamespaceResourceStrategy(
			"",
			fake.NewClientBuilder().WithScheme(scheme).Build(),
			fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				downstreamNamespace("ns-11111111-2222-3333-4444-555555555555", "", "demo"),
			).Build(),
		)

		got, err := strategy.GetDownstreamNamespaceNameForUpstreamNamespace(context.Background(), "demo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := "ns-11111111-2222-3333-4444-555555555555"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	// Nothing was ever created downstream, so there is no name to resolve. The
	// error names both halves so the cause is not hidden behind the fallback.
	t.Run("reports both failures when neither the upstream nor a downstream namespace exists", func(t *testing.T) {
		strategy := NewMappedNamespaceResourceStrategy(
			"project-a",
			fake.NewClientBuilder().WithScheme(scheme).Build(),
			fake.NewClientBuilder().WithScheme(scheme).Build(),
		)

		_, err := strategy.GetDownstreamNamespaceNameForUpstreamNamespace(context.Background(), "demo")
		if err == nil {
			t.Fatal("expected an error")
		}
		for _, want := range []string{"failed to get upstream namespace", "no downstream namespace is labelled"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})
}

func TestObjectMetaFromUpstreamObjectAfterNamespaceIsPurged(t *testing.T) {
	scheme := testScheme(t)

	strategy := NewMappedNamespaceResourceStrategy(
		"project-a",
		fake.NewClientBuilder().WithScheme(scheme).Build(),
		fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			downstreamNamespace("ns-11111111-2222-3333-4444-555555555555", "project-a", "demo"),
		).Build(),
	)

	meta, err := strategy.ObjectMetaFromUpstreamObject(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "some-gateway", Namespace: "demo"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "ns-11111111-2222-3333-4444-555555555555"; meta.Namespace != want {
		t.Errorf("got namespace %q, want %q", meta.Namespace, want)
	}
	if meta.Name != "some-gateway" {
		t.Errorf("got name %q, want %q", meta.Name, "some-gateway")
	}
	if got := meta.Labels[UpstreamOwnerNamespaceLabel]; got != "demo" {
		t.Errorf("got upstream namespace label %q, want %q", got, "demo")
	}
}
