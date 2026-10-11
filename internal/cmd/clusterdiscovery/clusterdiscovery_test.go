// SPDX-License-Identifier: AGPL-3.0-only

package clusterdiscovery

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	infrastructurev1alpha1 "go.miloapis.com/milo/pkg/apis/infrastructure/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

type fakeSyncedProvider struct{ synced bool }

func (p *fakeSyncedProvider) HasSynced() bool { return p.synced }

func TestProjectsRegisteredFollowsTheProvider(t *testing.T) {
	provider := &fakeSyncedProvider{}
	check := projectsRegistered(provider)
	request := httptest.NewRequest("GET", "/readyz/projects", nil)

	assert.Error(t, check(request), "not ready while projects are still registering")

	provider.synced = true
	assert.NoError(t, check(request), "ready once every project found at start is registered")
}

func TestProjectReadyReadsAProjectAsTheProviderDoes(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, resourcemanagerv1alpha1.AddToScheme(scheme))
	require.NoError(t, infrastructurev1alpha1.AddToScheme(scheme))
	condition := func(conditionType string, status metav1.ConditionStatus) []metav1.Condition {
		return []metav1.Condition{{Type: conditionType, Status: status, Reason: "Test", LastTransitionTime: metav1.Now()}}
	}
	project := func(name string, conditions []metav1.Condition) *resourcemanagerv1alpha1.Project {
		return &resourcemanagerv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: resourcemanagerv1alpha1.ProjectStatus{Conditions: conditions}}
	}
	controlPlane := func(name string, conditions []metav1.Condition) *infrastructurev1alpha1.ProjectControlPlane {
		return &infrastructurev1alpha1.ProjectControlPlane{ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: infrastructurev1alpha1.ProjectControlPlaneStatus{Conditions: conditions}}
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		project("ready", condition("Ready", metav1.ConditionTrue)),
		project("provisioning", condition("Ready", metav1.ConditionFalse)),
		controlPlane("ready", condition("ControlPlaneReady", metav1.ConditionTrue)),
		controlPlane("ready-but-not-its-control-plane", condition("Ready", metav1.ConditionTrue)),
	).Build()

	for _, tc := range []struct {
		name                     string
		internalServiceDiscovery bool
		project                  string
		want                     bool
	}{
		{"a Ready project", false, "ready", true},
		{"a project not Ready yet", false, "provisioning", false},
		{"a project that is gone", false, "gone", false},
		{"a control plane that is ready", true, "ready", true},
		{"a control plane read by the condition the provider reads", true, "ready-but-not-its-control-plane", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := projectReady(reader, tc.internalServiceDiscovery)(context.Background(), tc.project)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("a failed read is an error, not a project that is gone", func(t *testing.T) {
		failing := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return errors.New("cache not started")
			},
		}).Build()
		_, err := projectReady(failing, false)(context.Background(), "ready")
		assert.Error(t, err)
	})
}
