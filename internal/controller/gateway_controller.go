// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	mcbuilder "github.com/multicluster-runtime/multicluster-runtime/pkg/builder"
	mchandler "github.com/multicluster-runtime/multicluster-runtime/pkg/handler"
	mcmanager "github.com/multicluster-runtime/multicluster-runtime/pkg/manager"
	mcreconcile "github.com/multicluster-runtime/multicluster-runtime/pkg/reconcile"
	"go.datum.net/network-services-operator/internal/validation"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GatewayReconciler reconciles a Gateway object
type GatewayReconciler struct {
	mgr mcmanager.Manager

	ValidationOpts validation.GatewayValidationOptions
}

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/finalizers,verbs=update
// +kubebuilder:rbac:groups=externaldns.k8s.io,resources=dnsendpoints,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=externaldns.k8s.io,resources=dnsendpoints/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=externaldns.k8s.io,resources=dnsendpoints/finalizers,verbs=update

func (r *GatewayReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)
	ctx = log.IntoContext(ctx, logger)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	// https://github.com/envoyproxy/gateway/blob/292894057fd4083b1c4dce691d510c1a6bc53073/internal/gatewayapi/validate.go#L558

	var gateway gatewayv1.Gateway
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &gateway); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// TODO(jreese) The GatewayClassName check is currently necessary because of
	// how we enqueue requests for HTTPRoutes. In the handler, it simply places
	// a request in the queue for every Gateway parentRef in the HTTPRoute, without
	// checking if the parent gateway's gatewayclass.
	if !gateway.DeletionTimestamp.IsZero() || gateway.Spec.GatewayClassName != "datum-external-global-proxy" {
		// TODO(jreese) Finalizer
		return ctrl.Result{}, nil
	}

	logger.Info("reconciling gateway")
	defer logger.Info("reconcile complete")

	downstreamStrategy := getDownstreamResourceStrategy(cl)

	result, downstreamGateway := r.ensureDownstreamGateway(ctx, cl.GetClient(), &gateway, downstreamStrategy)
	if result.ShouldReturn() {
		return result.Complete(ctx)
	}

	// Do we want a GatewayAttachment to exist so that we can make a single
	// scheduling decision here? - Nope, depend on the gateway impl supporting
	// shared infra.
	//
	// See Envoy Gateway: https://gateway.envoyproxy.io/docs/tasks/operations/deployment-mode/#merged-gateways-onto-a-single-envoyproxy-fleet

	// Hostnames - Think about a type that can reference a listener to add
	// more hostnames to it.

	_ = downstreamGateway

	// https://github.com/kubernetes-sigs/gateway-api/issues/1485

	// 1. Identify downstream gateway to attach to based on upstream gateway
	//	- Put settings into config file to help with this.
	//	- Support hostnames on listeners defined for datum gateways, propagate
	//		those to the underlying HTTPRoutes.
	//	- Generate a hostname based on the uid of the gateway, insert into attached
	//		HTTPRoutes
	// - Default listeners for :80 and :443, named http and https
	// - May have multiple listeners in the downstream gateway, attached to different
	//	IP blocks, which we should schedule upstream gateways across based on
	//	strategy defined in https://github.com/datum-cloud/enhancements/issues/15

	// The gateway spec says that tls settings must be defined on the listeners
	// with an HTTPS protocol, but I'm going to make that optional for our end
	// users since we can have certs issued by LE. Maybe to stick with
	// conformance, we can inject default tls settings and have the certificateRef
	// point at some kind of CertificateManager type that indicates it'll be
	// issued from LE.

	// See GatewayConditionType and GatewayConditionReason
	// https://github.com/envoyproxy/gateway/blob/292894057fd4083b1c4dce691d510c1a6bc53073/internal/provider/kubernetes/status.go#L552

	// Gateway Status
	//	- addresses
	//		- type: Hostname
	//		  value: <uid>.prism.global.datum-dns.net
	//		- type: Hostname
	//		  value: v4.<uid>.prism.global.datum-dns.net
	//		- type: Hostname
	//		  value: v6.<uid>.prism.global.datum-dns.net
	//	- conditions:
	//		- type: Accepted
	//		- type: Programmed

	// Listener Status:
	//	- attachedRoutes: N
	//		conditions:
	//			- type: Programmed
	//			- type: Accepted
	//			- type: ResolvedRefs
	//		name:
	//		supportedKinds:
	//			- group: gateway.networking.k8s.io
	//			  kind: HTTPRoute

	// HTTPRoute can define a hostname - how do we validate it's theirs, and not
	// in use? An "Accounting" control plane client may make sense. k8s resources
	// can be created with dots in them, so a fqdn in the name of a configmap
	// would technically work (though would likely reach scaling issues).
	//
	// TLS Certs - Will need to leverage EnvoyPatchPolicy resources to insert
	// certificates into the Envoy config, as single Gateway Listener's TLS
	// configuration only allows up to 64 references to certs.

	return result.Complete(ctx)
}

