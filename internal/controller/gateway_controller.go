// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayv1alpha3 "sigs.k8s.io/gateway-api/apis/v1alpha3"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"

	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

const gatewayControllerFinalizer = "gateway.networking.datumapis.com/gateway-controller"
const certificateIssuerTLSOption = "gateway.networking.datumapis.com/certificate-issuer"
const KindGateway = "Gateway"
const KindService = "Service"
const KindEndpointSlice = "EndpointSlice"

// GatewayReconciler reconciles a Gateway object
type GatewayReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator

	DownstreamCluster cluster.Cluster
}

// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=services/finalizers,verbs=update
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices/finalizers,verbs=update

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/finalizers,verbs=update
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes/finalizers,verbs=update
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=backendtlspolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=backendtlspolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=backendtlspolicies/finalizers,verbs=update

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

	var gateway gatewayv1.Gateway
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &gateway); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Look up the GatewayClass to determine if it's applicable to this controller
	var upstreamGatewayClass gatewayv1.GatewayClass
	if err := cl.GetClient().Get(ctx, types.NamespacedName{Name: string(gateway.Spec.GatewayClassName)}, &upstreamGatewayClass); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if upstreamGatewayClass.Spec.ControllerName != r.Config.Gateway.ControllerName {
		return ctrl.Result{}, nil
	}

	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(req.ClusterName, cl.GetClient(), r.DownstreamCluster.GetClient())

	if !gateway.DeletionTimestamp.IsZero() {
		if result := r.finalizeGateway(ctx, cl.GetClient(), &gateway, downstreamStrategy); result.ShouldReturn() {
			return result.Complete(ctx)
		}

		controllerutil.RemoveFinalizer(&gateway, gatewayControllerFinalizer)
		if err := cl.GetClient().Update(ctx, &gateway); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from gateway: %w", err)
		}

		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&gateway, gatewayControllerFinalizer) {
		controllerutil.AddFinalizer(&gateway, gatewayControllerFinalizer)
		if err := cl.GetClient().Update(ctx, &gateway); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to gateway: %w", err)
		}

		return ctrl.Result{}, nil
	}

	logger.Info("reconciling gateway")
	defer logger.Info("reconcile complete")

	result, _ := r.ensureDownstreamGateway(ctx, cl.GetClient(), &gateway, downstreamStrategy)
	if result.ShouldReturn() {
		return result.Complete(ctx)
	}

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
	downstreamStrategy downstreamclient.ResourceStrategy,
) (result Result, downstreamGateway *gatewayv1.Gateway) {
	logger := log.FromContext(ctx)

	// Get the upstream gateway class so that we can pull the controller name out
	// of it and use it in route status updates.
	var upstreamGatewayClass gatewayv1.GatewayClass
	if err := upstreamClient.Get(ctx, types.NamespacedName{Name: string(upstreamGateway.Spec.GatewayClassName)}, &upstreamGatewayClass); err != nil {
		result.Err = err
		return result, nil
	}
	upstreamGatewayClassControllerName := string(upstreamGatewayClass.Spec.ControllerName)

	// addressHostnames are default hostnames that are unique to each gateway, and
	// will have DNS records created for them. Any custom hostnames provided in
	// listeners WILL NOT be added to the addresses list in the gateway status.
	addressHostnames := []string{
		fmt.Sprintf("%s.%s", upstreamGateway.UID, r.Config.Gateway.TargetDomain),
	}

	if r.Config.Gateway.IPv4Enabled() {
		addressHostnames = append(addressHostnames, fmt.Sprintf("v4.%s.%s", upstreamGateway.UID, r.Config.Gateway.TargetDomain))
	}

	if r.Config.Gateway.IPv6Enabled() {
		addressHostnames = append(addressHostnames, fmt.Sprintf("v6.%s.%s", upstreamGateway.UID, r.Config.Gateway.TargetDomain))
	}

	downstreamClient := downstreamStrategy.GetClient()
	downstreamGatewayObjectMeta, err := downstreamStrategy.ObjectMetaFromUpstreamObject(ctx, upstreamGateway)
	if err != nil {
		result.Err = fmt.Errorf("failed to get downstream gateway object metadata: %w", err)
		return result, nil
	}

	downstreamGateway = &gatewayv1.Gateway{
		ObjectMeta: downstreamGatewayObjectMeta,
	}

	gatewayResult, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, downstreamGateway, func() error {
		if err := downstreamStrategy.SetControllerReference(ctx, upstreamGateway, downstreamGateway); err != nil {
			return fmt.Errorf("failed to set controller reference on downstream gateway: %w", err)
		}

		if downstreamGateway.Annotations == nil {
			downstreamGateway.Annotations = map[string]string{}
		}
		var listeners []gatewayv1.Listener
		for _, l := range upstreamGateway.Spec.Listeners {

			// TODO(jreese) this approach actually leads to request coalescing, resulting
			// in the `OverlappingTLSConfig` condition being set to true on the downstream
			// gateway, and HTTP2 disabled. We may need to either handle our own
			// certificate requests for each listener, or create a gateway for each
			// unique hostname that needs TLS.
			if l.TLS != nil && l.TLS.Options[certificateIssuerTLSOption] != "" {
				if r.Config.Gateway.PerGatewayCertificateIssuer {
					downstreamGateway.Annotations["cert-manager.io/issuer"] = downstreamGateway.Name
				} else {
					clusterIssuerName := string(l.TLS.Options[certificateIssuerTLSOption])
					if r.Config.Gateway.ClusterIssuerMap[clusterIssuerName] != "" {
						clusterIssuerName = r.Config.Gateway.ClusterIssuerMap[clusterIssuerName]
					}
					downstreamGateway.Annotations["cert-manager.io/cluster-issuer"] = clusterIssuerName
				}
			}

			if l.Hostname != nil {
				// Custom hostname, add a listener for it.
				listeners = append(listeners, l)
			}
		}

		// TODO(jreese) get from "scheduler"
		downstreamGateway.Spec.GatewayClassName = gatewayv1.ObjectName(r.Config.Gateway.DownstreamGatewayClassName)

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
					// TODO(jreese) investigate secret deletion when Cert (gateway) is deleted
					// See: https://cert-manager.io/docs/usage/certificate/#cleaning-up-secrets-when-certificates-are-deleted
					CertificateRefs: []gatewayv1.SecretObjectReference{
						{
							Name: gatewayv1.ObjectName(downstreamGateway.Name),
						},
					},
				}
			}

			return listener
		}

		for i, hostname := range addressHostnames {
			listeners = append(listeners,
				listenerFactory(fmt.Sprintf("http-%d", i), hostname, gatewayv1.HTTPProtocolType, gatewayv1.PortNumber(80)),
				listenerFactory(fmt.Sprintf("https-%d", i), hostname, gatewayv1.HTTPSProtocolType, gatewayv1.PortNumber(443)),
			)
		}

		downstreamGateway.Spec.Listeners = listeners

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

	dnsResult := r.ensureDownstreamGatewayDNSEndpoints(
		ctx,
		downstreamGateway,
		downstreamStrategy,
		addressHostnames,
	)
	if dnsResult.ShouldReturn() {
		return dnsResult.Merge(result), nil
	}

	if c := apimeta.FindStatusCondition(downstreamGateway.Status.Conditions, string(gatewayv1.GatewayConditionAccepted)); c != nil {
		message := "The Gateway has not been scheduled by Datum Gateway"
		if c.Status == metav1.ConditionTrue {
			message = "The Gateway has been scheduled by Datum Gateway"
		}

		apimeta.SetStatusCondition(&upstreamGateway.Status.Conditions, metav1.Condition{
			Message:            message,
			Type:               string(gatewayv1.GatewayConditionAccepted),
			Reason:             c.Reason,
			Status:             c.Status,
			ObservedGeneration: upstreamGateway.Generation,
		})

		result.AddStatusUpdate(upstreamClient, upstreamGateway)
	}

	if c := apimeta.FindStatusCondition(downstreamGateway.Status.Conditions, string(gatewayv1.GatewayConditionProgrammed)); c != nil {
		message := "The Gateway has not been programmed"
		if c.Status == metav1.ConditionTrue {
			message = "The Gateway has been programmed"
		}

		apimeta.SetStatusCondition(&upstreamGateway.Status.Conditions, metav1.Condition{
			Message:            message,
			Type:               string(gatewayv1.GatewayConditionProgrammed),
			Reason:             c.Reason,
			Status:             c.Status,
			ObservedGeneration: upstreamGateway.Generation,
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

	addresses := make([]gatewayv1.GatewayStatusAddress, 0, len(addressHostnames))
	addressType := gatewayv1.HostnameAddressType

	for _, hostname := range addressHostnames {
		addresses = append(addresses, gatewayv1.GatewayStatusAddress{
			Type:  &addressType,
			Value: hostname,
		})
	}

	if !equality.Semantic.DeepEqual(upstreamGateway.Status.Addresses, addresses) {
		upstreamGateway.Status.Addresses = addresses
		result.AddStatusUpdate(upstreamClient, upstreamGateway)
	}

	return httpRouteResult.Merge(result), downstreamGateway
}

func (r *GatewayReconciler) ensureDownstreamGatewayDNSEndpoints(
	ctx context.Context,
	downstreamGateway *gatewayv1.Gateway,
	downstreamStrategy downstreamclient.ResourceStrategy,
	hostnames []string,
) (result Result) {
	logger := log.FromContext(ctx)

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
	if (r.Config.Gateway.IPv4Enabled() && len(v4IPs) == 0) || (r.Config.Gateway.IPv6Enabled() && len(v6IPs) == 0) {
		logger.Info(
			"IP addresses not yet available on downstream gateway",
			"ipv4", v4IPs, "ipv4_enabled", r.Config.Gateway.IPv4Enabled(),
			"ipv6", v6IPs, "ipv6_enabled", r.Config.Gateway.IPv6Enabled(),
		)
		return result
	}

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
		if len(v4IPs) > 0 && !strings.HasPrefix(hostname, "v6") {
			// v4 specific hostname, or hostname that includes both v4 and v6
			endpoints = append(endpoints, map[string]any{
				"dnsName":    hostname,
				"targets":    v4IPs,
				"recordType": "A",
				"recordTTL":  int64(300),
			})
		}

		if len(v6IPs) > 0 && !strings.HasPrefix(hostname, "v4") {
			// v6 specific hostname, or hostname that includes both v4 and v6
			endpoints = append(endpoints, map[string]any{
				"dnsName":    hostname,
				"targets":    v6IPs,
				"recordType": "AAAA",
				"recordTTL":  int64(300),
			})
		}
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, downstreamStrategy.GetClient(), &gatewayDNSEndpoint, func() error {
		if err := controllerutil.SetControllerReference(downstreamGateway, &gatewayDNSEndpoint, downstreamStrategy.GetClient().Scheme()); err != nil {
			return err
		}
		return unstructured.SetNestedSlice(gatewayDNSEndpoint.Object, endpoints, "spec", "endpoints")
	}); err != nil {
		result.Err = err
		return result
	}

	return result
}

func (r *GatewayReconciler) finalizeGateway(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	downstreamStrategy downstreamclient.ResourceStrategy,
) (result Result) {
	logger := log.FromContext(ctx)
	logger.Info("finalizing gateway")
	// Go through downstream http routes that are attached to the downstream
	// gateway and remove the parentRef from the status. If it's the last parent
	// ref, delete the downstream route. If there's a race condition on delete/create,
	// it'll be reconciled again in the next cycle and fighting is not expected.

	downstreamClient := downstreamStrategy.GetClient()

	downstreamGatewayObjectMeta, err := downstreamStrategy.ObjectMetaFromUpstreamObject(ctx, upstreamGateway)
	if err != nil {
		result.Err = err
		return result
	}
	downstreamGateway := &gatewayv1.Gateway{
		ObjectMeta: downstreamGatewayObjectMeta,
	}

	if err := downstreamClient.Get(ctx, client.ObjectKeyFromObject(downstreamGateway), downstreamGateway); err != nil {
		if apierrors.IsNotFound(err) {
			return result
		}
		result.Err = err
		return result
	}

	// Detach HTTPRoutes from the upstream gateway
	logger.Info("detaching httproutes from upstream gateway")
	detachResult := r.detachHTTPRoutes(ctx, upstreamClient, upstreamGateway, false)
	if detachResult.ShouldReturn() {
		return detachResult
	}

	// Detach HTTPRoutes from the downstream gateway
	logger.Info("detaching httproutes from downstream gateway")
	detachResult = r.detachHTTPRoutes(ctx, downstreamClient, downstreamGateway, true)
	if detachResult.ShouldReturn() {
		return detachResult
	}

	logger.Info("deleting anchor for upstream gateway")
	if err := downstreamStrategy.DeleteAnchorForObject(ctx, upstreamGateway); err != nil {
		result.Err = err
		return result
	}

	return result
}

// TODO(jreese) revisit the parameters here to clean them up. It's a bit messy
// as this function is used against both the upstream and downstream resources.
func (r *GatewayReconciler) detachHTTPRoutes(
	ctx context.Context,
	gatewayClient client.Client,
	gateway *gatewayv1.Gateway,
	deleteWhenNoParents bool,
) (result Result) {
	logger := log.FromContext(ctx)

	var httpRoutes gatewayv1.HTTPRouteList
	if err := gatewayClient.List(ctx, &httpRoutes, client.InNamespace(gateway.Namespace)); err != nil {
		result.Err = err
		return result
	}

	logger.Info("found httproutes", "count", len(httpRoutes.Items))

	for _, route := range httpRoutes.Items {
		if !route.DeletionTimestamp.IsZero() {
			continue
		}

		var parents []gatewayv1.RouteParentStatus
		for _, parent := range route.Status.Parents {
			if (parent.ParentRef.Group == nil || parent.ParentRef.Group != nil && string(*parent.ParentRef.Group) == gatewayv1.GroupName) &&
				(parent.ParentRef.Kind == nil || parent.ParentRef.Kind != nil && string(*parent.ParentRef.Kind) == KindGateway) &&
				string(parent.ParentRef.Name) == gateway.Name {
				logger.Info("removing parent ref from httproute", "name", route.Name, "parent", parent.ParentRef.Name)
				continue
			}
			parents = append(parents, parent)
		}

		if len(parents) == 0 && deleteWhenNoParents {
			logger.Info("deleting httproute due to no parents", "name", route.Name)
			if err := gatewayClient.Delete(ctx, &route); err != nil {
				result.Err = err
				return result
			}
		} else if !equality.Semantic.DeepEqual(route.Status.Parents, parents) {
			route.Status.Parents = parents
			result.AddStatusUpdate(gatewayClient, &route)
		}
	}
	return result
}

func (r *GatewayReconciler) ensureDownstreamGatewayHTTPRoutes(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	upstreamGatewayClassControllerName string,
	downstreamGateway *gatewayv1.Gateway,
	downstreamStrategy downstreamclient.ResourceStrategy,
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
					if parentRef.Kind != nil && *parentRef.Kind == KindGateway {
						attachedRoutes = append(attachedRoutes, route)
					}
				}
			}
		}
	}

	logger.Info("attached routes", "count", len(attachedRoutes))

	for _, route := range attachedRoutes {
		if !route.DeletionTimestamp.IsZero() {
			logger.Info("skipping httproute due to deletion timestamp", "name", route.Name)
			continue
		}
		httpRouteResult := r.ensureDownstreamHTTPRoute(
			ctx,
			upstreamClient,
			upstreamGateway,
			upstreamGatewayClassControllerName,
			downstreamGateway,
			downstreamStrategy,
			route,
		)
		if result.Err != nil {
			return result
		}
		result = result.Merge(httpRouteResult)
	}

	currentListenerStatus := map[gatewayv1.SectionName]gatewayv1.ListenerStatus{}
	for _, listener := range upstreamGateway.Status.Listeners {
		currentListenerStatus[listener.Name] = listener
	}

	// Update listener status for the upstream gateway
	listenerStatus := make([]gatewayv1.ListenerStatus, 0, len(upstreamGateway.Spec.Listeners))
	for _, listener := range upstreamGateway.Spec.Listeners {

		status, ok := currentListenerStatus[listener.Name]
		if !ok {
			status = gatewayv1.ListenerStatus{
				Name: listener.Name,
				SupportedKinds: []gatewayv1.RouteGroupKind{
					{
						Group: ptr.To(gatewayv1.Group(gatewayv1.GroupName)),
						Kind:  "HTTPRoute",
					},
				},
			}
		}
		status.AttachedRoutes = int32(len(attachedRoutes))

		// Add Accepted, Programmed ResolvedRefs conditions
		// See: https://gateway-api.sigs.k8s.io/guides/implementers/#standard-status-fields-and-conditions
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               string(gatewayv1.ListenerConditionAccepted),
			Status:             metav1.ConditionTrue,
			Reason:             "Accepted",
			Message:            "The listener has been accepted by the Datum Gateway",
			ObservedGeneration: upstreamGateway.Generation,
		})

		// TODO(jreese) update this based on the downstream gateway's status
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               string(gatewayv1.ListenerConditionProgrammed),
			Status:             metav1.ConditionTrue,
			Reason:             "Programmed",
			Message:            "The listener has been programmed by the Datum Gateway",
			ObservedGeneration: upstreamGateway.Generation,
		})

		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               string(gatewayv1.ListenerConditionResolvedRefs),
			Status:             metav1.ConditionTrue,
			Reason:             "ResolvedRefs",
			Message:            "The listener has been resolved by the Datum Gateway",
			ObservedGeneration: upstreamGateway.Generation,
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
	downstreamStrategy downstreamclient.ResourceStrategy,
	upstreamRoute gatewayv1.HTTPRoute,
) (result Result) {
	logger := log.FromContext(ctx)
	logger.Info("processing httproute", "name", upstreamRoute.Name)

	downstreamClient := downstreamStrategy.GetClient()
	downstreamRouteObjectMeta, err := downstreamStrategy.ObjectMetaFromUpstreamObject(ctx, &upstreamRoute)
	if err != nil {
		result.Err = fmt.Errorf("failed to get downstream httproute object metadata: %w", err)
		return result
	}

	downstreamRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: downstreamRouteObjectMeta,
	}

	rules, downstreamResources, err := r.processDownstreamHTTPRouteRules(
		ctx,
		upstreamClient,
		upstreamGateway,
		upstreamRoute,
		downstreamGateway,
	)
	if err != nil {
		result.Err = err
		return result
	}

	routeResult, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, downstreamRoute, func() error {
		downstreamRoute.Spec = gatewayv1.HTTPRouteSpec{
			Hostnames: upstreamRoute.Spec.Hostnames,
			Rules:     rules,
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

	// Create required downstream resources. Currently they're all specific to
	// the HTTPRoute resource, so we set it as the owner and let them get
	// cleaned up when the HTTPRoute is deleted.
	for _, resource := range downstreamResources {
		if err := controllerutil.SetControllerReference(downstreamRoute, resource, downstreamClient.Scheme()); err != nil {
			result.Err = err
			return result
		}

		resourceResult, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, resource, func() error {
			return nil
		})
		if err != nil {
			result.Err = err
			return result
		}

		gvk, err := apiutil.GVKForObject(resource, downstreamClient.Scheme())
		if err != nil {
			result.Err = err
			return result
		}

		logger.Info("downstream resource processed",
			"operation_result", resourceResult,
			"kind", gvk.Kind,
			"namespace", resource.GetNamespace(),
			"name", resource.GetName(),
		)
	}

	// Update the upstream route's parent status information
	var parentStatus *gatewayv1.RouteParentStatus
	for i, parent := range upstreamRoute.Status.Parents {
		// TODO(jreese) look for inspiration on util functions for making this easier,
		// the envoy gateway has some.
		if (parent.ParentRef.Group == nil || parent.ParentRef.Group != nil && string(*parent.ParentRef.Group) == gatewayv1.GroupName) &&
			(parent.ParentRef.Kind == nil || parent.ParentRef.Kind != nil && string(*parent.ParentRef.Kind) == KindGateway) &&
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
				Message:            message,
				Type:               string(gatewayv1.RouteConditionAccepted),
				Reason:             c.Reason,
				Status:             c.Status,
				ObservedGeneration: upstreamRoute.Generation,
			})
		}

		if c := apimeta.FindStatusCondition(downstreamParentStatus.Conditions, string(gatewayv1.RouteConditionResolvedRefs)); c != nil {
			message := "Object references for the Route have not been resolved"
			if c.Status == metav1.ConditionTrue {
				message = "Resolved all the Object references for the Route"
			}

			apimeta.SetStatusCondition(&parentStatus.Conditions, metav1.Condition{
				Message:            message,
				Type:               string(gatewayv1.RouteConditionResolvedRefs),
				Reason:             c.Reason,
				Status:             c.Status,
				ObservedGeneration: upstreamRoute.Generation,
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

// processDownstreamHTTPRouteRules is a helper function that processes the
// rules of an HTTPRoute and returns the rules and the downstream resources
// that need to be created.
func (r *GatewayReconciler) processDownstreamHTTPRouteRules(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	upstreamRoute gatewayv1.HTTPRoute,
	downstreamGateway *gatewayv1.Gateway,
) (rules []gatewayv1.HTTPRouteRule, downstreamResources []client.Object, err error) {

	// We need to create a Service for each (BackendRef, EndpointSlice)
	// combination as different backendRefs may use different hostnames in URL
	// rewrite filters which are used to drive the creation of BackendTLSPolicies.
	// A BackendTLSPolicy is associated with either an entire service, or a
	// specific port of a service, and we cannot build port aliases in the
	// downstream service, as TargetPort is ignored for headless services.
	//
	// As a result of this, we also need to create an EndpointSlice for each
	// downstream service, because an EndpointSlice may only be associated with
	// a single service.

	logger := log.FromContext(ctx)

	for ruleIdx, rule := range upstreamRoute.Spec.Rules {
		var backendRefs []gatewayv1.HTTPBackendRef
		for backendRefIdx, backendRef := range rule.BackendRefs {

			if backendRef.BackendObjectReference.Kind == nil {
				// Should not happen, as the default kind is Service
				continue
			}

			switch *backendRef.BackendObjectReference.Kind {
			// Transform EndpointSlice references into Service references.
			case KindEndpointSlice:
				// Fetch the upstream EndpointSlice
				var upstreamEndpointSlice discoveryv1.EndpointSlice
				if err := upstreamClient.Get(ctx, types.NamespacedName{
					Namespace: string(ptr.Deref(backendRef.Namespace, gatewayv1.Namespace(upstreamGateway.Namespace))),
					Name:      string(backendRef.Name),
				}, &upstreamEndpointSlice); err != nil {
					return nil, nil, err
				}

				if backendRef.BackendObjectReference.Port == nil {
					// Should be protected by validation, but check just in case.
					logger.Info("no port defined in backendRef", "backendRef", backendRef)
					return nil, nil, fmt.Errorf("no port defined in backendRef")
				}

				var ports []corev1.ServicePort
				var appProtocol *string
				var endpointPort *discoveryv1.EndpointPort
				for _, port := range upstreamEndpointSlice.Ports {
					ports = append(ports, corev1.ServicePort{
						Name:        ptr.Deref(port.Name, ""),
						Protocol:    ptr.Deref(port.Protocol, corev1.ProtocolTCP),
						AppProtocol: port.AppProtocol,
						Port:        *port.Port,
					})

					if int32(*backendRef.BackendObjectReference.Port) == *port.Port {
						if port.Name == nil {
							// This should be protected by validation, but check just in case.
							logger.Info("no port name defined in upstream endpointslice", "endpointslice", upstreamEndpointSlice.Name, "port", port)
							return nil, nil, fmt.Errorf("no port name defined in upstream endpointslice")
						}
						appProtocol = port.AppProtocol
						endpointPort = ptr.To(port)
					}
				}

				if endpointPort == nil {
					logger.Info("port not found in upstream endpointslice", "endpointslice", upstreamEndpointSlice.Name, "port", *backendRef.BackendObjectReference.Port)
					return nil, nil, fmt.Errorf("port not found in upstream endpointslice")
				}

				// Construct a name to use for the service and endpointslice that the
				// downstream backendRef will reference.
				resourceName := fmt.Sprintf("route-%s-rule-%d-backendref-%d", upstreamRoute.UID, ruleIdx, backendRefIdx)

				downstreamService := &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: downstreamGateway.Namespace,
						Name:      resourceName,
					},
					Spec: corev1.ServiceSpec{
						Type:                  corev1.ServiceTypeClusterIP,
						ClusterIP:             "None",
						Ports:                 ports,
						InternalTrafficPolicy: ptr.To(corev1.ServiceInternalTrafficPolicyCluster),
						TrafficDistribution:   ptr.To(corev1.ServiceTrafficDistributionPreferClose),
					},
				}
				downstreamResources = append(downstreamResources, downstreamService)

				// TODO(jreese) build another controller to sync endpointslice updates
				// to the downstream endpointslices.
				downstreamEndpointSlice := &discoveryv1.EndpointSlice{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: downstreamGateway.Namespace,
						Name:      resourceName,
						Labels: map[string]string{
							downstreamclient.UpstreamOwnerNameLabel: upstreamEndpointSlice.Name,
							discoveryv1.LabelServiceName:            downstreamService.Name,
						},
					},
					AddressType: upstreamEndpointSlice.AddressType,
					Endpoints:   upstreamEndpointSlice.Endpoints,
					Ports:       upstreamEndpointSlice.Ports,
				}

				downstreamResources = append(downstreamResources, downstreamEndpointSlice)

				backendObjectReference := gatewayv1.BackendObjectReference{
					Namespace: ptr.To(gatewayv1.Namespace(downstreamGateway.Namespace)),
					Kind:      ptr.To(gatewayv1.Kind(KindService)),
					Name:      gatewayv1.ObjectName(downstreamService.Name),
					Port:      backendRef.BackendObjectReference.Port,
				}

				downstreamHTTPBackendRef := gatewayv1.HTTPBackendRef{
					BackendRef: gatewayv1.BackendRef{
						Weight:                 backendRef.Weight,
						BackendObjectReference: backendObjectReference,
					},
					Filters: backendRef.Filters,
				}

				backendRefs = append(backendRefs, downstreamHTTPBackendRef)

				if appProtocol != nil && *appProtocol == "https" {
					// Extract the hostname from the URLRewrite filter.
					var hostname *gatewayv1.PreciseHostname
					for _, filter := range backendRef.Filters {
						if filter.URLRewrite != nil {
							hostname = filter.URLRewrite.Hostname
							break
						}
					}

					if hostname == nil {
						// Fall back to looking at rule filters for a hostname.
						for _, filter := range rule.Filters {
							if filter.URLRewrite != nil {
								hostname = filter.URLRewrite.Hostname
								break
							}
						}

						if hostname == nil {
							// TODO(jreese) set the RouteConditionResolvedRefs condition to
							// False, as the hostname is not present.
							return nil, nil, fmt.Errorf("no hostname found in URLRewrite filters on backendRef or Route %q", upstreamRoute.Name)
						}
					}

					backendTLSPolicy := &gatewayv1alpha3.BackendTLSPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: downstreamGateway.Namespace,
							Name:      resourceName,
						},
						Spec: gatewayv1alpha3.BackendTLSPolicySpec{
							TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
								// TODO(jreese): We may have multiple ports that we need to set
								// the policy on.
								{
									LocalPolicyTargetReference: gatewayv1alpha2.LocalPolicyTargetReference{
										Kind: gatewayv1.Kind(KindService),
										Name: gatewayv1.ObjectName(downstreamService.Name),
									},
									SectionName: ptr.To(gatewayv1.SectionName(*endpointPort.Name)),
								},
							},
							Validation: gatewayv1alpha3.BackendTLSPolicyValidation{
								WellKnownCACertificates: ptr.To(gatewayv1alpha3.WellKnownCACertificatesSystem),
								Hostname:                *hostname,
							},
						},
					}

					downstreamResources = append(downstreamResources, backendTLSPolicy)
				}

			// Other types of backend refs will be handled in the future.
			default:
				logger.Info("unknown backend ref kind", "kind", *backendRef.BackendObjectReference.Kind)
				continue
			}
		}

		rules = append(rules, gatewayv1.HTTPRouteRule{
			Filters:            rule.Filters,
			Matches:            rule.Matches,
			BackendRefs:        backendRefs,
			Timeouts:           rule.Timeouts,
			Retry:              rule.Retry,
			SessionPersistence: rule.SessionPersistence,
		})
	}

	return rules, downstreamResources, nil
}

type DownstreamResourceStrategy interface {
	GetClient() client.Client

	// GetDownstreamObjectMeta returns an ObjectMeta struct with Namespace and
	// Name fields populated.
	GetDownstreamObjectMeta(metav1.Object) metav1.ObjectMeta
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	src := mcsource.TypedKind(
		&gatewayv1.Gateway{},
		downstreamclient.TypedEnqueueRequestForUpstreamOwner[*gatewayv1.Gateway](&gatewayv1.Gateway{}),
	)

	clusterSrc, _ := src.ForCluster("", r.DownstreamCluster)

	return mcbuilder.ControllerManagedBy(mgr).
		For(&gatewayv1.Gateway{},
			mcbuilder.WithEngageWithLocalCluster(false),
		).
		Watches(
			&gatewayv1.HTTPRoute{},
			mchandler.EnqueueRequestsFromMapFunc(r.listGatewaysAttachedByHTTPRoute),
			mcbuilder.WithEngageWithLocalCluster(false),
		).
		WatchesRawSource(clusterSrc).
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
			(parentRef.Kind == nil || parentRef.Kind != nil && string(*parentRef.Kind) == KindGateway) {
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
