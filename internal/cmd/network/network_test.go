// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/networkagent"
)

func newTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme, err := util.NewScheme()
	require.NoError(t, err)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func withClient(t *testing.T, c client.Client) {
	t.Helper()
	prevClient, prevEnsure := newClient, ensureEntitlement
	newClient = func(string) (client.Client, error) { return c, nil }
	ensureEntitlement = func(context.Context, string, io.Reader, io.Writer) error { return nil }
	t.Cleanup(func() { newClient, ensureEntitlement = prevClient, prevEnsure })
}

func interactive(t *testing.T) {
	t.Helper()
	prev, had := os.LookupEnv("CI")
	require.NoError(t, os.Unsetenv("CI"))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("CI", prev)
		}
	})
}

type result struct {
	out, err string
	code     int
}

func run(t *testing.T, c client.Client, stdin string, args ...string) result {
	t.Helper()
	withClient(t, c)
	root := Command()
	root.SetArgs(append(args, "--project", "demo"))
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(stdin))
	r := result{}
	if err := root.Execute(); err != nil {
		r.code = util.RenderExit(&errOut, err, false)
	}
	r.out, r.err = out.String(), errOut.String()
	return r
}

func meta(name string, labels map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: util.ResourceNamespace, Labels: labels}
}

func network(name string, conditions ...metav1.Condition) *networkingv1alpha.Network {
	return &networkingv1alpha.Network{
		ObjectMeta: meta(name, nil),
		Spec: networkingv1alpha.NetworkSpec{
			IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
			IPFamilies: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
			MTU:        1440,
		},
		Status: networkingv1alpha.NetworkStatus{Conditions: conditions},
	}
}

func ready() metav1.Condition {
	return metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready"}
}

func iface(name, network, workload string, phase networkingv1alpha.NetworkInterfacePhase) *networkingv1alpha.NetworkInterface {
	return &networkingv1alpha.NetworkInterface{
		ObjectMeta: meta(name, map[string]string{
			networkagent.WorkloadNameLabel:                  workload,
			networkingv1alpha.NetworkInterfaceLocationLabel: "dfw",
		}),
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network:   networkingv1alpha.LocalNetworkRef{Name: network},
			Addresses: []networkingv1alpha.NetworkInterfaceAddress{{Family: "IPv6", Address: "fd20::10", Primary: true}},
		},
		Status: networkingv1alpha.NetworkInterfaceStatus{Phase: phase},
	}
}

func TestCommandTree(t *testing.T) {
	root := Command()
	want := map[string][]string{
		"list": nil, "describe": nil, "create": nil, "update": nil, "delete": nil, "diagnose": nil, "version": nil,
		"service":   {"list", "describe", "create", "update", "delete", "diagnose"},
		"interface": {"list", "release"},
	}
	for name, subs := range want {
		cmd, _, err := root.Find([]string{name})
		require.NoError(t, err, name)
		require.Equal(t, name, cmd.Name())
		for _, sub := range subs {
			c, _, err := root.Find([]string{name, sub})
			require.NoError(t, err)
			assert.Equal(t, sub, c.Name(), "%s %s", name, sub)
		}
	}
}

func TestExitCodesNameThisPlugin(t *testing.T) {
	r := run(t, newTestClient(t), "", "nope")
	assert.Equal(t, util.ExitUsage, r.code)
	assert.Contains(t, r.err, "NETWORK_USAGE")
	assert.Contains(t, util.UserAgent(), "datumctl-network")
}

func TestCreateMakesAnIPv6OnlyNetwork(t *testing.T) {
	c := newTestClient(t)
	r := run(t, c, "", "create", "backend", "--mtu", "1400")
	require.Zero(t, r.code, r.err)
	assert.Contains(t, r.out, `Network "backend" created.`)
	assert.Contains(t, r.err, "datumctl compute deploy")

	var n networkingv1alpha.Network
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "backend"}, &n))
	assert.Equal(t, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}, n.Spec.IPFamilies)
	assert.Equal(t, networkingv1alpha.NetworkIPAMModeAuto, n.Spec.IPAM.Mode)
	assert.Equal(t, int32(1400), n.Spec.MTU)
}

