// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

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

// tlsCertificateReadyCondition derives a wildcard hostname's CertificateReady
// condition from its TLSCertificate in the project control plane, the Secret
// serving it, and what the gateway reports about its listener.
func (r *HTTPProxyReconciler) tlsCertificateReadyCondition(
	ctx context.Context,
	upstreamClient client.Client,
	downstreamClient client.Client,
	downstreamNamespace string,
	upstreamNamespace string,
	certName string,
	secretName string,
	hostname string,
	listenerConditions []metav1.Condition,
	generation int64,
) metav1.Condition {
	condition := metav1.Condition{
		Type:               networkingv1alpha.HostnameConditionCertificateReady,
		ObservedGeneration: generation,
	}

	blocked := apimeta.FindStatusCondition(listenerConditions, listenerConditionCertificateRenewalBlocked)
	if blocked != nil && blocked.Status != metav1.ConditionTrue {
		blocked = nil
	}
	if blocked == nil {
		if issuance := apimeta.FindStatusCondition(listenerConditions, listenerConditionCertificateIssuanceBlocked); issuance != nil && issuance.Status == metav1.ConditionTrue {
			blocked = issuance
		}
	}

	var cert certificatesv1alpha1.TLSCertificate
	err := upstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace, Name: certName}, &cert)
	switch {
	case apierrors.IsNotFound(err):
		condition.Status = metav1.ConditionFalse
		condition.Reason = networkingv1alpha.CertificateReadyReasonPending
		condition.Message = certificateProvisioningMessage
		if blocked != nil {
			condition.Reason = networkingv1alpha.CertificateReadyReasonProvisioningFailed
			condition.Message = blocked.Message
		}
		return condition
	case err != nil:
		condition.Status = metav1.ConditionUnknown
		condition.Reason = networkingv1alpha.CertificateReadyReasonPending
		condition.Message = fmt.Sprintf("Failed to get certificate: %v", err)
		return condition
	}

	condition.Status, condition.Reason, condition.Message = tlsCertificateReadyState(&cert)
	serving, _ := servingSecretHealth(ctx, downstreamClient, downstreamNamespace, secretName, hostname, time.Now())

	switch {
	case serving.healthy && (condition.Status != metav1.ConditionTrue || blocked != nil):
		condition.Status = metav1.ConditionTrue
		condition.Reason = networkingv1alpha.CertificateReadyReasonCertificateIssued
		condition.Message = "Certificate is ready; a renewal is in progress"
		if blocked != nil {
			condition.Reason = networkingv1alpha.CertificateReadyReasonRenewalFailing
			condition.Message = "Certificate is ready but cannot be renewed: " + blocked.Message
		} else if accepted := apimeta.FindStatusCondition(cert.Status.Conditions, certificatesv1alpha1.ConditionAccepted); accepted != nil && accepted.Status == metav1.ConditionFalse {
			condition.Reason = networkingv1alpha.CertificateReadyReasonRenewalFailing
			condition.Message = "Certificate is ready but cannot be renewed"
			if accepted.Message != "" {
				condition.Message += ": " + accepted.Message
			}
		}
	case !serving.healthy && blocked != nil:
		condition.Status = metav1.ConditionFalse
		condition.Reason = networkingv1alpha.CertificateReadyReasonProvisioningFailed
		condition.Message = blocked.Message
	case !serving.healthy && condition.Status == metav1.ConditionTrue:
		condition.Status = metav1.ConditionFalse
		condition.Reason = networkingv1alpha.CertificateReadyReasonPending
		condition.Message = "The certificate has been issued and is being applied to this hostname"
	}
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
		return metav1.ConditionFalse, networkingv1alpha.CertificateReadyReasonChallengeInProgress, message + requiredDNSRecordsHint(cert)
	}

	return metav1.ConditionFalse, networkingv1alpha.CertificateReadyReasonPending, certificateProvisioningMessage + requiredDNSRecordsHint(cert)
}

// requiredDNSRecordsHint tells the customer which records issuance is waiting
// on, when the service has named any.
func requiredDNSRecordsHint(cert *certificatesv1alpha1.TLSCertificate) string {
	records := make([]string, 0, len(cert.Status.RequiredDNSRecords))
	for _, record := range cert.Status.RequiredDNSRecords {
		records = append(records, fmt.Sprintf("%s %s %s", record.Name, record.Type, record.Content))
	}
	if len(records) == 0 {
		return ""
	}
	return ". Publish these DNS records to continue: " + strings.Join(records, "; ")
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
