package controller

import (
	"context"
	"fmt"
	"slices"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
	"go.datum.net/network-services-operator/internal/util/resourcename"
)

// upstreamOwnerLabelKeys tie a downstream object to its upstream owner. The
// federation's propagation policy selects Envoy Gateway policies by them, so a
// policy without them never leaves the federation control plane.
var upstreamOwnerLabelKeys = []string{
	downstreamclient.UpstreamOwnerClusterNameLabel,
	downstreamclient.UpstreamOwnerGroupLabel,
	downstreamclient.UpstreamOwnerKindLabel,
	downstreamclient.UpstreamOwnerNameLabel,
	downstreamclient.UpstreamOwnerNamespaceLabel,
}

// copyUpstreamOwnerLabels copies from's upstream owner labels onto to, and
// keeps to's other labels.
func copyUpstreamOwnerLabels(to, from metav1.Object) {
	labels := to.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	for _, key := range upstreamOwnerLabelKeys {
		if v, ok := from.GetLabels()[key]; ok {
			labels[key] = v
		}
	}
	to.SetLabels(labels)
}

// ensureDownstreamClientTrafficPolicy writes traffic into a ClientTrafficPolicy
// on each HTTPS listener of a downstream gateway, as the gateway was written.
// With nothing configured, or no HTTPS listener, it deletes the policy it wrote
// before.
//
// The policy names each HTTPS listener, not the whole gateway. Envoy Gateway
// applies a policy that names a listener before a gateway-wide one, so it takes
// effect even beside a gateway-wide policy another controller writes. And merged
// gateways share one connection manager on port 80, where a gateway-wide setting
// would be dropped without a status.
func ensureDownstreamClientTrafficPolicy(
	ctx context.Context,
	cl client.Client,
	traffic config.DownstreamClientTrafficConfig,
	gateway *gatewayv1.Gateway,
) error {
	policy := &envoygatewayv1alpha1.ClientTrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: clientTrafficPolicyName(gateway.Name), Namespace: gateway.Namespace},
	}
	targets := httpsListenerTargets(gateway)
	if traffic.IsEmpty() || len(targets) == 0 {
		return deleteControlledClientTrafficPolicy(ctx, cl, policy, gateway)
	}
	_, err := controllerutil.CreateOrUpdate(ctx, cl, policy, func() error {
		copyUpstreamOwnerLabels(policy, gateway)
		policy.Spec = envoygatewayv1alpha1.ClientTrafficPolicySpec{
			PolicyTargetReferences: envoygatewayv1alpha1.PolicyTargetReferences{TargetRefs: targets},
			HTTP2:                  traffic.HTTP2.DeepCopy(),
			HTTP3:                  traffic.HTTP3.DeepCopy(),
			TLS:                    clientTLSSettings(traffic.ALPNProtocols),
		}
		return controllerutil.SetControllerReference(gateway, policy, cl.Scheme())
	})
	if err != nil {
		return fmt.Errorf("failed writing ClientTrafficPolicy %s/%s: %w", policy.Namespace, policy.Name, err)
	}
	return nil
}

// clientTLSSettings carries only the ALPN protocols. An empty list would turn
// ALPN off, so nothing is written unless protocols are listed.
func clientTLSSettings(protocols []envoygatewayv1alpha1.ALPNProtocol) *envoygatewayv1alpha1.ClientTLSSettings {
	if len(protocols) == 0 {
		return nil
	}
	return &envoygatewayv1alpha1.ClientTLSSettings{
		TLSSettings: envoygatewayv1alpha1.TLSSettings{ALPNProtocols: slices.Clone(protocols)},
	}
}

// clientTrafficPolicySuffix keeps the operator's policy name apart from the
// <gateway>-http3 that staging's Kyverno policy writes into the same namespace:
// a name ending in it can never end in -http3, and the hash a long name gets
// is hexadecimal, which cannot spell it either.
const clientTrafficPolicySuffix = "-client-traffic"

// clientTrafficPolicyName returns the name of a gateway's ClientTrafficPolicy,
// within Kubernetes' 253-character limit.
func clientTrafficPolicyName(gatewayName string) string {
	return resourcename.GetValidDNS1123Name(gatewayName + clientTrafficPolicySuffix)
}

// httpsListenerTargets names each HTTPS listener of the gateway, in order.
func httpsListenerTargets(gateway *gatewayv1.Gateway) []gatewayv1.LocalPolicyTargetReferenceWithSectionName {
	var targets []gatewayv1.LocalPolicyTargetReferenceWithSectionName
	for _, l := range gateway.Spec.Listeners {
		if l.Protocol != gatewayv1.HTTPSProtocolType {
			continue
		}
		targets = append(targets, gatewayv1.LocalPolicyTargetReferenceWithSectionName{
			LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
				Group: gatewayv1.GroupName,
				Kind:  KindGateway,
				Name:  gatewayv1.ObjectName(gateway.Name),
			},
			SectionName: ptr.To(l.Name),
		})
	}
	return targets
}

// deleteControlledClientTrafficPolicy deletes policy if the gateway controls
// it. A cluster without the ClientTrafficPolicy kind holds no such policy.
func deleteControlledClientTrafficPolicy(
	ctx context.Context,
	cl client.Client,
	policy *envoygatewayv1alpha1.ClientTrafficPolicy,
	gateway *gatewayv1.Gateway,
) error {
	err := cl.Get(ctx, client.ObjectKeyFromObject(policy), policy)
	if apimeta.IsNoMatchError(err) {
		return nil
	}
	if err != nil {
		return client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(policy, gateway) {
		return nil
	}
	if err := cl.Delete(ctx, policy); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed deleting ClientTrafficPolicy %s/%s: %w", policy.Namespace, policy.Name, err)
	}
	return nil
}
