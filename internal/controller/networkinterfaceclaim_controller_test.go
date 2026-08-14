// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	eventsv1client "k8s.io/client-go/kubernetes/typed/events/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	"go.miloapis.com/ipam/pkg/ipamerrors"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const (
	testProject       = "project-alpha"
	testProjectNS     = "default"
	testLocationName  = "us-central-1"
	testLocationNS    = "datum-locations"
	testPublicV4Class = "datum-public-v4"
)

// fakeIPAM stands in for the IPAM API server. Allocation is synchronous there,
// so the create response already carries the address.
type fakeIPAM struct {
	mu sync.Mutex

	scheme  *runtime.Scheme
	clients map[string]client.Client

	classes []*ipamv1alpha1.IPClass

	// createdIn records, per project, the IPClaim names Create was called with.
	createdIn map[string][]string
	// deletedIn records, per project, the IPClaim names Delete was called with.
	deletedIn map[string][]string
	// allocationPolicy is the policy IPAM froze onto the allocation. The server
	// releases on this value alone and never re-reads the IPClaim.
	allocationPolicy map[string]ipamv1alpha1.ReclaimPolicy
	// orphans maps an allocation to the CIDR it holds after its IPClaim was
	// deleted under a frozen Retain.
	orphans map[string]string

	// failOn refuses the named IPClaim with the given error.
	failOn map[string]error

	nextV4 int
	nextV6 int
}

func newFakeIPAM(t *testing.T, classes ...*ipamv1alpha1.IPClass) *fakeIPAM {
	t.Helper()
	scheme, err := IPAMScheme()
	require.NoError(t, err)

	return &fakeIPAM{
		scheme:           scheme,
		clients:          map[string]client.Client{},
		classes:          classes,
		createdIn:        map[string][]string{},
		deletedIn:        map[string][]string{},
		allocationPolicy: map[string]ipamv1alpha1.ReclaimPolicy{},
		orphans:          map[string]string{},
		failOn:           map[string]error{},
	}
}

func (f *fakeIPAM) ClientForProject(project string) (client.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if existing, ok := f.clients[project]; ok {
		return existing, nil
	}

	builder := fakeclient.NewClientBuilder().WithScheme(f.scheme)
	for _, class := range f.classes {
		builder = builder.WithObjects(class.DeepCopy())
	}

	cl := builder.WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			ipClaim, ok := obj.(*ipamv1alpha1.IPClaim)
			if !ok {
				return c.Create(ctx, obj, opts...)
			}

			f.mu.Lock()
			f.createdIn[project] = append(f.createdIn[project], ipClaim.Name)
			failure := f.failOn[ipClaim.Name]
			if failure == nil {
				failure = f.retainedConflictLocked(ipClaim)
			}
			if failure == nil {
				f.allocateLocked(project, ipClaim)
			}
			f.mu.Unlock()

			if failure != nil {
				return failure
			}

			if err := c.Create(ctx, obj, opts...); err != nil {
				// IPAM lets this collision escape as a raw 500, which no status
				// code tells apart from a genuine internal error.
				if apierrors.IsAlreadyExists(err) {
					return apierrors.NewInternalError(fmt.Errorf(
						`insert allocation: ERROR: duplicate key value violates unique constraint "ipam_cidr_allocations_claim_key_key" (SQLSTATE 23505)`))
				}
				return err
			}
			return nil
		},
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if ipClaim, ok := obj.(*ipamv1alpha1.IPClaim); ok {
				var stored ipamv1alpha1.IPClaim
				_ = c.Get(ctx, client.ObjectKeyFromObject(ipClaim), &stored)

				f.mu.Lock()
				f.deletedIn[project] = append(f.deletedIn[project], ipClaim.Name)
				if f.allocationPolicy[ipClaim.Name] == ipamv1alpha1.ReclaimRetain {
					f.orphans[allocationNameFor(ipClaim.Name)] = stored.Status.AllocatedCIDR
				}
				f.mu.Unlock()
			}
			return c.Delete(ctx, obj, opts...)
		},
	}).Build()

	f.clients[project] = cl
	return cl, nil
}

func allocationNameFor(ipClaimName string) string {
	return "alloc-" + ipClaimName
}

// retainedConflictLocked models the other duplicate, which IPAM maps to a 409.
// An allocation left behind by a deleted claim blocks the name it used.
func (f *fakeIPAM) retainedConflictLocked(ipClaim *ipamv1alpha1.IPClaim) error {
	allocationName := allocationNameFor(ipClaim.Name)
	if _, orphaned := f.orphans[allocationName]; !orphaned {
		return nil
	}
	return ipamerrors.NewRetainedAllocation(
		ipamv1alpha1.SchemeGroupVersion.WithResource("ipclaims").GroupResource(),
		ipClaim.Name,
		allocationName,
		fmt.Sprintf("an allocation under this identity already exists: IPAllocation %q, retained by an earlier claim of the same name; delete it to reuse the name", allocationName),
	)
}

func (f *fakeIPAM) allocateLocked(project string, ipClaim *ipamv1alpha1.IPClaim) {
	family := ipClaim.Spec.IPFamily
	if ipClaim.Spec.ClassName != "" {
		for _, class := range f.classes {
			if class.Name == ipClaim.Spec.ClassName {
				family = class.Spec.IPFamily
			}
		}
	}

	// Frozen once, here. Later edits to the IPClaim never reach it.
	f.allocationPolicy[ipClaim.Name] = ipClaim.Spec.ReclaimPolicy

	// The real server writes only allocatedCIDR, host prefixes included, and
	// never status.address.
	ipClaim.Status.Phase = ipamv1alpha1.ClaimBound
	if family == ipamv1alpha1.IPv6 {
		f.nextV6++
		ipClaim.Status.AllocatedCIDR = fmt.Sprintf("2001:db8:a000:%d::/96", f.nextV6)
	} else {
		f.nextV4++
		ipClaim.Status.AllocatedCIDR = fmt.Sprintf("10.128.0.%d/32", f.nextV4)
	}
	ipClaim.Status.PoolRef = &ipamv1alpha1.LocalRef{Name: "pool-" + project}
}

