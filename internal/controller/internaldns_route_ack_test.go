// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkv1alpha1 "go.datum.net/galactic/api/network/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestRoutePolicyAcknowledgmentsDiffer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.November, 6, 17, 0, 0, 0, time.UTC)
	base := &networkv1alpha1.ServiceRoutePolicy{
		ObjectMeta: metav1.ObjectMeta{Generation: 1},
		Status: networkv1alpha1.ServiceRoutePolicyStatus{
			ObservedGeneration: 1,
			Conditions: []metav1.Condition{{
				Type: networkv1alpha1.ConditionTypeServiceRouteAccepted, Status: metav1.ConditionTrue,
				Reason: "Accepted", LastTransitionTime: metav1.NewTime(now),
			}},
			Nodes: []networkv1alpha1.ServiceRouteNodeStatus{{
				NodeName: "node-a", PolicyUID: "policy-uid", ObservedGeneration: 1,
				InputDigest: "digest-a", Ready: true, ValidUntil: metav1.NewTime(now.Add(time.Minute)),
			}},
		},
	}

	renewed := base.DeepCopy()
	renewed.Status.Nodes[0].ValidUntil = metav1.NewTime(now.Add(2 * time.Minute))
	if routePolicyAcknowledgmentsDiffer(base, renewed, now) {
		t.Error("an acknowledgment renewal passed the filter")
	}

	reasonOnly := base.DeepCopy()
	reasonOnly.Status.Conditions[0].Reason = "StillAccepted"
	if routePolicyAcknowledgmentsDiffer(base, reasonOnly, now) {
		t.Error("an Accepted reason change that keeps it true passed the filter")
	}

	lapsed := base.DeepCopy()
	lapsed.Status.Nodes[0].ValidUntil = metav1.NewTime(now.Add(-time.Second))
	if !routePolicyAcknowledgmentsDiffer(lapsed, base, now) {
		t.Error("a lapsed acknowledgment renewed without other changes was filtered out")
	}

	for name, change := range map[string]func(*networkv1alpha1.ServiceRoutePolicy){
		"new digest":       func(p *networkv1alpha1.ServiceRoutePolicy) { p.Status.Nodes[0].InputDigest = "digest-b" },
		"not ready":        func(p *networkv1alpha1.ServiceRoutePolicy) { p.Status.Nodes[0].Ready = false },
		"new generation":   func(p *networkv1alpha1.ServiceRoutePolicy) { p.Generation = 2 },
		"observed gen":     func(p *networkv1alpha1.ServiceRoutePolicy) { p.Status.Nodes[0].ObservedGeneration = 2 },
		"other policy UID": func(p *networkv1alpha1.ServiceRoutePolicy) { p.Status.Nodes[0].PolicyUID = "other" },
		"not accepted":     func(p *networkv1alpha1.ServiceRoutePolicy) { p.Status.Conditions[0].Status = metav1.ConditionFalse },
		"node added": func(p *networkv1alpha1.ServiceRoutePolicy) {
			p.Status.Nodes = append(p.Status.Nodes, networkv1alpha1.ServiceRouteNodeStatus{NodeName: "node-b"})
		},
		"node replaced": func(p *networkv1alpha1.ServiceRoutePolicy) { p.Status.Nodes[0].NodeName = "node-b" },
	} {
		changed := base.DeepCopy()
		change(changed)
		if !routePolicyAcknowledgmentsDiffer(base, changed, now) {
			t.Errorf("%s was filtered out", name)
		}
	}
}

func TestEnqueueInternalDNSContextForRoutePolicy(t *testing.T) {
	t.Parallel()
	isController := true
	owned := &networkv1alpha1.ServiceRoutePolicy{ObjectMeta: metav1.ObjectMeta{
		Namespace: "tenant", Name: "context-dns-udp",
		Labels: map[string]string{internalDNSRouteContextLabel: "context-uid"},
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: networkingv1alpha.GroupVersion.String(), Kind: "NetworkContext",
			Name: "context", UID: "context-uid", Controller: &isController,
		}},
	}}
	unmanaged := owned.DeepCopy()
	unmanaged.Labels = nil
	otherOwner := owned.DeepCopy()
	otherOwner.OwnerReferences[0].Kind = "Something"
	noController := owned.DeepCopy()
	noController.OwnerReferences[0].Controller = nil

	handler := enqueueInternalDNSContextForRoutePolicy(multicluster.ClusterName("cell"), nil)
	for name, tc := range map[string]struct {
		policy *networkv1alpha1.ServiceRoutePolicy
		want   []types.NamespacedName
	}{
		"owned":         {owned, []types.NamespacedName{{Namespace: "tenant", Name: "context"}}},
		"unmanaged":     {unmanaged, nil},
		"other owner":   {otherOwner, nil},
		"no controller": {noController, nil},
	} {
		queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[mcreconcile.Request]())
		handler.Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: tc.policy}, queue)
		var got []types.NamespacedName
		for queue.Len() > 0 {
			item, _ := queue.Get()
			if item.ClusterName != "cell" {
				t.Errorf("%s: cluster %q", name, item.ClusterName)
			}
			got = append(got, item.NamespacedName)
			queue.Done(item)
		}
		if len(got) != len(tc.want) || (len(got) == 1 && got[0] != tc.want[0]) {
			t.Errorf("%s: enqueued %v, want %v", name, got, tc.want)
		}
	}
}
