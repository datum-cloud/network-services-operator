// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// declareEgress puts an internet egress declaration on the scenario's network,
// which is where a consumer writes it.
func (s *presenceScenario) declareEgress(internet *networkingv1alpha.NetworkInternetEgress) {
	s.t.Helper()
	s.network.Spec.Egress = &networkingv1alpha.NetworkEgress{Internet: internet}
	require.NoError(s.t, s.hub.Update(s.ctx, s.network))
}

// defineClass writes a class an operator defines. The name is the scenario's,
// so a test never reads a class another test left behind.
func (s *presenceScenario) defineClass(name string, byDefault bool) *networkingv1alpha.InternetEgressClass {
	s.t.Helper()

	class := &networkingv1alpha.InternetEgressClass{}
	class.Name = name
	if byDefault {
		class.Annotations = map[string]string{
			networkingv1alpha.InternetEgressClassDefaultAnnotation: "true",
		}
	}
	class.Spec = networkingv1alpha.InternetEgressClassSpec{
		ControllerName: "networking.datumapis.com/cell-egress",
		Sharing:        networkingv1alpha.InternetEgressSharingShared,
		Reach:          []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
		ParametersRef: &networkingv1alpha.InternetEgressClassParametersRef{
			Group: "cloud.datumapis.com",
			Kind:  "EgressShardParameters",
			Name:  name + "-parameters",
		},
	}
	require.NoError(s.t, s.hub.Create(s.ctx, class))
	s.t.Cleanup(func() { _ = s.hub.Delete(s.ctx, class) })
	return class
}

func (s *presenceScenario) projectedEgress() *networkingv1alpha.NetworkContextInternetEgress {
	s.t.Helper()
	networkContext, ok := s.networkContext()
	require.True(s.t, ok, "the presence controller created no network context")
	if networkContext.Spec.Egress == nil {
		return nil
	}
	return networkContext.Spec.Egress.Internet
}

func (s *presenceScenario) egressCondition() *metav1.Condition {
	s.t.Helper()
	networkContext, ok := s.networkContext()
	require.True(s.t, ok)
	return apimeta.FindStatusCondition(networkContext.Status.Conditions,
		networkingv1alpha.NetworkContextInternetEgressReady)
}

func (s *presenceScenario) requireEgressUnavailable() *metav1.Condition {
	s.t.Helper()
	condition := s.egressCondition()
	require.NotNil(s.t, condition, "a consumer was told nothing about why their egress was refused")
	require.Equal(s.t, metav1.ConditionFalse, condition.Status)
	require.Equal(s.t, networkingv1alpha.NetworkContextInternetEgressReasonUnavailable, condition.Reason)
	return condition
}

func TestNetworkPresenceProjectsResolvedEgressIntent(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	class := s.defineClass("egress-default-"+sanitizeName("resolved"), true)
	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode:  networkingv1alpha.NetworkInternetEgressEnabled,
		Reach: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
	})
	s.createBinding("consumer-a")
	s.reconcile()

	egress := s.projectedEgress()
	require.NotNil(t, egress, "a location was given no egress instruction to act on")
	require.Equal(t, networkingv1alpha.NetworkInternetEgressEnabled, egress.Mode)
	require.Equal(t, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}, egress.Reach)
	require.Equal(t, class.Name, egress.ClassName)
	require.Equal(t, networkingv1alpha.InternetEgressSharingShared, egress.Sharing)
	require.NotNil(t, egress.ParametersRef, "the serving class's parameters were not carried")
	require.Equal(t, "cloud.datumapis.com", egress.ParametersRef.Group)
	require.Equal(t, "EgressShardParameters", egress.ParametersRef.Kind)
	require.Equal(t, class.Name+"-parameters", egress.ParametersRef.Name)

	require.Nil(t, s.egressCondition(),
		"the result belongs to the location that realizes it, not to the controller that instructs it")
}

// A network naming nothing takes the default, so the common case names nothing.
func TestNetworkPresenceProjectsTheDefaultClassForANetworkNamingNone(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	class := s.defineClass("egress-default-"+sanitizeName("unnamed"), true)
	s.defineClass("egress-other-"+sanitizeName("unnamed"), false)
	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressEnabled,
	})
	s.createBinding("consumer-a")
	s.reconcile()

	egress := s.projectedEgress()
	require.NotNil(t, egress)
	require.Equal(t, class.Name, egress.ClassName)
	require.Equal(t, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}, egress.Reach,
		"a network asking for nothing in particular reaches what the class reaches")
}