// created and deleted are keyed by project, so a test can assert which project
// an allocation reached.
func (f *fakeIPAM) created() map[string][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.createdIn)
}

func (f *fakeIPAM) deleted() map[string][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.deletedIn)
}

func (f *fakeIPAM) createdAnywhere() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, names := range f.createdIn {
		total += len(names)
	}
	return total
}

// orphanedAllocations reports allocations no IPClaim references.
func (f *fakeIPAM) orphanedAllocations() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.orphans)
}

func (f *fakeIPAM) refuse(name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failOn[name] = err
}

func publicV4Class() *ipamv1alpha1.IPClass {
	class := &ipamv1alpha1.IPClass{}
	class.Name = testPublicV4Class
	class.Spec.IPFamily = ipamv1alpha1.IPv4
	return class
}

func startNetworkInterfaceEnv(t *testing.T) (client.Client, *rest.Config) {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}

	testScheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	cl, err := client.New(cfg, client.Options{Scheme: testScheme})
	require.NoError(t, err)
	return cl, cfg
}

type scenario struct {
	restConfig *rest.Config
	events     *events.FakeRecorder
	t          *testing.T
	ctx        context.Context
	client     client.Client
	ipam       *fakeIPAM
	reconciler *NetworkInterfaceClaimReconciler
	namespace  string
}

// newScenario builds one namespace of objects. Set labelled to false for a
// namespace that names no project.
func newScenario(t *testing.T, labelled bool, networkFamilies []networkingv1alpha.IPFamily, classes ...*ipamv1alpha1.IPClass) *scenario {
	t.Helper()
	cl, restConfig := startNetworkInterfaceEnv(t)
	ctx := context.Background()

	namespaceName := "ns-" + sanitizeName(strings.ToLower(t.Name()))

	namespace := &corev1.Namespace{}
	namespace.Name = namespaceName
	namespace.Labels = map[string]string{
		downstreamclient.UpstreamOwnerNamespaceLabel: testProjectNS,
	}
	if labelled {
		namespace.Labels[downstreamclient.UpstreamOwnerClusterNameLabel] = "cluster-" + testProject
	}
	require.NoError(t, cl.Create(ctx, namespace))

	network := &networkingv1alpha.Network{}
	network.Namespace = namespaceName
	network.Name = "default"
	network.Spec = networkingv1alpha.NetworkSpec{
		IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
		IPFamilies: networkFamilies,
		MTU:        1460,
	}
	require.NoError(t, cl.Create(ctx, network))

	ipam := newFakeIPAM(t, classes...)

	operatorConfig := config.NetworkServicesOperator{
		Controllers: config.ControllersConfig{
			Sets: []config.ControllerSet{config.ControllerSetCell},
		},
	}
	operatorConfig.NetworkInterface.Location = config.LocationConfig{
		Name:      testLocationName,
		Namespace: testLocationNS,
	}

	return &scenario{
		restConfig: restConfig,
		events:     events.NewFakeRecorder(64),
		t:          t,
		ctx:        ctx,
		client:     cl,
		ipam:       ipam,
		reconciler: &NetworkInterfaceClaimReconciler{Config: operatorConfig, IPAM: ipam},
		namespace:  namespaceName,
	}
}

// transientNamespaceClient fails namespace reads the way a flaky API server
// does, without the namespace itself changing.
type transientNamespaceClient struct {
	client.Client
}

