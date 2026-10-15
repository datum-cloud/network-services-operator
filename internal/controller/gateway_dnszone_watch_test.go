// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"maps"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// quietWindow is how long a change that must reach no Gateway is watched. An event reaches the recorder in
// milliseconds on envtest; the next step that must reach Gateways shows the watch was still live.
const quietWindow = 2 * time.Second

// TestDNSZoneChangeReachesOnlyCoveredGateways checks that a DNSZone change re-reconciles only the Gateways whose
// listener hostnames the zone covers, and only when the change is one a Gateway's DNS reads (NSO#438).
func TestDNSZoneChangeReachesOnlyCoveredGateways(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, s := startDNSStatusEnv(t)
	c, err := client.New(cfg, client.Options{Scheme: s})
	require.NoError(t, err)
	seen := startDNSZoneWatch(t, ctx, cfg, s)
	covered := createZoneGateways(t, ctx, c)

	zone := &dnsv1alpha1.DNSZone{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "example-test"},
		Spec:       dnsv1alpha1.DNSZoneSpec{DomainName: "example.test", DNSZoneClassName: "test"},
	}
	want := map[types.NamespacedName]int{}
	expect := func(step string, reaches bool) {
		t.Helper()
		if reaches {
			for gw := range covered {
				want[gw]++
			}
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
				if maps.Equal(seen.counts(), want) {
					return
				}
			}
			require.Equal(t, want, seen.counts(), step+" did not reach exactly the covered Gateways")
			return
		}
		time.Sleep(quietWindow)
		require.Equal(t, want, seen.counts(), step+" reached a Gateway")
	}

	require.NoError(t, c.Create(ctx, zone))
	expect("the zone's creation", true)
	for _, step := range zoneStatusSteps() {
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(zone), zone))
		step.change(&zone.Status)
		require.NoError(t, c.Status().Update(ctx, zone))
		expect(step.name, step.reaches)
	}
	require.NoError(t, c.Delete(ctx, zone))
	expect("the zone's deletion", true)
}

type zoneStatusStep struct {
	name    string
	change  func(*dnsv1alpha1.DNSZoneStatus)
	reaches bool
}

// zoneStatusSteps returns status changes in the order a zone goes through them, each with whether a Gateway's DNS
// reads it. A Gateway reads the zone's Accepted and Programmed conditions as true or not, and its nameservers.
func zoneStatusSteps() []zoneStatusStep {
	setCondition := func(conditionType string, status metav1.ConditionStatus, reason, message string) func(*dnsv1alpha1.DNSZoneStatus) {
		return func(st *dnsv1alpha1.DNSZoneStatus) {
			apimeta.SetStatusCondition(&st.Conditions, metav1.Condition{
				Type: conditionType, Status: status, Reason: reason, Message: message,
			})
		}
	}
	return []zoneStatusStep{
		{"its record count", func(st *dnsv1alpha1.DNSZoneStatus) { st.RecordCount = 1 }, false},
		{"Accepted reported False where it was absent", setCondition(conditionTypeAccepted, metav1.ConditionFalse, "Pending", "waiting"), false},
		{"Accepted's message alone", setCondition(conditionTypeAccepted, metav1.ConditionFalse, "Pending", "still waiting"), false},
		{"Accepted becoming True", setCondition(conditionTypeAccepted, metav1.ConditionTrue, "Accepted", ""), true},
		{"Programmed becoming True", setCondition(conditionTypeProgrammed, metav1.ConditionTrue, "Programmed", ""), true},
		{"its nameservers", func(st *dnsv1alpha1.DNSZoneStatus) { st.Nameservers = []string{"ns1.example.net"} }, true},
		{"its record count on a ready zone", func(st *dnsv1alpha1.DNSZoneStatus) { st.RecordCount = 2 }, false},
		{"Programmed becoming False", setCondition(conditionTypeProgrammed, metav1.ConditionFalse, "Failed", ""), true},
	}
}

// createZoneGateways creates Gateways around the zone's domain, example.test, and returns the ones it covers.
func createZoneGateways(t *testing.T, ctx context.Context, c client.Client) map[types.NamespacedName]bool {
	t.Helper()
	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "elsewhere"}}))
	hostnames := map[types.NamespacedName]struct {
		hostname string
		covered  bool
	}{
		{Namespace: "default", Name: "under"}:    {"app.example.test", true},
		{Namespace: "default", Name: "apex"}:     {"example.test", true},
		{Namespace: "default", Name: "wildcard"}: {"*.example.test", true},
		{Namespace: "default", Name: "other"}:    {"app.other.test", false},
		{Namespace: "default", Name: "suffix"}:   {"appexample.test", false},
		{Namespace: "default", Name: "none"}:     {"", false},
		{Namespace: "elsewhere", Name: "under"}:  {"app.example.test", false},
	}
	covered := map[types.NamespacedName]bool{}
	for key, h := range hostnames {
		require.NoError(t, c.Create(ctx, zoneTestGateway(key, h.hostname)))
		if h.covered {
			covered[key] = true
		}
	}
	return covered
}

func zoneTestGateway(key types.NamespacedName, hostname string) *gatewayv1.Gateway {
	listener := gatewayv1.Listener{Name: "http", Port: 80, Protocol: gatewayv1.HTTPProtocolType}
	if hostname != "" {
		listener.Hostname = ptr.To(gatewayv1.Hostname(hostname))
	}
	return &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "datum-external-global-proxy",
			Listeners:        []gatewayv1.Listener{listener},
		},
	}
}

// reconcileRecorder counts the requests a controller reconciles, per Gateway.
type reconcileRecorder struct {
	mu   sync.Mutex
	seen map[types.NamespacedName]int
}

func (r *reconcileRecorder) Reconcile(_ context.Context, req mcreconcile.Request) (reconcile.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen[req.NamespacedName]++
	return reconcile.Result{}, nil
}

func (r *reconcileRecorder) counts() map[types.NamespacedName]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.seen)
}

// startDNSZoneWatch runs a controller holding only the Gateway controller's DNSZone watch, and returns what it
// reconciles.
func startDNSZoneWatch(t *testing.T, ctx context.Context, cfg *rest.Config, s *runtime.Scheme) *reconcileRecorder {
	t.Helper()
	provider := &staticProvider{clusters: map[string]cluster.Cluster{}}
	mgr, err := mcmanager.New(cfg, provider, ctrl.Options{
		Scheme:     s,
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Controller: ctrlconfig.Controller{SkipNameValidation: ptr.To(true)},
	})
	require.NoError(t, err)
	const clusterName = "project"
	provider.clusters[clusterName] = mgr.GetLocalManager()

	recorder := &reconcileRecorder{seen: map[types.NamespacedName]int{}}
	r := &GatewayReconciler{}
	require.NoError(t, r.watchDNSZones(mcbuilder.ControllerManagedBy(mgr).Named("dnszone-watch")).Complete(recorder))
	require.NoError(t, mgr.Engage(ctx, clusterName, mgr.GetLocalManager()))
	go func() { _ = mgr.Start(ctx) }()
	return recorder
}
