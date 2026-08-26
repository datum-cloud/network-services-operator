// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const boundInterfaceName = "hello-0-eth0"

// hubFakeCluster wraps a client as the cluster.Cluster the write-back writes to.
type hubFakeCluster struct {
	scheme *runtime.Scheme
	client client.Client
}

func (c *hubFakeCluster) GetHTTPClient() *http.Client          { return &http.Client{} }
func (c *hubFakeCluster) GetConfig() *rest.Config              { return &rest.Config{} }
func (c *hubFakeCluster) GetCache() cache.Cache                { return nil }
func (c *hubFakeCluster) GetScheme() *runtime.Scheme           { return c.scheme }
func (c *hubFakeCluster) GetClient() client.Client             { return c.client }
func (c *hubFakeCluster) GetFieldIndexer() client.FieldIndexer { return nil }
func (c *hubFakeCluster) GetEventRecorderFor(string) record.EventRecorder {
	return record.NewFakeRecorder(10)
}
func (c *hubFakeCluster) GetEventRecorder(string) events.EventRecorder { return nil }
func (c *hubFakeCluster) GetRESTMapper() meta.RESTMapper               { return nil }
func (c *hubFakeCluster) GetAPIReader() client.Reader                  { return c.client }
func (c *hubFakeCluster) Start(context.Context) error                  { return nil }

// visibility stands the three planes an interface crosses on two API servers.
// The cell and the project control plane are separate clusters in production
// and one apiserver here, told apart by namespace, which is what the
// controllers use to tell them apart in any case. The hub is its own server
// because a cell namespace and its hub namespace carry the same name.
type visibility struct {
	t   *testing.T
	ctx context.Context

	cell    client.Client
	hub     client.Client
	project client.Client

	cellNamespace    string
	projectNamespace string

	writeBack *NetworkInterfaceWriteBackReconciler
	projector *NetworkInterfaceProjector
	collector *NetworkInterfaceProjectionGCReconciler
}

// The planes are started once for the package. An apiserver takes long enough
// that one per test does not finish, and every scenario names its own
// namespaces, so sharing them isolates nothing less.
var planes = sync.OnceValues(func() ([]client.Client, error) {
	testScheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(testScheme); err != nil {
		return nil, err
	}
	if err := networkingv1alpha.AddToScheme(testScheme); err != nil {
		return nil, err
	}

	clients := make([]client.Client, 0, 2)
	for range 2 {
		env := &envtest.Environment{
			CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
			ErrorIfCRDPathMissing: true,
		}
		cfg, err := env.Start()
		if err != nil {
			return nil, err
		}
		planeEnvironments = append(planeEnvironments, env)

		cl, err := client.New(cfg, client.Options{Scheme: testScheme})
		if err != nil {
			return nil, err
		}
		clients = append(clients, cl)
	}

	return clients, nil
})

var planeEnvironments []*envtest.Environment

func TestMain(m *testing.M) {
	code := m.Run()
	for _, env := range planeEnvironments {
		_ = env.Stop()
	}
	os.Exit(code)
}

func startPlanes(t *testing.T) (cellPlane, hubPlane client.Client) {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}

	clients, err := planes()
	require.NoError(t, err)
	return clients[0], clients[1]
}