func (c *transientNamespaceClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	if _, ok := obj.(*corev1.Namespace); ok {
		return apierrors.NewServiceUnavailable("the server is currently unable to handle the request")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func sanitizeName(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *scenario) createClaim(name string, spec networkingv1alpha.NetworkInterfaceClaimSpec) *networkingv1alpha.NetworkInterfaceClaim {
	s.t.Helper()
	claim := &networkingv1alpha.NetworkInterfaceClaim{}
	claim.Namespace = s.namespace
	claim.Name = name
	claim.Spec = spec
	claim.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: "default"}
	require.NoError(s.t, s.client.Create(s.ctx, claim))
	return claim
}

func (s *scenario) reconcile(claim *networkingv1alpha.NetworkInterfaceClaim) {
	s.t.Helper()
	_, err := s.reconciler.reconcileClaim(s.ctx, s.client, s.events, client.ObjectKeyFromObject(claim))
	require.NoError(s.t, err)
}

func (s *scenario) reconcileInterface(name string) {
	s.t.Helper()
	interfaces := &NetworkInterfaceReconciler{Config: s.reconciler.Config, IPAM: s.ipam, claims: s.reconciler}
	require.NoError(s.t, interfaces.reconcileInterface(s.ctx, s.client,
		client.ObjectKey{Namespace: s.namespace, Name: name}))
}

// programNetworkContext marks the binding's context ready, which is what makes
// the interface record a context and the gateway resolvable.
func (s *scenario) programNetworkContext() string {
	s.t.Helper()

	bindingName := fmt.Sprintf("default-%s-%s", testLocationNS, testLocationName)
	var binding networkingv1alpha.NetworkBinding
	require.NoError(s.t, s.client.Get(s.ctx,
		client.ObjectKey{Namespace: s.namespace, Name: bindingName}, &binding))

	contextName := bindingName
	binding.Status.NetworkContextRef = &networkingv1alpha.NetworkContextRef{
		Namespace: s.namespace,
		Name:      contextName,
	}
	require.NoError(s.t, s.client.Status().Update(s.ctx, &binding))
	return contextName
}

func (s *scenario) createSubnet(
	name, contextName string,
	family networkingv1alpha.IPFamily,
	startAddress string,
	prefixLength int32,
) {
	s.t.Helper()

	subnet := &networkingv1alpha.Subnet{}
	subnet.Namespace = s.namespace
	subnet.Name = name
	subnet.Spec = networkingv1alpha.SubnetSpec{
		SubnetClass:    "private",
		NetworkContext: networkingv1alpha.LocalNetworkContextRef{Name: contextName},
		Location: networkingv1alpha.LocationReference{
			Name:      testLocationName,
			Namespace: testLocationNS,
		},
		IPFamily:     family,
		StartAddress: startAddress,
		PrefixLength: prefixLength,
	}
	require.NoError(s.t, s.client.Create(s.ctx, subnet))
}

func (s *scenario) getClaim(name string) *networkingv1alpha.NetworkInterfaceClaim {
	s.t.Helper()
	var claim networkingv1alpha.NetworkInterfaceClaim
	require.NoError(s.t, s.client.Get(s.ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, &claim))
	return &claim
}

func (s *scenario) getInterface(name string) (*networkingv1alpha.NetworkInterface, error) {
	var iface networkingv1alpha.NetworkInterface
	err := s.client.Get(s.ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, &iface)
	return &iface, err
}

func (s *scenario) deleteClaim(claim *networkingv1alpha.NetworkInterfaceClaim) {
	s.t.Helper()
	require.NoError(s.t, s.client.Delete(s.ctx, claim))
	s.reconcile(claim)
}

func (s *scenario) ipClaim(name string) *ipamv1alpha1.IPClaim {
	s.t.Helper()
	cl, err := s.ipam.ClientForProject(testProject)
	require.NoError(s.t, err)

	var ipClaim ipamv1alpha1.IPClaim
	require.NoError(s.t, cl.Get(s.ctx, client.ObjectKey{Namespace: testProjectNS, Name: name}, &ipClaim))
	return &ipClaim
}

func conditionOf(claim *networkingv1alpha.NetworkInterfaceClaim, conditionType string) *metav1.Condition {
	return apimeta.FindStatusCondition(claim.Status.Conditions, conditionType)
}

func TestNetworkInterfaceClaimBindsDualStack(t *testing.T) {
	s := newScenario(t, true,
		[]networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol},
		publicV4Class())

	claim := s.createClaim("web-0-eth0", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies: []networkingv1alpha.IPFamily{
			networkingv1alpha.IPv6Protocol,
			networkingv1alpha.IPv4Protocol,
		},
		Addresses:     []networkingv1alpha.NetworkInterfaceAddressRequest{{Class: testPublicV4Class}},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	iface, err := s.getInterface("web-0-eth0")
	require.NoError(t, err)
	require.Equal(t, int32(1460), iface.Spec.MTU)
	require.Equal(t, "eth0", iface.Spec.InterfaceName)
	require.Equal(t, networkingv1alpha.NetworkInterfacePhaseBound, iface.Status.Phase)
	require.NotNil(t, iface.Spec.ClaimRef)
	require.Equal(t, claim.Name, iface.Spec.ClaimRef.Name)

	require.Len(t, iface.Spec.Addresses, 2)
	require.Equal(t, networkingv1alpha.IPv6Protocol, iface.Spec.Addresses[0].Family)
	require.True(t, iface.Spec.Addresses[0].Primary, "the first family listed holds the primary address")
	require.Equal(t, "2001:db8:a000:1::/96", iface.Spec.Addresses[0].Address)
	require.Equal(t, networkingv1alpha.IPv4Protocol, iface.Spec.Addresses[1].Family)
	require.False(t, iface.Spec.Addresses[1].Primary)
	require.Equal(t, "10.128.0.1/32", iface.Spec.Addresses[1].Address,
		"an address inside the network keeps its prefix, host prefixes included")

	require.Len(t, iface.Spec.ExternalAddresses, 1)
	require.Equal(t, testPublicV4Class, iface.Spec.ExternalAddresses[0].Class)
	require.Equal(t, "10.128.0.2", iface.Spec.ExternalAddresses[0].Address,
		"an externally reachable address is bare, with no prefix")

	require.ElementsMatch(t, []string{
		"web-0-eth0-f-ipv6",
		"web-0-eth0-f-ipv4",
		"web-0-eth0-c-" + testPublicV4Class,
	}, s.ipam.created()[testProject])

	bound := s.getClaim("web-0-eth0")
	require.Equal(t, "web-0-eth0", bound.Status.NetworkInterfaceRef.Name)
	require.Equal(t, iface.Spec.Addresses, bound.Status.Addresses)
	require.Equal(t, iface.Spec.ExternalAddresses, bound.Status.ExternalAddresses)

	require.Equal(t, metav1.ConditionTrue, conditionOf(bound, networkingv1alpha.NetworkInterfaceClaimBound).Status)
	require.Equal(t, metav1.ConditionTrue, conditionOf(bound, networkingv1alpha.NetworkInterfaceClaimAllocated).Status)
	require.Equal(t, metav1.ConditionUnknown, conditionOf(bound, networkingv1alpha.NetworkInterfaceClaimProgrammed).Status,
		"programming is out of scope and must stay unknown")
	require.NotEqual(t, metav1.ConditionTrue, conditionOf(bound, networkingv1alpha.NetworkInterfaceClaimReady).Status,
		"Ready requires Programmed, which nothing reports yet")
}

func TestNetworkInterfaceClaimFailsClosedWithoutProject(t *testing.T) {
	s := newScenario(t, false, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	claim := s.createClaim("orphan-eth0", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	require.Zero(t, s.ipam.createdAnywhere(),
		"a namespace naming no project must not reach IPAM at all")

	_, err := s.getInterface("orphan-eth0")
	require.True(t, apierrors.IsNotFound(err), "nothing may be published without a project")

	rejected := s.getClaim("orphan-eth0")
	for _, conditionType := range []string{
		networkingv1alpha.NetworkInterfaceClaimBound,
		networkingv1alpha.NetworkInterfaceClaimAllocated,
		networkingv1alpha.NetworkInterfaceClaimReady,
	} {
		condition := conditionOf(rejected, conditionType)
		require.Equal(t, metav1.ConditionFalse, condition.Status, conditionType)
		require.Equal(t, "ProjectUnresolved", condition.Reason, conditionType)
		require.Contains(t, condition.Message, downstreamclient.UpstreamOwnerClusterNameLabel,
			"the failure must name the label that is missing")
	}

	// A claim that allocated nothing must still be deletable.
	s.deleteClaim(rejected)
	var gone networkingv1alpha.NetworkInterfaceClaim
	err = s.client.Get(s.ctx, client.ObjectKeyFromObject(rejected), &gone)
	require.True(t, apierrors.IsNotFound(err))
}

func TestNetworkInterfaceClaimRejectsFamilyTheNetworkDoesNotCarry(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol})

	claim := s.createClaim("v6-on-v4-eth0", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	require.Zero(t, s.ipam.createdAnywhere(),
		"IPAM binds the class's family and would never report this, so NSO must catch it first")

	rejected := s.getClaim("v6-on-v4-eth0")
	condition := conditionOf(rejected, networkingv1alpha.NetworkInterfaceClaimAllocated)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "AddressFamilyNotCarried", condition.Reason)
	require.Contains(t, condition.Message, "default")
	require.Contains(t, condition.Message, "IPv6")
}

func TestNetworkInterfaceClaimRollsBackPartialAllocation(t *testing.T) {
	s := newScenario(t, true,
		[]networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol})

	s.ipam.refuse("half-eth0-f-ipv4",
		ipamerrors.NewPoolExhausted("datum-v4", `IPPool "datum-v4" is exhausted`))

	claim := s.createClaim("half-eth0", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies: []networkingv1alpha.IPFamily{
			networkingv1alpha.IPv6Protocol,
			networkingv1alpha.IPv4Protocol,
		},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	_, err := s.getInterface("half-eth0")
	require.True(t, apierrors.IsNotFound(err), "a partly addressed interface is never published")

	require.Equal(t, []string{"half-eth0-f-ipv6"}, s.ipam.deleted()[testProject],
		"the address that did allocate is released rather than leaked")

	rejected := s.getClaim("half-eth0")
	condition := conditionOf(rejected, networkingv1alpha.NetworkInterfaceClaimAllocated)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, string(allocationFailureExhausted), condition.Reason)
	require.Equal(t,
		`No address is left for an IPv4 address: IPPool "datum-v4" is exhausted`,
		condition.Message,
		"the condition names the address we wanted and the pool to widen, each once")
}

