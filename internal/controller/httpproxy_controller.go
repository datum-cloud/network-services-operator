// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	conditionutil "go.datum.net/network-services-operator/internal/util/condition"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

// HTTPProxyReconciler reconciles a HTTPProxy object
type HTTPProxyReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator
}

type desiredHTTPProxyResources struct {
	gateway        *gatewayv1.Gateway
	httpRoute      *gatewayv1.HTTPRoute
	endpointSlices []*discoveryv1.EndpointSlice
}

const (
	SchemeHTTP  = "http"
	SchemeHTTPS = "https"

	DefaultHTTPPort  = 80
	DefaultHTTPSPort = 443
)

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=httpproxies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=httpproxies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=httpproxies/finalizers,verbs=update

func (r *HTTPProxyReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)
	ctx = log.IntoContext(ctx, logger)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var httpProxy networkingv1alpha.HTTPProxy
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &httpProxy); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("reconciling httpproxy")
	defer logger.Info("reconcile complete")

	httpProxyCopy := httpProxy.DeepCopy()

	acceptedCondition := &metav1.Condition{
		Type:               networkingv1alpha.HTTPProxyConditionAccepted,
		Status:             metav1.ConditionFalse,
		Reason:             networkingv1alpha.HTTPProxyReasonPending,
		ObservedGeneration: httpProxy.Generation,
		Message:            "The HTTPProxy has not been scheduled",
	}

	programmedCondition := &metav1.Condition{
		Type:               networkingv1alpha.HTTPProxyConditionProgrammed,
		Status:             metav1.ConditionFalse,
		Reason:             networkingv1alpha.HTTPProxyReasonPending,
		ObservedGeneration: httpProxy.Generation,
		Message:            "The HTTPProxy has not been programmed",
	}

	defer func() {
		apimeta.SetStatusCondition(&httpProxyCopy.Status.Conditions, *acceptedCondition)
		apimeta.SetStatusCondition(&httpProxyCopy.Status.Conditions, *programmedCondition)

		if !equality.Semantic.DeepEqual(httpProxy.Status, httpProxyCopy.Status) {
			httpProxy.Status = httpProxyCopy.Status
			if statusErr := cl.GetClient().Status().Update(ctx, &httpProxy); statusErr != nil {
				err = errors.Join(err, fmt.Errorf("failed updating httpproxy status: %w", statusErr))
			}
			logger.Info("httpproxy status updated")
		}
	}()

	desiredResources, err := r.collectDesiredResources(&httpProxy)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to collect desired resources: %w", err)
	}

	// Maintain a Gateway for the HTTPProxy, handle conflicts in names by updating the
	// Programmed condition with info about the conflict.

	gateway := desiredResources.gateway.DeepCopy()

	result, err := controllerutil.CreateOrUpdate(ctx, cl.GetClient(), gateway, func() error {
		if hasControllerConflict(gateway, &httpProxy) {
			// return already exists error - a gateway exists with the name we want to
			// use, but it's owned by a different resource.
			return apierrors.NewAlreadyExists(gatewayv1.Resource("Gateway"), gateway.Name)
		}

		if err := controllerutil.SetControllerReference(&httpProxy, gateway, cl.GetScheme()); err != nil {
			return fmt.Errorf("failed to set controller on gateway: %w", err)
		}

		// Special handling for default gateway listeners, as the hostnames will be
		// updated by the controller. Only required on updates.
		if !gateway.CreationTimestamp.IsZero() {
			defaultHTTPListener := gatewayutil.GetListenerByName(gateway.Spec.Listeners, gatewayutil.DefaultHTTPListenerName)
			if defaultHTTPListener != nil {
				gatewayutil.SetListener(desiredResources.gateway, *defaultHTTPListener)
			}

			defaultHTTPSListener := gatewayutil.GetListenerByName(gateway.Spec.Listeners, gatewayutil.DefaultHTTPSListenerName)
			if defaultHTTPSListener != nil {
				gatewayutil.SetListener(desiredResources.gateway, *defaultHTTPSListener)
			}
		}

		gateway.Spec = desiredResources.gateway.Spec

		return nil
	})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			programmedCondition.Status = metav1.ConditionFalse
			programmedCondition.Reason = networkingv1alpha.HTTPProxyReasonConflict
			programmedCondition.Message = fmt.Sprintf("Underlying Gateway with the name %q already exists and is owned by a different resource.", gateway.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed updating gateway resource: %w", err)
	}

	logger.Info("processed gateway", "name", gateway.Name, "result", result)

	// Maintain an HTTPRoute for all rules in the HTTPProxy

	httpRoute := desiredResources.httpRoute.DeepCopy()

	result, err = controllerutil.CreateOrUpdate(ctx, cl.GetClient(), httpRoute, func() error {
		if hasControllerConflict(httpRoute, &httpProxy) {
			// return already exists error - an httproute exists with the name we want to
			// use, but it's owned by a different resource.
			return apierrors.NewAlreadyExists(gatewayv1.Resource("HTTPRoute"), httpRoute.Name)
		}

		if err := controllerutil.SetControllerReference(&httpProxy, httpRoute, cl.GetScheme()); err != nil {
			return fmt.Errorf("failed to set controller on httproute: %w", err)
		}

		httpRoute.Spec = desiredResources.httpRoute.Spec

		return nil
	})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			programmedCondition.Status = metav1.ConditionFalse
			programmedCondition.Reason = networkingv1alpha.HTTPProxyReasonConflict
			programmedCondition.Message = fmt.Sprintf("Underlying HTTPRoute with the name %q already exists and is owned by a different resource.", httpRoute.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed updating httproute resource: %w", err)
	}

	logger.Info("processed httproute", "name", httpRoute.Name, "result", result)

	for _, desiredEndpointSlice := range desiredResources.endpointSlices {
		endpointSlice := desiredEndpointSlice.DeepCopy()

		result, err := controllerutil.CreateOrUpdate(ctx, cl.GetClient(), endpointSlice, func() error {
			if hasControllerConflict(endpointSlice, &httpProxy) {
				// return already exists error - an endpointslice exists with the name we want to
				// use, but it's owned by a different resource.
				return apierrors.NewAlreadyExists(discoveryv1.Resource("EndpointSlice"), endpointSlice.Name)
			}

			if err := controllerutil.SetControllerReference(&httpProxy, endpointSlice, cl.GetScheme()); err != nil {
				return fmt.Errorf("failed to set controller reference on endpointslice: %w", err)
			}

			endpointSlice.AddressType = desiredEndpointSlice.AddressType
			endpointSlice.Endpoints = desiredEndpointSlice.Endpoints
			endpointSlice.Ports = desiredEndpointSlice.Ports
			return nil
		})

		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				programmedCondition.Status = metav1.ConditionFalse
				programmedCondition.Reason = networkingv1alpha.HTTPProxyReasonConflict
				programmedCondition.Message = fmt.Sprintf("Underlying EndpointSlice with the name %q already exists and is owned by a different resource.", endpointSlice.Name)
				return ctrl.Result{}, nil
			}

			return ctrl.Result{}, fmt.Errorf("failed to create or update endpointslice: %w", err)
		}

		logger.Info("processed endpointslice", "result", result, "name", desiredEndpointSlice.Name)
	}

	httpProxyCopy.Status.Addresses = gateway.Status.Addresses

	if c := apimeta.FindStatusCondition(gateway.Status.Conditions, string(gatewayv1.GatewayConditionAccepted)); c != nil {
		logger.Info("gateway accepted status", "status", c.Status)
		if c.Status == metav1.ConditionTrue {
			acceptedCondition.Status = metav1.ConditionTrue
			acceptedCondition.Reason = networkingv1alpha.HTTPProxyReasonAccepted
			acceptedCondition.Message = "The HTTPProxy has been scheduled"
		} else {
			acceptedCondition.Reason = c.Reason
		}
	}

	if c := apimeta.FindStatusCondition(gateway.Status.Conditions, string(gatewayv1.GatewayConditionProgrammed)); c != nil {
		if c.Status == metav1.ConditionTrue {
			programmedCondition.Status = metav1.ConditionTrue
			programmedCondition.Reason = networkingv1alpha.HTTPProxyReasonProgrammed
			programmedCondition.Message = "The HTTPProxy has been programmed"
		} else {
			programmedCondition.Reason = c.Reason
		}
	}

	r.reconcileHTTPProxyHostnameStatus(ctx, gateway, httpProxyCopy)

	return ctrl.Result{}, nil
}