func newVisibility(t *testing.T) *visibility {
	t.Helper()

	cellPlane, hubPlane := startPlanes(t)
	ctx := context.Background()

	suffix := sanitizeName(strings.ToLower(t.Name()))
	cellNamespace := "ns-" + suffix
	projectNamespace := "proj-" + suffix

	for _, cl := range []client.Client{cellPlane, hubPlane} {
		namespace := &corev1.Namespace{}
		namespace.Name = cellNamespace
		namespace.Labels = map[string]string{
			downstreamclient.UpstreamOwnerNamespaceLabel:   projectNamespace,
			downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + testProject,
		}
		require.NoError(t, cl.Create(ctx, namespace))
	}

	projectNS := &corev1.Namespace{}
	projectNS.Name = projectNamespace
	require.NoError(t, cellPlane.Create(ctx, projectNS))

	network := &networkingv1alpha.Network{}
	network.Name = "default"
	network.Namespace = projectNamespace
	network.Spec.IPAM.Mode = networkingv1alpha.NetworkIPAMModeAuto
	require.NoError(t, cellPlane.Create(ctx, network))

	hubCluster := &hubFakeCluster{scheme: hubPlane.Scheme(), client: hubPlane}
	resolver := &staticProjectResolver{clients: map[string]client.Client{testProject: cellPlane}}

	return &visibility{
		t:                t,
		ctx:              ctx,
		cell:             cellPlane,
		hub:              hubPlane,
		project:          cellPlane,
		cellNamespace:    cellNamespace,
		projectNamespace: projectNamespace,
		writeBack: &NetworkInterfaceWriteBackReconciler{
			Location:    config.LocationConfig{Name: testLocationName},
			HubCluster:  hubCluster,
			localReader: cellPlane,
		},
		projector: &NetworkInterfaceProjector{Projects: resolver, hub: hubPlane},
		collector: &NetworkInterfaceProjectionGCReconciler{Projects: resolver, hub: hubPlane},
	}
}

// interfaceOnCell writes the interface a cell holds, addresses and all, in the
// shape the claim controller leaves behind.
func (v *visibility) interfaceOnCell() *networkingv1alpha.NetworkInterface {
	v.t.Helper()
	name := boundInterfaceName

	iface := &networkingv1alpha.NetworkInterface{}
	iface.Name = name
	iface.Namespace = v.cellNamespace
	iface.Spec = networkingv1alpha.NetworkInterfaceSpec{
		Network:       networkingv1alpha.LocalNetworkRef{Name: "default"},
		ClaimRef:      &networkingv1alpha.NetworkInterfaceClaimRef{Name: name},
		InterfaceName: "eth0",
		MTU:           1460,
		Addresses: []networkingv1alpha.NetworkInterfaceAddress{{
			Family:  networkingv1alpha.IPv6Protocol,
			Address: "fd20:1abc:2def:1::/96",
			Gateway: "fd20:1abc:2def:1::1",
			Primary: true,
		}},
		ExternalAddresses: []networkingv1alpha.NetworkInterfaceExternalAddress{{
			Family:  networkingv1alpha.IPv4Protocol,
			Address: "198.51.100.11",
			Class:   testPublicV4Class,
		}},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyRetain,
	}
	require.NoError(v.t, v.cell.Create(v.ctx, iface))

	iface.Status.Phase = networkingv1alpha.NetworkInterfacePhaseBound
	iface.Status.NetworkContextRef = &networkingv1alpha.LocalNetworkContextRef{Name: "default-" + testLocationName}
	iface.Status.AttachmentRef = &networkingv1alpha.NetworkInterfaceAttachmentRef{
		APIGroup: "cloud.datumapis.com",
		Kind:     "VPCAttachment",
		Name:     name,
	}
	iface.Status.VPC = "3kF9qP2x"
	iface.Status.Conditions = []metav1.Condition{{
		Type:               networkingv1alpha.NetworkInterfaceAllocated,
		Status:             metav1.ConditionTrue,
		Reason:             "Allocated",
		LastTransitionTime: metav1.Now(),
	}}
	require.NoError(v.t, v.cell.Status().Update(v.ctx, iface))

	return iface
}

func (v *visibility) publish() {
	v.t.Helper()
	name := boundInterfaceName
	require.NoError(v.t, v.writeBack.publish(v.ctx, v.cell, client.ObjectKey{
		Namespace: v.cellNamespace, Name: name,
	}))
}

func (v *visibility) handToProject() {
	v.t.Helper()
	name := boundInterfaceName
	var published networkingv1alpha.NetworkInterface
	require.NoError(v.t, v.hub.Get(v.ctx, client.ObjectKey{Namespace: v.cellNamespace, Name: name}, &published))
	require.NoError(v.t, v.projector.project(v.ctx, &published))
}

func (v *visibility) collect(name string) {
	v.t.Helper()
	require.NoError(v.t, v.collector.collect(v.ctx, v.project, client.ObjectKey{
		Namespace: v.projectNamespace, Name: name,
	}))
}