func TestNetworkInterfaceRetainRebindsSameAddresses(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	spec := networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyRetain,
	}

	claim := s.createClaim("slot-0-eth0", spec)
	s.reconcile(claim)

	first, err := s.getInterface("slot-0-eth0")
	require.NoError(t, err)
	originalAddresses := first.Spec.Addresses
	originalClaimUID := claim.UID

	s.deleteClaim(s.getClaim("slot-0-eth0"))

	retained, err := s.getInterface("slot-0-eth0")
	require.NoError(t, err, "Retain keeps the interface")
	require.Nil(t, retained.Spec.ClaimRef)
	require.Equal(t, networkingv1alpha.NetworkInterfacePhaseAvailable, retained.Status.Phase)
	require.Equal(t, originalAddresses, retained.Spec.Addresses, "a retained interface keeps its addresses")
	require.Empty(t, s.ipam.deleted()[testProject], "Retain releases no addresses")
	require.Empty(t, s.ipam.orphanedAllocations(),
		"retention comes from keeping the IPClaim alive, so nothing is ever orphaned")

	allocationsBefore := len(s.ipam.created()[testProject])

	replacement := s.createClaim("slot-0-eth0", spec)
	require.NotEqual(t, originalClaimUID, replacement.UID,
		"the replacement is a genuinely different object, not the same claim resurrected")
	s.reconcile(replacement)

	rebound, err := s.getInterface("slot-0-eth0")
	require.NoError(t, err)
	require.Equal(t, originalAddresses, rebound.Spec.Addresses,
		"the replacement comes back to the addresses the predecessor held")
	require.Equal(t, "slot-0-eth0", rebound.Spec.ClaimRef.Name,
		"the interface records the claim now holding it")
	require.Equal(t, networkingv1alpha.NetworkInterfacePhaseBound, rebound.Status.Phase)
	require.Len(t, s.ipam.created()[testProject], allocationsBefore,
		"rebinding allocates nothing new")

	bound := s.getClaim("slot-0-eth0")
	require.Equal(t, originalAddresses, bound.Status.Addresses)
}

