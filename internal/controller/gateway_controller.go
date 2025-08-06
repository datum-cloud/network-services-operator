// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayv1alpha3 "sigs.k8s.io/gateway-api/apis/v1alpha3"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
	"go.datum.net/network-services-operator/internal/util/resourcename"
)

const gatewayControllerFinalizer = "gateway.networking.datumapis.com/gateway-controller"
const gatewayControllerGCFinalizer = "gateway.networking.datumapis.com/gateway-controller-gc"
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
		if controllerutil.ContainsFinalizer(&gateway, gatewayControllerFinalizer) {
			if result := r.finalizeGateway(ctx, cl.GetClient(), &gateway, downstreamStrategy); result.ShouldReturn() {
				return result.Complete(ctx)
			}

			controllerutil.RemoveFinalizer(&gateway, gatewayControllerFinalizer)
			if err := cl.GetClient().Update(ctx, &gateway); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from gateway: %w", err)
			}
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

	verifiedHostnames, err := r.ensureHostnameVerification(ctx, upstreamClient, upstreamGateway)
	if err != nil {
		result.Err = err
		return result, nil
	}

	gatewayResult, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, downstreamGateway, func() error {
		if err := downstreamStrategy.SetControllerReference(ctx, upstreamGateway, downstreamGateway); err != nil {
			return fmt.Errorf("failed to set controller reference on downstream gateway: %w", err)
		}

		if downstreamGateway.Annotations == nil {
			downstreamGateway.Annotations = map[string]string{}
		}
		var listeners []gatewayv1.Listener
		foundHTTPListener := false
		foundHTTPSListener := false
		for listenerIndex, l := range upstreamGateway.Spec.Listeners {
			switch l.Protocol {
			case gatewayv1.HTTPProtocolType:
				foundHTTPListener = true
			case gatewayv1.HTTPSProtocolType:
				foundHTTPSListener = true
			}

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

			// Add custom hostnames if they are verified
			if l.Hostname != nil {
				if !slices.Contains(verifiedHostnames, string(*l.Hostname)) {
					logger.Info("skipping downstream gateway listener with unverified hostname", "upstream_listener_index", listenerIndex, "hostname", *l.Hostname)
					continue
				}
				listenerCopy := l.DeepCopy()
				if l.TLS != nil && l.TLS.Options[certificateIssuerTLSOption] != "" {
					// Translate upstream TLS settings to downstream TLS settings
					delete(listenerCopy.TLS.Options, certificateIssuerTLSOption)

					tlsMode := gatewayv1.TLSModeTerminate
					listenerCopy.TLS = &gatewayv1.GatewayTLSConfig{
						Mode: &tlsMode,
						// TODO(jreese) investigate secret deletion when Cert (gateway) is deleted
						// See: https://cert-manager.io/docs/usage/certificate/#cleaning-up-secrets-when-certificates-are-deleted
						CertificateRefs: []gatewayv1.SecretObjectReference{
							{
								Name: gatewayv1.ObjectName(resourcename.GetValidDNS1123Name(fmt.Sprintf("%s-%s", downstreamGateway.Name, l.Name))),
							},
						},
					}
				}

				listeners = append(listeners, *listenerCopy)
			}
		}

		// TODO(jreese) get from "scheduler"
		downstreamGateway.Spec.GatewayClassName = gatewayv1.ObjectName(r.Config.Gateway.DownstreamGatewayClassName)

		for i, hostname := range addressHostnames {
			if foundHTTPListener {
				listenerName := fmt.Sprintf("http-%d", i)
				listeners = append(listeners,
					listenerFactory(
						listenerName,
						hostname,
						gatewayv1.HTTPProtocolType,
						gatewayv1.PortNumber(DefaultHTTPPort),
						"",
					),
				)
			}
			if foundHTTPSListener {
				listenerName := fmt.Sprintf("https-%d", i)
				listeners = append(listeners,
					listenerFactory(
						listenerName,
						hostname,
						gatewayv1.HTTPSProtocolType,
						gatewayv1.PortNumber(DefaultHTTPSPort),
						fmt.Sprintf("%s-%s", downstreamGateway.Name, listenerName),
					),
				)
			}
		}

		downstreamGateway.Spec.Listeners = listeners

		return nil
	})
	if err != nil {
		if apierrors.IsConflict(err) {
			result.RequeueAfter = 1 * time.Second
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

	gatewayStatusResult := r.reconcileGatewayStatus(
		upstreamClient,
		upstreamGateway,
		downstreamGateway,
	)
	if gatewayStatusResult.ShouldReturn() {
		return gatewayStatusResult.Merge(result), nil
	}

	httpRouteResult := r.ensureDownstreamGatewayHTTPRoutes(
		ctx,
		upstreamClient,
		upstreamGateway,
		upstreamGatewayClassControllerName,
		downstreamGateway,
		downstreamStrategy,
		verifiedHostnames,
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

func (r *GatewayReconciler) reconcileGatewayStatus(
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	downstreamGateway *gatewayv1.Gateway,
) (result Result) {
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

	return result
}

// isHostnameVerified returns hostnames found on listeners that are verified. A
// hostname is considered verified if any verified Domain is found in the same
// namespace with a `spec.domainName` value that matches the hostname exactly,
// or the hostname is a sub domain. If no matching Domain is found, one will be
// created.
func (r *GatewayReconciler) ensureHostnameVerification(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
) ([]string, error) {
	logger := log.FromContext(ctx)

	// TODO(jreese) Allow hostnames which have been successfully configured on
	// the gateway stay on the gateway, regardless of whether or not there is
	// a matching Domain that's verified.

	// TODO(jreese) add accounting control plane. Use configmaps to track
	// hostnames?

	// Get a unique set of hostnames
	hostnames := sets.New[string]()
	for _, l := range upstreamGateway.Spec.Listeners {
		if l.Hostname != nil {
			hostnames.Insert(string(*l.Hostname))
		}
	}

	// List all Domains in the same namespace as the upstream gateway. A field
	// selector will not work here, as set based operations are not supported, and
	// there's not way to check for a suffix match.

	var domainList networkingv1alpha.DomainList
	if err := upstreamClient.List(ctx, &domainList, client.InNamespace(upstreamGateway.Namespace)); err != nil {
		return nil, fmt.Errorf("failed listing domains: %w", err)
	}

	var verifiedHostnames []string
	domainsToCreate := sets.New[string]()

	for _, hostname := range hostnames.UnsortedList() {
		foundMatchingDomain := false
		for _, domain := range domainList.Items {
			if hostname == domain.Spec.DomainName || strings.HasSuffix(hostname, "."+domain.Spec.DomainName) {
				foundMatchingDomain = true
				if !apimeta.IsStatusConditionTrue(domain.Status.Conditions, networkingv1alpha.DomainConditionVerified) {
					logger.Info("domain is not verified", "domain", domain.Name)
					continue
				}
				verifiedHostnames = append(verifiedHostnames, hostname)
				break
			}
		}

		if !foundMatchingDomain {
			parts := strings.Split(hostname, ".")
			if len(parts) < 2 {
				logger.Info("malformed hostname, expected at least two parts", "hostname", hostname)
				continue
			}

			domainsToCreate.Insert(strings.Join(parts[len(parts)-2:], "."))
		}
	}

	if len(domainsToCreate) > 0 {
		logger.Info("creating domain resources for hostnames with no matching domain", "domains", domainsToCreate)

		// Create a Domain resource with the same name as the value that will be
		// placed in spec.domainName. This is done to avoid duplication upon cache
		// sync delays. AlreadyExists errors will be ignored.
		for _, domainName := range domainsToCreate.UnsortedList() {
			domain := &networkingv1alpha.Domain{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: upstreamGateway.Namespace,
					Name:      domainName,
				},
				Spec: networkingv1alpha.DomainSpec{
					DomainName: domainName,
				},
			}

			if err := upstreamClient.Create(ctx, domain); client.IgnoreAlreadyExists(err) != nil {
				return nil, fmt.Errorf("failed creating domain: %w", err)
			}

			logger.Info("domain created", "domain", domain.Name)
		}
	}

	return verifiedHostnames, nil
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
			if ptr.Deref(parent.ParentRef.Group, gatewayv1.GroupName) == gatewayv1.GroupName &&
				ptr.Deref(parent.ParentRef.Kind, KindGateway) == KindGateway &&
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
	verifiedHostnames []string,
) (result Result) {
	logger := log.FromContext(ctx)

	// Get HTTPRoutes in the same namespace as the upstream gateway
	var httpRoutes gatewayv1.HTTPRouteList
	if err := upstreamClient.List(ctx, &httpRoutes, client.InNamespace(upstreamGateway.Namespace)); err != nil {
		result.Err = err
		return result
	}

	// Collect routes attached to the gateway.
	attachedRoutes := map[client.ObjectKey]gatewayv1.HTTPRoute{}
	attachedRouteCount := make(map[gatewayv1.SectionName]int32, len(upstreamGateway.Spec.Listeners))
	for _, route := range httpRoutes.Items {
		if parentRefs := route.Spec.ParentRefs; parentRefs != nil {
			for _, parentRef := range parentRefs {
				if parentRef.Namespace != nil {
					// At the time of this writing, validation prohibits namespaces to be
					// set on parent refs. This check is here as a safety for when that
					// limitation is removed, as additional programming logic will need
					// to exist to translate downstream namespace names.
					result.Err = fmt.Errorf("unexpected namespace in parent ref: %s", *parentRef.Namespace)
					return result
				}

				if ptr.Deref(parentRef.Group, gatewayv1.GroupName) == gatewayv1.GroupName &&
					ptr.Deref(parentRef.Kind, KindGateway) == KindGateway &&
					string(parentRef.Name) == upstreamGateway.Name {
					// If the parentRef has a section name, only attach the route if the
					// listener exists in the gateway.
					if parentRef.SectionName != nil {
						foundSectionName := false
						for _, listener := range upstreamGateway.Spec.Listeners {
							if listener.Name == *parentRef.SectionName {
								foundSectionName = true
								break
							}
						}
						if !foundSectionName {
							logger.Info("section name not found in gateway", "section_name", *parentRef.SectionName)
							continue
						}

						attachedRouteCount[*parentRef.SectionName]++
					} else {
						// Attached to all sections, update all counts
						for _, l := range upstreamGateway.Spec.Listeners {
							attachedRouteCount[l.Name]++
						}
					}

					attachedRoutes[client.ObjectKeyFromObject(&route)] = route
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

		if !controllerutil.ContainsFinalizer(&route, gatewayControllerGCFinalizer) {
			controllerutil.AddFinalizer(&route, gatewayControllerGCFinalizer)
			if err := upstreamClient.Update(ctx, &route); err != nil {
				result.Err = fmt.Errorf("failed to add finalizer to httproute: %w", err)
				return result
			}
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

		status.AttachedRoutes = attachedRouteCount[listener.Name]

		acceptedCondition := metav1.Condition{
			Type:               string(gatewayv1.ListenerConditionAccepted),
			Status:             metav1.ConditionTrue,
			Reason:             "Accepted",
			Message:            "The listener has been accepted by the Datum Gateway",
			ObservedGeneration: upstreamGateway.Generation,
		}

		programmedCondition := metav1.Condition{
			Type:               string(gatewayv1.ListenerConditionProgrammed),
			Status:             metav1.ConditionTrue,
			Reason:             "Programmed",
			Message:            "The listener has been programmed by the Datum Gateway",
			ObservedGeneration: upstreamGateway.Generation,
		}

		if listener.Hostname != nil && !slices.Contains(verifiedHostnames, string(*listener.Hostname)) {
			acceptedCondition.Status = metav1.ConditionFalse
			acceptedCondition.Reason = "UnverifiedHostname"
			acceptedCondition.Message = "The hostname defined on the listener has not been verified. Check status of Domains in the same namespace."

			programmedCondition.Status = metav1.ConditionFalse
			programmedCondition.Reason = acceptedCondition.Reason
			programmedCondition.Message = acceptedCondition.Message
		}

		apimeta.SetStatusCondition(&status.Conditions, acceptedCondition)

		// TODO(jreese) update this based on the downstream gateway's status
		apimeta.SetStatusCondition(&status.Conditions, programmedCondition)

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
		downstreamStrategy,
	)
	if err != nil {
		result.Err = err
		return result
	}

	routeResult, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, downstreamRoute, func() error {
		if err := downstreamStrategy.SetControllerReference(ctx, &upstreamRoute, downstreamRoute); err != nil {
			return fmt.Errorf("failed to set controller reference on downstream httproute: %w", err)
		}

		downstreamRoute.Spec = gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				// We currently only support same-namespace references, so just copy over
				// parentRefs from the upstream route.
				ParentRefs: upstreamRoute.Spec.ParentRefs,
			},
			Hostnames: upstreamRoute.Spec.Hostnames,
			Rules:     rules,
		}
		return nil
	})
	if err != nil {
		if apierrors.IsConflict(err) {
			result.RequeueAfter = 1 * time.Second
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

		desiredDownstreamResource := resource.DeepCopyObject()
		resourceResult, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, resource, func() error {
			switch obj := resource.(type) {
			case *corev1.Service:
				obj.Spec = desiredDownstreamResource.(*corev1.Service).Spec
			case *discoveryv1.EndpointSlice:
				desiredEndpointSlice := desiredDownstreamResource.(*discoveryv1.EndpointSlice)
				// Since endpointslices get duplicated for routes, add them as a controller
				// owner in the downstream control plane.
				if err := controllerutil.SetControllerReference(downstreamRoute, obj, downstreamClient.Scheme()); err != nil {
					return fmt.Errorf("failed setting owner on endpointslice: %w", err)
				}
				obj.AddressType = desiredEndpointSlice.AddressType
				obj.Endpoints = desiredEndpointSlice.Endpoints
				obj.Ports = desiredEndpointSlice.Ports
			case *gatewayv1alpha3.BackendTLSPolicy:
				obj.Spec = desiredDownstreamResource.(*gatewayv1alpha3.BackendTLSPolicy).Spec
			}
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
		if ptr.Deref(parent.ParentRef.Group, gatewayv1.GroupName) == gatewayv1.GroupName &&
			ptr.Deref(parent.ParentRef.Kind, KindGateway) == KindGateway &&
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
	downstreamStrategy downstreamclient.ResourceStrategy,
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

			if backendRef.Kind == nil {
				// Should not happen, as the default kind is Service
				continue
			}

			switch *backendRef.Kind {
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

				if backendRef.Port == nil {
					// Should be protected by validation, but check just in case.
					logger.Info("no port defined in backendRef", "backendRef", backendRef)
					return nil, nil, fmt.Errorf("no port defined in backendRef")
				}

				if !controllerutil.ContainsFinalizer(&upstreamEndpointSlice, gatewayControllerGCFinalizer) {
					controllerutil.AddFinalizer(&upstreamEndpointSlice, gatewayControllerGCFinalizer)
					if err := upstreamClient.Update(ctx, &upstreamEndpointSlice); err != nil {
						return nil, nil, fmt.Errorf("failed to add finalizer to endpointslice: %w", err)
					}
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

					if int32(*backendRef.Port) == *port.Port {
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
					logger.Info("port not found in upstream endpointslice", "endpointslice", upstreamEndpointSlice.Name, "port", *backendRef.Port)
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

				if err := downstreamStrategy.SetControllerReference(ctx, &upstreamEndpointSlice, downstreamEndpointSlice); err != nil {
					return nil, nil, fmt.Errorf("failed to set controller reference on downstream endpointslice: %w", err)
				}

				downstreamResources = append(downstreamResources, downstreamEndpointSlice)

				backendObjectReference := gatewayv1.BackendObjectReference{
					Namespace: ptr.To(gatewayv1.Namespace(downstreamGateway.Namespace)),
					Kind:      ptr.To(gatewayv1.Kind(KindService)),
					Name:      gatewayv1.ObjectName(downstreamService.Name),
					Port:      backendRef.Port,
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
					var hostname *gatewayv1.PreciseHostname
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
				logger.Info("unknown backend ref kind", "kind", *backendRef.Kind)
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

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	src := mcsource.TypedKind(
		&gatewayv1.Gateway{},
		downstreamclient.TypedEnqueueRequestForUpstreamOwner[*gatewayv1.Gateway](&gatewayv1.Gateway{}),
	)

	clusterSrc, _ := src.ForCluster("", r.DownstreamCluster)

	return mcbuilder.ControllerManagedBy(mgr).
		For(&gatewayv1.Gateway{}).
		Watches(
			&gatewayv1.HTTPRoute{},
			mchandler.EnqueueRequestsFromMapFunc(r.listGatewaysAttachedByHTTPRoute),
		).
		Watches(
			&discoveryv1.EndpointSlice{},
			r.listGatewaysForEndpointSliceFunc,
		).
		Watches(
			&networkingv1alpha.Domain{},
			r.listGatewaysForDomainFunc,
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
		if ptr.Deref(parentRef.Group, gatewayv1.GroupName) == gatewayv1.GroupName &&
			ptr.Deref(parentRef.Kind, KindGateway) == KindGateway {
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

// listGatewaysForEndpointSliceFunc creates an event handler that watches EndpointSlice changes
// and determines which Gateways need to be reconciled as a result of those changes.
//
// This function implements the watch pattern for EndpointSlice resources in multi-cluster scenarios.
// When an EndpointSlice changes (created, updated, or deleted), this handler:
//
//  1. Examines all HTTPRoutes in the cluster to find those that reference the changed EndpointSlice
//     as a backend (via BackendRefs with Kind=EndpointSlice)
//  2. For each matching HTTPRoute, identifies the parent Gateways referenced in ParentRefs
//  3. Returns reconcile requests for those Gateways so they can update their configuration
//
// This ensures that Gateway resources are automatically updated when their backend EndpointSlices
// change, maintaining proper traffic routing and load balancing.
func (r *GatewayReconciler) listGatewaysForEndpointSliceFunc(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
		endpointSlice := obj.(*discoveryv1.EndpointSlice)
		logger := log.FromContext(ctx)

		var httpRoutes gatewayv1.HTTPRouteList
		if err := cl.GetClient().List(ctx, &httpRoutes); err != nil {
			logger.Error(err, "failed to list HTTPRoutes")
			return nil
		}

		var requests []mcreconcile.Request

		for _, route := range httpRoutes.Items {
			for _, rule := range route.Spec.Rules {
				for _, backendRef := range rule.BackendRefs {
					if ptr.Deref(backendRef.Kind, "") == KindEndpointSlice {
						backendNamespace := string(ptr.Deref(backendRef.Namespace, gatewayv1.Namespace(route.Namespace)))

						if backendNamespace == endpointSlice.Namespace && string(backendRef.Name) == endpointSlice.Name {
							for _, parentRef := range route.Spec.ParentRefs {
								if ptr.Deref(parentRef.Group, gatewayv1.GroupName) == gatewayv1.GroupName &&
									ptr.Deref(parentRef.Kind, KindGateway) == KindGateway {
									gatewayNamespace := string(ptr.Deref(parentRef.Namespace, gatewayv1.Namespace(route.Namespace)))

									requests = append(requests, mcreconcile.Request{
										ClusterName: clusterName,
										Request: reconcile.Request{
											NamespacedName: types.NamespacedName{
												Namespace: gatewayNamespace,
												Name:      string(parentRef.Name),
											},
										},
									})
								}
							}
						}
					}
				}
			}
		}

		return requests
	})
}

func (r *GatewayReconciler) listGatewaysForDomainFunc(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
		domain := obj.(*networkingv1alpha.Domain)

		// Only enqueue if the domain is verified
		if !apimeta.IsStatusConditionTrue(domain.Status.Conditions, networkingv1alpha.DomainConditionVerified) {
			return nil
		}

		logger := log.FromContext(ctx)

		var gatewayList gatewayv1.GatewayList
		if err := cl.GetClient().List(ctx, &gatewayList, client.InNamespace(domain.Namespace)); err != nil {
			logger.Error(err, "failed to list Gateways")
			return nil
		}

		var requests []mcreconcile.Request

		for _, gateway := range gatewayList.Items {
			for _, l := range gateway.Spec.Listeners {
				if l.Hostname == nil {
					continue
				}
				hostname := string(*l.Hostname)

				if hostname == domain.Spec.DomainName || strings.HasSuffix(hostname, "."+domain.Spec.DomainName) {
					requests = append(requests, mcreconcile.Request{
						ClusterName: clusterName,
						Request: reconcile.Request{
							NamespacedName: client.ObjectKeyFromObject(&gateway),
						},
					})
					continue
				}
			}
		}

		return requests
	})
}

func listenerFactory(
	name,
	hostname string,
	protocol gatewayv1.ProtocolType,
	port gatewayv1.PortNumber,
	tlsSecretName string,
) gatewayv1.Listener {
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
					Name: gatewayv1.ObjectName(resourcename.GetValidDNS1123Name(tlsSecretName)),
				},
			},
		}
	}

	return listener
}
