// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dnsv1alpha1 "go.miloapis.com/dns/api/v1alpha1"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

func TestApplyClaim_ForeignClaimDoesNotDiscloseOwner(t *testing.T) {
	const (
		ownerCluster   = "victim-project"
		ownerNamespace = "victim-namespace"
		ownerName      = "victim-connector"
		ownerUID       = "11111111-2222-3333-4444-555555555555"
	)

	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, networkingv1alpha1.AddToScheme(s))
	require.NoError(t, dnsv1alpha1.AddToScheme(s))

	r := newReconciler()
	connector := newConnector(&networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
		Id:        testEndpointHex,
		HomeRelay: testRelayURL,
	})

	desired, ok, err := r.buildDesiredRecordSet(testClusterName, connector)
	require.NoError(t, err)
	require.True(t, ok)

	existing := &dnsv1alpha1.DNSRecordSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": irohDNSManagedByLabelValue,
				irohDNSClaimedByUIDLabel:       ownerUID,
				irohDNSConnectorClusterLabel:   encodeIrohClusterLabel(ownerCluster),
				irohDNSConnectorNamespaceLabel: ownerNamespace,
				irohDNSConnectorNameLabel:      ownerName,
			},
		},
		Spec: dnsv1alpha1.DNSRecordSetSpec{
			RecordType: dnsv1alpha1.RRTypeTXT,
		},
	}

	downstream := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()
	upstream := fake.NewClientBuilder().WithScheme(s).WithObjects(connector).WithStatusSubresource(connector).Build()
	r.Downstream = &fakeCluster{cl: downstream}

	ctx := context.Background()
	require.NoError(t, r.applyClaim(ctx, &fakeCluster{cl: upstream}, connector, desired))

	var got networkingv1alpha1.Connector
	require.NoError(t, upstream.Get(ctx, client.ObjectKeyFromObject(connector), &got))
	cond := apimeta.FindStatusCondition(got.Status.Conditions, connectorConditionIrohDNSPublished)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, connectorReasonIrohDeferredToOwner, cond.Reason)
	for _, secret := range []string{ownerCluster, ownerNamespace, ownerName, ownerUID} {
		assert.NotContains(t, cond.Message, secret)
	}

	var after dnsv1alpha1.DNSRecordSet
	require.NoError(t, downstream.Get(ctx, client.ObjectKeyFromObject(existing), &after))
	assert.Equal(t, ownerUID, after.Labels[irohDNSClaimedByUIDLabel])
}
