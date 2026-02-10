// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
	conditionutil "go.datum.net/network-services-operator/internal/util/condition"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

// HTTPProxyReconciler reconciles a HTTPProxy object
type HTTPProxyReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator

	DownstreamCluster cluster.Cluster
}

type desiredHTTPProxyResources struct {
	gateway          *gatewayv1.Gateway
	httpRoute        *gatewayv1.HTTPRoute
	endpointSlices   []*discoveryv1.EndpointSlice
	httpRouteFilters []*envoygatewayv1alpha1.HTTPRouteFilter
}

const httpProxyFinalizer = "networking.datumapis.com/httpproxy-cleanup"
const connectorOfflineFilterPrefix = "connector-offline"

const (
	SchemeHTTP  = "http"
	SchemeHTTPS = "https"

	DefaultHTTPPort  = 80
	DefaultHTTPSPort = 443
)

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=httpproxies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=httpproxies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=httpproxies/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.envoyproxy.io,resources=httproutefilters,verbs=get;list;watch;create;update;patch;delete

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

	if !httpProxy.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&httpProxy, httpProxyFinalizer) {
			if err := r.cleanupConnectorEnvoyPatchPolicy(ctx, cl.GetClient(), req.ClusterName, &httpProxy); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&httpProxy, httpProxyFinalizer)
			if err := cl.GetClient().Update(ctx, &httpProxy); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	logger.Info("reconciling httpproxy")
	defer logger.Info("reconcile complete")

	if updated := ensureConnectorNameAnnotation(&httpProxy); updated {
		if err := cl.GetClient().Update(ctx, &httpProxy); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed updating httpproxy connector annotation: %w", err)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&httpProxy, httpProxyFinalizer) {
		controllerutil.AddFinalizer(&httpProxy, httpProxyFinalizer)
		if err := cl.GetClient().Update(ctx, &httpProxy); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

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

	tunnelMetadataCondition := &metav1.Condition{
		Type:               networkingv1alpha.HTTPProxyConditionTunnelMetadataProgrammed,
		Status:             metav1.ConditionFalse,
		Reason:             networkingv1alpha.HTTPProxyReasonPending,
		ObservedGeneration: httpProxy.Generation,
		Message:            "Waiting for downstream EnvoyPatchPolicy to be accepted and programmed",
	}
	setTunnelMetadataCondition := false

	defer func() {
		apimeta.SetStatusCondition(&httpProxyCopy.Status.Conditions, *acceptedCondition)
		apimeta.SetStatusCondition(&httpProxyCopy.Status.Conditions, *programmedCondition)
		if setTunnelMetadataCondition {
			apimeta.SetStatusCondition(&httpProxyCopy.Status.Conditions, *tunnelMetadataCondition)
		} else {
			apimeta.RemoveStatusCondition(&httpProxyCopy.Status.Conditions, networkingv1alpha.HTTPProxyConditionTunnelMetadataProgrammed)
		}

		if !equality.Semantic.DeepEqual(httpProxy.Status, httpProxyCopy.Status) {
			httpProxy.Status = httpProxyCopy.Status
			if statusErr := cl.GetClient().Status().Update(ctx, &httpProxy); statusErr != nil {
				err = errors.Join(err, fmt.Errorf("failed updating httpproxy status: %w", statusErr))
			}
			logger.Info("httpproxy status updated")
		}
	}()

	desiredResources, err := r.collectDesiredResources(ctx, cl.GetClient(), &httpProxy)
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

	if len(desiredResources.httpRouteFilters) == 0 {
		if err := cleanupConnectorOfflineHTTPRouteFilter(ctx, cl.GetClient(), &httpProxy); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		for _, desiredFilter := range desiredResources.httpRouteFilters {
			httpRouteFilter := desiredFilter.DeepCopy()
			result, err := controllerutil.CreateOrUpdate(ctx, cl.GetClient(), httpRouteFilter, func() error {
				if err := controllerutil.SetControllerReference(&httpProxy, httpRouteFilter, cl.GetScheme()); err != nil {
					return fmt.Errorf("failed to set controller on HTTPRouteFilter: %w", err)
				}
				httpRouteFilter.Spec = desiredFilter.Spec
				return nil
			})
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed updating httproutefilter resource: %w", err)
			}
			logger.Info("processed httproutefilter", "name", httpRouteFilter.Name, "result", result)
		}
	}

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

	patchPolicy, hasConnectorBackends, err := r.reconcileConnectorEnvoyPatchPolicy(
		ctx,
		cl.GetClient(),
		req.ClusterName,
		&httpProxy,
		gateway,
	)
	if err != nil {
		programmedCondition.Status = metav1.ConditionFalse
		programmedCondition.Reason = networkingv1alpha.HTTPProxyReasonPending
		programmedCondition.Message = err.Error()
		return ctrl.Result{}, err
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

	if hasConnectorBackends {
		connectorPolicyReady, connectorPolicyMessage := downstreamPatchPolicyReady(
			patchPolicy,
			r.Config.Gateway.DownstreamGatewayClassName,
		)
		if !connectorPolicyReady {
			programmedCondition.Status = metav1.ConditionFalse
			programmedCondition.Reason = networkingv1alpha.HTTPProxyReasonPending
			if connectorPolicyMessage == "" {
				connectorPolicyMessage = "Waiting for downstream EnvoyPatchPolicy to be accepted and programmed"
			}
			programmedCondition.Message = connectorPolicyMessage

			tunnelMetadataCondition.Status = metav1.ConditionFalse
			tunnelMetadataCondition.Reason = networkingv1alpha.HTTPProxyReasonPending
			tunnelMetadataCondition.Message = connectorPolicyMessage
		} else {
			tunnelMetadataCondition.Status = metav1.ConditionTrue
			tunnelMetadataCondition.Reason = networkingv1alpha.HTTPProxyReasonTunnelMetadataApplied
			tunnelMetadataCondition.Message = "Connector tunnel metadata applied"
		}
		setTunnelMetadataCondition = true
	} else {
		apimeta.RemoveStatusCondition(&httpProxyCopy.Status.Conditions, networkingv1alpha.HTTPProxyConditionTunnelMetadataProgrammed)
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

func ensureConnectorNameAnnotation(httpProxy *networkingv1alpha.HTTPProxy) bool {
	var connectorName string
	for _, rule := range httpProxy.Spec.Rules {
		for _, backend := range rule.Backends {
			if backend.Connector != nil && backend.Connector.Name != "" {
				if connectorName == "" {
					connectorName = backend.Connector.Name
				} else if connectorName != backend.Connector.Name {
					// Prefer first connector for annotation stability if multiple are present.
					break
				}
			}
		}
	}

	annotations := httpProxy.GetAnnotations()
	if connectorName == "" {
		if annotations == nil {
			return false
		}
		if _, ok := annotations[networkingv1alpha1.ConnectorNameAnnotation]; !ok {
			return false
		}
		delete(annotations, networkingv1alpha1.ConnectorNameAnnotation)
		if len(annotations) == 0 {
			httpProxy.SetAnnotations(nil)
		} else {
			httpProxy.SetAnnotations(annotations)
		}
		return true
	}

	if annotations == nil {
		annotations = map[string]string{}
	}
	if annotations[networkingv1alpha1.ConnectorNameAnnotation] == connectorName {
		return false
	}
	annotations[networkingv1alpha1.ConnectorNameAnnotation] = connectorName
	httpProxy.SetAnnotations(annotations)
	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *HTTPProxyReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	builder := mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.HTTPProxy{}).
		Owns(&gatewayv1.Gateway{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Owns(&discoveryv1.EndpointSlice{}).
		// Watch Connectors and reconcile HTTPProxies that reference them.
		// This ensures EnvoyPatchPolicy headers are updated when a Connector's
		// publicKey.id changes (e.g., after connector restart/reconnect).
		Watches(
			&networkingv1alpha1.Connector{},
			func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
				return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					logger := log.FromContext(ctx)

					connector, ok := obj.(*networkingv1alpha1.Connector)
					if !ok {
						return nil
					}

					// List all HTTPProxies in the same namespace
					var httpProxies networkingv1alpha.HTTPProxyList
					if err := cl.GetClient().List(ctx, &httpProxies, client.InNamespace(connector.Namespace)); err != nil {
						logger.Error(err, "failed to list HTTPProxies for Connector watch", "connector", connector.Name)
						return nil
					}

					var requests []mcreconcile.Request
					for i := range httpProxies.Items {
						httpProxy := &httpProxies.Items[i]
						// Check if this HTTPProxy references the changed Connector
						if httpProxyReferencesConnector(httpProxy, connector.Name) {
							requests = append(requests, mcreconcile.Request{
								ClusterName: clusterName,
								Request: ctrl.Request{
									NamespacedName: client.ObjectKeyFromObject(httpProxy),
								},
							})
						}
					}

					if len(requests) > 0 {
						logger.Info("Connector changed, requeueing HTTPProxies",
							"connector", connector.Name,
							"httpProxyCount", len(requests))
					}

					return requests
				})
			},
		)

	if r.DownstreamCluster != nil {
		downstreamPolicySource := mcsource.TypedKind(
			&envoygatewayv1alpha1.EnvoyPatchPolicy{},
			downstreamclient.TypedEnqueueRequestForUpstreamOwner[*envoygatewayv1alpha1.EnvoyPatchPolicy](&networkingv1alpha.HTTPProxy{}),
		)

		downstreamPolicyClusterSource, _ := downstreamPolicySource.ForCluster("", r.DownstreamCluster)
		builder = builder.WatchesRawSource(downstreamPolicyClusterSource)
	}

	return builder.Named("httpproxy").Complete(r)
}

