// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func locationRef(name string) locationsv1alpha1.LocationReference {
	return locationsv1alpha1.LocationReference{Name: name}
}

func depsFor(r Reader) DepsFor {
	return func(context.Context) (ToolDeps, error) {
		return ToolDeps{Reader: r, Namespace: "default"}, nil
	}
}

func pinClock(t *testing.T) {
	t.Helper()
	prev := nowFunc
	nowFunc = func() time.Time { return testNow }
	t.Cleanup(func() { nowFunc = prev })
}

func serverTools(t *testing.T, s *mcp.Server) []*mcp.Tool {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	_, err := s.Connect(context.Background(), st, nil)
	require.NoError(t, err)
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := c.Connect(context.Background(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	res, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	return res.Tools
}

func diagnose(t *testing.T, r *fakeReader, name string) *NetworkDiagnosis {
	t.Helper()
	pinClock(t)
	d, err := DiagnoseNetwork(context.Background(), r, "default", name)
	require.NoError(t, err)
	return d
}

func TestToolsAreReadOnly(t *testing.T) {
	rt := reflect.TypeOf((*Reader)(nil)).Elem()
	for i := 0; i < rt.NumMethod(); i++ {
		name := rt.Method(i).Name
		assert.True(t, strings.HasPrefix(name, "List") || strings.HasPrefix(name, "Get"),
			"Reader.%s is not a read; changes belong in the assistant's plan and apply path", name)
	}

	for _, tool := range serverTools(t, newServer()) {
		require.NotNil(t, tool.Annotations, tool.Name)
		assert.True(t, tool.Annotations.ReadOnlyHint, "%s must be marked read-only", tool.Name)
		assert.True(t, strings.HasSuffix(tool.Description, "Read-only.") ||
			strings.HasSuffix(tool.Description, "Writes nothing."), "%s must say it writes nothing", tool.Name)
	}
}

func newServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	RegisterTools(s, depsFor(&fakeReader{}))
	return s
}

func TestEveryToolIsPrefixedAndRegistered(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range serverTools(t, newServer()) {
		names[tool.Name] = true
		assert.True(t, strings.HasPrefix(tool.Name, "network_"), tool.Name)
	}
	for _, want := range []string{ToolList, ToolGet, ToolDiagnose, ToolServiceList, ToolServiceDiagnose, ToolReasonExplain, ToolRender} {
		assert.True(t, names[want], "%s is not registered", want)
	}
}

func TestAHealthyNetworkWithNoLocationsIsNotAFault(t *testing.T) {
	d := diagnose(t, &fakeReader{networks: []networkingv1alpha.Network{readyNetwork("default")}}, "default")
	assert.True(t, d.Ready)
	assert.Nil(t, d.RootCause)
	assert.Equal(t, ConfidenceClean, d.Confidence)
	assert.Contains(t, d.Summary, "not in use yet")
}

func TestReadyIsReportedOnceWhenItRepeatsTheAddressStep(t *testing.T) {
	n := readyNetwork("backend")
	n.Status.Conditions = []metav1.Condition{
		cond("IPAMAllocated", "AddressPoolExhausted", metav1.ConditionFalse, time.Hour),
		cond("Ready", "AddressPoolExhausted", metav1.ConditionFalse, time.Hour),
	}
	d := diagnose(t, &fakeReader{networks: []networkingv1alpha.Network{n}}, "backend")
	require.NotNil(t, d.RootCause)
	assert.Equal(t, "AddressPoolExhausted", d.RootCause.Reason)
	assert.Equal(t, ActionabilityPlatform, d.RootCause.Actionability)
	assert.Equal(t, ScopeNetwork, d.RootCause.Scope)
	assert.Empty(t, d.OtherCauses)
}

func TestANetworkWithoutIPv6IsTheCustomersToFix(t *testing.T) {
	n := readyNetwork("legacy")
	n.Spec.IPFamilies = []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol}
	n.Status.Conditions = []metav1.Condition{cond("Ready", "IPv6Required", metav1.ConditionFalse, time.Hour)}
	d := diagnose(t, &fakeReader{networks: []networkingv1alpha.Network{n}}, "legacy")
	require.NotNil(t, d.RootCause)
	assert.Equal(t, ActionabilityUser, d.RootCause.Actionability)
	assert.Equal(t, SkillNetworkNotReady, d.RootCause.Skill)
}