func (r *GatewayReconciler) ensureDownstreamGateway(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	downstreamStrategy DownstreamResourceStrategy,
) (result Result, downstreamGateway *gatewayv1.Gateway) {
	logger := log.FromContext(ctx)

	// This validation will be redundant once the webhook is in place
	if errs := validation.ValidateGateway(upstreamGateway, r.ValidationOpts); len(errs) != 0 {
		result.Err = errs.ToAggregate()
		return result, nil
	}

	// Get the upstream gateway class so that we can pull the controller name out
	// of it and use it in route status updates.
	var upstreamGatewayClass gatewayv1.GatewayClass
	if err := upstreamClient.Get(ctx, types.NamespacedName{Name: string(upstreamGateway.Spec.GatewayClassName)}, &upstreamGatewayClass); err != nil {
		result.Err = err
		return result, nil
	}
	upstreamGatewayClassControllerName := string(upstreamGatewayClass.Spec.ControllerName)

	// TODO(jreese) handle hostnames defined on upstream listeners
	hostnames := []string{
		fmt.Sprintf("%s.prism.global.datum-dns.net", upstreamGateway.UID),
		fmt.Sprintf("v4.%s.prism.global.datum-dns.net", upstreamGateway.UID),
		fmt.Sprintf("v6.%s.prism.global.datum-dns.net", upstreamGateway.UID),
	}

	downstreamClient := downstreamStrategy.GetClient()
	downstreamGateway = &gatewayv1.Gateway{
		ObjectMeta: downstreamStrategy.GetDownstreamObjectMeta(upstreamGateway),
	}

	gatewayResult, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, downstreamGateway, func() error {
		if err := controllerutil.SetControllerReference(upstreamGateway, downstreamGateway, downstreamClient.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference on downstream gateway: %w", err)
		}

		// TODO(jreese) validate tls certificateRefs
		// TODO(jreese) we could use TLS options instead of certificateRefs
		downstreamGateway.Annotations = map[string]string{}
		for _, l := range upstreamGateway.Spec.Listeners {
			if l.TLS != nil && len(l.TLS.CertificateRefs) == 1 && l.TLS.CertificateRefs[0].Kind != nil {
				switch *l.TLS.CertificateRefs[0].Kind {
				case "ClusterIssuer":
					downstreamGateway.Annotations["cert-manager.io/cluster-issuer"] = string(l.TLS.CertificateRefs[0].Name)
				case "Issuer":
					downstreamGateway.Annotations["cert-manager.io/issuer"] = string(l.TLS.CertificateRefs[0].Name)
				}
			}
		}

		downstreamGateway.Spec = gatewayv1.GatewaySpec{
			// TODO(jreese) get from "scheduler"
			GatewayClassName: "envoy-gateway",

			// TODO(jreese) get from "scheduler"
			Addresses: []gatewayv1.GatewayAddress{},

			Listeners: []gatewayv1.Listener{},

			// Ignored fields - placed here to be clear about intent
			//
			// Infrastructure: &gatewayv1.GatewayInfrastructure{},
			// BackendTLS: &gatewayv1.GatewayBackendTLS{},
		}

		listenerFactory := func(name, hostname string, protocol gatewayv1.ProtocolType, port gatewayv1.PortNumber) gatewayv1.Listener {
			h := gatewayv1.Hostname(hostname)

			fromSelector := gatewayv1.NamespacesFromSame
			listener := gatewayv1.Listener{
				Protocol: protocol,
				Port:     port,
				Name:     gatewayv1.SectionName(name),
				Hostname: &h,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Namespaces: &gatewayv1.RouteNamespaces{
						From: &fromSelector,
					},
				},
			}

			if protocol == gatewayv1.HTTPSProtocolType {
				tlsMode := gatewayv1.TLSModeTerminate
				listener.TLS = &gatewayv1.GatewayTLSConfig{
					Mode: &tlsMode,
					CertificateRefs: []gatewayv1.SecretObjectReference{
						{
							Name: gatewayv1.ObjectName(downstreamGateway.Name),
						},
					},
				}
			}

			return listener
		}

		for i, hostname := range hostnames {
			downstreamGateway.Spec.Listeners = append(downstreamGateway.Spec.Listeners,
				listenerFactory(fmt.Sprintf("http-%d", i), hostname, gatewayv1.HTTPProtocolType, gatewayv1.PortNumber(80)),
				listenerFactory(fmt.Sprintf("https-%d", i), hostname, gatewayv1.HTTPSProtocolType, gatewayv1.PortNumber(443)),
			)
		}

		return nil
	})
	if err != nil {
		if apierrors.IsConflict(err) {
			result.Requeue = true
			return result, nil
		}
		result.Err = err
		return result, nil
	}

	logger.Info("downstream gateway processed", "operation_result", gatewayResult)

	// Extract IP addresses from the downstream gateway's status
	// Using the `any`` type due to deep copy logic requirements in the unstructured
	// lib used to set DNSEndpoint values.
	var v4IPs, v6IPs []any
	for _, addr := range downstreamGateway.Status.Addresses {
		if addr.Type == nil {
			continue
		}
		switch *addr.Type {
		case gatewayv1.IPAddressType:
			// Check if it's an IPv4 or IPv6 address
			if strings.Contains(addr.Value, ":") {
				v6IPs = append(v6IPs, addr.Value)
			} else {
				v4IPs = append(v4IPs, addr.Value)
			}
		}
	}

	// Return early if no IP addresses were found
	if len(v4IPs) == 0 || len(v6IPs) == 0 {
		logger.Info("IP addresses not yet available on downstream gateway", "ipv4", v4IPs, "ipv6", v6IPs)
		return result, nil
	}

	addresses := make([]gatewayv1.GatewayStatusAddress, 0, len(hostnames))
	addressType := gatewayv1.HostnameAddressType

	endpoints := []any{}
	var gatewayDNSEndpoint unstructured.Unstructured
	gatewayDNSEndpoint.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "externaldns.k8s.io",
		Version: "v1alpha1",
		Kind:    "DNSEndpoint",
	})
	gatewayDNSEndpoint.SetNamespace(downstreamGateway.Namespace)
	gatewayDNSEndpoint.SetName(downstreamGateway.Name)

	for _, hostname := range hostnames {
		addresses = append(addresses, gatewayv1.GatewayStatusAddress{
			Type:  &addressType,
			Value: hostname,
		})

		if !strings.HasPrefix(hostname, "v6") {
			// v4 specific hostname, or hostname that includes both v4 and v6
			endpoints = append(endpoints, map[string]any{
				"dnsName":    hostname,
				"targets":    v4IPs,
				"recordType": "A",
				"recordTTL":  int64(300),
			})
		}

		if !strings.HasPrefix(hostname, "v4") {
			// v6 specific hostname, or hostname that includes both v4 and v6
			endpoints = append(endpoints, map[string]any{
				"dnsName":    hostname,
				"targets":    v6IPs,
				"recordType": "AAAA",
				"recordTTL":  int64(300),
			})
		}
	}

	if !equality.Semantic.DeepEqual(upstreamGateway.Status.Addresses, addresses) {
		upstreamGateway.Status.Addresses = addresses
		result.AddStatusUpdate(upstreamClient, upstreamGateway)
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, &gatewayDNSEndpoint, func() error {
		if err := controllerutil.SetOwnerReference(downstreamGateway, &gatewayDNSEndpoint, downstreamClient.Scheme()); err != nil {
			return err
		}
		return unstructured.SetNestedSlice(gatewayDNSEndpoint.Object, endpoints, "spec", "endpoints")
	}); err != nil {
		result.Err = err
		return result, nil
	}

	if c := apimeta.FindStatusCondition(downstreamGateway.Status.Conditions, string(gatewayv1.GatewayConditionAccepted)); c != nil {
		message := "The Gateway has not been scheduled by Datum Gateway"
		if c.Status == metav1.ConditionTrue {
			message = "The Gateway has been scheduled by Datum Gateway"
		}

		apimeta.SetStatusCondition(&upstreamGateway.Status.Conditions, metav1.Condition{
			Message: message,
			Type:    string(gatewayv1.GatewayConditionAccepted),
			Reason:  c.Reason,
			Status:  c.Status,
		})

		result.AddStatusUpdate(upstreamClient, upstreamGateway)
	}

	if c := apimeta.FindStatusCondition(downstreamGateway.Status.Conditions, string(gatewayv1.GatewayConditionProgrammed)); c != nil {
		message := "The Gateway has not been programmed"
		if c.Status == metav1.ConditionTrue {
			message = "The Gateway has been programmed"
		}

		apimeta.SetStatusCondition(&upstreamGateway.Status.Conditions, metav1.Condition{
			Message: message,
			Type:    string(gatewayv1.GatewayConditionProgrammed),
			Reason:  c.Reason,
			Status:  c.Status,
		})

		result.AddStatusUpdate(upstreamClient, upstreamGateway)
	}

	httpRouteResult := r.ensureDownstreamGatewayHTTPRoutes(
		ctx,
		upstreamClient,
		upstreamGateway,
		upstreamGatewayClassControllerName,
		downstreamGateway,
		downstreamStrategy,
	)

	return httpRouteResult.Merge(result), downstreamGateway
}

