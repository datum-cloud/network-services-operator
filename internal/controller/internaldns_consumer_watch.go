// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"reflect"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	cloudv1alpha1 "go.datum.net/galactic/api/cloud/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	internalDNSAttachmentAPIGroup = "galactic.datumapis.com"
	internalDNSAttachmentKind     = "VPCAttachment"
)

// isInternalDNSConsumer reports whether iface is bound to a network context
// through a Galactic VPC attachment, the interfaces that consume internal DNS.
func isInternalDNSConsumer(iface *networkingv1alpha.NetworkInterface) bool {
	ref := iface.Status.AttachmentRef
	return iface.Status.NetworkContextRef != nil && ref != nil &&
		ref.APIGroup == internalDNSAttachmentAPIGroup && ref.Kind == internalDNSAttachmentKind
}

func internalDNSContextRequest(
	clusterName multicluster.ClusterName, iface *networkingv1alpha.NetworkInterface,
) mcreconcile.Request {
	return mcreconcile.Request{
		ClusterName: clusterName,
		Request: ctrl.Request{NamespacedName: client.ObjectKey{
			Namespace: iface.Namespace, Name: iface.Status.NetworkContextRef.Name,
		}},
	}
}

// enqueueInternalDNSContextForInterface maps a consumer interface to its
// network context. An update maps both versions, so a context the interface
// left is reconciled too.
func enqueueInternalDNSContextForInterface(
	clusterName multicluster.ClusterName,
	_ cluster.Cluster,
) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []mcreconcile.Request {
		iface, ok := obj.(*networkingv1alpha.NetworkInterface)
		if !ok || !isInternalDNSConsumer(iface) {
			return nil
		}
		return []mcreconcile.Request{internalDNSContextRequest(clusterName, iface)}
	})
}

// internalDNSInterfaceChanged passes interface events that can change which
// attachments a context labels: an interface created or deleted, a change to
// its context or attachment reference, or the start of its deletion.
func internalDNSInterfaceChanged() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, ok := e.ObjectOld.(*networkingv1alpha.NetworkInterface)
			if !ok {
				return true
			}
			after, ok := e.ObjectNew.(*networkingv1alpha.NetworkInterface)
			if !ok {
				return true
			}
			return !reflect.DeepEqual(before.Status.NetworkContextRef, after.Status.NetworkContextRef) ||
				!reflect.DeepEqual(before.Status.AttachmentRef, after.Status.AttachmentRef) ||
				before.DeletionTimestamp.IsZero() != after.DeletionTimestamp.IsZero()
		},
	}
}

// internalDNSContextsInNamespace returns a request for the network context of
// each consumer interface in namespace that keep accepts, without duplicates.
func internalDNSContextsInNamespace(
	ctx context.Context,
	cl client.Reader,
	clusterName multicluster.ClusterName,
	namespace string,
	keep func(*networkingv1alpha.NetworkInterface) bool,
) []mcreconcile.Request {
	var interfaces networkingv1alpha.NetworkInterfaceList
	if err := cl.List(ctx, &interfaces, client.InNamespace(namespace)); err != nil {
		log.FromContext(ctx).Error(err, "list network interfaces for internal DNS", "namespace", namespace)
		return nil
	}
	seen := map[mcreconcile.Request]struct{}{}
	var requests []mcreconcile.Request
	for i := range interfaces.Items {
		iface := &interfaces.Items[i]
		if !isInternalDNSConsumer(iface) || !keep(iface) {
			continue
		}
		request := internalDNSContextRequest(clusterName, iface)
		if _, duplicate := seen[request]; duplicate {
			continue
		}
		seen[request] = struct{}{}
		requests = append(requests, request)
	}
	return requests
}

// enqueueInternalDNSContextsForAttachment maps an attachment to the context
// of the interface that references it.
func enqueueInternalDNSContextsForAttachment(
	clusterName multicluster.ClusterName,
	cl cluster.Cluster,
) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
		return internalDNSContextsInNamespace(ctx, cl.GetClient(), clusterName, obj.GetNamespace(),
			func(iface *networkingv1alpha.NetworkInterface) bool {
				return iface.Status.AttachmentRef.Name == obj.GetName()
			})
	})
}

// internalDNSAttachmentChanged passes attachment events that can change its
// label: an attachment created or deleted, its consumer label changed, or a
// move to another VPC. Status writes change neither.
func internalDNSAttachmentChanged() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, ok := e.ObjectOld.(*cloudv1alpha1.VPCAttachment)
			if !ok {
				return true
			}
			after, ok := e.ObjectNew.(*cloudv1alpha1.VPCAttachment)
			if !ok {
				return true
			}
			return before.Labels[internalDNSConsumerVPCLabel] != after.Labels[internalDNSConsumerVPCLabel] ||
				before.Spec.VPC.Name != after.Spec.VPC.Name
		},
	}
}

// enqueueInternalDNSContextsForVPC maps a VPC to the contexts of the consumer
// interfaces in its namespace, since a context waits for its VPC's identity
// before it labels anything.
func enqueueInternalDNSContextsForVPC(
	clusterName multicluster.ClusterName,
	cl cluster.Cluster,
) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
		return internalDNSContextsInNamespace(ctx, cl.GetClient(), clusterName, obj.GetNamespace(),
			func(*networkingv1alpha.NetworkInterface) bool { return true })
	})
}

// internalDNSVPCIdentityChanged passes VPC events that can change a context's
// consumer VPC: a VPC created or deleted, or its network identity changed.
func internalDNSVPCIdentityChanged() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, ok := e.ObjectOld.(*cloudv1alpha1.VPC)
			if !ok {
				return true
			}
			after, ok := e.ObjectNew.(*cloudv1alpha1.VPC)
			if !ok {
				return true
			}
			return before.Status.VPC != after.Status.VPC
		},
	}
}