func TestTheWalkFollowsAWorkloadsAttachmentToTheLocationThatSaysWhy(t *testing.T) {
	ctx := readyContext("default", "us-east-1")
	ctx.Status.Conditions = []metav1.Condition{
		cond("IPAMAllocated", "AllocationFailed", metav1.ConditionFalse, time.Hour),
		cond("Ready", "AllocationFailed", metav1.ConditionFalse, time.Hour),
	}
	binding := networkingv1alpha.NetworkBinding{
		ObjectMeta: meta("api-us-east-1", nil),
		Spec: networkingv1alpha.NetworkBindingSpec{
			Network:  networkingv1alpha.NetworkRef{Name: "default"},
			Location: locationRef("us-east-1"),
			Consumer: &networkingv1alpha.NetworkBindingConsumer{Kind: "WorkloadDeployment", Name: "api"},
		},
		Status: networkingv1alpha.NetworkBindingStatus{Conditions: []metav1.Condition{
			cond("Ready", "NetworkContextNotReady", metav1.ConditionFalse, time.Hour),
		}},
	}
	d := diagnose(t, &fakeReader{
		networks: []networkingv1alpha.Network{readyNetwork("default")},
		contexts: []networkingv1alpha.NetworkContext{ctx},
		bindings: []networkingv1alpha.NetworkBinding{binding},
	}, "default")

	require.NotNil(t, d.RootCause)
	assert.Equal(t, KindNetworkContext, d.RootCause.Kind)
	assert.Equal(t, "AllocationFailed", d.RootCause.Reason)
	assert.Equal(t, "us-east-1", d.RootCause.Location)
	for _, c := range d.OtherCauses {
		assert.NotEqual(t, "NetworkContextNotReady", c.Reason, "a pointer must not be reported when its target is known")
	}
}

func TestAMissingNetworkNamedByAWorkloadIsReportedOnTheWorkload(t *testing.T) {
	binding := networkingv1alpha.NetworkBinding{
		ObjectMeta: meta("api-dfw", nil),
		Spec: networkingv1alpha.NetworkBindingSpec{
			Network:  networkingv1alpha.NetworkRef{Name: "default"},
			Location: locationRef("dfw"),
			Consumer: &networkingv1alpha.NetworkBindingConsumer{Kind: "WorkloadDeployment", Name: "api"},
		},
		Status: networkingv1alpha.NetworkBindingStatus{Conditions: []metav1.Condition{
			cond("Ready", "LocationNotAvailable", metav1.ConditionFalse, time.Hour),
		}},
	}
	d := diagnose(t, &fakeReader{
		networks: []networkingv1alpha.Network{readyNetwork("default")},
		bindings: []networkingv1alpha.NetworkBinding{binding},
	}, "default")
	require.NotNil(t, d.RootCause)
	assert.Equal(t, "LocationNotAvailable", d.RootCause.Reason)
	assert.Equal(t, "api", d.RootCause.Workload)
	assert.Equal(t, ActionabilityUser, d.RootCause.Actionability)
}

