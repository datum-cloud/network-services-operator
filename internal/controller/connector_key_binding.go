// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"go.miloapis.com/instrumentation/metrics"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"

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
	verdicts map[string]connectorbinding.Verdict
}

func newConnectorKeyBindingTracker(gauge *prometheus.GaugeVec) *connectorKeyBindingTracker {
	for _, v := range connectorbinding.Verdicts {
		gauge.WithLabelValues(string(v)).Set(0)
	}
	return &connectorKeyBindingTracker{gauge: gauge, verdicts: map[string]connectorbinding.Verdict{}}
}

func (t *connectorKeyBindingTracker) record(key string, verdict connectorbinding.Verdict) (previous connectorbinding.Verdict, changed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	previous, known := t.verdicts[key]
	if known && previous == verdict {
		return previous, false
	}
	if known {
		t.gauge.WithLabelValues(string(previous)).Dec()
	}
	t.verdicts[key] = verdict
	t.gauge.WithLabelValues(string(verdict)).Inc()
	return previous, true
}

func (t *connectorKeyBindingTracker) forget(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	previous, known := t.verdicts[key]
	if !known {
		return
	}
	delete(t.verdicts, key)
	t.gauge.WithLabelValues(string(previous)).Dec()
}

func connectorKeyBindingKey(cluster, namespace, name string) string {
	return cluster + "/" + namespace + "/" + name
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

func stampConnectorKeyBinding(ctx context.Context, downstreamObj, upstreamObj *unstructured.Unstructured) {
	verdict := upstreamConnectorKeyBinding(upstreamObj)

	annotations := downstreamObj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}
	annotations[networkingv1alpha1.ConnectorKeyBindingAnnotation] = string(verdict)
	downstreamObj.SetAnnotations(annotations)

	cluster, _ := mccontext.ClusterFrom(ctx)
	key := connectorKeyBindingKey(string(cluster), upstreamObj.GetNamespace(), upstreamObj.GetName())
	if _, changed := connectorKeyBindings.record(key, verdict); changed && verdict == connectorbinding.Invalid {
		log.FromContext(ctx).Info("connector status carries an endpoint key binding that does not verify")
	}
}

func forgetConnectorKeyBinding(cluster, namespace, name string) {
	connectorKeyBindings.forget(connectorKeyBindingKey(cluster, namespace, name))
}
