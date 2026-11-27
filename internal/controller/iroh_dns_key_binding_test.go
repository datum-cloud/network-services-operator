// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	dnsv1alpha1 "go.miloapis.com/dns/api/v1alpha1"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/connectorbinding"
)

const (
	bindingTestSeedHex = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
	bindingTestProject = "binding-project"
	holderUID          = "aaaaaaaa-0000-0000-0000-000000000001"
)

type irohBindingFixture struct {
	r          *IrohDNSReconciler
	upstream   client.Client
	downstream client.Client
	connector  *networkingv1alpha1.Connector
	recordKey  client.ObjectKey
}

func bindingTestKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	seed, err := hex.DecodeString(bindingTestSeedHex)
	require.NoError(t, err)
	priv := ed25519.NewKeyFromSeed(seed)
	return hex.EncodeToString(priv.Public().(ed25519.PublicKey)), priv
}

func newIrohBindingFixture(t *testing.T, mode connectorbinding.Mode, signedUID string, holder map[string]string) *irohBindingFixture {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, networkingv1alpha1.AddToScheme(s))
	require.NoError(t, dnsv1alpha1.AddToScheme(s))

	id, priv := bindingTestKey(t)
	pk := &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{Id: id, HomeRelay: testRelayURL}
	if signedUID != "" {
		pk.Binding = &networkingv1alpha1.ConnectorKeyBinding{
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, connectorbinding.Message(signedUID, id))),
		}
	}
	connector := newConnector(pk)
	connector.Finalizers = []string{irohDNSFinalizer}

	class := &networkingv1alpha1.ConnectorClass{
		ObjectMeta: metav1.ObjectMeta{Name: connector.Spec.ConnectorClassName},
		Spec:       networkingv1alpha1.ConnectorClassSpec{ControllerName: "networking.datumapis.com/datum-connect"},
	}

	r := newReconciler()
	r.Config.Connector.KeyBinding = mode
	desired, ok, err := r.buildDesiredRecordSet(bindingTestProject, connector)
	require.NoError(t, err)
	require.True(t, ok)

	downstreamBuilder := fake.NewClientBuilder().WithScheme(s)
	if holder != nil {
		labels := map[string]string{
			"app.kubernetes.io/managed-by": irohDNSManagedByLabelValue,
			irohDNSConnectorClusterLabel:   encodeIrohClusterLabel("other-project"),
			irohDNSConnectorNamespaceLabel: "default",
			irohDNSConnectorNameLabel:      "other",
		}
		for k, v := range holder {
			labels[k] = v
		}
		downstreamBuilder = downstreamBuilder.WithObjects(&dnsv1alpha1.DNSRecordSet{
			ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace, Labels: labels},
			Spec: dnsv1alpha1.DNSRecordSetSpec{
				RecordType: dnsv1alpha1.RRTypeTXT,
				Records:    []dnsv1alpha1.RecordEntry{{Name: "held", TXT: &dnsv1alpha1.TXTRecordSpec{Content: "relay=https://held.example.com"}}},
			},
		})
	}
	downstream := downstreamBuilder.Build()
	upstream := fake.NewClientBuilder().WithScheme(s).WithObjects(connector, class).WithStatusSubresource(connector).Build()

	r.Downstream = &fakeCluster{cl: downstream}
	r.mgr = &replicatorFakeManager{clusters: map[multicluster.ClusterName]cluster.Cluster{
		bindingTestProject: &fakeCluster{cl: upstream},
	}}

	return &irohBindingFixture{
		r:          r,
		upstream:   upstream,
		downstream: downstream,
		connector:  connector,
		recordKey:  client.ObjectKeyFromObject(desired),
	}
}

func (f *irohBindingFixture) reconcile(t *testing.T) {
	t.Helper()
	_, err := f.r.Reconcile(context.Background(), mcreconcile.Request{
		ClusterName: bindingTestProject,
		Request:     ctrl.Request{NamespacedName: client.ObjectKeyFromObject(f.connector)},
	})
	require.NoError(t, err)
}

func (f *irohBindingFixture) record(t *testing.T) (*dnsv1alpha1.DNSRecordSet, bool) {
	t.Helper()
	var drs dnsv1alpha1.DNSRecordSet
	err := f.downstream.Get(context.Background(), f.recordKey, &drs)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(t, err)
	return &drs, true
}

func (f *irohBindingFixture) published(t *testing.T) *metav1.Condition {
	t.Helper()
	var got networkingv1alpha1.Connector
	require.NoError(t, f.upstream.Get(context.Background(), client.ObjectKeyFromObject(f.connector), &got))
	cond := apimeta.FindStatusCondition(got.Status.Conditions, connectorConditionIrohDNSPublished)
	require.NotNil(t, cond)
	return cond
}

func TestIrohDNSKeyBinding_EnforceRefusesUnverifiedClaim(t *testing.T) {
	for name, signedUID := range map[string]string{
		"missing": "",
		"invalid": "another-connector-uid",
	} {
		t.Run(name, func(t *testing.T) {
			f := newIrohBindingFixture(t, connectorbinding.Enforce, signedUID, nil)
			f.reconcile(t)

			_, exists := f.record(t)
			assert.False(t, exists, "an unverified Connector must not claim the record")
			cond := f.published(t)
			assert.Equal(t, metav1.ConditionFalse, cond.Status)
			assert.Equal(t, connectorReasonIrohKeyUnbound, cond.Reason)
		})
	}
}