func TestAnInterfaceClaimPointingAtAStepReportsTheStep(t *testing.T) {
	claim := networkingv1alpha.NetworkInterfaceClaim{
		ObjectMeta: meta("api-0-eth0", map[string]string{WorkloadNameLabel: "api"}),
		Spec:       networkingv1alpha.NetworkInterfaceClaimSpec{Network: networkingv1alpha.LocalNetworkRef{Name: "default"}},
		Status: networkingv1alpha.NetworkInterfaceClaimStatus{Conditions: []metav1.Condition{
			cond("Bound", "AddressFamilyNotCarried", metav1.ConditionFalse, time.Hour),
			cond("Ready", "NotBound", metav1.ConditionFalse, time.Hour),
		}},
	}
	d := diagnose(t, &fakeReader{
		networks: []networkingv1alpha.Network{readyNetwork("default")},
		claims:   []networkingv1alpha.NetworkInterfaceClaim{claim},
	}, "default")
	require.NotNil(t, d.RootCause)
	assert.Equal(t, "AddressFamilyNotCarried", d.RootCause.Reason)
	assert.Equal(t, ActionabilityUser, d.RootCause.Actionability)
	assert.Equal(t, "api", d.RootCause.Workload)
}

func TestScopeDecidesTheRootCause(t *testing.T) {
	n := readyNetwork("default")
	n.Status.Conditions = []metav1.Condition{cond("Ready", "IPv6Required", metav1.ConditionFalse, time.Hour)}
	iface := boundInterface("api-0", "dfw", "api", true)
	iface.Status.Conditions[1] = cond("Prepared", "Pending", metav1.ConditionUnknown, time.Hour)
	d := diagnose(t, &fakeReader{
		networks:   []networkingv1alpha.Network{n},
		interfaces: []networkingv1alpha.NetworkInterface{iface},
	}, "default")
	require.NotNil(t, d.RootCause)
	assert.Equal(t, ScopeNetwork, d.RootCause.Scope)
	require.Len(t, d.OtherCauses, 1)
	assert.Equal(t, ActionabilityStalled, d.OtherCauses[0].Actionability,
		"Prepared pending for an hour is past its window")
}

func TestUnreadableEvidenceIsNeverACleanBillOfHealth(t *testing.T) {
	d := diagnose(t, &fakeReader{
		networks:       []networkingv1alpha.Network{readyNetwork("default")},
		claimsReadable: errors.New("forbidden"),
	}, "default")
	assert.Nil(t, d.RootCause)
	assert.Equal(t, ConfidencePartial, d.Confidence)
	assert.NotEmpty(t, d.Unreadable)
}

func TestAnUncataloguedReasonIsStillReportedWithItsEvidence(t *testing.T) {
	n := readyNetwork("default")
	n.Status.Conditions = []metav1.Condition{cond("Ready", "SomethingNew", metav1.ConditionFalse, time.Hour)}
	d := diagnose(t, &fakeReader{networks: []networkingv1alpha.Network{n}}, "default")
	require.NotNil(t, d.RootCause)
	assert.False(t, d.RootCause.Catalogued)
	assert.Equal(t, "SomethingNew", d.RootCause.Reason)
	assert.Equal(t, "SomethingNew message", d.RootCause.Message)
}

func TestGetShowsLocationsWorkloadsServicesAndKeptInterfaces(t *testing.T) {
	pinClock(t)
	kept := boundInterface("old-0", "dfw", "old", false)
	kept.Status.Phase = networkingv1alpha.NetworkInterfacePhaseAvailable
	sub := networkingv1alpha.Subnet{
		ObjectMeta: meta("default-dfw-v6", nil),
		Spec: networkingv1alpha.SubnetSpec{
			NetworkContext: networkingv1alpha.LocalNetworkContextRef{Name: "default-dfw"},
			Location:       locationRef("dfw"),
		},
		Status: networkingv1alpha.SubnetStatus{StartAddress: ptrTo("fd20:1:0:1::"), PrefixLength: ptrTo(int32(64))},
	}
	r := &fakeReader{
		networks: []networkingv1alpha.Network{readyNetwork("default")},
		contexts: []networkingv1alpha.NetworkContext{readyContext("default", "dfw")},
		subnets:  []networkingv1alpha.Subnet{sub},
		interfaces: []networkingv1alpha.NetworkInterface{
			boundInterface("api-0", "dfw", "api", true),
			boundInterface("api-1", "dfw", "api", false),
			boundInterface("api-2", "fra", "api", true),
			kept,
		},
		services: []networkingv1alpha.NetworkService{service("api", "api")},
	}
	v, err := Network(context.Background(), r, "default", "default")
	require.NoError(t, err)

	assert.Equal(t, "fd20:1::/48", v.IPv6Prefix)
	assert.Equal(t, []string{"api"}, v.Workloads)
	assert.Equal(t, []string{"api"}, v.Services)
	assert.Equal(t, []string{"old-0"}, v.KeptInterfaces)
	assert.Equal(t, []LocationView{
		{Location: "dfw", Ready: true, AddressRanges: []string{"fd20:1:0:1::/64"}, Instances: 2, InstancesServing: 1},
		{Location: "fra", Instances: 1, InstancesServing: 1},
	}, v.Locations, "an instance in a location the network has not reported on still shows, not ready")
}

