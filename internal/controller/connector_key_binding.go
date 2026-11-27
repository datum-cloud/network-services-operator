// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"go.miloapis.com/instrumentation/metrics"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/connectorbinding"
)

const metricLabelVerdict = "verdict"

var connectorKeyBindingConnectors = metrics.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "nso_connector_key_binding_connectors",
		Help: "Connectors replicated downstream, by whether their status is bound to their endpoint key (verified, missing, invalid).",
	},
	[]string{metricLabelVerdict},
)

var connectorKeyBindings = newConnectorKeyBindingTracker(connectorKeyBindingConnectors)

type connectorKeyBindingTracker struct {
	mu       sync.Mutex
	gauge    *prometheus.GaugeVec
	engaged  map[multicluster.ClusterName]cluster.Cluster
	verdicts map[multicluster.ClusterName]map[types.NamespacedName]connectorbinding.Verdict
}

func newConnectorKeyBindingTracker(gauge *prometheus.GaugeVec) *connectorKeyBindingTracker {
	for _, v := range connectorbinding.Verdicts {
		gauge.WithLabelValues(string(v)).Set(0)
	}
	return &connectorKeyBindingTracker{
		gauge:    gauge,
		engaged:  map[multicluster.ClusterName]cluster.Cluster{},
		verdicts: map[multicluster.ClusterName]map[types.NamespacedName]connectorbinding.Verdict{},
	}
}

func (t *connectorKeyBindingTracker) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (t *connectorKeyBindingTracker) Engage(ctx context.Context, name multicluster.ClusterName, cl cluster.Cluster) error {
	t.mu.Lock()
	t.engaged[name] = cl
	t.mu.Unlock()

	go func() {
		<-ctx.Done()
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.engaged[name] != cl {
			return
		}
		delete(t.engaged, name)
		t.forgetClusterLocked(name)
	}()
	return nil
}

func (t *connectorKeyBindingTracker) record(clusterName multicluster.ClusterName, key types.NamespacedName, verdict connectorbinding.Verdict) (previous connectorbinding.Verdict, changed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	connectors := t.verdicts[clusterName]
	if connectors == nil {
		connectors = map[types.NamespacedName]connectorbinding.Verdict{}
		t.verdicts[clusterName] = connectors
	}
	previous, known := connectors[key]
	if known && previous == verdict {
		return previous, false
	}
	if known {
		t.gauge.WithLabelValues(string(previous)).Dec()
	}
	connectors[key] = verdict
	t.gauge.WithLabelValues(string(verdict)).Inc()
	return previous, true
}

func (t *connectorKeyBindingTracker) forget(clusterName multicluster.ClusterName, key types.NamespacedName) {
	t.mu.Lock()
	defer t.mu.Unlock()
	previous, known := t.verdicts[clusterName][key]
	if !known {
		return
	}
	delete(t.verdicts[clusterName], key)
	t.gauge.WithLabelValues(string(previous)).Dec()
}

func (t *connectorKeyBindingTracker) forgetClusterLocked(clusterName multicluster.ClusterName) {
	for _, verdict := range t.verdicts[clusterName] {
		t.gauge.WithLabelValues(string(verdict)).Dec()
	}
	delete(t.verdicts, clusterName)
}

func upstreamConnectorKeyBinding(upstreamObj *unstructured.Unstructured) connectorbinding.Verdict {
	raw, ok := upstreamObj.Object[jsonKeyStatus].(map[string]any)
	if !ok {
		return connectorbinding.Missing
	}
	var status networkingv1alpha1.ConnectorStatus
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &status); err != nil {
		return connectorbinding.Invalid
	}
	return connectorbinding.VerifyStatus(string(upstreamObj.GetUID()), &status)
}

func setConnectorKeyBindingAnnotation(downstreamObj *unstructured.Unstructured, verdict connectorbinding.Verdict) {
	annotations := downstreamObj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}
	annotations[networkingv1alpha1.ConnectorKeyBindingAnnotation] = string(verdict)
	downstreamObj.SetAnnotations(annotations)
}

func recordConnectorKeyBinding(ctx context.Context, clusterName multicluster.ClusterName, upstreamObj *unstructured.Unstructured, verdict connectorbinding.Verdict) {
	key := types.NamespacedName{Namespace: upstreamObj.GetNamespace(), Name: upstreamObj.GetName()}
	if _, changed := connectorKeyBindings.record(clusterName, key, verdict); changed && verdict == connectorbinding.Invalid {
		log.FromContext(ctx).Info("connector status carries an endpoint key binding that does not verify")
	}
}
