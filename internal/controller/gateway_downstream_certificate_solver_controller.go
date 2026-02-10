package controller

import (
	"context"
	"fmt"
	"net/http"

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

// GatewayDownstreamCertificateSolverReconciler watches cert-manager Challenge resources
// and creates HTTP Routes to serve ACME HTTP-01 challenge responses for Gateway-owned
// certificates. By watching Challenges directly (rather than Certificates), this
// controller correctly handles both initial certificate issuance and renewals.
type GatewayDownstreamCertificateSolverReconciler struct {
	Config config.NetworkServicesOperator

	DownstreamCluster cluster.Cluster
}

func (r *GatewayDownstreamCertificateSolverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "namespace", req.Namespace, "challenge", req.Name)
	ctx = log.IntoContext(ctx, logger)

	logger.Info("Reconciling ACME challenge solver")

	cl := r.DownstreamCluster.GetClient()

	// Get the Challenge that triggered this reconciliation
	challenge := newUnstructuredForGVK(challengeGVK)
	if err := cl.Get(ctx, req.NamespacedName, challenge); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Challenge not found, might have been deleted after successful validation")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get downstream challenge")
		return ctrl.Result{}, err
	}

	// Find the owning Order from the Challenge's controller reference
	orderRef := metav1.GetControllerOf(challenge)
	if orderRef == nil {
		logger.Info("Challenge has no owning Order, skipping")
		return ctrl.Result{}, nil
	}

	order := newUnstructuredForGVK(orderGVK)
	orderKey := client.ObjectKey{Namespace: req.Namespace, Name: orderRef.Name}
	if err := cl.Get(ctx, orderKey, order); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Owning Order not found", "order", orderRef.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Get the Certificate name from the Order's annotation
	certName, found := order.GetAnnotations()[certManagerCertificateNameAnnotation]
	if !found || certName == "" {
		logger.Info("Order does not have certificate name annotation", "order", order.GetName())
		return ctrl.Result{}, nil
	}

	// Get the Certificate
	certificate := newUnstructuredForGVK(certificateGVK)
	certKey := client.ObjectKey{Namespace: req.Namespace, Name: certName}
	if err := cl.Get(ctx, certKey, certificate); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Certificate not found", "certificate", certName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Make sure the issuer ref is one we care about
	issuerRef, found, err := unstructured.NestedMap(certificate.Object, "spec", "issuerRef")
	if err != nil || !found {
		logger.Error(err, "Failed to get issuerRef from downstream certificate")
		return ctrl.Result{}, fmt.Errorf("failed to get issuerRef from certificate")
	}

	// Skip any non cluster issuers
	issuerKind, found, err := unstructured.NestedString(issuerRef, "kind")
	if err != nil || !found || issuerKind != "ClusterIssuer" {
		logger.Info("Issuer kind is not ClusterIssuer, skipping")
		return ctrl.Result{}, nil
	}

	issuerName, found, err := unstructured.NestedString(issuerRef, "name")
	if err != nil || !found {
		logger.Error(err, "Failed to get issuer name from downstream certificate")
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
		logger.Info("Issuer name not in configured list, skipping", "issuerName", issuerName)
		// Not an issuer we care about
		return ctrl.Result{}, nil
	}

	// Verify the Certificate is owned by a Gateway
	owningGatewayRef := metav1.GetControllerOf(certificate)
	if owningGatewayRef == nil {
		logger.Info("Downstream certificate has no owning Gateway, skipping")
		// Nothing to do - not a certificate we care about
		return ctrl.Result{}, nil
	}

	// Get the Gateway
	var gateway gatewayv1.Gateway
	gatewayKey := client.ObjectKey{Namespace: req.Namespace, Name: owningGatewayRef.Name}
	if err := cl.Get(ctx, gatewayKey, &gateway); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Owning Gateway not found", "gateway", owningGatewayRef.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Processing challenge for Gateway-owned certificate",
		"certificate", certName,
		"gateway", gateway.Name,
		"order", order.GetName())

	// Read spec.key and spec.token from the challenge
	key, found, err := unstructured.NestedString(challenge.Object, "spec", "key")
	if err != nil || !found {
		return ctrl.Result{}, fmt.Errorf("spec.key not found in cert-manager Challenge")
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
		httpRouteFilter.Labels = map[string]string{
			"meta.datumapis.com/http01-solver": "true",
		}
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
		httpRoute.Labels = map[string]string{
			"meta.datumapis.com/http01-solver": "true",
		}
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
	// Watch Challenge resources directly - this ensures we handle both initial
	// certificate issuance and renewals, since cert-manager creates new Challenges
	// for renewals even when the Certificate is still marked as Ready.
	downstreamChallengeSource := source.TypedKind(
		r.DownstreamCluster.GetCache(),
		newUnstructuredForGVK(challengeGVK),
		&handler.TypedEnqueueRequestForObject[*unstructured.Unstructured]{},
	)

	return ctrl.NewControllerManagedBy(mgr.GetLocalManager()).
		WatchesRawSource(downstreamChallengeSource).
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

// isCertificateReady checks if a cert-manager Certificate has Ready=True condition.
func isCertificateReady(certificate *unstructured.Unstructured) (bool, error) {
	conditions, found, err := unstructured.NestedSlice(certificate.Object, "status", "conditions")
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	for _, cond := range conditions {
		condMap, ok := cond.(map[string]any)
		if !ok {
			continue
		}
		if condMap["type"] == "Ready" && condMap["status"] == string(metav1.ConditionTrue) {
			return true, nil
		}
	}
	return false, nil
}
