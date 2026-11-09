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

func isInternalDNSConsumer(iface *networkingv1alpha.NetworkInterface) bool {
	ref := iface.Status.AttachmentRef
	return iface.Status.NetworkContextRef != nil && ref != nil &&
		ref.APIGroup == internalDNSAttachmentAPIGroup && ref.Kind == internalDNSAttachmentKind
}

func internalDNSContextRequest(clusterName multicluster.ClusterName, namespace, name string) mcreconcile.Request {
	return mcreconcile.Request{
		ClusterName: clusterName,
		Request:     ctrl.Request{NamespacedName: client.ObjectKey{Namespace: namespace, Name: name}},
	}
}

func enqueueInternalDNSContextForInterface(
	clusterName multicluster.ClusterName,
	_ cluster.Cluster,
) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []mcreconcile.Request {
		iface, ok := obj.(*networkingv1alpha.NetworkInterface)
		if !ok || !isInternalDNSConsumer(iface) {
			return nil
		}
		return []mcreconcile.Request{
			internalDNSContextRequest(clusterName, iface.Namespace, iface.Status.NetworkContextRef.Name),
		}
	})
}

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

func enqueueInternalDNSContextForAttachment(
	clusterName multicluster.ClusterName,
	_ cluster.Cluster,
) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []mcreconcile.Request {
		attachment, ok := obj.(*cloudv1alpha1.VPCAttachment)
		if !ok || attachment.Spec.VPC.Name == "" {
			return nil
		}
		return []mcreconcile.Request{
			internalDNSContextRequest(clusterName, attachment.Namespace, attachment.Spec.VPC.Name),
		}
	})
}

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

func enqueueInternalDNSContextForVPC(
	clusterName multicluster.ClusterName,
	_ cluster.Cluster,
) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []mcreconcile.Request {
		return []mcreconcile.Request{internalDNSContextRequest(clusterName, obj.GetNamespace(), obj.GetName())}
	})
}

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
