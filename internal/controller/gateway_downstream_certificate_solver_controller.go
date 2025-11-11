package controller

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/davecgh/go-spew/spew"
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/config"
)

type GatewayDownstreamCertificateSolverReconciler struct {
	Config config.NetworkServicesOperator

	DownstreamCluster cluster.Cluster
}

func (r *GatewayDownstreamCertificateSolverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "namespace", req.Namespace, "name", req.Name)
	ctx = log.IntoContext(ctx, logger)

	cl := r.DownstreamCluster.GetClient()

	certificate := newUnstructuredForGVK(certificateGVK)
	if err := cl.Get(ctx, req.NamespacedName, certificate); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	conditions, found, err := unstructured.NestedSlice(certificate.Object, "status", "conditions")
	if err != nil || !found {
		return ctrl.Result{}, err
	}

	isReady := false
	for _, cond := range conditions {
		condMap, ok := cond.(map[string]interface{})
		if !ok {
			continue
		}
		if condMap["type"] == "Ready" && condMap["status"] == "True" {
			isReady = true
			break
		}
	}

	if isReady {
		// Nothing to do - certificate is already ready
		return ctrl.Result{}, nil
	}

	owningGatewayRef := metav1.GetControllerOf(certificate)
	if owningGatewayRef == nil {
		// Nothing to do - not a certificate we care about
		return ctrl.Result{}, nil
	}

	// Make sure the issuer ref is one we care about
	issuerRef, found, err := unstructured.NestedMap(certificate.Object, "spec", "issuerRef")
	if err != nil || !found {
		return ctrl.Result{}, fmt.Errorf("failed to get issuerRef from certificate")
	}

	// Skip any non cluster issuers
	issuerKind, found, err := unstructured.NestedString(issuerRef, "kind")
	if err != nil || !found || issuerKind != "ClusterIssuer" {
		return ctrl.Result{}, nil
	}

	issuerName, found, err := unstructured.NestedString(issuerRef, "name")
	if err != nil || !found {
		return ctrl.Result{}, fmt.Errorf("failed to get issuer name from certificate")
	}

	// Check if this issuer is in our configured list
	foundIssuer := false
	for _, v := range r.Config.Gateway.ClusterIssuerMap {
		if v == issuerName {
			foundIssuer = true
			break
		}
	}

	if !foundIssuer {
		// Not an issuer we care about
		return ctrl.Result{}, nil
	}

	logger.Info("Found non-ready downstream certificate owned by Gateway", "gateway", owningGatewayRef.Name)

	var gateway gatewayv1.Gateway
	gatewayNamespacedName := client.ObjectKey{
		Namespace: req.Namespace,
		Name:      owningGatewayRef.Name,
	}
	if err := cl.Get(ctx, gatewayNamespacedName, &gateway); err != nil {
		if apierrors.IsNotFound(err) {
			// Gateway was deleted. A certificate is driven from a Gateway, so we
			// wouldn't get here unless the Gateway was deleted.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	var orders unstructured.UnstructuredList
	orders.SetGroupVersionKind(orderGVK)
	if err := cl.List(ctx, &orders, client.InNamespace(req.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	// Find the Order associated with the certificate
	var order *unstructured.Unstructured
	for i, o := range orders.Items {
		annotations := o.GetAnnotations()
		if annotations[certManagerCertificateNameAnnotation] == certificate.GetName() {
			order = &orders.Items[i]
			break
		}
	}

	if order == nil {
		logger.Info("No cert-manager Order found for downstream certificate")
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	logger.Info("Found cert-manager Order for downstream certificate", "order", orders.Items[0].GetName())

	var challenges unstructured.UnstructuredList
	challenges.SetGroupVersionKind(challengeGVK)
	if err := cl.List(ctx, &challenges, client.InNamespace(req.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	var challenge *unstructured.Unstructured
	for i, c := range challenges.Items {
		owner := metav1.GetControllerOf(&c)
		spew.Dump(owner)
		if owner != nil && owner.UID == order.GetUID() {
			challenge = &challenges.Items[i]
			break
		}
	}

	if challenge == nil {
		logger.Info("No cert-manager Challenge found for cert-manager Order", "order", order.GetName())
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	logger.Info("Found cert-manager Challenge for cert-manager Order", "challenge", challenge.GetName())

	// Read spec.key and spec.token from the challenge
	key, found, err := unstructured.NestedString(challenge.Object, "spec", "key")
	if err != nil || !found {
		logger.Error(err, "spec.key not found in cert-manager Challenge")
		return ctrl.Result{}, err
	}
	token, found, err := unstructured.NestedString(challenge.Object, "spec", "token")
	if err != nil || !found {
		logger.Error(err, "spec.token not found in cert-manager Challenge")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully retrieved key and token from cert-manager Challenge for downstream certificate")

	// Create HTTPRouteFilter with inline direct response
	httpRouteFilter := &envoygatewayv1alpha1.HTTPRouteFilter{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: req.Namespace,
			Name:      challenge.GetName(),
		},
	}

	if err := controllerutil.SetControllerReference(challenge, httpRouteFilter, r.DownstreamCluster.GetScheme()); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set controller reference on HTTPRouteFilter: %w", err)
	}

	result, err := controllerutil.CreateOrUpdate(ctx, cl, httpRouteFilter, func() error {
		httpRouteFilter.Spec = envoygatewayv1alpha1.HTTPRouteFilterSpec{
			DirectResponse: &envoygatewayv1alpha1.HTTPDirectResponseFilter{
				ContentType: ptr.To("text/plain"),
				StatusCode:  ptr.To(http.StatusOK),
				Body: &envoygatewayv1alpha1.CustomResponseBody{
					Type:   ptr.To(envoygatewayv1alpha1.ResponseValueTypeInline),
					Inline: ptr.To(key),
				},
			},
		}

		return nil
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create or update HTTPRouteFilter: %w", err)
	}
	logger.Info("HTTPRouteFilter reconciled", "result", result)

	// Attach HTTPRoute at expected path with direct response filter
	httpRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: req.Namespace,
			Name:      challenge.GetName(),
		},
	}

	if err := controllerutil.SetControllerReference(challenge, httpRoute, r.DownstreamCluster.GetScheme()); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set controller reference on HTTPRoute: %w", err)
	}

	result, err = controllerutil.CreateOrUpdate(ctx, cl, httpRoute, func() error {
		httpRoute.Spec = gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name: gatewayv1.ObjectName(gateway.GetName()),
					},
				},
			},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{
							Path: &gatewayv1.HTTPPathMatch{
								Type:  ptr.To(gatewayv1.PathMatchExact),
								Value: ptr.To(fmt.Sprintf("/.well-known/acme-challenge/%s", token)),
							},
						},
					},
					Filters: []gatewayv1.HTTPRouteFilter{
						{
							Type: gatewayv1.HTTPRouteFilterExtensionRef,
							ExtensionRef: &gatewayv1.LocalObjectReference{
								Group: envoygatewayv1alpha1.GroupName,
								Kind:  envoygatewayv1alpha1.KindHTTPRouteFilter,
								Name:  gatewayv1.ObjectName(httpRouteFilter.GetName()),
							},
						},
					},
				},
			},
		}

		return nil
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create or update HTTPRoute: %w", err)
	}
	logger.Info("HTTPRoute reconciled", "result", result)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayDownstreamCertificateSolverReconciler) SetupWithManager(mgr mcmanager.Manager) error {

	downstreamCertificateSource := source.TypedKind(
		r.DownstreamCluster.GetCache(),
		newUnstructuredForGVK(certificateGVK),
		&handler.TypedEnqueueRequestForObject[*unstructured.Unstructured]{},
	)

	return ctrl.NewControllerManagedBy(mgr.GetLocalManager()).
		WatchesRawSource(downstreamCertificateSource).
		Named("downstream-certificate-solver").
		Complete(r)
}

var (
	certManagerGV = schema.GroupVersion{
		Group:   "cert-manager.io",
		Version: "v1",
	}
	acmeCertManagerGV = schema.GroupVersion{
		Group:   "acme.cert-manager.io",
		Version: "v1",
	}

	certificateGVK = certManagerGV.WithKind("Certificate")
	orderGVK       = acmeCertManagerGV.WithKind("Order")
	challengeGVK   = acmeCertManagerGV.WithKind("Challenge")
)