func ptrTo[T any](v T) *T { return &v }

func TestServiceWithNoServingInstancesPointsAtTheWorkload(t *testing.T) {
	pinClock(t)
	svc := service("api", "api",
		cond("MembersResolved", "Resolved", metav1.ConditionTrue, time.Hour),
		cond("Ready", "NoServingLocations", metav1.ConditionFalse, time.Hour))
	r := &fakeReader{
		interfaces: []networkingv1alpha.NetworkInterface{boundInterface("api-0", "dfw", "api", false)},
		services:   []networkingv1alpha.NetworkService{svc},
	}
	d := diagnoseService(&r.services[0], readAll(context.Background(), r, "default"), testNow)
	require.NotNil(t, d.RootCause)
	assert.Equal(t, "NoServingLocations", d.RootCause.Reason)
	assert.Equal(t, ActionabilityUser, d.RootCause.Actionability)
	require.Len(t, d.NotServing, 1)
	assert.Equal(t, "HolderUnavailable", d.NotServing[0].Reason)
	assert.Equal(t, []string{"default"}, d.Networks)
}

func TestServiceMatchingNothingListsTheWorkloadsThatExist(t *testing.T) {
	pinClock(t)
	svc := service("api", "ap1",
		cond("MembersResolved", "NoMatchingInterfaces", metav1.ConditionFalse, time.Hour),
		cond("Ready", "NoMatchingInterfaces", metav1.ConditionFalse, time.Hour))
	r := &fakeReader{
		interfaces: []networkingv1alpha.NetworkInterface{boundInterface("api-0", "dfw", "api", true)},
		services:   []networkingv1alpha.NetworkService{svc},
	}
	d := diagnoseService(&r.services[0], readAll(context.Background(), r, "default"), testNow)
	require.NotNil(t, d.RootCause)
	assert.Equal(t, "NoMatchingInterfaces", d.RootCause.Reason)
	assert.Empty(t, d.OtherCauses, "Ready repeats MembersResolved and must be reported once")
	assert.Equal(t, []string{"api"}, d.WorkloadsInProject)
	assert.Contains(t, d.Summary, "ap1")
}

func TestReasonExplainNeedsTheKindWhenMeaningsDiffer(t *testing.T) {
	h := reasonExplain(depsFor(&fakeReader{}))
	_, out, err := h(context.Background(), nil, ReasonExplainInput{Reason: "RangeOccupied"})
	require.NoError(t, err)
	assert.True(t, out.Ambiguous)

	_, out, err = h(context.Background(), nil, ReasonExplainInput{Reason: "RangeOccupied", Kind: KindNetwork})
	require.NoError(t, err)
	require.NotNil(t, out.Reason)
	assert.Equal(t, KindNetwork, out.Reason.Kind)
}

func render(t *testing.T, in RenderInput) (map[string]any, RenderOutput, error) {
	t.Helper()
	_, out, err := networkRender(depsFor(&fakeReader{}))(context.Background(), nil, in)
	if err != nil {
		return nil, out, err
	}
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out.Manifest), &doc))
	return doc, out, nil
}