func (r *HTTPProxyReconciler) reconcileHTTPProxyHostnameStatus(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	httpProxyCopy *networkingv1alpha.HTTPProxy,
) {
	logger := log.FromContext(ctx)

	gatewayAcceptedCondition := apimeta.FindStatusCondition(gateway.Status.Conditions, string(gatewayv1.GatewayConditionAccepted))
	if gatewayAcceptedCondition == nil {
		// Should never happen due to defaulting, but just in case
		logger.Info("accepted condition not found on gateway")
		return
	} else if gatewayAcceptedCondition.ObservedGeneration != gateway.Generation {
		logger.Info(
			"observed generation on accepted condition does not match generation on gateway, delaying processing",
			"gateway_generation", gateway.Generation,
			"condition_generation", gatewayAcceptedCondition.ObservedGeneration,
		)
		return
	}
	logger.Info("updating hostname status")

	var hostnames []gatewayv1.Hostname
	currentListenerStatus := map[gatewayv1.SectionName]gatewayv1.ListenerStatus{}
	for _, listener := range gateway.Status.Listeners {
		currentListenerStatus[listener.Name] = *listener.DeepCopy()
	}

	acceptedHostnames := sets.New[gatewayv1.Hostname]()
	nonAcceptedHostnames := sets.New[string]()
	inUseHostnames := sets.New[string]()
	for _, listener := range gateway.Spec.Listeners {
		if listener.Hostname == nil {
			// Should only happen shortly after creation, before the default hostnames
			// are assigned
			continue
		}

		listenerStatus, ok := currentListenerStatus[listener.Name]
		if !ok {
			logger.Info("listener status not found", "listener_name", listener.Name)
			continue
		}

		listenerAcceptedCondition := apimeta.FindStatusCondition(listenerStatus.Conditions, string(gatewayv1.ListenerConditionAccepted))
		if listenerAcceptedCondition != nil {
			if listenerAcceptedCondition.Status == metav1.ConditionTrue {
				acceptedHostnames.Insert(*listener.Hostname)
			} else if listenerAcceptedCondition.Reason == networkingv1alpha.HostnameInUseReason {
				inUseHostnames.Insert(string(*listener.Hostname))
			} else {
				nonAcceptedHostnames.Insert(string(*listener.Hostname))
			}
		} else {
			nonAcceptedHostnames.Insert(string(*listener.Hostname))
		}
	}

	acceptedHostnamesSlice := acceptedHostnames.UnsortedList()
	slices.Sort(acceptedHostnamesSlice)
	httpProxyCopy.Status.Hostnames = append(hostnames, acceptedHostnamesSlice...)

	if len(httpProxyCopy.Spec.Hostnames) > 0 {
		hostnamesVerifiedCondition := conditionutil.FindStatusConditionOrDefault(httpProxyCopy.Status.Conditions, &metav1.Condition{
			Type:   networkingv1alpha.HTTPProxyConditionHostnamesVerified,
			Status: metav1.ConditionFalse,
		})
		hostnamesVerifiedCondition.ObservedGeneration = httpProxyCopy.Generation

		if nonAcceptedHostnames.Len() > 0 {
			nonAcceptedHostnamesSlice := nonAcceptedHostnames.UnsortedList()
			slices.Sort(nonAcceptedHostnamesSlice)
			hostnamesVerifiedCondition.Status = metav1.ConditionFalse
			hostnamesVerifiedCondition.Reason = networkingv1alpha.UnverifiedHostnamesPresent
			hostnamesVerifiedCondition.Message = fmt.Sprintf("unverified hostnames present, check status of Domains in the same namespace: %s", strings.Join(nonAcceptedHostnamesSlice, ","))
		} else if acceptedHostnames.Len() == len(httpProxyCopy.Spec.Hostnames) || acceptedHostnames.Len() == len(httpProxyCopy.Spec.Hostnames)+1 {
			// acceptedHostnames may contain the default listener hostname if it has
			// not been removed by the user.
			hostnamesVerifiedCondition.Status = metav1.ConditionTrue
			hostnamesVerifiedCondition.Reason = networkingv1alpha.HTTPProxyReasonHostnamesVerified
			hostnamesVerifiedCondition.Message = "All hostnames have been accepted and programmed"
		} else {
			hostnamesVerifiedCondition.Status = metav1.ConditionFalse
			hostnamesVerifiedCondition.Reason = networkingv1alpha.HTTPProxyReasonPending
			hostnamesVerifiedCondition.Message = "Pending downstream Gateway status updates"
		}

		apimeta.SetStatusCondition(&httpProxyCopy.Status.Conditions, *hostnamesVerifiedCondition)

		if inUseHostnames.Len() > 0 {
			inUseHostnamesSlice := inUseHostnames.UnsortedList()
			slices.Sort(inUseHostnamesSlice)
			apimeta.SetStatusCondition(&httpProxyCopy.Status.Conditions, metav1.Condition{
				Type:               networkingv1alpha.HTTPProxyConditionHostnamesInUse,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: httpProxyCopy.Generation,
				Reason:             networkingv1alpha.HostnameInUseReason,
				Message:            fmt.Sprintf("Hostnames are already attached to another resource: %s", strings.Join(inUseHostnamesSlice, ",")),
			})
		} else {
			apimeta.RemoveStatusCondition(&httpProxyCopy.Status.Conditions, networkingv1alpha.HTTPProxyConditionHostnamesInUse)
		}
	} else {
		apimeta.RemoveStatusCondition(&httpProxyCopy.Status.Conditions, networkingv1alpha.HTTPProxyConditionHostnamesVerified)
		apimeta.RemoveStatusCondition(&httpProxyCopy.Status.Conditions, networkingv1alpha.HTTPProxyConditionHostnamesInUse)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *HTTPProxyReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.HTTPProxy{}).
		Owns(&gatewayv1.Gateway{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Owns(&discoveryv1.EndpointSlice{}).
		Named("httpproxy").
		Complete(r)
}

func (r *HTTPProxyReconciler) collectDesiredResources(
	httpProxy *networkingv1alpha.HTTPProxy,
) (*desiredHTTPProxyResources, error) {

	gateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: httpProxy.Namespace,
			Name:      httpProxy.Name,
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: r.Config.HTTPProxy.GatewayClassName,
		},
	}

	// Hostname fields will be nil on the default listeners until the gateway
	// controller updates them. There's special handling for this in the
	// CreateOrUpdate logic for maintaining the gateway.
	gatewayutil.SetDefaultListeners(gateway, r.Config.Gateway)

	// Add listeners for each hostname
	for i, hostname := range httpProxy.Spec.Hostnames {
		gateway.Spec.Listeners = append(gateway.Spec.Listeners, gatewayv1.Listener{
			Name:     gatewayv1.SectionName(fmt.Sprintf("%s-hostname-%d", SchemeHTTP, i)),
			Protocol: gatewayv1.HTTPProtocolType,
			Port:     DefaultHTTPPort,
			Hostname: ptr.To(hostname),
			AllowedRoutes: &gatewayv1.AllowedRoutes{
				Namespaces: &gatewayv1.RouteNamespaces{
					From: ptr.To(gatewayv1.NamespacesFromSame),
				},
			},
		})

		gateway.Spec.Listeners = append(gateway.Spec.Listeners, gatewayv1.Listener{
			Name:     gatewayv1.SectionName(fmt.Sprintf("%s-hostname-%d", SchemeHTTPS, i)),
			Protocol: gatewayv1.HTTPSProtocolType,
			Port:     DefaultHTTPSPort,
			Hostname: ptr.To(hostname),
			AllowedRoutes: &gatewayv1.AllowedRoutes{
				Namespaces: &gatewayv1.RouteNamespaces{
					From: ptr.To(gatewayv1.NamespacesFromSame),
				},
			},
			TLS: &gatewayv1.GatewayTLSConfig{
				Mode:    ptr.To(gatewayv1.TLSModeTerminate),
				Options: r.Config.Gateway.ListenerTLSOptions,
			},
		})
	}

	httpRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: httpProxy.Namespace,
			Name:      httpProxy.Name,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name: gatewayv1.ObjectName(gateway.Name),
					},
				},
			},
		},
	}

	var desiredEndpointSlices []*discoveryv1.EndpointSlice

	desiredRouteRules := make([]gatewayv1.HTTPRouteRule, len(httpProxy.Spec.Rules))
	for ruleIndex, rule := range httpProxy.Spec.Rules {
		backendRefs := make([]gatewayv1.HTTPBackendRef, len(rule.Backends))

		// Validation will prevent this from occurring, unless the maximum items for
		// backends is adjusted. The following error has been placed here so that
		// if/when that occurs, we're sure to address obvious programming changes
		// required (which should happen anyways, but just to be safe...).
		if len(rule.Backends) > 1 {
			return nil, fmt.Errorf("invalid number of backends for rule - expected 1 got %d", len(rule.Backends))
		}

		for backendIndex, backend := range rule.Backends {
			appProtocol := SchemeHTTP
			backendPort := DefaultHTTPPort

			u, err := url.Parse(backend.Endpoint)
			if err != nil {
				return nil, fmt.Errorf("failed parsing endpoint for backend %d in rule %d: %w", backendIndex, ruleIndex, err)
			}

			if u.Scheme == SchemeHTTPS {
				backendPort = DefaultHTTPSPort
				appProtocol = SchemeHTTPS
			}

			if endpointPort := u.Port(); endpointPort != "" {
				backendPort, err = strconv.Atoi(endpointPort)
				if err != nil {
					return nil, fmt.Errorf("failed parsing endpoint port for backend %d in rule %d: %w", backendIndex, ruleIndex, err)
				}
			}

			// TODO(jreese) Move away from FQDN and EndpointSlices
			//
			// The FQDN AddressType has been deprecated, but as we control the
			// programming of an HTTPProxy, we can easily transition to an alternative
			// once we have one. This is in an effort to not block MVP goals.
			addressType := discoveryv1.AddressTypeFQDN

			host := u.Hostname()
			isIPAddress := false
			if ip := net.ParseIP(host); ip != nil {
				isIPAddress = true
				if i := ip.To4(); i != nil && len(i) == net.IPv4len {
					addressType = discoveryv1.AddressTypeIPv4
				} else {
					addressType = discoveryv1.AddressTypeIPv6
				}
			}

			// Determine the hostname to use for TLS validation and URL rewriting.
			// For HTTPS backends, we need a hostname for certificate validation.
			var tlsHostname string
			if backend.TLS != nil && backend.TLS.Hostname != nil {
				// Use explicitly configured TLS hostname
				tlsHostname = *backend.TLS.Hostname
			} else if !isIPAddress {
				// Use hostname from the endpoint URL
				tlsHostname = host
			}

			// For HTTPS backends, we must have a hostname for TLS validation
			if appProtocol == SchemeHTTPS && tlsHostname == "" {
				return nil, fmt.Errorf("backend %d in rule %d: HTTPS endpoint with IP address requires tls.hostname to be specified for certificate validation", backendIndex, ruleIndex)
			}

			// Add URLRewrite filter with hostname for TLS validation if we have one
			if tlsHostname != "" {
				hostnameRewriteFound := false
				for _, filter := range rule.Filters {
					if filter.Type == gatewayv1.HTTPRouteFilterURLRewrite {
						filter.URLRewrite.Hostname = ptr.To(gatewayv1.PreciseHostname(tlsHostname))
						hostnameRewriteFound = true
						break
					}
				}

				if !hostnameRewriteFound {
					rule.Filters = append(rule.Filters, gatewayv1.HTTPRouteFilter{
						Type: gatewayv1.HTTPRouteFilterURLRewrite,
						URLRewrite: &gatewayv1.HTTPURLRewriteFilter{
							Hostname: ptr.To(gatewayv1.PreciseHostname(tlsHostname)),
						},
					})
				}
			}

			endpointSlice := &discoveryv1.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: httpProxy.Namespace,
					Name:      fmt.Sprintf("%s-%d-%d", httpProxy.Name, ruleIndex, backendIndex),
				},
				AddressType: addressType,
				Endpoints: []discoveryv1.Endpoint{
					{
						Addresses: []string{
							host,
						},
						Conditions: discoveryv1.EndpointConditions{
							Ready:       ptr.To(true),
							Serving:     ptr.To(true),
							Terminating: ptr.To(false),
						},
					},
				},
				Ports: []discoveryv1.EndpointPort{
					{
						Name:        ptr.To(fmt.Sprintf("httpproxy-%d-%d", ruleIndex, backendIndex)),
						Protocol:    ptr.To(v1.ProtocolTCP),
						AppProtocol: ptr.To(appProtocol),
						Port:        ptr.To(int32(backendPort)),
					},
				},
			}

			desiredEndpointSlices = append(desiredEndpointSlices, endpointSlice)

			backendRefs[backendIndex] = gatewayv1.HTTPBackendRef{
				BackendRef: gatewayv1.BackendRef{
					BackendObjectReference: gatewayv1.BackendObjectReference{
						Group: ptr.To(gatewayv1.Group("discovery.k8s.io")),
						Kind:  ptr.To(gatewayv1.Kind("EndpointSlice")),
						Name:  gatewayv1.ObjectName(endpointSlice.Name),
						Port:  ptr.To(gatewayv1.PortNumber(backendPort)),
					},
				},
				Filters: backend.Filters,
			}
		}

		desiredRouteRules[ruleIndex] = gatewayv1.HTTPRouteRule{
			Name:        rule.Name,
			Matches:     rule.Matches,
			Filters:     rule.Filters,
			BackendRefs: backendRefs,
		}
	}

	httpRoute.Spec.Rules = desiredRouteRules

	return &desiredHTTPProxyResources{
		gateway:        gateway,
		httpRoute:      httpRoute,
		endpointSlices: desiredEndpointSlices,
	}, nil
}

func hasControllerConflict(obj, owner metav1.Object) bool {
	if t := obj.GetCreationTimestamp(); t.IsZero() {
		return false
	}

	return controllerutil.HasControllerReference(obj) && !metav1.IsControlledBy(obj, owner)
}