func TestCreateRefusesWhatAdmissionWouldBeforeCallingIt(t *testing.T) {
	for _, args := range [][]string{
		{"create", "Bad_Name"},
		{"create", "ok", "--mtu", "9000"},
	} {
		r := run(t, newTestClient(t), "", args...)
		assert.Equal(t, util.ExitUsage, r.code, "%v: %s", args, r.err)
	}
}

func TestUpdateChangesOnlyTheMTU(t *testing.T) {
	c := newTestClient(t, network("backend", ready()))

	r := run(t, c, "", "update", "backend")
	assert.Equal(t, util.ExitUsage, r.code)

	r = run(t, c, "", "update", "backend", "--mtu", "1200")
	assert.Equal(t, util.ExitUsage, r.code)

	r = run(t, c, "", "update", "backend", "--mtu", "1400", "--yes")
	require.Zero(t, r.code, r.err)
	var n networkingv1alpha.Network
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "backend"}, &n))
	assert.Equal(t, int32(1400), n.Spec.MTU)
	assert.Equal(t, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}, n.Spec.IPFamilies)
}

func TestDeleteNamesWhatLosesItsNetworkBeforeAsking(t *testing.T) {
	interactive(t)
	c := newTestClient(t, network("backend", ready()),
		iface("api-0", "backend", "api", networkingv1alpha.NetworkInterfacePhaseBound),
		iface("old-0", "backend", "old", networkingv1alpha.NetworkInterfacePhaseAvailable))

	r := run(t, c, "nope\n", "delete", "backend")
	assert.Equal(t, util.ExitAborted, r.code)
	assert.Contains(t, r.err, "Workloads that lose their network: api")
	assert.Contains(t, r.err, "Kept interfaces whose addresses are given up: old-0")

	r = run(t, c, "backend\n", "delete", "backend")
	require.Zero(t, r.code, r.err)
	assert.Contains(t, r.out, "removed once the instances and kept interfaces")
}

func TestDeleteOfAMissingNetworkIsNotFound(t *testing.T) {
	r := run(t, newTestClient(t), "", "delete", "ghost", "--yes")
	assert.Equal(t, util.ExitNotFound, r.code)
	assert.Contains(t, r.err, "datumctl network list")
}

func TestDiagnoseSaysWhoActs(t *testing.T) {
	c := newTestClient(t, network("legacy", metav1.Condition{
		Type: "Ready", Status: metav1.ConditionFalse, Reason: "IPv6Required", Message: "add IPv6",
	}))
	r := run(t, c, "", "diagnose", "legacy")
	require.Zero(t, r.code, r.err)
	assert.Contains(t, r.out, "Ready=IPv6Required")
	assert.Contains(t, r.out, "you — a change on your side fixes this")
	assert.Contains(t, r.out, "Add IPv6")
}

func TestListAndDescribe(t *testing.T) {
	c := newTestClient(t, network("default", ready()),
		iface("api-0", "default", "api", networkingv1alpha.NetworkInterfacePhaseBound))

	r := run(t, c, "", "list")
	require.Zero(t, r.code, r.err)
	assert.Contains(t, r.out, "NAME")
	assert.Contains(t, r.out, "default")

	r = run(t, c, "", "describe", "default")
	require.Zero(t, r.code, r.err)
	assert.Contains(t, r.out, "Workloads:\n  api")
	assert.Contains(t, r.out, "dfw")

	r = run(t, c, "", "describe", "default", "-o", "json")
	require.Zero(t, r.code, r.err)
	assert.Contains(t, r.out, `"name": "default"`)
}

func TestServiceCreateSelectsTheWorkload(t *testing.T) {
	c := newTestClient(t)
	r := run(t, c, "", "service", "create", "api", "--workload", "api-backend", "--port", "http:8080")
	require.Zero(t, r.code, r.err)
	assert.Contains(t, r.err, "--network-service api --port http")

	var s networkingv1alpha.NetworkService
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "api"}, &s))
	assert.Equal(t, map[string]string{networkagent.WorkloadNameLabel: "api-backend"}, s.Spec.NetworkInterfaces.Selector.MatchLabels)
	assert.Equal(t, []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}}, s.Spec.Ports)
}