func (r *GatewayReconciler) ensureDownstreamGatewayHTTPRoutes(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	upstreamGatewayClassControllerName string,
	downstreamGateway *gatewayv1.Gateway,
	downstreamStrategy DownstreamResourceStrategy,
) (result Result) {
	logger := log.FromContext(ctx)

	// Get HTTPRoutes in the same namespace as the upstream gateway
	var httpRoutes gatewayv1.HTTPRouteList
	if err := upstreamClient.List(ctx, &httpRoutes, client.InNamespace(upstreamGateway.Namespace)); err != nil {
		result.Err = err
		return result
	}

	// Collect routes attached to the gateway.
	// Currently, we only support routes that are attached directly to the gateway,
	// not sections in it.
	var attachedRoutes []gatewayv1.HTTPRoute
	for _, route := range httpRoutes.Items {
		if parentRefs := route.Spec.ParentRefs; parentRefs != nil {
			for _, parentRef := range parentRefs {
				if string(parentRef.Name) == upstreamGateway.Name {
					if parentRef.Kind != nil && *parentRef.Kind == "Gateway" {
						attachedRoutes = append(attachedRoutes, route)
					}
				}
			}
		}
	}

	logger.Info("attached routes", "count", len(attachedRoutes))

	for _, routeContext := range attachedRoutes {
		httpRouteResult := r.ensureDownstreamHTTPRoute(
			ctx,
			upstreamClient,
			upstreamGateway,
			upstreamGatewayClassControllerName,
			downstreamGateway,
			downstreamStrategy,
			routeContext,
		)
		if result.Err != nil {
			return result
		}
		result = result.Merge(httpRouteResult)
	}

	// Update listener status for the upstream gateway
	listenerStatus := make([]gatewayv1.ListenerStatus, 0, len(upstreamGateway.Spec.Listeners))
	for _, listener := range upstreamGateway.Spec.Listeners {
		status := gatewayv1.ListenerStatus{
			Name: listener.Name,
			SupportedKinds: []gatewayv1.RouteGroupKind{
				{
					Group: ptr.To(gatewayv1.Group(gatewayv1.GroupName)),
					Kind:  "HTTPRoute",
				},
			},
			AttachedRoutes: int32(len(attachedRoutes)),
		}

		// Add Accepted, Programmed ResolvedRefs conditions
		// See: https://gateway-api.sigs.k8s.io/guides/implementers/#standard-status-fields-and-conditions
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:    string(gatewayv1.ListenerConditionAccepted),
			Status:  metav1.ConditionTrue,
			Reason:  "Accepted",
			Message: "The listener has been accepted by the Datum Gateway",
		})

		// TODO(jreese) update this based on the downstream gateway's status
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:    string(gatewayv1.ListenerConditionProgrammed),
			Status:  metav1.ConditionTrue,
			Reason:  "Programmed",
			Message: "The listener has been programmed by the Datum Gateway",
		})

		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:    string(gatewayv1.ListenerConditionResolvedRefs),
			Status:  metav1.ConditionTrue,
			Reason:  "ResolvedRefs",
			Message: "The listener has been resolved by the Datum Gateway",
		})

		listenerStatus = append(listenerStatus, status)
	}

	if !equality.Semantic.DeepEqual(upstreamGateway.Status.Listeners, listenerStatus) {
		upstreamGateway.Status.Listeners = listenerStatus
		result.AddStatusUpdate(upstreamClient, upstreamGateway)
	}

	return result
}

