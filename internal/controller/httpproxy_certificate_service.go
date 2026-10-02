// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	certificatesv1alpha1 "go.datum.net/network-services-operator/internal/certificates/v1alpha1"
)

const certificateProvisioningMessage = "We're provisioning and applying a certificate to this hostname - it may take a few minutes"

// tlsCertificateReadyCondition derives a hostname's CertificateReady condition
// from its TLSCertificate in the project control plane. A listener still served
// by a cert-manager Certificate that is not yet due for renewal has no
// TLSCertificate, so that Certificate answers for it until the switch.
func (r *HTTPProxyReconciler) tlsCertificateReadyCondition(
	ctx context.Context,
	upstreamClient client.Client,
	downstreamClient client.Client,
	downstreamNamespace string,
	upstreamNamespace string,
	certName string,
	legacyName string,
	generation int64,
) metav1.Condition {
	condition := metav1.Condition{
		Type:               networkingv1alpha.HostnameConditionCertificateReady,
		ObservedGeneration: generation,
	}

	var cert certificatesv1alpha1.TLSCertificate
	err := upstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace, Name: certName}, &cert)
	switch {
	case err == nil:
		condition.Status, condition.Reason, condition.Message = tlsCertificateReadyState(&cert)
		return condition
	case !apierrors.IsNotFound(err):
		condition.Status = metav1.ConditionUnknown
		condition.Reason = networkingv1alpha.CertificateReadyReasonPending
		condition.Message = fmt.Sprintf("Failed to get certificate: %v", err)
		return condition
	}

	legacy := newUnstructuredForGVK(certificateGVK)
	if err := downstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespace, Name: legacyName}, legacy); err == nil {
		ready, readyErr := isCertificateReady(legacy)
		switch {
		case readyErr != nil:
			condition.Status = metav1.ConditionUnknown
			condition.Reason = networkingv1alpha.CertificateReadyReasonPending
			condition.Message = fmt.Sprintf("Failed to check certificate status: %v", readyErr)
		case ready:
			condition.Status = metav1.ConditionTrue
			condition.Reason = networkingv1alpha.CertificateReadyReasonCertificateIssued
			condition.Message = "Certificate is ready"
		default:
			condition.Status = metav1.ConditionFalse
			condition.Reason, condition.Message = getCertificateReadyConditionReason(legacy)
		}
		return condition
	}

	condition.Status = metav1.ConditionFalse
	condition.Reason = networkingv1alpha.CertificateReadyReasonPending
	condition.Message = certificateProvisioningMessage
	return condition
}

// tlsCertificateReadyState maps TLSCertificate conditions onto the
// CertificateReady vocabulary: Ready wins, a rejected spec is a provisioning
// failure carrying the service's explanation, an in-flight order is a
// challenge in progress, and anything else is pending.
func tlsCertificateReadyState(cert *certificatesv1alpha1.TLSCertificate) (metav1.ConditionStatus, string, string) {
	if apimeta.IsStatusConditionTrue(cert.Status.Conditions, certificatesv1alpha1.ConditionReady) {
		return metav1.ConditionTrue, networkingv1alpha.CertificateReadyReasonCertificateIssued, "Certificate is ready"
	}

	if accepted := apimeta.FindStatusCondition(cert.Status.Conditions, certificatesv1alpha1.ConditionAccepted); accepted != nil && accepted.Status == metav1.ConditionFalse {
		message := accepted.Message
		if message == "" {
			message = "Certificate request was rejected"
		}
		return metav1.ConditionFalse, networkingv1alpha.CertificateReadyReasonProvisioningFailed, message
	}

	if issuing := apimeta.FindStatusCondition(cert.Status.Conditions, certificatesv1alpha1.ConditionIssuing); issuing != nil && issuing.Status == metav1.ConditionTrue {
		message := certificateProvisioningMessage
		if issuing.Message != "" {
			message = fmt.Sprintf("%s (%s)", certificateProvisioningMessage, issuing.Message)
		}
		return metav1.ConditionFalse, networkingv1alpha.CertificateReadyReasonChallengeInProgress, message
	}

	return metav1.ConditionFalse, networkingv1alpha.CertificateReadyReasonPending, certificateProvisioningMessage
}

// enqueueHTTPProxyForTLSCertificate follows TLSCertificate -> Gateway ->
// HTTPProxy through controller references in the same project control plane.
func (r *HTTPProxyReconciler) enqueueHTTPProxyForTLSCertificate(clusterName multicluster.ClusterName, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
		gatewayRef := metav1.GetControllerOf(obj)
		if gatewayRef == nil || gatewayRef.Kind != KindGateway {
			return nil
		}

		var gateway gatewayv1.Gateway
		if err := cl.GetClient().Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: gatewayRef.Name}, &gateway); err != nil {
			if !apierrors.IsNotFound(err) {
				log.FromContext(ctx).Error(err, "failed to get Gateway owning TLSCertificate", "tlscertificate", obj.GetName())
			}
			return nil
		}

		proxyRef := metav1.GetControllerOf(&gateway)
		if proxyRef == nil || proxyRef.Kind != KindHTTPProxy {
			return nil
		}

		return []mcreconcile.Request{{
			ClusterName: clusterName,
			Request:     ctrl.Request{NamespacedName: client.ObjectKey{Namespace: gateway.Namespace, Name: proxyRef.Name}},
		}}
	})
}