func TestRenderedNetworkIsIPv6OnlyAndAccepted(t *testing.T) {
	doc, out, err := render(t, RenderInput{Kind: KindNetwork, Name: "backend"})
	require.NoError(t, err)
	assert.Equal(t, "networking.datumapis.com/v1alpha", doc["apiVersion"])
	spec := doc["spec"].(map[string]any)
	assert.Equal(t, []any{"IPv6"}, spec["ipFamilies"])
	assert.Equal(t, map[string]any{"mode": "Auto"}, spec["ipam"])
	assert.NotContains(t, spec, "mtu")
	assert.NotEmpty(t, out.Notes)
}

func TestRenderRefusesWhatAdmissionWould(t *testing.T) {
	for name, in := range map[string]RenderInput{
		"bad name":             {Kind: KindNetwork, Name: "Backend_1"},
		"mtu too low":          {Kind: KindNetwork, Name: "n", MTU: 1200},
		"mtu too high":         {Kind: KindNetwork, Name: "n", MTU: 9000},
		"service field":        {Kind: KindNetwork, Name: "n", Workload: "api"},
		"no selector":          {Kind: KindNetworkService, Name: "s", Ports: []RenderPort{{Name: "http", Port: 80}}},
		"two selectors":        {Kind: KindNetworkService, Name: "s", Workload: "a", MatchLabels: map[string]string{"x": "y"}, Ports: []RenderPort{{Name: "http", Port: 80}}},
		"no ports":             {Kind: KindNetworkService, Name: "s", Workload: "api"},
		"duplicate port name":  {Kind: KindNetworkService, Name: "s", Workload: "api", Ports: []RenderPort{{Name: "http", Port: 80}, {Name: "http", Port: 81}}},
		"duplicate port":       {Kind: KindNetworkService, Name: "s", Workload: "api", Ports: []RenderPort{{Name: "a", Port: 80}, {Name: "b", Port: 80}}},
		"bad port name":        {Kind: KindNetworkService, Name: "s", Workload: "api", Ports: []RenderPort{{Name: "HTTP", Port: 80}}},
		"unknown app protocol": {Kind: KindNetworkService, Name: "s", Workload: "api", Ports: []RenderPort{{Name: "grpc", Port: 9090, AppProtocol: "grpc"}}},
		"unknown kind":         {Kind: "Subnet", Name: "s"},
	} {
		_, _, err := render(t, in)
		assert.Error(t, err, name)
	}
}

func TestRenderedServiceSelectsTheWorkloadByItsInterfaceLabel(t *testing.T) {
	doc, _, err := render(t, RenderInput{Kind: KindNetworkService, Name: "api", Workload: "api-backend",
		Ports: []RenderPort{{Name: "http", Port: 8080}}})
	require.NoError(t, err)
	spec := doc["spec"].(map[string]any)
	sel := spec["networkInterfaces"].(map[string]any)["selector"].(map[string]any)
	assert.Equal(t, map[string]any{WorkloadNameLabel: "api-backend"}, sel["matchLabels"])
	assert.Equal(t, []any{map[string]any{"name": "http", "port": float64(8080)}}, spec["ports"])
}

func TestRenderedServiceCarriesAppProtocol(t *testing.T) {
	for _, value := range []string{"h2c", "kubernetes.io/h2c"} {
		doc, _, err := render(t, RenderInput{Kind: KindNetworkService, Name: "api", Workload: "api-backend",
			Ports: []RenderPort{{Name: "http", Port: 8080}, {Name: "grpc", Port: 9090, AppProtocol: value}}})
		require.NoError(t, err, value)
		assert.Equal(t, []any{
			map[string]any{"name": "http", "port": float64(8080)},
			map[string]any{"name": "grpc", "port": float64(9090), "appProtocol": "kubernetes.io/h2c"},
		}, doc["spec"].(map[string]any)["ports"], value)
	}
}