func TestNetworkPresenceServesTheClassANetworkNames(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.defineClass("egress-default-"+sanitizeName("named"), true)
	named := s.defineClass("egress-named-"+sanitizeName("named"), false)
	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode:  networkingv1alpha.NetworkInternetEgressEnabled,
		Class: named.Name,
	})
	s.createBinding("consumer-a")
	s.reconcile()

	egress := s.projectedEgress()
	require.NotNil(t, egress)
	require.Equal(t, named.Name, egress.ClassName)
}

// A network naming a class that does not exist is served by nothing. Falling
// back to the default would put traffic on a path the consumer never asked for.
func TestNetworkPresenceRefusesAClassThatDoesNotExist(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.defineClass("egress-default-"+sanitizeName("missing"), true)
	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode:  networkingv1alpha.NetworkInternetEgressEnabled,
		Class: "egress-absent",
	})
	s.createBinding("consumer-a")

	result, err := s.reconciler.Reconcile(s.ctx, s.request())
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter,
		"nothing watches a class, so an unresolved one needs a way back")

	require.Nil(t, s.projectedEgress(),
		"an intent naming no resolvable class is not one a location can act on")
	require.Contains(t, s.requireEgressUnavailable().Message, "egress-absent")
}

// Two defaults is an unanswered design question. Refusing states the
// ambiguity; picking a winner would answer it by accident of ordering.
func TestNetworkPresenceRefusesTwoDefaultClasses(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	first := s.defineClass("egress-alpha-"+sanitizeName("two"), true)
	second := s.defineClass("egress-beta-"+sanitizeName("two"), true)
	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressEnabled,
	})
	s.createBinding("consumer-a")
	s.reconcile()

	require.Nil(t, s.projectedEgress())
	message := s.requireEgressUnavailable().Message
	require.Contains(t, message, first.Name)
	require.Contains(t, message, second.Name)
	require.Contains(t, message, "ambiguous")
}

func TestNetworkPresenceRefusesWhenNoClassIsTheDefault(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.defineClass("egress-plain-"+sanitizeName("nodefault"), false)
	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressEnabled,
	})
	s.createBinding("consumer-a")
	s.reconcile()

	require.Nil(t, s.projectedEgress())
	require.Contains(t, s.requireEgressUnavailable().Message, "default")
}

// Disabled is projected, not omitted. A location that reads no instruction
// cannot tell a network that reaches nothing from one written before the field
// was carried, and the two mean opposite things.
func TestNetworkPresenceProjectsDisabledEgressExplicitly(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressDisabled,
	})
	s.createBinding("consumer-a")
	s.reconcile()

	egress := s.projectedEgress()
	require.NotNil(t, egress, "a network that reaches nothing still instructs the location it reaches nothing")
	require.Equal(t, networkingv1alpha.NetworkInternetEgressDisabled, egress.Mode)
	require.Empty(t, egress.ClassName, "disabling egress never depends on a class being defined")
	require.Empty(t, egress.Reach)
	require.Nil(t, egress.ParametersRef)
	require.Nil(t, s.egressCondition())
}

func TestNetworkPresenceProjectsNoEgressForANetworkDeclaringNone(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()

	require.Nil(t, s.projectedEgress())
	require.Nil(t, s.egressCondition())
}

// A class that cannot be read is not a consumer asking for their traffic to
// stop. The intent already carried stays, and the refusal is reported beside it.
func TestNetworkPresenceDoesNotWithdrawEgressItCannotResolve(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	class := s.defineClass("egress-default-"+sanitizeName("withdrawn"), true)
	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressEnabled,
	})
	s.createBinding("consumer-a")
	s.reconcile()
	require.NotNil(t, s.projectedEgress())

	require.NoError(t, s.hub.Delete(s.ctx, class))
	s.reconcile()

	egress := s.projectedEgress()
	require.NotNil(t, egress, "the location's egress route was taken away by a class read")
	require.Equal(t, class.Name, egress.ClassName)
	require.Contains(t, s.requireEgressUnavailable().Message, "default")
}
