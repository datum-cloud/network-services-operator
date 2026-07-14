package parity

import (
	"fmt"

	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// ConfigDump is the parsed view of the proxy's live configuration, with each
// resource decoded into its concrete type.
type ConfigDump struct {
	Clusters  []*clusterv3.Cluster
	Routes    []*routev3.RouteConfiguration
	Listeners []*listenerv3.Listener
	// SecretNames are the TLS certificates present, used to confirm that
	// certificates we expected to be removed are actually gone.
	SecretNames []string
	// ErrorStates holds, per kind of configuration, the reasons the proxy gave
	// for rejecting an individual resource.
	ErrorStates map[string][]string
}

// ParseConfigDump decodes the proxy's live configuration into typed resources.
// We decode against the real schema rather than poking at raw JSON so that a
// shape we don't recognize is a hard error, not a silent miss.
func ParseConfigDump(raw []byte) (*ConfigDump, error) {
	var dump adminv3.ConfigDump
	// Tolerate fields newer than this binary knows about, so a proxy running a
	// newer version doesn't fail the parse.
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &dump); err != nil {
		return nil, fmt.Errorf("unmarshal config_dump envelope: %w", err)
	}

	out := &ConfigDump{ErrorStates: map[string][]string{}}

	for i, cfgAny := range dump.GetConfigs() {
		msg, err := cfgAny.UnmarshalNew()
		if err != nil {
			return nil, fmt.Errorf("unmarshal config_dump section %d (%s): %w", i, cfgAny.GetTypeUrl(), err)
		}
		switch section := msg.(type) {
		case *adminv3.ClustersConfigDump:
			if err := out.absorbClusters(section); err != nil {
				return nil, err
			}
		case *adminv3.RoutesConfigDump:
			if err := out.absorbRoutes(section); err != nil {
				return nil, err
			}
		case *adminv3.ListenersConfigDump:
			if err := out.absorbListeners(section); err != nil {
				return nil, err
			}
		case *adminv3.SecretsConfigDump:
			out.absorbSecrets(section)
		default:
			// Other sections aren't needed by the scanners.
		}
	}
	return out, nil
}

func (d *ConfigDump) absorbClusters(s *adminv3.ClustersConfigDump) error {
	for _, dc := range s.GetDynamicActiveClusters() {
		if es := dc.GetErrorState(); es != nil {
			d.ErrorStates["cluster"] = append(d.ErrorStates["cluster"], es.GetDetails())
		}
		cl := &clusterv3.Cluster{}
		if err := unwrap(dc.GetCluster(), cl); err != nil {
			return fmt.Errorf("unwrap dynamic cluster: %w", err)
		}
		if cl.GetName() != "" {
			d.Clusters = append(d.Clusters, cl)
		}
	}
	// Clusters still being applied may carry rejection details too.
	for _, dc := range s.GetDynamicWarmingClusters() {
		if es := dc.GetErrorState(); es != nil {
			d.ErrorStates["cluster"] = append(d.ErrorStates["cluster"], es.GetDetails())
		}
	}
	return nil
}

func (d *ConfigDump) absorbRoutes(s *adminv3.RoutesConfigDump) error {
	for _, dr := range s.GetDynamicRouteConfigs() {
		if es := dr.GetErrorState(); es != nil {
			d.ErrorStates["route"] = append(d.ErrorStates["route"], es.GetDetails())
		}
		rc := &routev3.RouteConfiguration{}
		if err := unwrap(dr.GetRouteConfig(), rc); err != nil {
			return fmt.Errorf("unwrap dynamic route config: %w", err)
		}
		if rc.GetName() != "" {
			d.Routes = append(d.Routes, rc)
		}
	}
	return nil
}

func (d *ConfigDump) absorbListeners(s *adminv3.ListenersConfigDump) error {
	for _, dl := range s.GetDynamicListeners() {
		if es := dl.GetErrorState(); es != nil {
			d.ErrorStates["listener"] = append(d.ErrorStates["listener"], es.GetDetails())
		}
		// Scan the listener the proxy is currently serving, not one still being
		// applied.
		active := dl.GetActiveState()
		if active == nil {
			continue
		}
		l := &listenerv3.Listener{}
		if err := unwrap(active.GetListener(), l); err != nil {
			return fmt.Errorf("unwrap dynamic listener: %w", err)
		}
		if l.GetName() != "" {
			d.Listeners = append(d.Listeners, l)
		}
	}
	return nil
}

func (d *ConfigDump) absorbSecrets(s *adminv3.SecretsConfigDump) {
	for _, ds := range s.GetDynamicActiveSecrets() {
		if es := ds.GetErrorState(); es != nil {
			d.ErrorStates["secret"] = append(d.ErrorStates["secret"], es.GetDetails())
		}
		if n := ds.GetName(); n != "" {
			d.SecretNames = append(d.SecretNames, n)
		}
	}
}

// unwrap decodes a wrapped resource into dst. A nil input is not an error (the
// resource is simply absent); dst is left zero.
func unwrap(a *anypb.Any, dst proto.Message) error {
	if a == nil {
		return nil
	}
	return a.UnmarshalTo(dst)
}
