package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
)

// programmedSetEndpointPath is a read-only debug endpoint that reports the exact
// set of changes the last build intended to make, so a test can confirm the
// proxy is running that set and not merely the right number of things.
const programmedSetEndpointPath = "/debug/programmed-set"

// Marker strings, kept in sync with where the configuration is written, used to
// recognize each change when reading the configuration back.
const (
	datumGatewayMetaKey        = "datum-gateway"
	connectorInternalTransport = "envoy.transport_sockets.internal_upstream"
	hcmNetworkFilterName       = "envoy.filters.network.http_connection_manager"
)

// Family names a kind of change recorded in a snapshot. The strings are stable
// JSON keys the parity test depends on; do not rename.
type Family string

const (
	FamilyWAFRoute         Family = "waf_route"
	FamilyWAFHCM           Family = "waf_hcm"
	FamilyLocalReply       Family = "local_reply"
	FamilyConnectorCluster Family = "connector_cluster"
	FamilyConnectorRoute   Family = "connector_route"
	FamilyConnectorOffline Family = "connector_offline"
	FamilyTLSPrune         Family = "tls_prune"
)

// ProgrammedSet is a snapshot of one build's intended changes. It is served as
// JSON and read back by the parity test.
type ProgrammedSet struct {
	// BuildID increments once per build so a caller can tell two successive
	// snapshots apart and know a fresh build has happened.
	BuildID uint64 `json:"buildID"`
	// CapturedAt is when the snapshot was recorded.
	CapturedAt time.Time `json:"capturedAt"`
	// Keys is, per kind of change, the identity of each thing changed. Each value
	// is sorted and de-duplicated. The identity format is described on the
	// record helpers below and mirrored by the parity test.
	Keys map[Family][]string `json:"keys"`
	// Counts holds the size of each set in Keys, plus the removed-certificate
	// outcomes below that have no per-item identity. Used as a secondary check.
	Counts map[Family]int `json:"counts"`
	// Removing invalid TLS certificates is confirmed by counting outcomes rather
	// than by identity, so the raw numbers are carried here.
	TLSPrunedChains        int `json:"tlsPrunedChains"`
	TLSPrunedSecrets       int `json:"tlsPrunedSecrets"`
	TLSListenersLeftIntact int `json:"tlsListenersLeftIntact"`
}

// programmedRecorder holds the most recent snapshot under a lock, shared between
// the build that writes it and the endpoint that reads it. Only the last build
// is kept; each recording replaces the previous one.
type programmedRecorder struct {
	mu      sync.RWMutex
	buildID uint64
	last    *ProgrammedSet
}

// newProgrammedRecorder returns an empty recorder. Before the first build it
// reports a valid empty snapshot with a zero build count, so callers can tell
// "no build yet" from a real build.
func newProgrammedRecorder() *programmedRecorder {
	return &programmedRecorder{}
}

// snapshot returns the last recorded set, or an empty one if no build has run.
// The returned value is a copy safe to serialize without the lock.
func (r *programmedRecorder) snapshot() ProgrammedSet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.last == nil {
		return ProgrammedSet{Keys: map[Family][]string{}, Counts: map[Family]int{}}
	}
	return *r.last
}

// record reads the configuration the build just produced and stores a snapshot
// of it. It only reads, never changes the configuration, and holds the write
// lock just long enough to store the result so it doesn't slow the build.
func (r *programmedRecorder) record(
	listeners []*listenerv3.Listener,
	routes []*routev3.RouteConfiguration,
	clusters []*clusterv3.Cluster,
	corazaFilterName string,
	tlsPrunedChains, tlsPrunedSecrets, tlsListenersLeftIntact int,
) {
	ps := buildProgrammedSet(listeners, routes, clusters, corazaFilterName,
		tlsPrunedChains, tlsPrunedSecrets, tlsListenersLeftIntact)

	r.mu.Lock()
	r.buildID++
	ps.BuildID = r.buildID
	r.last = &ps
	r.mu.Unlock()
}

// buildProgrammedSet walks the produced configuration and extracts an identity
// for each change. It is a pure function so it can be unit tested directly. The
// identity formats must match what the parity test looks for; keep both sides
// in lockstep.
func buildProgrammedSet(
	listeners []*listenerv3.Listener,
	routes []*routev3.RouteConfiguration,
	clusters []*clusterv3.Cluster,
	corazaFilterName string,
	tlsPrunedChains, tlsPrunedSecrets, tlsListenersLeftIntact int,
) ProgrammedSet {
	keys := map[Family][]string{}
	add := func(f Family, k string) { keys[f] = append(keys[f], k) }

	for _, rc := range routes {
		rcName := rc.GetName()
		for _, vh := range rc.GetVirtualHosts() {
			vhName := vh.GetName()
			for _, rt := range vh.GetRoutes() {
				// The identity includes the protection policy governing the route,
				// so a route protected by the wrong policy shows up as a mismatch.
				if ns, name, mode, ok := datumGatewayTPP(rt); ok {
					add(FamilyWAFRoute, wafRouteKey(rcName, vhName, rt.GetName(), ns, name, mode))
				}
				if isConnectRoute(rt) {
					add(FamilyConnectorRoute, connectorRouteKey(rcName, vhName, rt.GetName()))
				}
				if isOfflineDirectResponse(rt) {
					add(FamilyConnectorOffline, connectorRouteKey(rcName, vhName, rt.GetName()))
				}
			}
		}
	}

	for _, cl := range clusters {
		if isReplacedConnectorCluster(cl) {
			add(FamilyConnectorCluster, cl.GetName())
		}
	}

	for _, l := range listeners {
		lName := l.GetName()
		eachHCM(l, func(fcName string, hcm *hcmv3.HttpConnectionManager) {
			if corazaFilterName != "" && hcmHasFilterAtZero(hcm, corazaFilterName) {
				add(FamilyWAFHCM, listenerChainKey(lName, fcName))
			}
			if hcm.GetLocalReplyConfig() != nil {
				add(FamilyLocalReply, listenerChainKey(lName, fcName))
			}
		})
	}

	// Sort and de-duplicate so the output can be compared as a set.
	counts := map[Family]int{}
	for f := range keys {
		keys[f] = sortDedup(keys[f])
		counts[f] = len(keys[f])
	}
	// Removed certificates are recorded by count, since they have no identity.
	counts[FamilyTLSPrune] = tlsPrunedChains

	return ProgrammedSet{
		CapturedAt:             time.Now().UTC(),
		Keys:                   keys,
		Counts:                 counts,
		TLSPrunedChains:        tlsPrunedChains,
		TLSPrunedSecrets:       tlsPrunedSecrets,
		TLSListenersLeftIntact: tlsListenersLeftIntact,
	}
}

// programmedSetHandler serves the latest snapshot as JSON. It is a read-only,
// test-only debug endpoint and returns an empty snapshot before the first build.
func (r *programmedRecorder) programmedSetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		ps := r.snapshot()
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(ps)
	}
}
