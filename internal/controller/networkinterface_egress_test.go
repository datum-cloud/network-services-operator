// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	attachmentGroup   = "cloud.datumapis.com"
	attachmentVersion = "v1alpha"
	attachmentKind    = "VPCAttachment"
)

var attachmentGroupKind = schema.GroupKind{Group: attachmentGroup, Kind: attachmentKind}

// publishedAttachment is the attachment as the cell-resident controller in the
// cloud repository writes it. The path and the field names are literal on
// purpose: a rename on either side has to break a test rather than silently
// report a consumer no address.
func publishedAttachment(namespace, name string, sourceAddresses []any) *unstructured.Unstructured {
	attachment := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": attachmentGroup + "/" + attachmentVersion,
		"kind":       attachmentKind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
	}}
	if sourceAddresses != nil {
		attachment.Object["status"] = map[string]any{
			"egress": map[string]any{
				"internet": map[string]any{
					"sourceAddresses": sourceAddresses,
				},
			},
		}
	}
	return attachment
}

func sourceAddress(family, address, stability string) map[string]any {
	return map[string]any{
		"family":    family,
		"address":   address,
		"stability": stability,
	}
}

func TestAttachmentSourceAddressesReadsPublishedPath(t *testing.T) {
	attachment := publishedAttachment("ns", "vpc-attachment", []any{
		sourceAddress("IPv6", "2001:db8:f00d::100", "None"),
	})

	addresses, err := attachmentSourceAddresses(attachment)
	require.NoError(t, err)
	require.Len(t, addresses, 1)
	assert.Equal(t, networkingv1alpha.IPv6Protocol, addresses[0].Family)
	assert.Equal(t, "2001:db8:f00d::100", addresses[0].Address)
	assert.Equal(t, networkingv1alpha.InternetEgressAddressStabilityNone, addresses[0].Stability)
}

// TestAttachmentSourceAddressesTrustsPublishedStability asserts the stability a
// consumer reads is the publisher's. It is resolved from the class's sharing
// where the class is readable, and deriving a second answer here would let the
// two disagree about whether allow-listing is safe.
func TestAttachmentSourceAddressesTrustsPublishedStability(t *testing.T) {
	attachment := publishedAttachment("ns", "vpc-attachment", []any{
		sourceAddress("IPv6", "2001:db8:f00d::100", "Network"),
	})

	addresses, err := attachmentSourceAddresses(attachment)
	require.NoError(t, err)
	require.Len(t, addresses, 1)
	assert.Equal(t, networkingv1alpha.InternetEgressAddressStabilityNetwork, addresses[0].Stability)
}

// TestAttachmentSourceAddressesKeysOnFamily asserts entries are read by family
// rather than by position. The publisher stores them as a map keyed on family,
// which promises neither order nor count.
func TestAttachmentSourceAddressesKeysOnFamily(t *testing.T) {
	attachment := publishedAttachment("ns", "vpc-attachment", []any{
		sourceAddress("IPv6", "2001:db8:f00d::100", "None"),
		sourceAddress("IPv4", "198.51.100.7", "None"),
	})

	addresses, err := attachmentSourceAddresses(attachment)
	require.NoError(t, err)
	require.Len(t, addresses, 2)

	byFamily := map[networkingv1alpha.IPFamily]string{}
	for _, address := range addresses {
		byFamily[address.Family] = address.Address
	}
	assert.Equal(t, "2001:db8:f00d::100", byFamily[networkingv1alpha.IPv6Protocol])
	assert.Equal(t, "198.51.100.7", byFamily[networkingv1alpha.IPv4Protocol])
}

