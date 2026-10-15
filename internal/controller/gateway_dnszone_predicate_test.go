// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	dnsutil "go.datum.net/network-services-operator/internal/util/dns"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// TestDNSZonePredicatePassesExactlyWhenItsInputsChange checks the zone watch's
// predicate against dnsutil.ZoneInputs, the one place that names what a
// Gateway's DNS reads from a zone: an update passes exactly when that value
// changes (NSO#438).
func TestDNSZonePredicatePassesExactlyWhenItsInputsChange(t *testing.T) {
	setCondition := func(conditionType string, status metav1.ConditionStatus, reason string) func(*dnsv1alpha1.DNSZone) {
		return func(z *dnsv1alpha1.DNSZone) {
			apimeta.SetStatusCondition(&z.Status.Conditions, metav1.Condition{Type: conditionType, Status: status, Reason: reason})
		}
	}
	cases := []struct {
		name   string
		change func(*dnsv1alpha1.DNSZone)
		passes bool
	}{
		{"a domain name given to a zone stored without one", func(z *dnsv1alpha1.DNSZone) { z.Spec.DomainName = "example.test" }, true},
		{"Accepted becoming True", setCondition(conditionTypeAccepted, metav1.ConditionTrue, "Accepted"), true},
		{"Programmed becoming True", setCondition(conditionTypeProgrammed, metav1.ConditionTrue, "Programmed"), true},
		{"its nameservers", func(z *dnsv1alpha1.DNSZone) { z.Status.Nameservers = []string{"ns1.example.net"} }, true},
		{"its record count", func(z *dnsv1alpha1.DNSZone) { z.Status.RecordCount = 3 }, false},
		{"Accepted False with another reason", setCondition(conditionTypeAccepted, metav1.ConditionFalse, "StillPending"), false},
		{"a condition no Gateway reads", setCondition("Degraded", metav1.ConditionTrue, "Slow"), false},
		{"its zone class", func(z *dnsv1alpha1.DNSZone) { z.Spec.DNSZoneClassName = "other" }, false},
	}
	predicate := dnsZoneAuthorityChangedPredicate()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := &dnsv1alpha1.DNSZone{Spec: dnsv1alpha1.DNSZoneSpec{DNSZoneClassName: "default"}}
			setCondition(conditionTypeAccepted, metav1.ConditionFalse, "Pending")(before)
			after := before.DeepCopy()
			tc.change(after)

			inputsChanged := !dnsutil.ReadZoneInputs(before).Equal(dnsutil.ReadZoneInputs(after))
			require.Equal(t, tc.passes, inputsChanged, "what a Gateway reads from the zone")
			require.Equal(t, inputsChanged, predicate.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: after}))
		})
	}
}
