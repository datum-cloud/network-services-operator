package datum

import (
	"context"
	"testing"

	mcmanager "github.com/multicluster-runtime/multicluster-runtime/pkg/manager"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

type testMultiClusterManager struct {
	mcmanager.Manager
}

func (m *testMultiClusterManager) Engage(context.Context, string, cluster.Cluster) error {
	return nil
}

var runtimeScheme = runtime.NewScheme()

func init() {
	utilruntime.Must((&scheme.Builder{GroupVersion: projectGV}).AddToScheme(runtimeScheme))
}

func TestNotReadyProject(t *testing.T) {
	provider, project, _ := newTestProvider(metav1.ConditionFalse)

	req := ctrl.Request{
		NamespacedName: client.ObjectKeyFromObject(project),
	}

	result, err := provider.Reconcile(context.Background(), req)
	assert.NoError(t, err, "unexpected error returned from reconciler")
	assert.Equal(t, false, result.Requeue)
	assert.Zero(t, result.RequeueAfter)
	assert.Len(t, provider.projects, 0)
}

func TestReadyProject(t *testing.T) {
	provider, project, _ := newTestProvider(metav1.ConditionTrue)

	req := ctrl.Request{
		NamespacedName: client.ObjectKeyFromObject(project),
	}

	result, err := provider.Reconcile(context.Background(), req)
	assert.NoError(t, err, "unexpected error returned from reconciler")
	assert.Equal(t, false, result.Requeue)
	assert.Zero(t, result.RequeueAfter)
	assert.Len(t, provider.projects, 1)
}

func newTestProvider(projectStatus metav1.ConditionStatus) (*Provider, client.Object, client.Client) {
	project := &unstructured.Unstructured{}
	project.SetGroupVersionKind(projectGVK)
	project.SetName("test-project")

	conditions := []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": string(projectStatus),
		},
	}

	unstructured.SetNestedSlice(project.Object, conditions, "status", "conditions")

	fakeClient := fake.NewClientBuilder().
		WithScheme(runtimeScheme).
		WithObjects(project).
		Build()

	p := &Provider{
		client:    fakeClient,
		mcMgr:     &testMultiClusterManager{},
		config:    &rest.Config{},
		projects:  map[string]cluster.Cluster{},
		cancelFns: map[string]context.CancelFunc{},
		opts: Options{
			ClusterOptions: []cluster.Option{
				func(o *cluster.Options) {
					o.NewClient = func(config *rest.Config, options client.Options) (client.Client, error) {
						return fakeClient, nil
					}
				},
			},
		},
	}

	return p, project, fakeClient
}
