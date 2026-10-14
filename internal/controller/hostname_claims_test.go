// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

const claimsNamespace = "hostname-claims"

func TestHostnameClaimName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "app.example.com", hostnameClaimName("app.example.com"))

	wildcard := hostnameClaimName("*.s3.example.com")
	assert.Regexp(t, `^wildcard-[0-9a-f]{40}$`, wildcard)
	assert.NotContains(t, wildcard, ".", "a wildcard claim name must never equal a custom hostname")
	assert.NotEqual(t, wildcard, hostnameClaimName("*.s4.example.com"))
}

func TestHostnameAncestors(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"b.example.com", "example.com", "com"}, hostnameAncestors("a.b.example.com"))
	assert.Equal(t, []string{"s3.example.com", "example.com", "com"}, hostnameAncestors("*.s3.example.com"))
	assert.Empty(t, hostnameAncestors("com"))
}

func claimFor(project, hostname string, created time.Time) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         claimsNamespace,
			Name:              hostnameClaimName(hostname),
			CreationTimestamp: metav1.NewTime(created),
			Labels: map[string]string{
				downstreamclient.UpstreamOwnerClusterNameLabel: hostnameClaimProject(project),
				downstreamclient.UpstreamOwnerNamespaceLabel:   "default",
				downstreamclient.UpstreamOwnerNameLabel:        "gw",
			},
		},
		Data: map[string]string{jsonKeyOwner: project + "/default/gw"},
	}
	if cm.Name != hostname {
		cm.Data[jsonKeyHostname] = hostname
	}
	return cm
}

func claimsTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	require.NoError(t, networkingv1alpha.AddToScheme(s))
	return s
}

func claimsDownstream(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(claimsTestScheme(t)).
		WithObjects(objects...).
		WithIndex(&corev1.ConfigMap{}, hostnameClaimAncestorIndex, hostnameClaimAncestorIndexFunc(claimsNamespace)).
		Build()
}

func claimingGateway(hostnames ...string) *gatewayv1.Gateway {
	gw := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "gw", UID: "gw-uid"}}
	for i, h := range hostnames {
		gw.Spec.Listeners = append(gw.Spec.Listeners, gatewayv1.Listener{
			Name:     gatewayv1.SectionName(fmt.Sprintf("http-hostname-%d", i)),
			Protocol: gatewayv1.HTTPProtocolType,
			Hostname: ptr.To(gatewayv1.Hostname(h)),
		})
	}
	return gw
}

func claimHostnames(t *testing.T, enabled bool, project string, downstream client.Client, hostnames ...string) (claimed []string, refused map[string]string) {
	t.Helper()

	all, refusals := claimHostnameRefusals(t, enabled, project, downstream, hostnames...)
	refused = map[string]string{}
	for h, refusal := range refusals {
		refused[h] = refusal.message
	}
	for _, h := range all {
		if !strings.HasSuffix(h, ".datumproxy.net") {
			claimed = append(claimed, h)
		}
	}
	return claimed, refused
}

func claimHostnameRefusals(t *testing.T, enabled bool, project string, downstream client.Client, hostnames ...string) ([]string, map[string]hostnameRefusal) {
	t.Helper()

	upstream := fake.NewClientBuilder().WithScheme(claimsTestScheme(t)).WithObjects(
		&networkingv1alpha.Domain{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "example.com"},
			Spec:       networkingv1alpha.DomainSpec{DomainName: "example.com"},
			Status: networkingv1alpha.DomainStatus{Conditions: []metav1.Condition{
				{Type: networkingv1alpha.DomainConditionVerified, Status: metav1.ConditionTrue, Reason: networkingv1alpha.DomainReasonVerified},
				{Type: networkingv1alpha.DomainConditionVerifiedDNS, Status: metav1.ConditionTrue, Reason: networkingv1alpha.DomainReasonVerified},
			}},
		},
	).Build()

	r := &GatewayReconciler{
		Config: config.NetworkServicesOperator{Gateway: config.GatewayConfig{
			DownstreamHostnameAccountingNamespace: claimsNamespace,
			TargetDomain:                          "datumproxy.net",
			CertificateService:                    config.CertificateServiceConfig{Enabled: enabled},
		}},
		DownstreamCluster: &fakeCluster{cl: downstream},
	}

	gw := claimingGateway(hostnames...)
	_, all, refusals, err := r.ensureHostnamesClaimed(context.Background(), project, upstream, gw, &gatewayv1.Gateway{})
	require.NoError(t, err)
	return all, refusals
}

