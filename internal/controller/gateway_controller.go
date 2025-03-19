// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	mcbuilder "github.com/multicluster-runtime/multicluster-runtime/pkg/builder"
	mcmanager "github.com/multicluster-runtime/multicluster-runtime/pkg/manager"
	mcreconcile "github.com/multicluster-runtime/multicluster-runtime/pkg/reconcile"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
}

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/finalizers,verbs=update

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

	if !gateway.DeletionTimestamp.IsZero() {
		// TODO(jreese) Finalizer
		return ctrl.Result{}, nil
	}

	logger.Info("reconciling gateway")
	defer logger.Info("reconcile complete")

	downstreamStrategy := getDownstreamResourceStrategy(cl)

	result, downstreamGateway := r.ensureDownstreamGateway(ctx, cl.GetClient(), &gateway, downstreamStrategy)
	if result.ShouldReturn() {
		return result.Finish(ctx)
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

	return result.Finish(ctx)
}

func (r *GatewayReconciler) ensureDownstreamGateway(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	downstreamStrategy DownstreamResourceStrategy,
) (result Result, downstreamGateway *gatewayv1.Gateway) {
	logger := log.FromContext(ctx)

	downstreamClient := downstreamStrategy.GetClient()

	downstreamGateway = &gatewayv1.Gateway{
		ObjectMeta: downstreamStrategy.GetDownstreamObjectMeta(upstreamGateway),
	}

	hostnames := []string{
		fmt.Sprintf("%s.prism.global.datum-dns.net", upstreamGateway.UID),
		fmt.Sprintf("v4.%s.prism.global.datum-dns.net", upstreamGateway.UID),
		fmt.Sprintf("v6.%s.prism.global.datum-dns.net", upstreamGateway.UID),
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, downstreamClient, downstreamGateway, func() error {
		if err := controllerutil.SetControllerReference(upstreamGateway, downstreamGateway, downstreamClient.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference on downstream gateway: %w", err)
		}

		// TODO(jreese) validate tls certificateRefs
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

		// TODO(jreese) build transform libraries - look at envoy proxy for inspiration

		// TODO(jreese)
		// - Can attach HTTPRoutes to multiple parent Gateway sections
		// - Create a DNSEndpoint for each gateway, with endpoints for v4, v6,
		//	and dual stack.

		downstreamGateway.Spec = gatewayv1.GatewaySpec{
			// TODO(jreese) get from "scheduler"
			GatewayClassName: "envoy-gateway",

			// TODO(jreese) get from "scheduler"
			// May need separate v4 and v6 gateways to align DNS automagically
			Addresses: []gatewayv1.GatewayAddress{},

			Listeners: []gatewayv1.Listener{},

			// Ignored fields - placed here to be clear about intent
			//
			// Infrastructure: &gatewayv1.GatewayInfrastructure{},
			// BackendTLS: &gatewayv1.GatewayBackendTLS{},
		}

		listenerFactory := func(name, hostname string, protocol gatewayv1.ProtocolType, port gatewayv1.PortNumber) gatewayv1.Listener {
			h := gatewayv1.Hostname(hostname)

			fromSelector := gatewayv1.NamespacesFromAll
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
	}); err != nil {
		if apierrors.IsConflict(err) {
			result.Requeue = true
			return result, nil
		}
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

	return result, downstreamGateway
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
				// Example: gateway.datumapis.com/external-global-proxy-controller
				//
				// https://github.com/envoyproxy/gateway/blob/4143f5c8eb2d468c093cca8871e6eb18262aef7e/internal/provider/kubernetes/predicates.go#L122
				//	https://github.com/envoyproxy/gateway/blob/4143f5c8eb2d468c093cca8871e6eb18262aef7e/internal/provider/kubernetes/controller.go#L1231
				// https://github.com/envoyproxy/gateway/blob/4143f5c8eb2d468c093cca8871e6eb18262aef7e/internal/provider/kubernetes/predicates.go#L44
				return o.Spec.GatewayClassName == "datum-external-global-proxy"
			}),
		)).
		// TODO(jreese) watch other clusters
		Owns(&gatewayv1.Gateway{}).
		Named("gateway").
		Complete(r)
}