func TestNetworkInterfaceDeleteReleasesAddresses(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	claim := s.createClaim("ephemeral-eth0", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	_, err := s.getInterface("ephemeral-eth0")
	require.NoError(t, err)

	s.deleteClaim(s.getClaim("ephemeral-eth0"))

	_, err = s.getInterface("ephemeral-eth0")
	require.True(t, apierrors.IsNotFound(err), "Delete removes the interface")
	require.Equal(t, []string{"ephemeral-eth0-f-ipv6"}, s.ipam.deleted()[testProject])
	require.Empty(t, s.ipam.orphanedAllocations(),
		"an address claimed under Delete goes back to its pool")
}

// IPAM reports a duplicate name as a 500. Treating that as a hard failure
// rejects the claim forever, because every retry asks for the same name.
func TestAllocationResumesAfterTheInterfaceFailsToLand(t *testing.T) {
	s := newScenario(t, true,
		[]networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol})

	spec := networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies: []networkingv1alpha.IPFamily{
			networkingv1alpha.IPv6Protocol,
			networkingv1alpha.IPv4Protocol,
		},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	}

	claim := s.createClaim("resumed", spec)
	s.reconcile(claim)

	original, err := s.getInterface("resumed")
	require.NoError(t, err)
	originalAddresses := original.Spec.Addresses

	// Stand in for the interface write never landing.
	controllerutil.RemoveFinalizer(original, networkInterfaceFinalizer)
	require.NoError(t, s.client.Update(s.ctx, original))
	require.NoError(t, s.client.Delete(s.ctx, original))

	allocationsBefore := len(s.ipam.created()[testProject])

	s.reconcile(s.getClaim("resumed"))

	recovered, err := s.getInterface("resumed")
	require.NoError(t, err, "the next reconcile must rebuild the interface, not reject the claim")
	require.Equal(t, originalAddresses, recovered.Spec.Addresses,
		"it comes back on the addresses already allocated under those names")

	require.Len(t, s.ipam.created()[testProject], allocationsBefore,
		"the addresses are found by name, so nothing is re-allocated")

	bound := s.getClaim("resumed")
	require.Equal(t, metav1.ConditionTrue, conditionOf(bound, networkingv1alpha.NetworkInterfaceClaimAllocated).Status,
		"a 409 from a name we already own is not an allocation failure")
}

// A retained interface sits Available and bound to nothing, which is when an
// operator is most likely to delete it. Its addresses must still be released.
func TestDeletingAnInterfaceReleasesItsAddresses(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	claim := s.createClaim("stranded", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyRetain,
	})
	s.reconcile(claim)
	s.deleteClaim(s.getClaim("stranded"))

	available, err := s.getInterface("stranded")
	require.NoError(t, err)
	require.Equal(t, networkingv1alpha.NetworkInterfacePhaseAvailable, available.Status.Phase)
	require.Contains(t, available.Finalizers, networkInterfaceFinalizer,
		"a retained interface carries its own finalizer, having no claim to carry one for it")

	require.NoError(t, s.client.Delete(s.ctx, available))
	s.reconcileInterface("stranded")

	_, err = s.getInterface("stranded")
	require.True(t, apierrors.IsNotFound(err), "the finalizer must not hold the delete open once released")
	require.Equal(t, []string{"stranded-f-ipv6"}, s.ipam.deleted()[testProject],
		"the addresses go back to the pool rather than outliving every object naming them")
}

// A live claim rebuilds the interface, so releasing its addresses here would
// renumber a running workload.
func TestDeletingABoundInterfaceKeepsItsAddresses(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	claim := s.createClaim("still-held", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	bound, err := s.getInterface("still-held")
	require.NoError(t, err)
	originalAddresses := bound.Spec.Addresses

	require.NoError(t, s.client.Delete(s.ctx, bound))
	s.reconcileInterface("still-held")

	require.Empty(t, s.ipam.deleted()[testProject],
		"a claim still holds this interface, so its addresses stay allocated")

	s.reconcile(s.getClaim("still-held"))

	rebuilt, err := s.getInterface("still-held")
	require.NoError(t, err, "the claim rebuilds the interface it still holds")
	require.Equal(t, originalAddresses, rebuilt.Spec.Addresses,
		"and it comes back on the same addresses rather than renumbering")
}

// A fake recorder cannot be forbidden, so no other test catches an event the
// controller has no grant to write. The generated role is the only signal.
func TestManagerRoleGrantsEventCreation(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("..", "..", "config", "rbac", "role.yaml"))
	require.NoError(t, err)

	var role rbacv1.ClusterRole
	require.NoError(t, yaml.Unmarshal(manifest, &role))

	granted := false
	for _, rule := range role.Rules {
		if slices.Contains(rule.APIGroups, "events.k8s.io") &&
			slices.Contains(rule.Resources, "events") &&
			slices.Contains(rule.Verbs, "create") {
			granted = true
			break
		}
	}

	require.True(t, granted,
		"the controller records events through the events.k8s.io API; without that group granted they are "+
			"rejected and the warning never reaches an operator, whatever the core-group grant says")
}

