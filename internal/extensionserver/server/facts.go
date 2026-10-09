package server

import (
	"context"
	"fmt"

	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// WatchReportFacts makes the Programmed report recheck its removals when a fact
// they depend on changes, without waiting for a build: a policy's spec, an
// HTTPProxy's rules, and whether a Gateway or HTTPRoute exists. An HTTPProxy
// edit, for one, builds nothing in Envoy Gateway, so a renamed rule would
// otherwise keep its claim until an unrelated build.
func (s *Server) WatchReportFacts(ctx context.Context, informers cache.Informers) error {
	onSpecChange := toolscache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj any) {
			if specChanged(oldObj, newObj) {
				s.reporter.recheckFacts()
			}
		},
		DeleteFunc: func(any) { s.reporter.recheckFacts() },
	}
	onDelete := toolscache.ResourceEventHandlerFuncs{
		DeleteFunc: func(any) { s.reporter.recheckFacts() },
	}
	watches := []struct {
		obj     client.Object
		handler toolscache.ResourceEventHandler
	}{
		{&networkingv1alpha.TrafficProtectionPolicy{}, onSpecChange},
		{&networkingv1alpha.HTTPProxy{}, onSpecChange},
		{extcache.GatewayMetadata(), onDelete},
		{extcache.HTTPRouteMetadata(), onDelete},
	}
	for _, w := range watches {
		informer, err := informers.GetInformer(ctx, w.obj)
		if err != nil {
			return fmt.Errorf("informer for %T: %w", w.obj, err)
		}
		if _, err := informer.AddEventHandler(w.handler); err != nil {
			return fmt.Errorf("watch %T: %w", w.obj, err)
		}
	}
	return nil
}

// specChanged reports whether an update changed the object's generation, which
// moves only with its spec; the report's own status writes do not count.
func specChanged(oldObj, newObj any) bool {
	o, ok1 := oldObj.(client.Object)
	n, ok2 := newObj.(client.Object)
	return ok1 && ok2 && o.GetGeneration() != n.GetGeneration()
}
