// SPDX-License-Identifier: AGPL-3.0-only
package nlb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/require"
	api "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/cmd/nlb/spec"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type testClient struct {
	client.Client
	failRoute bool
}

func (c *testClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*api.NetworkLoadBalancerRoute); ok && c.failRoute {
		return errors.New("route temporarily unavailable")
	}
	if obj.GetUID() == "" {
		obj.SetUID(types.UID("uid-" + obj.GetName()))
	}
	return c.Client.Create(ctx, obj, opts...)
}
func setup(t *testing.T) *testClient {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	network := &api.Network{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: spec.Namespace}}
	svc := &api.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: "media", Namespace: spec.Namespace}, Spec: api.NetworkServiceSpec{NetworkInterfaces: api.NetworkServiceInterfaceSelector{Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "media"}}}, Ports: []api.NetworkServicePort{{Name: "control", Port: 443}, {Name: "media", Protocol: "UDP", PortRange: &api.PortRange{Start: 10000, End: 10127}}, {Name: "health", Port: 8080}}}}
	c := &testClient{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(network, svc).Build()}
	old, oldEnsure, brand := newClient, ensureEntitlement, util.CurrentBrand()
	newClient = func(string) (client.Client, error) { return c, nil }
	ensureEntitlement = func(context.Context, string, io.Reader, io.Writer) error { return nil }
	t.Cleanup(func() { newClient = old; ensureEntitlement = oldEnsure; util.SetBrand(brand) })
	return c
}
func execute(args ...string) (string, error) {
	cmd := Command()
	cmd.SetArgs(append(args, "--project", "demo"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetIn(bytes.NewReader(nil))
	err := cmd.Execute()
	return out.String(), err
}

var createArgs = []string{"create", "media", "--network", "default", "--listener", "control", "--protocol", "tcp", "--port", "443", "--network-service", "media", "--backend-port", "control", "--no-wait"}

func TestCreateResumeAndSharedRange(t *testing.T) {
	c := setup(t)
	c.failRoute = true
	_, err := execute(createArgs...)
	require.ErrorContains(t, err, "route")
	var n api.NetworkLoadBalancer
	require.NoError(t, c.Get(context.Background(), key("media"), &n))
	uid := n.UID
	c.failRoute = false
	_, err = execute(createArgs...)
	require.NoError(t, err)
	require.NoError(t, c.Get(context.Background(), key("media"), &n))
	require.Equal(t, uid, n.UID)
	_, err = execute("listener", "add", "media", "media", "--protocol", "udp", "--port-range", "10000-10127", "--network-service", "media", "--backend-port", "media", "--health-protocol", "tcp", "--health-port", "health", "--no-wait")
	require.NoError(t, err)
	require.NoError(t, c.Get(context.Background(), key("media"), &n))
	require.Len(t, n.Spec.Listeners, 2)
	require.Equal(t, int32(443), n.Spec.Listeners[0].Port)
	var routes api.NetworkLoadBalancerRouteList
	require.NoError(t, c.List(context.Background(), &routes))
	require.Len(t, routes.Items, 2)
	for _, r := range routes.Items {
		require.True(t, spec.Owned(&r, &n))
	}
	out, err := execute("describe", "media", "-o", "json")
	require.NoError(t, err)
	var d detail
	require.NoError(t, json.Unmarshal([]byte(out), &d))
	require.Len(t, d.Routes, 2)
	_, err = execute("address", "remove", "media", "public", "--yes")
	require.ErrorContains(t, err, "still used")
	_, err = execute("listener", "remove", "media", "media", "--yes")
	require.NoError(t, err)
	require.NoError(t, c.Get(context.Background(), key("media"), &n))
	require.Len(t, n.Spec.Listeners, 1)
	var svc api.NetworkService
	require.NoError(t, c.Get(context.Background(), key("media"), &svc))
}
func TestDryRunNoWritesAndInvalidUDP(t *testing.T) {
	c := setup(t)
	args := append(slicesCopy(createArgs), "--dry-run")
	_, err := execute(args...)
	require.NoError(t, err)
	var list api.NetworkLoadBalancerList
	require.NoError(t, c.List(context.Background(), &list))
	require.Empty(t, list.Items)
	_, err = execute("create", "bad", "--network", "default", "--listener", "media", "--protocol", "udp", "--port-range", "10000-10127", "--network-service", "media", "--backend-port", "media", "--no-wait")
	require.ErrorContains(t, err, "health")
	require.NoError(t, c.List(context.Background(), &list))
	require.Empty(t, list.Items)
}
func slicesCopy(a []string) []string { return append([]string(nil), a...) }
func TestExternalRouteBlocksRemoval(t *testing.T) {
	c := setup(t)
	_, err := execute(createArgs...)
	require.NoError(t, err)
	var n api.NetworkLoadBalancer
	require.NoError(t, c.Get(context.Background(), key("media"), &n))
	n.Spec.Listeners = append(n.Spec.Listeners, api.NetworkLoadBalancerListener{Name: "other", Protocol: "TCP", Port: 8443, AddressRefs: []string{"public"}})
	require.NoError(t, c.Update(context.Background(), &n))
	r := &api.NetworkLoadBalancerRoute{ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: spec.Namespace}, Spec: api.NetworkLoadBalancerRouteSpec{ParentRefs: []api.NetworkLoadBalancerRouteParentRef{{Name: "media", Listener: "control"}}}}
	require.NoError(t, c.Create(context.Background(), r))
	_, err = execute("listener", "remove", "media", "control", "--yes")
	require.ErrorContains(t, err, "externally managed")
	require.NoError(t, c.Get(context.Background(), key("media"), &n))
	require.Len(t, n.Spec.Listeners, 2)
}
func TestPatchPreservesUnknownNestedFields(t *testing.T) {
	old := map[string]any{"spec": map[string]any{"listeners": []any{map[string]any{"name": "a", "port": float64(80)}, map[string]any{"name": "b", "port": float64(90)}}}}
	next := map[string]any{"spec": map[string]any{"listeners": []any{map[string]any{"name": "b", "port": float64(91)}}}}
	data, err := json.Marshal(diffJSON(old, next, ""))
	require.NoError(t, err)
	patch, err := jsonpatch.DecodePatch(data)
	require.NoError(t, err)
	result, err := patch.Apply([]byte(`{"spec":{"listeners":[{"name":"a","port":80,"future":1},{"name":"b","port":90,"future":{"keep":true}}],"futureTop":42}}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"spec":{"listeners":[{"name":"b","port":91,"future":{"keep":true}}],"futureTop":42}}`, string(result))
}
func TestVersionOffline(t *testing.T) {
	setup(t)
	newClient = func(string) (client.Client, error) { t.Fatal("offline command loaded credentials"); return nil, nil }
	ensureEntitlement = func(context.Context, string, io.Reader, io.Writer) error {
		t.Fatal("offline command checked entitlement")
		return nil
	}
	out, err := execute("version")
	require.NoError(t, err)
	require.Contains(t, out, "datumctl-nlb")
}
func TestNoWaitAndTimeoutValidation(t *testing.T) {
	c := setup(t)
	args := append(slicesCopy(createArgs), "--timeout", "0s")
	_, err := execute(args...)
	require.ErrorContains(t, err, "timeout")
	var list api.NetworkLoadBalancerList
	require.NoError(t, c.List(context.Background(), &list))
	require.Empty(t, list.Items)
}

func TestPatchConflictsDoNotOverwriteConcurrentChanges(t *testing.T) {
	c := setup(t)
	_, err := execute(createArgs...)
	require.NoError(t, err)
	var original api.NetworkLoadBalancer
	require.NoError(t, c.Get(context.Background(), key("media"), &original))
	concurrent := original.DeepCopy()
	concurrent.Annotations = map[string]string{"other": "keep"}
	require.NoError(t, c.Update(context.Background(), concurrent))
	desired := original.DeepCopy()
	desired.Spec.Listeners[0].Port = 8443
	cmd := Command()
	cmd.SetContext(context.Background())
	require.Error(t, patch(cmd, c, &original, desired))
	var current api.NetworkLoadBalancer
	require.NoError(t, c.Get(context.Background(), key("media"), &current))
	require.Equal(t, int32(443), current.Spec.Listeners[0].Port)
	require.Equal(t, "keep", current.Annotations["other"])
}

func TestUpdateAndDeletionPreserveService(t *testing.T) {
	c := setup(t)
	_, err := execute(createArgs...)
	require.NoError(t, err)
	out, err := execute("update", "media", "--display-name", "Media API", "-o", "name")
	require.NoError(t, err)
	require.Equal(t, "media\n", out)
	_, err = execute("delete", "media", "--yes", "--no-wait")
	require.NoError(t, err)
	var service api.NetworkService
	require.NoError(t, c.Get(context.Background(), key("media"), &service))
}