func (r *GatewayReconciler) ensureDownstreamHTTPRoute(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	upstreamGatewayClassControllerName string,
	downstreamGateway *gatewayv1.Gateway,
	downstreamStrategy DownstreamResourceStrategy,
	upstreamRoute gatewayv1.HTTPRoute,
) (result Result) {
	logger := log.FromContext(ctx)
	logger.Info("processing httproute", "name", upstreamRoute.Name)

	downstreamClient := downstreamStrategy.GetClient()

	downstreamRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: downstreamStrategy.GetDownstreamObjectMeta(&upstreamRoute),
	}

	routeResult, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, downstreamRoute, func() error {
		if err := controllerutil.SetOwnerReference(downstreamRoute, &upstreamRoute, downstreamClient.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference on downstream httproute: %w", err)
		}

		downstreamRoute.Spec = gatewayv1.HTTPRouteSpec{
			Hostnames: upstreamRoute.Spec.Hostnames,
			Rules:     upstreamRoute.Spec.Rules,
		}

		// Insert a parentRef for the downstream gateway. If a route is attached to
		// multiple gateways, a reconcile invocation for the other gateways will
		// insert a parentRef here. We do this due to potential name and namespace
		// rewriting that could be Gateway specific.

		// First, look to see if there is already a parentRef for the downstream
		// gateway.
		var parentRefFound bool
		for _, parentRef := range downstreamRoute.Spec.ParentRefs {
			if string(parentRef.Name) == downstreamGateway.Name {
				parentRefFound = true
				break
			}
		}

		if !parentRefFound {
			downstreamRoute.Spec.ParentRefs = append(downstreamRoute.Spec.ParentRefs, gatewayv1.ParentReference{
				Name: gatewayv1.ObjectName(downstreamGateway.Name),
			})
		}

		return nil
	})
	if err != nil {
		if apierrors.IsConflict(err) {
			result.Requeue = true
			return result
		}
		result.Err = err
		return result
	}

	// Update the upstream route's parent status information
	var parentStatus *gatewayv1.RouteParentStatus
	for i, parent := range upstreamRoute.Status.Parents {
		// TODO(jreese) look for inspiration on util functions for making this easier,
		// the envoy gateway has some.
		if (parent.ParentRef.Group == nil || parent.ParentRef.Group != nil && string(*parent.ParentRef.Group) == gatewayv1.GroupName) &&
			(parent.ParentRef.Kind == nil || parent.ParentRef.Kind != nil && string(*parent.ParentRef.Kind) == "Gateway") &&
			string(parent.ParentRef.Name) == upstreamGateway.Name {
			parentStatus = &upstreamRoute.Status.Parents[i]
			break
		}
	}

	var insertParentStatus bool
	if parentStatus == nil {
		insertParentStatus = true
		parentStatus = &gatewayv1.RouteParentStatus{
			ControllerName: gatewayv1.GatewayController(upstreamGatewayClassControllerName),
			ParentRef: gatewayv1.ParentReference{
				Name: gatewayv1.ObjectName(upstreamGateway.Name),
			},
		}
	}

	// Get the status of this parent from the downstream route
	var downstreamParentStatus *gatewayv1.RouteParentStatus
	for _, parent := range downstreamRoute.Status.Parents {
		if string(parent.ParentRef.Name) == downstreamGateway.Name {
			downstreamParentStatus = &parent
			break
		}
	}

	if downstreamParentStatus != nil {
		if c := apimeta.FindStatusCondition(downstreamParentStatus.Conditions, string(gatewayv1.RouteConditionAccepted)); c != nil {
			message := "Route has not been accepted"
			if c.Status == metav1.ConditionTrue {
				message = "Route is accepted"
			}

			apimeta.SetStatusCondition(&parentStatus.Conditions, metav1.Condition{
				Message: message,
				Type:    string(gatewayv1.RouteConditionAccepted),
				Reason:  c.Reason,
				Status:  c.Status,
			})
		}

		if c := apimeta.FindStatusCondition(downstreamParentStatus.Conditions, string(gatewayv1.RouteConditionResolvedRefs)); c != nil {
			message := "Object references for the Route have not been resolved"
			if c.Status == metav1.ConditionTrue {
				message = "Resolved all the Object references for the Route"
			}

			apimeta.SetStatusCondition(&parentStatus.Conditions, metav1.Condition{
				Message: message,
				Type:    string(gatewayv1.RouteConditionResolvedRefs),
				Reason:  c.Reason,
				Status:  c.Status,
			})
		}
	} else {
		logger.Info("did not find downstream parent status for gateway")
	}

	if insertParentStatus {
		upstreamRoute.Status.Parents = append(upstreamRoute.Status.Parents, *parentStatus)
	}

	result.AddStatusUpdate(upstreamClient, &upstreamRoute)

	logger.Info("downstream httproute processed", "operation_result", routeResult)

	return result
}

