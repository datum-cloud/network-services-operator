// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	var hostnames []gatewayv1.Hostname
	// Copy over addresses for the TargetDomain, as they are also configured
	// as hostnames. Eventually we will also copy over hostnames which have been
	// successfully programmed on the HTTPProxy (custom domains need to be added,
	// along with validation for them)
	for _, address := range gateway.Status.Addresses {
		if ptr.Deref(address.Type, gatewayv1.IPAddressType) == gatewayv1.HostnameAddressType &&
			strings.HasSuffix(address.Value, r.Config.Gateway.TargetDomain) {
			hostnames = append(hostnames, gatewayv1.Hostname(address.Value))
		}
	}

	httpProxyCopy.Status.Hostnames = hostnames

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

	return ctrl.Result{}, nil
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
			Listeners: []gatewayv1.Listener{
				{
					Name:     SchemeHTTP,
					Protocol: gatewayv1.HTTPProtocolType,
					Port:     DefaultHTTPPort,
					AllowedRoutes: &gatewayv1.AllowedRoutes{
						Namespaces: &gatewayv1.RouteNamespaces{
							From: ptr.To(gatewayv1.NamespacesFromSame),
						},
					},
				},
				{
					Name:     SchemeHTTPS,
					Protocol: gatewayv1.HTTPSProtocolType,
					Port:     DefaultHTTPSPort,
					AllowedRoutes: &gatewayv1.AllowedRoutes{
						Namespaces: &gatewayv1.RouteNamespaces{
							From: ptr.To(gatewayv1.NamespacesFromSame),
						},
					},
					TLS: &gatewayv1.GatewayTLSConfig{
						Mode:    ptr.To(gatewayv1.TLSModeTerminate),
						Options: r.Config.HTTPProxy.GatewayTLSOptions,
					},
				},
			},
		},
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
			if ip := net.ParseIP(host); ip != nil {
				if i := ip.To4(); i != nil && len(i) == net.IPv4len {
					addressType = discoveryv1.AddressTypeIPv4
				} else {
					addressType = discoveryv1.AddressTypeIPv6
				}
			} else {

				hostnameRewriteFound := false
				for _, filter := range rule.Filters {
					if filter.Type == gatewayv1.HTTPRouteFilterURLRewrite {
						filter.URLRewrite.Hostname = ptr.To(gatewayv1.PreciseHostname(host))
						hostnameRewriteFound = true
						break
					}
				}

				if !hostnameRewriteFound {
					rule.Filters = append(rule.Filters, gatewayv1.HTTPRouteFilter{
						Type: gatewayv1.HTTPRouteFilterURLRewrite,
						URLRewrite: &gatewayv1.HTTPURLRewriteFilter{
							Hostname: ptr.To(gatewayv1.PreciseHostname(host)),
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