func TestSubtreeAwareHostnameClaims(t *testing.T) {
	t.Parallel()

	earlier := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	t.Run("a later claim under another project's wildcard is refused", func(t *testing.T) {
		t.Parallel()
		downstream := claimsDownstream(t, claimFor("project-a", "*.s3.example.com", earlier))

		claimed, refused := claimHostnames(t, true, "project-b", downstream, "bucket.s3.example.com", "deep.bucket.s3.example.com")

		assert.NotContains(t, claimed, "bucket.s3.example.com")
		assert.NotContains(t, claimed, "deep.bucket.s3.example.com")
		assert.Contains(t, refused["bucket.s3.example.com"], `beneath the wildcard "*.s3.example.com"`)
		assert.Contains(t, refused["deep.bucket.s3.example.com"], `beneath the wildcard "*.s3.example.com"`)
		assert.NotContains(t, refused["bucket.s3.example.com"], "project-a")
		assert.Contains(t, refused["bucket.s3.example.com"], "not supported yet")

		var claim corev1.ConfigMap
		err := downstream.Get(context.Background(), client.ObjectKey{Namespace: claimsNamespace, Name: "bucket.s3.example.com"}, &claim)
		assert.True(t, client.IgnoreNotFound(err) == nil && err != nil, "a refused hostname must not be claimed")
	})

	t.Run("a wildcard is refused while other projects hold names beneath it, and names them", func(t *testing.T) {
		t.Parallel()
		downstream := claimsDownstream(t,
			claimFor("project-a", "photos.s3.example.com", earlier),
			claimFor("project-a", "a.b.s3.example.com", earlier),
			claimFor("project-a", "s3.example.com", earlier),
			claimFor("project-a", "other.example.com", earlier),
		)

		claimed, refused := claimHostnames(t, true, "project-b", downstream, "*.s3.example.com")

		assert.NotContains(t, claimed, "*.s3.example.com")
		message := refused["*.s3.example.com"]
		assert.Contains(t, message, "a.b.s3.example.com, photos.s3.example.com.")
		assert.NotContains(t, message, "other.example.com")
		assert.NotContains(t, message, "project-a")
	})

	t.Run("a wildcard under another project's wildcard is refused", func(t *testing.T) {
		t.Parallel()
		downstream := claimsDownstream(t, claimFor("project-a", "*.example.com", earlier))

		_, refused := claimHostnames(t, true, "project-b", downstream, "*.s3.example.com")

		assert.Contains(t, refused["*.s3.example.com"], `beneath the wildcard "*.example.com"`)
	})

	t.Run("a wildcard over another project's narrower wildcard is refused", func(t *testing.T) {
		t.Parallel()
		downstream := claimsDownstream(t, claimFor("project-a", "*.b.s3.example.com", earlier))

		_, refused := claimHostnames(t, true, "project-b", downstream, "*.s3.example.com")

		assert.Contains(t, refused["*.s3.example.com"], "*.b.s3.example.com")
	})

	t.Run("a long list of names beneath a wildcard is cut short", func(t *testing.T) {
		t.Parallel()
		held := make([]client.Object, 0, 8)
		for i := range 8 {
			held = append(held, claimFor("project-a", fmt.Sprintf("b%d.s3.example.com", i), earlier))
		}
		downstream := claimsDownstream(t, held...)

		_, refused := claimHostnames(t, true, "project-b", downstream, "*.s3.example.com")

		assert.Contains(t, refused["*.s3.example.com"], "b4.s3.example.com and 3 more.")
	})

	t.Run("one project may hold a wildcard and names beneath it", func(t *testing.T) {
		t.Parallel()
		downstream := claimsDownstream(t, claimFor("project-a", "photos.s3.example.com", earlier))

		claimed, refused := claimHostnames(t, true, "project-a", downstream, "*.s3.example.com", "videos.s3.example.com")

		assert.Empty(t, refused)
		assert.ElementsMatch(t, []string{"*.s3.example.com", "videos.s3.example.com"}, claimed)

		var claim corev1.ConfigMap
		require.NoError(t, downstream.Get(context.Background(), client.ObjectKey{Namespace: claimsNamespace, Name: hostnameClaimName("*.s3.example.com")}, &claim))
		assert.Equal(t, "*.s3.example.com", claim.Data[jsonKeyHostname])
	})

	t.Run("claims that raced settle on the older one", func(t *testing.T) {
		t.Parallel()
		later := earlier.Add(time.Minute)
		ours := claimFor("project-b", "bucket.s3.example.com", later)
		downstream := claimsDownstream(t, claimFor("project-a", "*.s3.example.com", earlier), ours)

		claimed, refused := claimHostnames(t, true, "project-b", downstream, "bucket.s3.example.com")
		assert.NotContains(t, claimed, "bucket.s3.example.com")
		assert.Contains(t, refused, "bucket.s3.example.com")

		var claim corev1.ConfigMap
		err := downstream.Get(context.Background(), client.ObjectKeyFromObject(ours), &claim)
		assert.True(t, err != nil && client.IgnoreNotFound(err) == nil, "the newer claim must be released")

		claimed, refused = claimHostnames(t, true, "project-a", claimsDownstream(t, claimFor("project-a", "*.s3.example.com", earlier), claimFor("project-b", "bucket.s3.example.com", later)), "*.s3.example.com")
		assert.Empty(t, refused, "the older wildcard keeps its claim")
		assert.Contains(t, claimed, "*.s3.example.com")
	})

	t.Run("a removed wildcard releases its claim", func(t *testing.T) {
		t.Parallel()
		downstream := claimsDownstream(t, claimFor("project-a", "*.s3.example.com", earlier))

		claimed, _ := claimHostnames(t, true, "project-a", downstream, "www.example.com")
		assert.Equal(t, []string{"www.example.com"}, claimed)

		var claim corev1.ConfigMap
		err := downstream.Get(context.Background(), client.ObjectKey{Namespace: claimsNamespace, Name: hostnameClaimName("*.s3.example.com")}, &claim)
		assert.True(t, err != nil && client.IgnoreNotFound(err) == nil)
	})

	t.Run("with the certificate service off claims stay exact-name only", func(t *testing.T) {
		t.Parallel()
		downstream := claimsDownstream(t, claimFor("project-a", "*.s3.example.com", earlier))

		claimed, refused := claimHostnames(t, false, "project-b", downstream, "bucket.s3.example.com")

		assert.Empty(t, refused)
		assert.Contains(t, claimed, "bucket.s3.example.com")
	})
}