func TestServiceCreateRefusesIncompleteInput(t *testing.T) {
	for _, args := range [][]string{
		{"service", "create", "api", "--port", "http:8080"},
		{"service", "create", "api", "--workload", "w"},
		{"service", "create", "api", "--workload", "w", "--port", "8080"},
		{"service", "create", "api", "--workload", "w", "--port", "HTTP:8080"},
	} {
		r := run(t, newTestClient(t), "", args...)
		assert.Equal(t, util.ExitUsage, r.code, "%v: %s", args, r.err)
	}
}

func service(name string) *networkingv1alpha.NetworkService {
	return &networkingv1alpha.NetworkService{
		ObjectMeta: meta(name, nil),
		Spec: networkingv1alpha.NetworkServiceSpec{
			NetworkInterfaces: networkingv1alpha.NetworkServiceInterfaceSelector{
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{networkagent.WorkloadNameLabel: "api"}},
			},
			Ports: []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}},
		},
	}
}

func TestServiceUpdateReplacesPortsAndKeepsTheSelector(t *testing.T) {
	c := newTestClient(t, service("api"))
	r := run(t, c, "", "service", "update", "api", "--port", "http:8081", "--port", "metrics:9100")
	require.Zero(t, r.code, r.err)

	var s networkingv1alpha.NetworkService
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "api"}, &s))
	assert.Equal(t, map[string]string{networkagent.WorkloadNameLabel: "api"}, s.Spec.NetworkInterfaces.Selector.MatchLabels)
	assert.Len(t, s.Spec.Ports, 2)

	r = run(t, c, "", "service", "update", "api", "--workload", "api-v2")
	require.Zero(t, r.code, r.err)
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "api"}, &s))
	assert.Equal(t, map[string]string{networkagent.WorkloadNameLabel: "api-v2"}, s.Spec.NetworkInterfaces.Selector.MatchLabels)
	assert.Len(t, s.Spec.Ports, 2, "changing the selector keeps the ports")
}

func TestServiceDeleteNamesTheLoadBalancersUsingIt(t *testing.T) {
	interactive(t)
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: meta("shop", nil),
		Spec: networkingv1alpha.HTTPProxySpec{Rules: []networkingv1alpha.HTTPProxyRule{{
			Backends: []networkingv1alpha.HTTPProxyRuleBackend{{
				NetworkService: &networkingv1alpha.NetworkServiceBackendRef{Name: "api", Port: "http"},
			}},
		}}},
	}
	c := newTestClient(t, service("api"), proxy)
	r := run(t, c, "no\n", "service", "delete", "api")
	assert.Equal(t, util.ExitAborted, r.code)
	assert.Contains(t, r.err, "Load balancers that stop reaching it: shop")
}

func TestInterfaceReleaseOnlyReleasesKeptInterfaces(t *testing.T) {
	c := newTestClient(t,
		iface("api-0", "default", "api", networkingv1alpha.NetworkInterfacePhaseBound),
		iface("old-0", "default", "old", networkingv1alpha.NetworkInterfacePhaseAvailable))

	r := run(t, c, "", "interface", "release", "api-0", "--yes")
	assert.Equal(t, util.ExitConflict, r.code)
	assert.Contains(t, r.err, "held by a running instance")

	r = run(t, c, "", "interface", "list", "--kept", "-o", "name")
	require.Zero(t, r.code, r.err)
	assert.Equal(t, "old-0\n", r.out)

	r = run(t, c, "", "interface", "release", "old-0", "--yes")
	require.Zero(t, r.code, r.err)
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "old-0"}, &networkingv1alpha.NetworkInterface{})
	assert.True(t, apierrors.IsNotFound(err), "the kept interface should be gone: %v", err)
}