// events.k8s.io/v1 requires an action and a resolvable regarding reference,
// and a fake recorder checks neither. This posts through a real broadcaster so
// the event has to survive validation. Authorization is covered separately by
// TestManagerRoleGrantsEventCreation, because envtest does not enforce RBAC.
func TestMissingAllocationEventReachesTheAPIServer(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	claim := s.createClaim("delivered", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	iface, err := s.getInterface("delivered")
	require.NoError(t, err)

	eventClient, err := eventsv1client.NewForConfig(s.restConfig)
	require.NoError(t, err)

	broadcaster := events.NewBroadcaster(&events.EventSinkImpl{Interface: eventClient})
	require.NoError(t, broadcaster.StartRecordingToSinkWithContext(s.ctx))
	t.Cleanup(broadcaster.Shutdown)

	recorder := broadcaster.NewRecorder(s.client.Scheme(), "networkinterfaceclaim-controller")

	s.reconciler.reportMissingAllocation(s.ctx, recorder,
		projectRouting{project: testProject, projectNamespace: testProjectNS},
		s.getClaim("delivered"), iface,
		allocationEntry{discriminator: "f-ipv6", address: iface.Spec.Addresses[0].Address},
		"delivered-f-ipv6", "")

	var delivered *eventsv1.Event
	require.Eventually(t, func() bool {
		list, err := eventClient.Events(s.namespace).List(s.ctx, metav1.ListOptions{})
		if err != nil {
			return false
		}
		for i := range list.Items {
			if list.Items[i].Reason == "AddressAllocationMissing" {
				delivered = &list.Items[i]
				return true
			}
		}
		return false
	}, 30*time.Second, 250*time.Millisecond,
		"the event never landed; the API server rejected it or the recorder never posted it")

	require.Equal(t, corev1.EventTypeWarning, delivered.Type)
	require.NotEmpty(t, delivered.Action, "events.k8s.io/v1 requires an action")
	require.Equal(t, "NetworkInterfaceClaim", delivered.Regarding.Kind)
	require.Equal(t, "delivered", delivered.Regarding.Name)
	require.NotNil(t, delivered.Related, "the interface is carried as the related object")
	require.Equal(t, "NetworkInterface", delivered.Related.Kind)
	require.Contains(t, delivered.Note, iface.Spec.Addresses[0].Address)
	require.Contains(t, delivered.Note, "delivered-f-ipv6")
	require.Contains(t, delivered.Note, testProject)
}

// Addresses belong to the network they were allocated from. Publishing them
// under another network hands one network's addresses to another.
func TestAdoptionRefusesAnInterfaceOnAnotherNetwork(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	other := &networkingv1alpha.Network{}
	other.Namespace = s.namespace
	other.Name = "other"
	other.Spec = networkingv1alpha.NetworkSpec{
		IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
		IPFamilies: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		MTU:        1460,
	}
	require.NoError(t, s.client.Create(s.ctx, other))

	spec := networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyRetain,
	}

	first := s.createClaim("crossnet", spec)
	s.reconcile(first)
	s.deleteClaim(s.getClaim("crossnet"))

	retained, err := s.getInterface("crossnet")
	require.NoError(t, err)
	heldAddress := retained.Spec.Addresses[0].Address

	onOtherNetwork := spec
	onOtherNetwork.Network = networkingv1alpha.LocalNetworkRef{Name: "other"}
	claim := &networkingv1alpha.NetworkInterfaceClaim{}
	claim.Namespace = s.namespace
	claim.Name = "crossnet"
	claim.Spec = onOtherNetwork
	require.NoError(t, s.client.Create(s.ctx, claim))
	s.reconcile(claim)

	rejected := s.getClaim("crossnet")
	condition := conditionOf(rejected, networkingv1alpha.NetworkInterfaceClaimReady)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "NetworkMismatch", condition.Reason)
	require.Contains(t, condition.Message, "other")

	require.Empty(t, rejected.Status.Addresses,
		"a claim on another network must not publish addresses allocated for this one")

	unchanged, err := s.getInterface("crossnet")
	require.NoError(t, err)
	require.Equal(t, heldAddress, unchanged.Spec.Addresses[0].Address)
	require.Nil(t, unchanged.Spec.ClaimRef, "the interface stays unbound")
}