func getDownstreamResourceStrategy(cl cluster.Cluster) DownstreamResourceStrategy {
	// TODO(jreese) have different downstream client "providers"

	// One implementation could just return the client from the cluster that was
	// passed in. Another could return a client that ends up rewriting namespaces
	// in a way that you can target a single API server and not have conflicts.
	// Another could return a client that aligns each source cluster with a target
	// cluster, which could be a whole API server, or something like a KCP
	// workspace, and doesn't do any namespace/name rewriting.
	//
	// This way, the controller can be written as if it's putting resources into
	// the same namespace as the upstream resource, but that doesn't mean it'll
	// land in the same place as that resource.
	return &SameNamespaceDownstreamResourceStrategy{
		client: cl.GetClient(),
	}
}

type DownstreamResourceStrategy interface {
	GetClient() client.Client

	// GetDownstreamObjectMeta returns an ObjectMeta struct with Namespace and
	// Name fields populated.
	GetDownstreamObjectMeta(metav1.Object) metav1.ObjectMeta
}

type SameNamespaceDownstreamResourceStrategy struct {
	client client.Client
}

func (c *SameNamespaceDownstreamResourceStrategy) GetClient() client.Client {
	return c.client
}

// GetDownstreamObjectKey returns a name derived from the input object's name, where
// the value is the first 188 characters of the input object's name, suffixed by
// the sha256 hash of the full input object's name.
func (c *SameNamespaceDownstreamResourceStrategy) GetDownstreamObjectMeta(obj metav1.Object) metav1.ObjectMeta {
	name := obj.GetName()
	sum := sha256.Sum256([]byte(name))
	if len(name) > 188 {
		name = name[0:188]
	}
	return metav1.ObjectMeta{
		Namespace: obj.GetNamespace(),
		Name:      fmt.Sprintf("%s-%x", name, sum),
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&gatewayv1.Gateway{}, mcbuilder.WithPredicates(
			predicate.NewPredicateFuncs(func(object client.Object) bool {
				o := object.(*gatewayv1.Gateway)
				// TODO(jreese) get from config
				// TODO(jreese) might be expected to look at the controllerName on
				// the GatewayClass, instead of just the name of the GatewayClass.
				//
				// Example: gateway.networking.datumapis.com/external-global-proxy-controller
				//
				// https://github.com/envoyproxy/gateway/blob/4143f5c8eb2d468c093cca8871e6eb18262aef7e/internal/provider/kubernetes/predicates.go#L122
				//	https://github.com/envoyproxy/gateway/blob/4143f5c8eb2d468c093cca8871e6eb18262aef7e/internal/provider/kubernetes/controller.go#L1231
				// https://github.com/envoyproxy/gateway/blob/4143f5c8eb2d468c093cca8871e6eb18262aef7e/internal/provider/kubernetes/predicates.go#L44
				return o.Spec.GatewayClassName == "datum-external-global-proxy"
			}),
		)).
		// TODO(jreese) watch other clusters
		Owns(&gatewayv1.Gateway{}).
		Watches(
			&gatewayv1.HTTPRoute{},
			mchandler.EnqueueRequestsFromMapFunc(r.listGatewaysAttachedByHTTPRoute),
		).
		Named("gateway").
		Complete(r)
}

// listGatewaysAttachedByHTTPRoute is a watch predicate which finds all Gateways mentioned
// in HTTPRoutes' Parents field.
func (r *GatewayReconciler) listGatewaysAttachedByHTTPRoute(ctx context.Context, obj client.Object) []ctrl.Request {
	logger := log.FromContext(ctx)

	httpRoute, ok := obj.(*gatewayv1.HTTPRoute)
	if !ok {
		logger.Error(
			fmt.Errorf("unexpected object type"),
			"HTTPRoute watch predicate received unexpected object type",
			"expected", "*gatewayapi.HTTPRoute", "found", fmt.Sprintf("%T", obj),
		)
		return nil
	}

	var reqs []ctrl.Request

	for _, parentRef := range httpRoute.Spec.ParentRefs {
		if (parentRef.Group == nil || parentRef.Group != nil && string(*parentRef.Group) == gatewayv1.GroupName) &&
			(parentRef.Kind == nil || parentRef.Kind != nil && string(*parentRef.Kind) == "Gateway") {
			reqs = append(reqs, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: string(ptr.Deref(parentRef.Namespace, gatewayv1.Namespace(httpRoute.Namespace))),
					Name:      string(parentRef.Name),
				},
			})
		}
	}

	return reqs
}