func (v *visibility) hubCopy(name string) (*networkingv1alpha.NetworkInterface, bool) {
	v.t.Helper()
	var copied networkingv1alpha.NetworkInterface
	err := v.hub.Get(v.ctx, client.ObjectKey{Namespace: v.cellNamespace, Name: name}, &copied)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(v.t, err)
	return &copied, true
}

func (v *visibility) projectCopy() (*networkingv1alpha.NetworkInterface, bool) {
	v.t.Helper()
	name := boundInterfaceName
	var copied networkingv1alpha.NetworkInterface
	err := v.project.Get(v.ctx, client.ObjectKey{Namespace: v.projectNamespace, Name: name}, &copied)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(v.t, err)
	return &copied, true
}

func TestInterfaceReachesTheProjectControlPlane(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()

	v.publish()
	v.handToProject()

	copied, found := v.projectCopy()
	require.True(t, found, "a consumer must be able to see the interface behind their instance")

	require.Equal(t, "default", copied.Spec.Network.Name)
	require.Equal(t, "eth0", copied.Spec.InterfaceName)
	require.Equal(t, int32(1460), copied.Spec.MTU)
	require.Equal(t, "fd20:1abc:2def:1::/96", copied.Spec.Addresses[0].Address)
	require.Equal(t, "fd20:1abc:2def:1::1", copied.Spec.Addresses[0].Gateway)
	require.Equal(t, "198.51.100.11", copied.Spec.ExternalAddresses[0].Address)
	require.Equal(t, networkingv1alpha.NetworkInterfaceReclaimPolicyRetain, copied.Spec.ReclaimPolicy)
	require.Equal(t, networkingv1alpha.NetworkInterfacePhaseBound, copied.Status.Phase)
	require.NotNil(t, meta.FindStatusCondition(copied.Status.Conditions, networkingv1alpha.NetworkInterfaceAllocated))

	require.Equal(t, testLocationName, copied.Labels[networkingv1alpha.NetworkInterfaceLocationLabel])
	require.Equal(t, boundInterfaceName, copied.Labels[networkingv1alpha.NetworkInterfaceHolderLabel])
}

func TestCopyDropsWhatOnlyMeansSomethingOnTheCell(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()

	v.publish()
	v.handToProject()

	copied, found := v.projectCopy()
	require.True(t, found)

	require.Nil(t, copied.Spec.ClaimRef, "the claim does not exist here to be referenced")
	require.Nil(t, copied.Status.NetworkContextRef, "a network context is a cell breadcrumb")
	require.Nil(t, copied.Status.AttachmentRef, "an attachment is a cell object")
	require.Empty(t, copied.Status.VPC, "a VPC identifier is the fabric's, not a consumer's")
}

func TestCopyOwnedByTheNetworkItBelongsTo(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()

	v.publish()
	v.handToProject()

	copied, found := v.projectCopy()
	require.True(t, found)
	require.Len(t, copied.OwnerReferences, 1)
	require.Equal(t, "Network", copied.OwnerReferences[0].Kind)
	require.Equal(t, "default", copied.OwnerReferences[0].Name)
}

func TestUpdateOnTheCellReachesTheCopy(t *testing.T) {
	v := newVisibility(t)
	iface := v.interfaceOnCell()

	v.publish()
	v.handToProject()

	iface.Spec.ExternalAddresses[0].Address = "198.51.100.29"
	require.NoError(t, v.cell.Update(v.ctx, iface))

	require.NoError(t, v.cell.Get(v.ctx, client.ObjectKeyFromObject(iface), iface))
	meta.SetStatusCondition(&iface.Status.Conditions, metav1.Condition{
		Type:   networkingv1alpha.NetworkInterfaceProgrammed,
		Status: metav1.ConditionTrue,
		Reason: "Programmed",
	})
	require.NoError(t, v.cell.Status().Update(v.ctx, iface))

	v.publish()
	v.handToProject()

	copied, found := v.projectCopy()
	require.True(t, found)
	require.Equal(t, "198.51.100.29", copied.Spec.ExternalAddresses[0].Address)

	programmed := meta.FindStatusCondition(copied.Status.Conditions, networkingv1alpha.NetworkInterfaceProgrammed)
	require.NotNil(t, programmed)
	require.Equal(t, metav1.ConditionTrue, programmed.Status)
}