// httpProxyReferencesConnector checks if an HTTPProxy has any backends
// that reference the given Connector name.
func httpProxyReferencesConnector(httpProxy *networkingv1alpha.HTTPProxy, connectorName string) bool {
	for _, rule := range httpProxy.Spec.Rules {
		for _, backend := range rule.Backends {
			if backend.Connector != nil && backend.Connector.Name == connectorName {
				return true
			}
		}
	}
	return false
}

func (r *HTTPProxyReconciler) collectDesiredResources(
	ctx context.Context,
	cl client.Client,
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
	var desiredRouteFilters []*envoygatewayv1alpha1.HTTPRouteFilter

	desiredRouteRules := make([]gatewayv1.HTTPRouteRule, len(httpProxy.Spec.Rules))
	for ruleIndex, rule := range httpProxy.Spec.Rules {
		ruleFilters := slices.Clone(rule.Filters)
		backendRefs := make([]gatewayv1.HTTPBackendRef, len(rule.Backends))
		offlineRuleSet := false

		// Validation will prevent this from occurring, unless the maximum items for
		// backends is adjusted. The following error has been placed here so that
		// if/when that occurs, we're sure to address obvious programming changes
		// required (which should happen anyways, but just to be safe...).
		if len(rule.Backends) > 1 {
			return nil, fmt.Errorf("invalid number of backends for rule - expected 1 got %d", len(rule.Backends))
		}

		for backendIndex, backend := range rule.Backends {
			if backend.Connector != nil {
				ready, err := connectorReady(ctx, cl, httpProxy.Namespace, backend.Connector.Name)
				if err != nil {
					return nil, err
				}
				if !ready {
					filterName := connectorOfflineFilterName(httpProxy)
					ruleFilters = append(ruleFilters, gatewayv1.HTTPRouteFilter{
						Type: gatewayv1.HTTPRouteFilterExtensionRef,
						ExtensionRef: &gatewayv1.LocalObjectReference{
							Group: envoygatewayv1alpha1.GroupName,
							Kind:  envoygatewayv1alpha1.KindHTTPRouteFilter,
							Name:  gatewayv1.ObjectName(filterName),
						},
					})
					desiredRouteRules[ruleIndex] = gatewayv1.HTTPRouteRule{
						Name:        rule.Name,
						Matches:     rule.Matches,
						Filters:     ruleFilters,
						BackendRefs: nil,
					}
					if len(desiredRouteFilters) == 0 {
						desiredRouteFilters = append(desiredRouteFilters, buildConnectorOfflineHTTPRouteFilter(httpProxy))
					}
					offlineRuleSet = true
					break
				}
			}

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
			endpointHost := host
			isIPAddress := false
			if backend.Connector != nil {
				// Connector backends don't rely on EndpointSlice addresses; use a safe placeholder.
				endpointHost = "connector.invalid"
				addressType = discoveryv1.AddressTypeFQDN
			} else if ip := net.ParseIP(host); ip != nil {
				isIPAddress = true
				if i := ip.To4(); i != nil && len(i) == net.IPv4len {
					addressType = discoveryv1.AddressTypeIPv4
				} else {
					addressType = discoveryv1.AddressTypeIPv6
				}
			}

			// For HTTPS endpoints with IP addresses, require tls.hostname for certificate validation
			// and use it as the Host header for the upstream request.
			if u.Scheme == "https" && isIPAddress {
				if backend.TLS == nil || backend.TLS.Hostname == nil || *backend.TLS.Hostname == "" {
					return nil, fmt.Errorf("HTTPS endpoint with IP address requires tls.hostname for backend %d in rule %d", backendIndex, ruleIndex)
				}
				// Use tls.hostname for the Host header rewrite
				hostnameRewriteFound := false
				for i, filter := range ruleFilters {
					if filter.Type == gatewayv1.HTTPRouteFilterURLRewrite {
						ruleFilters[i].URLRewrite.Hostname = ptr.To(gatewayv1.PreciseHostname(*backend.TLS.Hostname))
						hostnameRewriteFound = true
						break
					}
				}
				if !hostnameRewriteFound {
					ruleFilters = append(ruleFilters, gatewayv1.HTTPRouteFilter{
						Type: gatewayv1.HTTPRouteFilterURLRewrite,
						URLRewrite: &gatewayv1.HTTPURLRewriteFilter{
							Hostname: ptr.To(gatewayv1.PreciseHostname(*backend.TLS.Hostname)),
						},
					})
				}
			} else if !isIPAddress && backend.Connector == nil {
				// For FQDN endpoints, rewrite the Host header to match the backend hostname
				hostnameRewriteFound := false
				for i, filter := range ruleFilters {
					if filter.Type == gatewayv1.HTTPRouteFilterURLRewrite {
						ruleFilters[i].URLRewrite.Hostname = ptr.To(gatewayv1.PreciseHostname(host))
						hostnameRewriteFound = true
						break
					}
				}

				if !hostnameRewriteFound {
					ruleFilters = append(ruleFilters, gatewayv1.HTTPRouteFilter{
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
							endpointHost,
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

		if offlineRuleSet {
			continue
		}

		desiredRouteRules[ruleIndex] = gatewayv1.HTTPRouteRule{
			Name:        rule.Name,
			Matches:     rule.Matches,
			Filters:     ruleFilters,
			BackendRefs: backendRefs,
		}
	}

	httpRoute.Spec.Rules = desiredRouteRules

	return &desiredHTTPProxyResources{
		gateway:          gateway,
		httpRoute:        httpRoute,
		endpointSlices:   desiredEndpointSlices,
		httpRouteFilters: desiredRouteFilters,
	}, nil
}

func hasControllerConflict(obj, owner metav1.Object) bool {
	if t := obj.GetCreationTimestamp(); t.IsZero() {
		return false
	}

	return controllerutil.HasControllerReference(obj) && !metav1.IsControlledBy(obj, owner)
}

type connectorBackendPatch struct {
	// Gateway listener section name (default-https, etc.).
	sectionName *gatewayv1.SectionName

	// Identify the HTTPRoute rule/match this backend applies to.
	ruleIndex  int
	matchIndex int

	targetHost string
	targetPort int
	nodeID     string
}

func (r *HTTPProxyReconciler) reconcileConnectorEnvoyPatchPolicy(
	ctx context.Context,
	upstreamClient client.Client,
	clusterName string,
	httpProxy *networkingv1alpha.HTTPProxy,
	gateway *gatewayv1.Gateway,
) (*envoygatewayv1alpha1.EnvoyPatchPolicy, bool, error) {
	if r.DownstreamCluster == nil {
		return nil, false, nil
	}

	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(
		clusterName,
		upstreamClient,
		r.DownstreamCluster.GetClient(),
	)
	downstreamNamespaceName, err := downstreamStrategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, httpProxy.Namespace)
	if err != nil {
		return nil, false, err
	}

	connectorBackends, err := collectConnectorBackends(ctx, upstreamClient, httpProxy)
	if err != nil {
		return nil, false, err
	}

	policyName := fmt.Sprintf("connector-%s", httpProxy.Name)
	policyKey := client.ObjectKey{Namespace: downstreamNamespaceName, Name: policyName}
	downstreamClient := downstreamStrategy.GetClient()

	if len(connectorBackends) == 0 {
		var existing envoygatewayv1alpha1.EnvoyPatchPolicy
		if err := downstreamClient.Get(ctx, policyKey, &existing); err != nil {
			if apierrors.IsNotFound(err) {
				if err := downstreamStrategy.DeleteAnchorForObject(ctx, httpProxy); err != nil {
					return nil, false, err
				}
				return nil, false, nil
			}
			return nil, false, err
		}
		if err := downstreamClient.Delete(ctx, &existing); err != nil {
			return nil, false, err
		}
		if err := downstreamStrategy.DeleteAnchorForObject(ctx, httpProxy); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	// Wait for the Gateway to be Programmed before creating the EnvoyPatchPolicy.
	// This ensures the RouteConfiguration exists in Envoy's xDS, so the patch
	// can be applied immediately rather than waiting for Envoy Gateway to retry.
	gatewayProgrammed := apimeta.IsStatusConditionTrue(
		gateway.Status.Conditions,
		string(gatewayv1.GatewayConditionProgrammed),
	)
	if !gatewayProgrammed {
		// Gateway not yet programmed; requeue will happen when Gateway status changes.
		return nil, true, nil
	}

	if r.Config.Gateway.DownstreamGatewayClassName == "" {
		return nil, true, fmt.Errorf("downstreamGatewayClassName is required for connector patching")
	}

	jsonPatches, err := buildConnectorEnvoyPatches(
		downstreamNamespaceName,
		gateway,
		httpProxy,
		connectorBackends,
	)
	if err != nil {
		return nil, true, err
	}

	policy := envoygatewayv1alpha1.EnvoyPatchPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: downstreamNamespaceName,
			Name:      policyName,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, downstreamClient, &policy, func() error {
		if err := downstreamStrategy.SetControllerReference(ctx, httpProxy, &policy); err != nil {
			return err
		}
		policy.Spec = envoygatewayv1alpha1.EnvoyPatchPolicySpec{
			TargetRef: gatewayv1alpha2.LocalPolicyTargetReference{
				Group: gatewayv1.GroupName,
				Kind:  "GatewayClass",
				Name:  gatewayv1.ObjectName(r.Config.Gateway.DownstreamGatewayClassName),
			},
			Type:        envoygatewayv1alpha1.JSONPatchEnvoyPatchType,
			JSONPatches: jsonPatches,
		}
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	return &policy, true, nil
}

func (r *HTTPProxyReconciler) cleanupConnectorEnvoyPatchPolicy(
	ctx context.Context,
	upstreamClient client.Client,
	clusterName string,
	httpProxy *networkingv1alpha.HTTPProxy,
) error {
	if r.DownstreamCluster == nil {
		return nil
	}

	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(
		clusterName,
		upstreamClient,
		r.DownstreamCluster.GetClient(),
	)
	downstreamNamespaceName, err := downstreamStrategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, httpProxy.Namespace)
	if err != nil {
		return err
	}

	policyName := fmt.Sprintf("connector-%s", httpProxy.Name)
	policyKey := client.ObjectKey{Namespace: downstreamNamespaceName, Name: policyName}
	downstreamClient := downstreamStrategy.GetClient()

	var policy envoygatewayv1alpha1.EnvoyPatchPolicy
	if err := downstreamClient.Get(ctx, policyKey, &policy); err != nil {
		if apierrors.IsNotFound(err) {
			return downstreamStrategy.DeleteAnchorForObject(ctx, httpProxy)
		}
		return err
	}
	if err := downstreamClient.Delete(ctx, &policy); err != nil {
		return err
	}
	return downstreamStrategy.DeleteAnchorForObject(ctx, httpProxy)
}

func cleanupConnectorOfflineHTTPRouteFilter(ctx context.Context, cl client.Client, httpProxy *networkingv1alpha.HTTPProxy) error {
	filterKey := client.ObjectKey{Namespace: httpProxy.Namespace, Name: connectorOfflineFilterName(httpProxy)}
	var filter envoygatewayv1alpha1.HTTPRouteFilter
	if err := cl.Get(ctx, filterKey, &filter); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return cl.Delete(ctx, &filter)
}

func downstreamPatchPolicyReady(policy *envoygatewayv1alpha1.EnvoyPatchPolicy, gatewayClassName string) (bool, string) {
	if policy == nil {
		return false, "Downstream EnvoyPatchPolicy not found"
	}

	if len(policy.Status.Ancestors) == 0 {
		return false, "Downstream EnvoyPatchPolicy has no status yet"
	}

	for _, ancestor := range policy.Status.Ancestors {
		if ptr.Deref(ancestor.AncestorRef.Kind, gatewayv1.Kind("")) != gatewayv1.Kind("GatewayClass") ||
			ancestor.AncestorRef.Name != gatewayv1.ObjectName(gatewayClassName) {
			continue
		}

		accepted := apimeta.FindStatusCondition(ancestor.Conditions, "Accepted")
		if accepted == nil || accepted.Status != metav1.ConditionTrue {
			return false, formatPolicyConditionMessage("Accepted", accepted)
		}

		programmed := apimeta.FindStatusCondition(ancestor.Conditions, "Programmed")
		if programmed == nil || programmed.Status != metav1.ConditionTrue {
			return false, formatPolicyConditionMessage("Programmed", programmed)
		}

		return true, ""
	}

	return false, fmt.Sprintf("Downstream EnvoyPatchPolicy has no ancestor status for GatewayClass %q", gatewayClassName)
}

func formatPolicyConditionMessage(conditionType string, condition *metav1.Condition) string {
	if condition == nil {
		return fmt.Sprintf("Downstream EnvoyPatchPolicy is missing %s condition", conditionType)
	}
	if condition.Message == "" {
		return fmt.Sprintf("Downstream EnvoyPatchPolicy %s=%s (%s)", condition.Type, condition.Status, condition.Reason)
	}
	return fmt.Sprintf("Downstream EnvoyPatchPolicy %s=%s (%s): %s", condition.Type, condition.Status, condition.Reason, condition.Message)
}

func collectConnectorBackends(
	ctx context.Context,
	cl client.Client,
	httpProxy *networkingv1alpha.HTTPProxy,
) ([]connectorBackendPatch, error) {
	connectorBackends := make([]connectorBackendPatch, 0)
	for ruleIndex, rule := range httpProxy.Spec.Rules {
		matchCount := len(rule.Matches)
		if matchCount == 0 {
			matchCount = 1
		}
		for matchIndex := 0; matchIndex < matchCount; matchIndex++ {
			for _, backend := range rule.Backends {
				if backend.Connector == nil {
					continue
				}

				targetHost, targetPort, err := backendEndpointTarget(backend)
				if err != nil {
					return nil, err
				}

				connectorReady, nodeID, err := connectorPatchDetails(ctx, cl, httpProxy.Namespace, backend.Connector.Name)
				if err != nil {
					return nil, err
				}
				if !connectorReady {
					continue
				}

				connectorBackends = append(connectorBackends, connectorBackendPatch{
					sectionName: nil,
					ruleIndex:   ruleIndex,
					matchIndex:  matchIndex,
					targetHost:  targetHost,
					targetPort:  targetPort,
					nodeID:      nodeID,
				})
			}
		}
	}
	return connectorBackends, nil
}

func backendEndpointTarget(backend networkingv1alpha.HTTPProxyRuleBackend) (string, int, error) {
	u, err := url.Parse(backend.Endpoint)
	if err != nil {
		return "", 0, fmt.Errorf("failed parsing backend endpoint: %w", err)
	}

	targetHost := u.Hostname()
	if targetHost == "" {
		return "", 0, fmt.Errorf("backend endpoint host is required")
	}

	targetPort := DefaultHTTPPort
	if u.Scheme == SchemeHTTPS {
		targetPort = DefaultHTTPSPort
	}
	if endpointPort := u.Port(); endpointPort != "" {
		targetPort, err = strconv.Atoi(endpointPort)
		if err != nil {
			return "", 0, fmt.Errorf("invalid backend endpoint port: %w", err)
		}
	}

	return targetHost, targetPort, nil
}

func connectorOfflineFilterName(httpProxy *networkingv1alpha.HTTPProxy) string {
	return fmt.Sprintf("%s-%s", connectorOfflineFilterPrefix, httpProxy.Name)
}

func buildConnectorOfflineHTTPRouteFilter(httpProxy *networkingv1alpha.HTTPProxy) *envoygatewayv1alpha1.HTTPRouteFilter {
	return &envoygatewayv1alpha1.HTTPRouteFilter{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: httpProxy.Namespace,
			Name:      connectorOfflineFilterName(httpProxy),
		},
		Spec: envoygatewayv1alpha1.HTTPRouteFilterSpec{
			DirectResponse: &envoygatewayv1alpha1.HTTPDirectResponseFilter{
				ContentType: ptr.To("text/plain; charset=utf-8"),
				StatusCode:  ptr.To(http.StatusServiceUnavailable),
				Body: &envoygatewayv1alpha1.CustomResponseBody{
					Type:   ptr.To(envoygatewayv1alpha1.ResponseValueTypeInline),
					Inline: ptr.To("Tunnel not online"),
				},
			},
		},
	}
}

func connectorReady(ctx context.Context, cl client.Client, namespace, name string) (bool, error) {
	var connector networkingv1alpha1.Connector
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &connector); err != nil {
		return false, err
	}
	return apimeta.IsStatusConditionTrue(connector.Status.Conditions, networkingv1alpha1.ConnectorConditionReady), nil
}

func connectorPatchDetails(ctx context.Context, cl client.Client, namespace, name string) (bool, string, error) {
	var connector networkingv1alpha1.Connector
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &connector); err != nil {
		return false, "", err
	}

	ready := apimeta.IsStatusConditionTrue(connector.Status.Conditions, networkingv1alpha1.ConnectorConditionReady)
	if !ready {
		return false, "", nil
	}

	details := connector.Status.ConnectionDetails
	if details == nil || details.Type != networkingv1alpha1.PublicKeyConnectorConnectionType || details.PublicKey == nil {
		return false, "", fmt.Errorf("connector %q does not have public key connection details", name)
	}
	if details.PublicKey.Id == "" {
		return false, "", fmt.Errorf("connector %q public key id is empty", name)
	}
	return true, details.PublicKey.Id, nil
}

func buildConnectorEnvoyPatches(
	downstreamNamespace string,
	gateway *gatewayv1.Gateway,
	httpProxy *networkingv1alpha.HTTPProxy,
	backends []connectorBackendPatch,
) ([]envoygatewayv1alpha1.EnvoyJSONPatchConfig, error) {
	routeConfigNames := connectorRouteConfigNames(downstreamNamespace, gateway)
	patches := make([]envoygatewayv1alpha1.EnvoyJSONPatchConfig, 0)
	// TODO: Make this idempotent
	headersOp := envoygatewayv1alpha1.JSONPatchOperationType("add")

	// Connector traffic is routed to the local iroh-gateway instance (sidecar)
	// via a bootstrap-defined cluster. The connector-specific tunnel destination
	// is selected per request via headers injected below.
	clusterJSON, err := json.Marshal("iroh-gateway")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cluster name: %w", err)
	}

	// Enable CONNECT (and thus Extended CONNECT / WebSocket) on HTTPS listeners by
	// patching the HCM upgrade_configs. Doing this via EnvoyPatchPolicy avoids
	// gateway-level BackendTrafficPolicy, which can break route matching and cause
	// 404s when combined with connector route patches (iroh-gateway cluster).
	for _, listenerName := range routeConfigNames {
		upgradeConfigsJSON, err := json.Marshal([]map[string]any{
			{"upgrade_type": "CONNECT"},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal upgrade_configs: %w", err)
		}
		patches = append(patches, envoygatewayv1alpha1.EnvoyJSONPatchConfig{
			Type: "type.googleapis.com/envoy.config.listener.v3.Listener",
			Name: listenerName,
			Operation: envoygatewayv1alpha1.JSONPatchOperation{
				Op:   envoygatewayv1alpha1.JSONPatchOperationType("add"),
				Path: ptr.To("/default_filter_chain/filters/0/typed_config/upgrade_configs"),
				Value: &apiextensionsv1.JSON{Raw: upgradeConfigsJSON},
			},
		})
	}

	for _, routeConfigName := range routeConfigNames {
		for _, backend := range backends {
			jsonPath := connectorRouteJSONPath(
				downstreamNamespace,
				gateway,
				httpProxy.Name,
				backend.sectionName,
				backend.ruleIndex,
				backend.matchIndex,
			)

			headersJSON, err := json.Marshal([]map[string]any{
				{
					"header": map[string]any{
						"key":   "x-datum-target-host",
						"value": backend.targetHost,
					},
					"append_action": "OVERWRITE_IF_EXISTS_OR_ADD",
				},
				{
					"header": map[string]any{
						"key":   "x-datum-target-port",
						"value": strconv.Itoa(backend.targetPort),
					},
					"append_action": "OVERWRITE_IF_EXISTS_OR_ADD",
				},
				{
					"header": map[string]any{
						"key":   "x-iroh-endpoint-id",
						"value": backend.nodeID,
					},
					"append_action": "OVERWRITE_IF_EXISTS_OR_ADD",
				},
			})
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request headers: %w", err)
			}

			patches = append(patches,
				// 1) Send matched requests to the iroh-gateway cluster.
				envoygatewayv1alpha1.EnvoyJSONPatchConfig{
					Type: "type.googleapis.com/envoy.config.route.v3.RouteConfiguration",
					Name: routeConfigName,
					Operation: envoygatewayv1alpha1.JSONPatchOperation{
						Op:       envoygatewayv1alpha1.JSONPatchOperationType("replace"),
						JSONPath: ptr.To(jsonPath),
						Path:     ptr.To("/route/cluster"),
						Value:    &apiextensionsv1.JSON{Raw: clusterJSON},
					},
				},
				// 2) Inject internal control headers based on the selected route/backend.
				//    The client does not send these.
				envoygatewayv1alpha1.EnvoyJSONPatchConfig{
					Type: "type.googleapis.com/envoy.config.route.v3.RouteConfiguration",
					Name: routeConfigName,
					Operation: envoygatewayv1alpha1.JSONPatchOperation{
						Op:       headersOp,
						JSONPath: ptr.To(jsonPath),
						Path:     ptr.To("/request_headers_to_add"),
						Value:    &apiextensionsv1.JSON{Raw: headersJSON},
					},
				},
			)
		}
	}

	return patches, nil
}

func connectorRouteConfigNames(downstreamNamespace string, gateway *gatewayv1.Gateway) []string {
	routeConfigNames := []string{}
	for _, listener := range gateway.Spec.Listeners {
		if listener.Protocol != gatewayv1.HTTPSProtocolType {
			continue
		}
		routeConfigNames = append(routeConfigNames, fmt.Sprintf("%s/%s/%s", downstreamNamespace, gateway.Name, listener.Name))
	}
	return routeConfigNames
}

func connectorRouteJSONPath(
	downstreamNamespace string,
	gateway *gatewayv1.Gateway,
	httpRouteName string,
	sectionName *gatewayv1.SectionName,
	ruleIndex int,
	matchIndex int,
) string {
	// vhost matches the Gateway + optional sectionName
	vhostConstraints := fmt.Sprintf(
		`@.metadata.filter_metadata["envoy-gateway"].resources[0].kind=="%s" && @.metadata.filter_metadata["envoy-gateway"].resources[0].namespace=="%s" && @.metadata.filter_metadata["envoy-gateway"].resources[0].name=="%s"`,
		KindGateway,
		downstreamNamespace,
		gateway.Name,
	)

	if sectionName != nil {
		vhostConstraints += fmt.Sprintf(
			` && @.metadata.filter_metadata["envoy-gateway"].resources[0].sectionName=="%s"`,
			string(*sectionName),
		)
	}

	// routes match the HTTPRoute + rule/match
	routeConstraints := fmt.Sprintf(
		`@.metadata.filter_metadata["envoy-gateway"].resources[0].kind=="%s" && @.metadata.filter_metadata["envoy-gateway"].resources[0].namespace=="%s" && @.metadata.filter_metadata["envoy-gateway"].resources[0].name=="%s" && @.name =~ ".*?/rule/%d/match/%d/.*"`,
		KindHTTPRoute,
		downstreamNamespace,
		httpRouteName,
		ruleIndex,
		matchIndex,
	)

	return sanitizeJSONPath(fmt.Sprintf(
		`..virtual_hosts[?(%s)]..routes[?(!@.bogus && %s)]`,
		vhostConstraints,
		routeConstraints,
	))
}