func refusedHostnames[V any](refusals map[string]V) []string {
	if len(refusals) == 0 {
		return nil
	}
	hostnames := make([]string, 0, len(refusals))
	for h := range refusals {
		hostnames = append(hostnames, h)
	}
	slices.Sort(hostnames)
	return hostnames
}

func TestExplainInUseHostnames(t *testing.T) {
	t.Parallel()

	statuses := buildAvailabilityStatuses(nil, sets.New("bucket.s3.example.com"), 1)
	explainInUseHostnames(statuses, map[string]string{"bucket.s3.example.com": "beneath a wildcard"})

	require.Len(t, statuses, 1)
	assert.Equal(t, "beneath a wildcard", statuses[0].Conditions[0].Message)
}

func TestAwaitsHostnameClaim(t *testing.T) {
	assert.False(t, awaitsHostnameClaim(nil), "no refusal")
	assert.True(t, awaitsHostnameClaim(map[string]hostnameRefusal{
		"example.com": hostnameInUseRefusal("example.com"),
	}), "another gateway holds the hostname")
	assert.False(t, awaitsHostnameClaim(map[string]hostnameRefusal{
		"*.example.com": {reason: networkingv1alpha.HostnameVerifiedReasonDNSVerificationRequired},
	}), "a refusal a Domain change ends")
}

func TestSubtreeRefusalAwaitsAClaim(t *testing.T) {
	t.Parallel()

	earlier := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	beneath := claimsDownstream(t, claimFor("project-a", "*.s3.example.com", earlier))
	_, refusals := claimHostnameRefusals(t, true, "project-b", beneath, "bucket.s3.example.com")
	require.Contains(t, refusals, "bucket.s3.example.com")
	assert.True(t, awaitsHostnameClaim(refusals), "a name beneath another project's wildcard")

	above := claimsDownstream(t, claimFor("project-a", "bucket.s3.example.com", earlier))
	_, refusals = claimHostnameRefusals(t, true, "project-b", above, "*.s3.example.com")
	require.Contains(t, refusals, "*.s3.example.com")
	assert.True(t, awaitsHostnameClaim(refusals), "a wildcard over another project's name")
}