func TestEditingACopyDoesNotSurvive(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()

	v.publish()
	v.handToProject()

	copied, _ := v.projectCopy()
	copied.Spec.MTU = 1300
	require.NoError(t, v.project.Update(v.ctx, copied))

	v.handToProject()

	copied, _ = v.projectCopy()
	require.Equal(t, int32(1460), copied.Spec.MTU, "the cell stays the only writer")

	var onCell networkingv1alpha.NetworkInterface
	require.NoError(t, v.cell.Get(v.ctx, client.ObjectKey{Namespace: v.cellNamespace, Name: boundInterfaceName}, &onCell))
	require.Equal(t, int32(1460), onCell.Spec.MTU, "an edit to a copy never reaches the cell")
}

func TestDeletingOnTheCellRemovesBothCopies(t *testing.T) {
	v := newVisibility(t)
	iface := v.interfaceOnCell()

	v.publish()
	v.handToProject()

	require.NoError(t, v.cell.Delete(v.ctx, iface))

	v.publish()
	_, found := v.hubCopy(boundInterfaceName)
	require.False(t, found, "the published copy goes with the interface")

	v.collect(boundInterfaceName)
	_, found = v.projectCopy()
	require.False(t, found, "the copy a consumer reads goes with it")
}

// A deletion nothing was watching is the case that produces a permanent orphan:
// no event replays, so the reconcile that would have removed the copy never
// runs. Only the sweep closes it.
func TestACopyIsCollectedAfterADeletionNothingSaw(t *testing.T) {
	v := newVisibility(t)
	iface := v.interfaceOnCell()

	v.publish()
	v.handToProject()

	require.NoError(t, v.cell.Delete(v.ctx, iface))

	// Nothing reconciles the interface: the cell could not see the hub when it
	// went, and there is no second event to come back to.
	copied, found := v.hubCopy(boundInterfaceName)
	require.True(t, found, "the copy outlives the interface until something notices")
	require.Equal(t, testLocationName, copied.Labels[networkingv1alpha.NetworkInterfaceLocationLabel])

	require.NoError(t, v.writeBack.sweep(v.ctx))

	_, found = v.hubCopy(boundInterfaceName)
	require.False(t, found, "the sweep collects a copy with no interface behind it")

	v.collect(boundInterfaceName)
	_, found = v.projectCopy()
	require.False(t, found)
}

// A cell only ever collects what it published. Another location's copies sit in
// the same namespaces and are none of its business.
func TestTheSweepLeavesAnotherLocationAlone(t *testing.T) {
	v := newVisibility(t)

	elsewhere := &networkingv1alpha.NetworkInterface{}
	elsewhere.Name = "elsewhere-0-eth0"
	elsewhere.Namespace = v.cellNamespace
	elsewhere.Labels = map[string]string{
		networkingv1alpha.NetworkInterfaceProjectionLabel: "true",
		networkingv1alpha.NetworkInterfaceLocationLabel:   "eu-west-1",
	}
	elsewhere.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: "default"}
	require.NoError(t, v.hub.Create(v.ctx, elsewhere))

	require.NoError(t, v.writeBack.sweep(v.ctx))

	_, found := v.hubCopy("elsewhere-0-eth0")
	require.True(t, found)
}

func TestAHandDeletedCopyComesBack(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()

	v.publish()
	v.handToProject()

	copied, _ := v.projectCopy()
	require.NoError(t, v.project.Delete(v.ctx, copied))

	v.handToProject()

	_, found := v.projectCopy()
	require.True(t, found, "a copy removed by hand is not a decision the platform keeps")
}