func TestIrohDNSKeyBinding_EnforceReleasesUnverifiedHold(t *testing.T) {
	f := newIrohBindingFixture(t, connectorbinding.Enforce, "", map[string]string{
		irohDNSClaimedByUIDLabel: testConnectorUID,
		irohDNSKeyBindingLabel:   string(connectorbinding.Missing),
	})
	f.reconcile(t)

	_, exists := f.record(t)
	assert.False(t, exists, "an unverified Connector must not keep holding the record")
	assert.Equal(t, connectorReasonIrohKeyUnbound, f.published(t).Reason)
}

func TestIrohDNSKeyBinding_EnforceVerifiedClaimsFreeRecord(t *testing.T) {
	f := newIrohBindingFixture(t, connectorbinding.Enforce, testConnectorUID, nil)
	f.reconcile(t)

	drs, exists := f.record(t)
	require.True(t, exists)
	assert.Equal(t, testConnectorUID, drs.Labels[irohDNSClaimedByUIDLabel])
	assert.Equal(t, string(connectorbinding.Verified), drs.Labels[irohDNSKeyBindingLabel])
	assert.Equal(t, connectorReasonIrohOwner, f.published(t).Reason)
}

func TestIrohDNSKeyBinding_VerifiedDisplacesUnverifiedHolder(t *testing.T) {
	for name, holderBinding := range map[string]map[string]string{
		"holder missing":   {irohDNSClaimedByUIDLabel: holderUID, irohDNSKeyBindingLabel: string(connectorbinding.Missing)},
		"holder invalid":   {irohDNSClaimedByUIDLabel: holderUID, irohDNSKeyBindingLabel: string(connectorbinding.Invalid)},
		"holder unlabeled": {irohDNSClaimedByUIDLabel: holderUID},
	} {
		t.Run(name, func(t *testing.T) {
			f := newIrohBindingFixture(t, connectorbinding.Enforce, testConnectorUID, holderBinding)
			f.reconcile(t)

			drs, exists := f.record(t)
			require.True(t, exists)
			assert.Equal(t, testConnectorUID, drs.Labels[irohDNSClaimedByUIDLabel])
			assert.Equal(t, string(connectorbinding.Verified), drs.Labels[irohDNSKeyBindingLabel])
			assert.Equal(t, encodeIrohClusterLabel(bindingTestProject), drs.Labels[irohDNSConnectorClusterLabel])
			assert.Equal(t, f.connector.Name, drs.Labels[irohDNSConnectorNameLabel])
			for _, entry := range drs.Spec.Records {
				assert.NotEqual(t, "held", entry.Name, "the previous holder's records must be replaced")
			}
			assert.Equal(t, connectorReasonIrohOwner, f.published(t).Reason)
		})
	}
}

func TestIrohDNSKeyBinding_VerifiedDefersToVerifiedHolder(t *testing.T) {
	f := newIrohBindingFixture(t, connectorbinding.Enforce, testConnectorUID, map[string]string{
		irohDNSClaimedByUIDLabel: holderUID,
		irohDNSKeyBindingLabel:   string(connectorbinding.Verified),
	})
	f.reconcile(t)

	drs, exists := f.record(t)
	require.True(t, exists)
	assert.Equal(t, holderUID, drs.Labels[irohDNSClaimedByUIDLabel])
	assert.Equal(t, connectorReasonIrohDeferredToOwner, f.published(t).Reason)
}

func TestIrohDNSKeyBinding_ObserveLeavesClaimsAlone(t *testing.T) {
	t.Run("unverified connector still claims", func(t *testing.T) {
		f := newIrohBindingFixture(t, connectorbinding.Observe, "", nil)
		f.reconcile(t)

		drs, exists := f.record(t)
		require.True(t, exists)
		assert.Equal(t, testConnectorUID, drs.Labels[irohDNSClaimedByUIDLabel])
		assert.Equal(t, string(connectorbinding.Missing), drs.Labels[irohDNSKeyBindingLabel])
		assert.Equal(t, connectorReasonIrohOwner, f.published(t).Reason)
	})

	t.Run("verified connector does not displace", func(t *testing.T) {
		f := newIrohBindingFixture(t, connectorbinding.Observe, testConnectorUID, map[string]string{
			irohDNSClaimedByUIDLabel: holderUID,
			irohDNSKeyBindingLabel:   string(connectorbinding.Missing),
		})
		f.reconcile(t)

		drs, exists := f.record(t)
		require.True(t, exists)
		assert.Equal(t, holderUID, drs.Labels[irohDNSClaimedByUIDLabel])
		assert.Equal(t, connectorReasonIrohDeferredToOwner, f.published(t).Reason)
	})

	t.Run("default mode is observe", func(t *testing.T) {
		f := newIrohBindingFixture(t, "", "", nil)
		f.reconcile(t)

		_, exists := f.record(t)
		assert.True(t, exists)
	})
}
