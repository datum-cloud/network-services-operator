package downstreamclient

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

func TestUpstreamOwnerRequest(t *testing.T) {
	obj := &metav1.ObjectMeta{Labels: map[string]string{
		UpstreamOwnerClusterNameLabel: "cluster-_my-project",
		UpstreamOwnerNamespaceLabel:   "default",
		UpstreamOwnerNameLabel:        "gw",
	}}

	want := mcreconcile.Request{
		Request:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "gw"}},
		ClusterName: multicluster.ClusterName("my-project"),
	}
	if got := UpstreamOwnerRequest(obj); got != want {
		t.Errorf("UpstreamOwnerRequest() = %+v, want %+v", got, want)
	}
}