func TestDeletingACopyStrandsNothing(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()

	v.publish()
	v.handToProject()

	copied, _ := v.projectCopy()
	require.Empty(t, copied.Finalizers, "a copy in a project namespace cannot hold that namespace open")

	published, _ := v.hubCopy(boundInterfaceName)
	require.Empty(t, published.Finalizers)

	require.NoError(t, v.project.Delete(v.ctx, copied))
	require.NoError(t, v.hub.Delete(v.ctx, published))

	var onCell networkingv1alpha.NetworkInterface
	require.NoError(t, v.cell.Get(v.ctx, client.ObjectKey{Namespace: v.cellNamespace, Name: boundInterfaceName}, &onCell))
	require.True(t, onCell.DeletionTimestamp.IsZero(), "removing a copy touches nothing real")
	require.Equal(t, "fd20:1abc:2def:1::/96", onCell.Spec.Addresses[0].Address)
}

func TestAnUnroutableInterfaceIsNotPublished(t *testing.T) {
	v := newVisibility(t)

	namespace := &corev1.Namespace{}
	namespace.Name = "unlabelled-" + sanitizeName(strings.ToLower(t.Name()))
	require.NoError(t, v.cell.Create(v.ctx, namespace))

	iface := &networkingv1alpha.NetworkInterface{}
	iface.Name = "orphan-eth0"
	iface.Namespace = namespace.Name
	iface.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: "default"}
	require.NoError(t, v.cell.Create(v.ctx, iface))

	require.NoError(t, v.writeBack.publish(v.ctx, v.cell, client.ObjectKeyFromObject(iface)))

	var copied networkingv1alpha.NetworkInterface
	err := v.hub.Get(v.ctx, client.ObjectKeyFromObject(iface), &copied)
	require.True(t, apierrors.IsNotFound(err), "nothing is published where nothing could collect it")
}

func TestACopyGoesWhenItsNetworkDoes(t *testing.T) {
	v := newVisibility(t)
	v.interfaceOnCell()

	v.publish()
	v.handToProject()

	network := &networkingv1alpha.Network{}
	require.NoError(t, v.project.Get(v.ctx, client.ObjectKey{Namespace: v.projectNamespace, Name: "default"}, network))
	require.NoError(t, v.project.Delete(v.ctx, network))

	v.handToProject()

	_, found := v.projectCopy()
	require.False(t, found)
}

var _ cluster.Cluster = &hubFakeCluster{}

// A consumer selects the members of a network service by the keys whatever
// created the claim wrote. They only reach a service if they reach the copy.
func TestConsumerLabelsReachTheCopy(t *testing.T) {
	v := newVisibility(t)
	iface := v.interfaceOnCell()

	iface.Labels = map[string]string{
		"compute.datumapis.com/workload-name": "storefront",
		"app":                                 "storefront",
	}
	require.NoError(t, v.cell.Update(v.ctx, iface))

	v.publish()
	v.handToProject()

	copied, found := v.projectCopy()
	require.True(t, found)
	require.Equal(t, "storefront", copied.Labels["compute.datumapis.com/workload-name"])
	require.NotContains(t, copied.Labels, "app",
		"only the allow-listed prefixes travel to a copy")
}

// A label whose source has dropped it must leave the copy. A copy is selected
// by its labels, and a stale one keeps retired capacity a member of a service.
func TestACopyLosesALabelItsSourceDropped(t *testing.T) {
	v := newVisibility(t)
	iface := v.interfaceOnCell()

	iface.Labels = map[string]string{"compute.datumapis.com/workload-name": "storefront"}
	require.NoError(t, v.cell.Update(v.ctx, iface))

	v.publish()
	v.handToProject()

	copied, found := v.projectCopy()
	require.True(t, found)
	require.Equal(t, boundInterfaceName, copied.Labels[networkingv1alpha.NetworkInterfaceHolderLabel])

	require.NoError(t, v.cell.Get(v.ctx, client.ObjectKeyFromObject(iface), iface))
	iface.Spec.ClaimRef = nil
	iface.Labels = map[string]string{}
	require.NoError(t, v.cell.Update(v.ctx, iface))

	v.publish()
	v.handToProject()

	copied, found = v.projectCopy()
	require.True(t, found)
	require.NotContains(t, copied.Labels, networkingv1alpha.NetworkInterfaceHolderLabel,
		"nothing holds the interface any more")
	require.NotContains(t, copied.Labels, "compute.datumapis.com/workload-name")
	require.Equal(t, testLocationName, copied.Labels[networkingv1alpha.NetworkInterfaceLocationLabel])
}