func TestAttachmentSourceAddressesReportsNothingWhenUnpublished(t *testing.T) {
	for name, attachment := range map[string]*unstructured.Unstructured{
		"no status":    publishedAttachment("ns", "vpc-attachment", nil),
		"empty list":   publishedAttachment("ns", "vpc-attachment", []any{}),
		"status only":  {Object: map[string]any{"status": map[string]any{}}},
		"egress only":  {Object: map[string]any{"status": map[string]any{"egress": map[string]any{}}}},
		"no addresses": {Object: map[string]any{"status": map[string]any{"egress": map[string]any{"internet": map[string]any{}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			addresses, err := attachmentSourceAddresses(attachment)
			require.NoError(t, err)
			assert.Empty(t, addresses)
		})
	}
}

// TestAttachmentSourceAddressesRefusesAPartialEntry asserts an entry missing a
// field is refused rather than completed. The publisher withholds the whole
// block rather than publishing half an answer, so a half-published entry is a
// broken publisher, and a stability filled in here is an allow-listing promise
// nobody made.
func TestAttachmentSourceAddressesRefusesAPartialEntry(t *testing.T) {
	for name, entry := range map[string]map[string]any{
		"no stability": {"family": "IPv6", "address": "2001:db8:f00d::100"},
		"no address":   {"family": "IPv6", "stability": "None"},
		"no family":    {"address": "2001:db8:f00d::100", "stability": "None"},
	} {
		t.Run(name, func(t *testing.T) {
			addresses, err := attachmentSourceAddresses(
				publishedAttachment("ns", "vpc-attachment", []any{entry}))
			require.Error(t, err)
			assert.Empty(t, addresses)
		})
	}
}

// requireAttachmentKind installs a minimal CRD for the provider's attachment on
// a plane, so the reader resolves the same group and kind the reference names
// through a real REST mapper. The schema keeps no field of its own: this
// operator compiles against no type in the provider's group, and a test that
// mirrored the provider's schema would assert this repository's copy of it.
func requireAttachmentKind(t *testing.T, cl client.Client) {
	t.Helper()
	ctx := context.Background()

	crd := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": "vpcattachments." + attachmentGroup},
		"spec": map[string]any{
			"group": attachmentGroup,
			"scope": "Namespaced",
			"names": map[string]any{
				"plural":   "vpcattachments",
				"singular": "vpcattachment",
				"kind":     attachmentKind,
			},
			"versions": []any{map[string]any{
				"name":    attachmentVersion,
				"served":  true,
				"storage": true,
				"schema": map[string]any{
					"openAPIV3Schema": map[string]any{
						"type":                                 "object",
						"x-kubernetes-preserve-unknown-fields": true,
					},
				},
			}},
		},
	}}

	if err := cl.Create(ctx, crd); err != nil {
		require.Truef(t, apierrors.IsAlreadyExists(err), "failed installing the attachment kind: %v", err)
	}

	require.Eventually(t, func() bool {
		_, err := cl.RESTMapper().RESTMapping(attachmentGroupKind)
		return err == nil
	}, 30*time.Second, 100*time.Millisecond, "the attachment kind never became servable")
}

type egressScenario struct {
	t      *testing.T
	ctx    context.Context
	cell   client.Client
	mapper meta.RESTMapper

	namespace string
	reporter  *NetworkInterfaceEgressReconciler
}

func newEgressScenario(t *testing.T) *egressScenario {
	t.Helper()

	cellPlane, _ := startPlanes(t)
	requireAttachmentKind(t, cellPlane)

	ctx := context.Background()
	namespace := "egress-" + sanitizeName(strings.ToLower(t.Name()))
	ns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]any{"name": namespace},
	}}
	require.NoError(t, cellPlane.Create(ctx, ns))

	return &egressScenario{
		t:         t,
		ctx:       ctx,
		cell:      cellPlane,
		mapper:    cellPlane.RESTMapper(),
		namespace: namespace,
		reporter:  &NetworkInterfaceEgressReconciler{},
	}
}

// interfaceWithAttachment writes an interface already carrying the reference a
// provider set when it realized the interface.
func (s *egressScenario) interfaceWithAttachment(name, attachmentName string) *networkingv1alpha.NetworkInterface {
	s.t.Helper()

	iface := &networkingv1alpha.NetworkInterface{}
	iface.Name = name
	iface.Namespace = s.namespace
	iface.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: "default"}
	require.NoError(s.t, s.cell.Create(s.ctx, iface))

	if attachmentName != "" {
		iface.Status.AttachmentRef = &networkingv1alpha.NetworkInterfaceAttachmentRef{
			APIGroup: attachmentGroup,
			Kind:     attachmentKind,
			Name:     attachmentName,
		}
		require.NoError(s.t, s.cell.Status().Update(s.ctx, iface))
	}

	return iface
}

func (s *egressScenario) reconcile(iface *networkingv1alpha.NetworkInterface) {
	s.t.Helper()
	_, err := s.reporter.report(s.ctx, s.cell, s.mapper, client.ObjectKeyFromObject(iface))
	require.NoError(s.t, err)
}

