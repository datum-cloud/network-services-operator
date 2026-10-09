// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

// oneProject serves every project from one cluster.
type oneProject struct {
	mcmanager.Manager
	project cluster.Cluster
}

func (m oneProject) GetCluster(context.Context, multicluster.ClusterName) (cluster.Cluster, error) {
	return m.project, nil
}

// splitProject is a project whose cache and API server hold different objects, so a test can tell
// which of the two a read went to.
type splitProject struct {
	cluster.Cluster
	cache, apiServer client.Client
}

func (c splitProject) GetClient() client.Client    { return c.cache }
func (c splitProject) GetAPIReader() client.Reader { return c.apiServer }

func TestProjectReaderReadsTheAPIServer(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	object := func(name string) *corev1.ConfigMap {
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name}}
	}
	mgr := oneProject{project: splitProject{
		cache:     fake.NewClientBuilder().WithScheme(scheme).WithObjects(object("in-cache")).Build(),
		apiServer: fake.NewClientBuilder().WithScheme(scheme).WithObjects(object("on-api-server")).Build(),
	}}

	reader, err := ProjectReader(context.Background(), mgr, "project")
	require.NoError(t, err)

	var list corev1.ConfigMapList
	require.NoError(t, reader.List(context.Background(), &list))
	require.Len(t, list.Items, 1)
	require.Equal(t, "on-api-server", list.Items[0].Name)
}