// The data plane owns Programmed. Overwriting it puts Ready permanently out of
// reach, and Ready is what consumers gate on.
func TestProgrammedIsSeededThenLeftAlone(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	claim := s.createClaim("gated", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	seeded := s.getClaim("gated")
	require.Equal(t, metav1.ConditionUnknown,
		conditionOf(seeded, networkingv1alpha.NetworkInterfaceClaimProgrammed).Status)
	require.NotEqual(t, metav1.ConditionTrue,
		conditionOf(seeded, networkingv1alpha.NetworkInterfaceClaimReady).Status)

	// Stand in for the data plane reporting the attachment.
	apimeta.SetStatusCondition(&seeded.Status.Conditions, metav1.Condition{
		Type:    networkingv1alpha.NetworkInterfaceClaimProgrammed,
		Status:  metav1.ConditionTrue,
		Reason:  "Programmed",
		Message: "The attachment is ready",
	})
	require.NoError(t, s.client.Status().Update(s.ctx, seeded))

	s.reconcile(s.getClaim("gated"))

	settled := s.getClaim("gated")
	require.Equal(t, metav1.ConditionTrue,
		conditionOf(settled, networkingv1alpha.NetworkInterfaceClaimProgrammed).Status,
		"NSO must not revert the condition the data plane owns")
	require.Equal(t, metav1.ConditionTrue,
		conditionOf(settled, networkingv1alpha.NetworkInterfaceClaimReady).Status,
		"Ready follows from the other three rather than being hardcoded")
}

// A blip reading the namespace must not demote a healthy claim and then leave
// nothing to bring it back.
func TestTransientProjectFailureDoesNotWedgeTheClaim(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	claim := s.createClaim("blip", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	failing := &transientNamespaceClient{Client: s.client}
	_, err := s.reconciler.reconcileClaim(s.ctx, failing, s.events,
		client.ObjectKey{Namespace: s.namespace, Name: "blip"})
	require.Error(t, err, "a failed read must be retried, not turned into a rejection")

	bound := s.getClaim("blip")
	require.Equal(t, metav1.ConditionTrue,
		conditionOf(bound, networkingv1alpha.NetworkInterfaceClaimBound).Status,
		"a read failure says nothing about whether the claim is bound")
}

// A rejection must carry its own way back, because nothing watches the network.
func TestRejectionRequeues(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	claim := &networkingv1alpha.NetworkInterfaceClaim{}
	claim.Namespace = s.namespace
	claim.Name = "nonetwork"
	claim.Spec = networkingv1alpha.NetworkInterfaceClaimSpec{
		Network:       networkingv1alpha.LocalNetworkRef{Name: "absent"},
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	}
	require.NoError(t, s.client.Create(s.ctx, claim))

	result, err := s.reconciler.reconcileClaim(s.ctx, s.client, s.events,
		client.ObjectKeyFromObject(claim))
	require.NoError(t, err)
	require.NotZero(t, result.RequeueAfter,
		"nothing else will bring this claim back once the network appears")
}

// A provider configures a NIC from the interface alone, so the gateway has to
// reach NetworkInterface.spec. The subnet usually appears after the interface,
// so it has to be filled in later rather than only at creation.
func TestGatewayReachesTheInterfaceWhenTheSubnetAppears(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol})

	claim := s.createClaim("routed", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	iface, err := s.getInterface("routed")
	require.NoError(t, err)
	require.Empty(t, iface.Spec.Addresses[0].Gateway,
		"no subnet yet, so no gateway, which is a legitimate state")

	contextName := s.programNetworkContext()
	s.createSubnet("v4", contextName, networkingv1alpha.IPv4Protocol, "10.128.0.0", 24)

	s.reconcile(s.getClaim("routed"))

	routed, err := s.getInterface("routed")
	require.NoError(t, err)
	require.Equal(t, "10.128.0.1", routed.Spec.Addresses[0].Gateway,
		"the gateway must be on the interface, which is all a provider reads")

	bound := s.getClaim("routed")
	require.Equal(t, "10.128.0.1", bound.Status.Addresses[0].Gateway,
		"the claim's copy comes from the interface rather than being resolved twice")

	// Reconciling again must not keep rewriting the same value.
	before := routed.ResourceVersion
	s.reconcile(s.getClaim("routed"))
	settled, err := s.getInterface("routed")
	require.NoError(t, err)
	require.Equal(t, before, settled.ResourceVersion,
		"an unchanged gateway must not write to the API server")
}

// IPAM may give a missing address to another claim, so an operator has to see
// it. Reallocating instead would renumber a running workload.
func TestMissingAllocationIsReportedOnTheClaim(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	claim := s.createClaim("unbacked", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	iface, err := s.getInterface("unbacked")
	require.NoError(t, err)
	advertised := iface.Spec.Addresses[0].Address

	ipamClient, err := s.ipam.ClientForProject(testProject)
	require.NoError(t, err)
	require.NoError(t, ipamClient.Delete(s.ctx, s.ipClaim("unbacked-f-ipv6")))

	before := testutil.ToFloat64(missingAllocationsTotal.WithLabelValues(testProject))
	s.reconcile(s.getClaim("unbacked"))

	require.Equal(t, before+1, testutil.ToFloat64(missingAllocationsTotal.WithLabelValues(testProject)),
		"the metric is what makes this alertable")

	var warning string
	select {
	case warning = <-s.events.Events:
	default:
		t.Fatal("no event was recorded for an address nothing holds")
	}

	require.Contains(t, warning, corev1.EventTypeWarning)
	require.Contains(t, warning, "AddressAllocationMissing")
	require.Contains(t, warning, advertised, "the event names the address at risk")
	require.Contains(t, warning, "unbacked-f-ipv6", "and the allocation that went missing")
	require.Contains(t, warning, testProject)

	// Allocated must not claim every address is held once one is not.
	reported := s.getClaim("unbacked")
	allocated := conditionOf(reported, networkingv1alpha.NetworkInterfaceClaimAllocated)
	require.Equal(t, metav1.ConditionFalse, allocated.Status)
	require.Equal(t, "AddressAllocationMissing", allocated.Reason)
	require.Contains(t, allocated.Message, advertised)

	require.NotEqual(t, metav1.ConditionTrue,
		conditionOf(reported, networkingv1alpha.NetworkInterfaceClaimReady).Status)

	still, err := s.getInterface("unbacked")
	require.NoError(t, err)
	require.Equal(t, advertised, still.Spec.Addresses[0].Address,
		"the address is never silently renumbered")
}

// Release reads addresses off the interface. Deleting the interface first
// leaves nothing to read, so it must fall back to what the claim asked for.
func TestDeletingAClaimWhoseInterfaceIsGoneReleasesItsAddresses(t *testing.T) {
	s := newScenario(t, true,
		[]networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol},
		publicV4Class())

	claim := s.createClaim("outlived", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies: []networkingv1alpha.IPFamily{
			networkingv1alpha.IPv6Protocol,
			networkingv1alpha.IPv4Protocol,
		},
		Addresses:     []networkingv1alpha.NetworkInterfaceAddressRequest{{Class: testPublicV4Class}},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(claim)

	iface, err := s.getInterface("outlived")
	require.NoError(t, err)
	controllerutil.RemoveFinalizer(iface, networkInterfaceFinalizer)
	require.NoError(t, s.client.Update(s.ctx, iface))
	require.NoError(t, s.client.Delete(s.ctx, iface))

	s.deleteClaim(s.getClaim("outlived"))

	require.ElementsMatch(t, []string{
		"outlived-f-ipv6",
		"outlived-f-ipv4",
		"outlived-c-" + testPublicV4Class,
	}, s.ipam.deleted()[testProject],
		"every address the claim minted goes back, interface or no interface")
}

// An interface outlives the claim that allocated its addresses. A lookup keyed
// on the current holder finds nothing, which reads as nothing to release.
func TestAllocationsFollowTheMintingClaimNotTheHolder(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	spec := func() networkingv1alpha.NetworkInterfaceClaimSpec {
		return networkingv1alpha.NetworkInterfaceClaimSpec{
			InterfaceName:        "eth0",
			IPFamilies:           []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
			ReclaimPolicy:        networkingv1alpha.NetworkInterfaceReclaimPolicyRetain,
			NetworkInterfaceName: "shared",
		}
	}

	minter := s.createClaim("minter", spec())
	s.reconcile(minter)
	require.Equal(t, []string{"minter-f-ipv6"}, s.ipam.created()[testProject])

	s.deleteClaim(s.getClaim("minter"))

	adopter := s.createClaim("adopter", spec())
	s.reconcile(adopter)

	iface, err := s.getInterface("shared")
	require.NoError(t, err)
	require.Equal(t, "minter", iface.Annotations[allocationClaimAnnotation],
		"the interface keeps naming the claim its addresses were minted under")
	require.Equal(t, "adopter", iface.Spec.ClaimRef.Name)
	require.Len(t, s.ipam.created()[testProject], 1, "adoption allocates nothing new")

	s.deleteClaim(s.getClaim("adopter"))
	require.NoError(t, s.client.Delete(s.ctx, iface))
	s.reconcileInterface("shared")

	require.Equal(t, []string{"minter-f-ipv6"}, s.ipam.deleted()[testProject],
		"release must find the address under the name it was minted with")
}

func TestAdoptedInterfaceMustSatisfyTheClaim(t *testing.T) {
	s := newScenario(t, true,
		[]networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol})

	single := s.createClaim("grower", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyRetain,
	})
	s.reconcile(single)
	s.deleteClaim(s.getClaim("grower"))

	dualStack := s.createClaim("grower", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies: []networkingv1alpha.IPFamily{
			networkingv1alpha.IPv6Protocol,
			networkingv1alpha.IPv4Protocol,
		},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyRetain,
	})
	s.reconcile(dualStack)

	rejected := s.getClaim("grower")
	condition := conditionOf(rejected, networkingv1alpha.NetworkInterfaceClaimAllocated)
	require.Equal(t, metav1.ConditionFalse, condition.Status,
		"a retained interface holding one family cannot satisfy a dual-stack claim")
	require.Contains(t, condition.Message, "IPv4")
}

