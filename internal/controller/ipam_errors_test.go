// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	"go.miloapis.com/ipam/pkg/ipamerrors"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

var ipclaimsResource = schema.GroupResource{Group: "ipam.miloapis.com", Resource: "ipclaims"}

// fromTheWire is the error the controller actually classifies: the refusal as
// it arrives after the apiserver serialised it and client-go decoded it. A
// classifier proven only against an in-process value proves nothing.
func fromTheWire(t *testing.T, err *apierrors.StatusError) error {
	t.Helper()
	data, marshalErr := json.Marshal(err.ErrStatus)
	require.NoError(t, marshalErr)
	var status metav1.Status
	require.NoError(t, json.Unmarshal(data, &status))
	return &apierrors.StatusError{ErrStatus: status}
}

func v4Request() allocationRequest {
	return allocationRequest{family: networkingv1alpha.IPv4Protocol}
}

func classRequest() allocationRequest {
	return allocationRequest{className: "public-v4", family: networkingv1alpha.IPv4Protocol}
}

// The condition reasons are our API surface. IPAM's taxonomy is finer than
// ours, and this is where the two are pinned together.
func TestClassifyAllocationFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *apierrors.StatusError
		want allocationFailureReason
	}{
		{"pool exhausted", ipamerrors.NewPoolExhausted("datum-v4", `IPPool "datum-v4" is exhausted`), allocationFailureExhausted},
		{"ancestor exhausted", ipamerrors.New(ipamerrors.ReasonExhausted, "ipam: pool exhausted"), allocationFailureExhausted},
		{"retained allocation", ipamerrors.NewRetainedAllocation(ipclaimsResource, "eth0-f-ipv4", "alloc-9f2c", "retained"), allocationFailureConflict},
		{"claim exists", ipamerrors.NewClaimExists(ipclaimsResource, "eth0-f-ipv4", "already holds an allocation"), allocationFailureConflict},
		{"class not found", ipamerrors.New(ipamerrors.ReasonClassNotFound, "ipam: class not found"), allocationFailureRejected},
		{"no default class", ipamerrors.New(ipamerrors.ReasonNoDefaultClass, "ipam: no default class"), allocationFailureRejected},
		{"no offering pool", ipamerrors.New(ipamerrors.ReasonNoOfferingPool, "ipam: no pool offers this class"), allocationFailureRejected},
		{"prefix length", ipamerrors.New(ipamerrors.ReasonPrefixLengthRejected, "prefixLength 20 is outside the class range"), allocationFailureRejected},
		{"scope roles", ipamerrors.NewScopeRolesMissing([]string{"location"}, "scope is missing role"), allocationFailureRejected},
		{"no project scope", ipamerrors.New(ipamerrors.ReasonNoProjectScope, "ipam: request carries no project scope"), allocationFailureRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, classifyAllocationFailure(fromTheWire(t, tc.err)))
		})
	}
}

// A reason added to IPAM after this build, and anything that never came from
// IPAM, land on the catch-all. Guessing would put a wrong reason on our API.
func TestClassifyAllocationFailureDoesNotGuess(t *testing.T) {
	added := &apierrors.StatusError{ErrStatus: metav1.Status{
		Status: metav1.StatusFailure,
		Code:   http.StatusBadRequest,
		Details: &metav1.StatusDetails{Causes: []metav1.StatusCause{{
			Type: metav1.CauseType("AddedAfterThisBuild"),
		}}},
	}}
	require.Equal(t, allocationFailureUnknown, classifyAllocationFailure(fromTheWire(t, added)))
	require.Equal(t, allocationFailureUnknown, classifyAllocationFailure(errors.New("connection refused")))
	require.Equal(t, allocationFailureUnknown,
		classifyAllocationFailure(apierrors.NewInternalError(errors.New("boom"))))
}

// The refusal a missing namespace produces is not something to assume the shape
// of: it decides whether a claim is told its project has no namespace or is told
// something else entirely. So the discriminator is held against a real API
// server, alongside the other refusals that reach the same code path and must
// not be read as this one.
func TestNamespaceNotFoundIsReadOffTheServersOwnRefusal(t *testing.T) {
	_, cfg := startNetworkInterfaceEnv(t)
	ctx := context.Background()

	testScheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(testScheme))
	require.NoError(t, ipamv1alpha1.AddToScheme(testScheme))

	cl, err := client.New(cfg, client.Options{Scheme: testScheme})
	require.NoError(t, err)

	const missing = "no-such-namespace"
	inMissing := func(namespace string) client.Object {
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "probe"}}
	}

	// The write the namespace lifecycle admission plugin refuses. This is the
	// one case the claim is told about, and it names the namespace itself.
	refused := cl.Create(ctx, inMissing(missing))
	require.Error(t, refused)
	require.True(t, isNamespaceNotFound(refused, missing), "actual refusal: %v", refused)

	// The same refusal read against a namespace this caller never addressed.
	require.False(t, isNamespaceNotFound(refused, "some-other-namespace"))

	// A read in a missing namespace is a 404 about the object, so it says
	// nothing about the namespace and must not be read as if it did.
	var read corev1.ConfigMap
	readErr := cl.Get(ctx, client.ObjectKey{Namespace: missing, Name: "probe"}, &read)
	require.True(t, apierrors.IsNotFound(readErr))
	require.False(t, isNamespaceNotFound(readErr, missing), "actual refusal: %v", readErr)

	// An API this build knows but the server does not serve, which is what an
	// IPAM the platform has not installed looks like. It fails in discovery,
	// with no status at all.
	unserved := cl.Create(ctx, &ipamv1alpha1.IPClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: missing, Name: "probe"}})
	require.Error(t, unserved)
	require.False(t, isNamespaceNotFound(unserved, missing), "actual refusal: %v", unserved)

	// A project path that 404s, as a deleted project's does. It also fails in
	// discovery, and reads as a NotFound while naming nothing, which is why the
	// status code alone cannot carry this decision.
	pathConfig := rest.CopyConfig(cfg)
	pathConfig.Host += "/apis/resourcemanager.miloapis.com/v1alpha1/projects/deleted/control-plane"
	pathClient, err := client.New(pathConfig, client.Options{Scheme: testScheme})
	require.NoError(t, err)

	gone := pathClient.Create(ctx, inMissing("default"))
	require.Error(t, gone)
	require.True(t, apierrors.IsNotFound(gone), "a deleted project's path reads as a NotFound")
	require.False(t, isNamespaceNotFound(gone, "default"), "actual refusal: %v", gone)
}