func (s *egressScenario) reported(iface *networkingv1alpha.NetworkInterface) *networkingv1alpha.NetworkInterfaceEgressStatus {
	s.t.Helper()
	var got networkingv1alpha.NetworkInterface
	require.NoError(s.t, s.cell.Get(s.ctx, client.ObjectKeyFromObject(iface), &got))
	return got.Status.Egress
}

// TestInterfaceReportsTheAttachmentsAddresses asserts the address a consumer
// allow-lists reaches the interface from the attachment that resolved it.
func TestInterfaceReportsTheAttachmentsAddresses(t *testing.T) {
	s := newEgressScenario(t)

	attachment := publishedAttachment(s.namespace, "attached", []any{
		sourceAddress("IPv6", "2001:db8:f00d::100", "None"),
	})
	require.NoError(t, s.cell.Create(s.ctx, attachment))

	iface := s.interfaceWithAttachment("reports", "attached")
	s.reconcile(iface)

	egress := s.reported(iface)
	require.NotNil(t, egress)
	require.NotNil(t, egress.Internet)
	require.Len(t, egress.Internet.SourceAddresses, 1)
	assert.Equal(t, "2001:db8:f00d::100", egress.Internet.SourceAddresses[0].Address)
	assert.Equal(t,
		networkingv1alpha.InternetEgressAddressStabilityNone,
		egress.Internet.SourceAddresses[0].Stability)
}

// TestInterfaceReportsNothingWhenTheAttachmentDoes asserts an attachment with
// no address leaves the interface reporting none. An empty block would read as
// an answer, and a consumer cannot tell a wrong answer from a missing one once
// they have allow-listed it.
func TestInterfaceReportsNothingWhenTheAttachmentDoes(t *testing.T) {
	s := newEgressScenario(t)

	attachment := publishedAttachment(s.namespace, "silent", nil)
	require.NoError(t, s.cell.Create(s.ctx, attachment))

	iface := s.interfaceWithAttachment("unreported", "silent")
	s.reconcile(iface)

	assert.Nil(t, s.reported(iface))
}

// TestInterfaceFollowsTheAttachmentsAddresses asserts an address the provider
// changes replaces the one reported, and that withdrawing it leaves nothing
// behind for a consumer to keep trusting.
func TestInterfaceFollowsTheAttachmentsAddresses(t *testing.T) {
	s := newEgressScenario(t)

	attachment := publishedAttachment(s.namespace, "moving", []any{
		sourceAddress("IPv6", "2001:db8:f00d::100", "None"),
	})
	require.NoError(t, s.cell.Create(s.ctx, attachment))

	iface := s.interfaceWithAttachment("follows", "moving")
	s.reconcile(iface)
	require.Equal(t, "2001:db8:f00d::100", s.reported(iface).Internet.SourceAddresses[0].Address)

	require.NoError(t, unstructured.SetNestedSlice(attachment.Object,
		[]any{sourceAddress("IPv6", "2001:db8:f00d::200", "Network")},
		"status", "egress", "internet", "sourceAddresses"))
	require.NoError(t, s.cell.Update(s.ctx, attachment))

	s.reconcile(iface)
	moved := s.reported(iface)
	require.NotNil(t, moved)
	require.Len(t, moved.Internet.SourceAddresses, 1)
	assert.Equal(t, "2001:db8:f00d::200", moved.Internet.SourceAddresses[0].Address)
	assert.Equal(t,
		networkingv1alpha.InternetEgressAddressStabilityNetwork,
		moved.Internet.SourceAddresses[0].Stability)

	unstructured.RemoveNestedField(attachment.Object, "status", "egress")
	require.NoError(t, s.cell.Update(s.ctx, attachment))

	s.reconcile(iface)
	assert.Nil(t, s.reported(iface), "a withdrawn address must not be left reported")
}

// TestInterfaceWithNoAttachmentIsHandled asserts an interface nothing has
// realized yet, and one naming an attachment that is not there, are both read
// without error and report nothing.
func TestInterfaceWithNoAttachmentIsHandled(t *testing.T) {
	s := newEgressScenario(t)

	unrealized := s.interfaceWithAttachment("unrealized", "")
	s.reconcile(unrealized)
	assert.Nil(t, s.reported(unrealized))

	dangling := s.interfaceWithAttachment("dangling", "never-created")
	s.reconcile(dangling)
	assert.Nil(t, s.reported(dangling))
}