// IPAM freezes the reclaim policy onto the allocation. A claim asking for a
// different one cannot be honoured, so binding it would strand the address.
func TestAdoptionRefusesADifferentReclaimPolicy(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	retained := s.createClaim("switcher", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyRetain,
	})
	s.reconcile(retained)
	s.deleteClaim(s.getClaim("switcher"))

	replacement := s.createClaim("switcher", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyDelete,
	})
	s.reconcile(replacement)

	condition := conditionOf(s.getClaim("switcher"), networkingv1alpha.NetworkInterfaceClaimAllocated)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Contains(t, condition.Message, "Retain")
	require.Empty(t, s.ipam.orphanedAllocations(),
		"refusing at bind time is what keeps the allocation from being stranded later")
}

// An immutable reclaim policy is what stops an address being stranded.
func TestReclaimPolicyIsImmutable(t *testing.T) {
	s := newScenario(t, true, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol})

	claim := s.createClaim("frozen", networkingv1alpha.NetworkInterfaceClaimSpec{
		InterfaceName: "eth0",
		IPFamilies:    []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ReclaimPolicy: networkingv1alpha.NetworkInterfaceReclaimPolicyRetain,
	})
	s.reconcile(claim)

	switched := s.getClaim("frozen")
	switched.Spec.ReclaimPolicy = networkingv1alpha.NetworkInterfaceReclaimPolicyDelete
	err := s.client.Update(s.ctx, switched)
	require.Error(t, err, "the API must reject the switch that would strand the allocation")
	require.Contains(t, err.Error(), "immutable")
}

func TestExternalAddressesDropTheHostPrefix(t *testing.T) {
	for _, tc := range []struct {
		allocated string
		want      string
	}{
		{"198.51.100.11/32", "198.51.100.11"},
		{"2001:db8::1/128", "2001:db8::1"},
		{"10.128.0.0/24", "10.128.0.0/24"},
		{"2001:db8:a000:1::/96", "2001:db8:a000:1::/96"},
		{"not-an-address", "not-an-address"},
	} {
		require.Equal(t, tc.want, allocatedAddress{cidr: tc.allocated}.bareAddress(), tc.allocated)
	}
}

func TestIPClaimNamesSurviveInstanceReplacement(t *testing.T) {
	const claimName = "workload-default-us-central-1-0-eth0"

	require.Equal(t, "workload-default-us-central-1-0-eth0-f-ipv6",
		ipClaimName(claimName, familyDiscriminator(networkingv1alpha.IPv6Protocol)))

	require.Equal(t,
		ipClaimName(claimName, familyDiscriminator(networkingv1alpha.IPv6Protocol)),
		ipClaimName(claimName, familyDiscriminator(networkingv1alpha.IPv6Protocol)),
		"the name depends on the claim and the request, and on nothing that changes")

	require.NotEqual(t,
		ipClaimName(claimName, familyDiscriminator(networkingv1alpha.IPv4Protocol)),
		ipClaimName(claimName, familyDiscriminator(networkingv1alpha.IPv6Protocol)))

	require.NotEqual(t,
		ipClaimName(claimName, classDiscriminator("f-ipv6")),
		ipClaimName(claimName, familyDiscriminator(networkingv1alpha.IPv6Protocol)),
		"a class named like a family must not collide with the family")

	longName := strings.Repeat("a", 253)
	for _, discriminator := range []string{
		familyDiscriminator(networkingv1alpha.IPv4Protocol),
		familyDiscriminator(networkingv1alpha.IPv6Protocol),
		classDiscriminator(strings.Repeat("c", 63)),
	} {
		name := ipClaimName(longName, discriminator)
		require.LessOrEqual(t, len(name), 253, "names must stay valid at the maximum claim length")
		require.Empty(t, validation.IsDNS1123Subdomain(name))
		require.Equal(t, name, ipClaimName(longName, discriminator), "the hashed form is deterministic too")
	}

	require.NotEqual(t,
		ipClaimName(longName, familyDiscriminator(networkingv1alpha.IPv4Protocol)),
		ipClaimName(longName, familyDiscriminator(networkingv1alpha.IPv6Protocol)),
		"truncation must not merge two requests into one name")
}