// An operator may upgrade this operator before IPAM. An older server answers
// exhaustion with a bare 507 and no reason, and that must still read as
// exhaustion rather than as an unexplained failure.
func TestClassifyAllocationFailureReadsAnOlderServer(t *testing.T) {
	legacy := apierrors.NewGenericServerResponse(
		507, "POST", ipclaimsResource, "", "IPPool exhausted", 0, false)
	require.Equal(t, allocationFailureExhausted, classifyAllocationFailure(fromTheWire(t, legacy)))
}

// Every branch, rendered whole. A test matching a fragment passes while the
// rest of the message repeats itself, which is how a doubly-named pool shipped.
func TestAllocationFailureMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request allocationRequest
		err     *apierrors.StatusError
		want    string
	}{
		{
			name:    "exhaustion names the pool once",
			request: v4Request(),
			err:     ipamerrors.NewPoolExhausted("datum-v4", `IPPool "datum-v4" is exhausted`),
			want:    `No address is left for an IPv4 address: IPPool "datum-v4" is exhausted`,
		},
		{
			// No pool accounts for this one, so IPAM's account of which level
			// of the chain ran out is all there is.
			name:    "exhaustion with no pool falls back to IPAM",
			request: classRequest(),
			err:     ipamerrors.New(ipamerrors.ReasonExhausted, `provisioning IPPool "datum-v4-us-east": ipam: pool exhausted`),
			want:    `No address is left for an address of class "public-v4": provisioning IPPool "datum-v4-us-east": ipam: pool exhausted`,
		},
		{
			name:    "retained allocation names the allocation once",
			request: v4Request(),
			err: ipamerrors.NewRetainedAllocation(ipclaimsResource, "eth0-f-ipv4", "alloc-9f2c",
				`an allocation under this identity already exists: IPAllocation "alloc-9f2c", retained by an earlier claim of the same name; delete it to reuse the name`),
			want: `Another allocation still holds the name for an IPv4 address: Operation cannot be fulfilled on ipclaims.ipam.miloapis.com "eth0-f-ipv4": an allocation under this identity already exists: IPAllocation "alloc-9f2c", retained by an earlier claim of the same name; delete it to reuse the name`,
		},
		{
			name:    "scope roles are named once, by IPAM",
			request: v4Request(),
			err: ipamerrors.NewScopeRolesMissing([]string{"network", "location"},
				`scope is missing roles "network", "location" required by uniqueWithin (class "public-v4")`),
			want: `IPAM would not allocate an IPv4 address: scope is missing roles "network", "location" required by uniqueWithin (class "public-v4")`,
		},
		{
			name:    "an unconfigured class is named by IPAM, with its project",
			request: classRequest(),
			err:     ipamerrors.New(ipamerrors.ReasonClassNotFound, `ipam: class not found: "public-v4" in project "shared-infra"`),
			want:    `IPAM would not allocate an address of class "public-v4": ipam: class not found: "public-v4" in project "shared-infra"`,
		},
		{
			name:    "no pool offers the class",
			request: classRequest(),
			err:     ipamerrors.New(ipamerrors.ReasonNoOfferingPool, `ipam: no pool offers this class: class "public-v4" in project "shared-infra"`),
			want:    `IPAM would not allocate an address of class "public-v4": ipam: no pool offers this class: class "public-v4" in project "shared-infra"`,
		},
		{
			name:    "a failure we cannot classify is still reported",
			request: v4Request(),
			err:     apierrors.NewInternalError(errors.New("postgres is unreachable")),
			want:    "Allocating an IPv4 address failed: Internal error occurred: postgres is unreachable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := fromTheWire(t, tc.err)
			require.Equal(t, tc.want,
				allocationFailureMessage(classifyAllocationFailure(err), tc.request, err))
		})
	}
}

// The message must never carry a fact twice. An operator reading a condition
// should learn something from every clause of it.
func TestMessagesDoNotRepeatThemselves(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    *apierrors.StatusError
		repeat string
	}{
		{"pool", ipamerrors.NewPoolExhausted("datum-v4", `IPPool "datum-v4" is exhausted`), "datum-v4"},
		{
			"roles",
			ipamerrors.NewScopeRolesMissing([]string{"location"}, `scope is missing role "location" required by uniqueWithin`),
			`"location"`,
		},
		{
			"allocation",
			ipamerrors.NewRetainedAllocation(ipclaimsResource, "eth0-f-ipv4", "alloc-9f2c", `IPAllocation "alloc-9f2c" is retained`),
			"alloc-9f2c",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := fromTheWire(t, tc.err)
			message := allocationFailureMessage(classifyAllocationFailure(err), v4Request(), err)
			require.Contains(t, message, tc.repeat)
			require.Equal(t, 1, countOccurrences(message, tc.repeat),
				"%q appears more than once in %q", tc.repeat, message)
		})
	}
}

func countOccurrences(haystack, needle string) int {
	count := 0
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			count++
		}
	}
	return count
}
