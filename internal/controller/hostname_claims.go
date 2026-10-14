// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

// hostnameClaimAncestorIndex indexes each hostname claim under every domain it
// sits beneath, so the claims a wildcard would cover are one indexed lookup
// rather than a scan of every claim.
const hostnameClaimAncestorIndex = "hostnameClaim.ancestors"

const jsonKeyHostname = "hostname"

const wildcardClaimPrefix = "wildcard-"

const maxNamedClaimConflicts = 5

const (
	awaitingHostnameClaimAnnotation = "networking.datumapis.com/awaiting-hostname-claim"
	awaitingHostnameClaimValue      = "true"
	awaitingHostnameClaimIndex      = "gateway.awaitingHostnameClaim"
)

// hostnameClaimName names the ConfigMap that claims a hostname. A wildcard
// cannot be a ConfigMap name, so its claim is named from a hash of its base;
// the name has no dot, so no custom hostname's claim can ever take it.
func hostnameClaimName(hostname string) string {
	base, ok := strings.CutPrefix(hostname, "*.")
	if !ok {
		return hostname
	}
	sum := sha256.Sum256([]byte(base))
	return wildcardClaimPrefix + hex.EncodeToString(sum[:])[:40]
}

func claimedHostname(claim *corev1.ConfigMap) string {
	if hostname := claim.Data[jsonKeyHostname]; hostname != "" {
		return hostname
	}
	return claim.Name
}

func hostnameClaimProject(upstreamClusterName string) string {
	return fmt.Sprintf("cluster-%s", strings.ReplaceAll(upstreamClusterName, "/", "_"))
}

// hostnameAncestors returns every domain strictly above a hostname, nearest
// first: "a.b.example.com" gives "b.example.com", "example.com" and "com".
func hostnameAncestors(hostname string) []string {
	var ancestors []string
	for rest := hostname; ; {
		_, parent, found := strings.Cut(rest, ".")
		if !found || parent == "" {
			return ancestors
		}
		ancestors = append(ancestors, parent)
		rest = parent
	}
}

func isHostnameClaim(cm *corev1.ConfigMap, namespace string) bool {
	return cm.Namespace == namespace && cm.Data[jsonKeyOwner] != ""
}

func hostnameClaimAncestorIndexFunc(namespace string) client.IndexerFunc {
	return func(obj client.Object) []string {
		claim, ok := obj.(*corev1.ConfigMap)
		if !ok || !isHostnameClaim(claim, namespace) {
			return nil
		}
		return hostnameAncestors(claimedHostname(claim))
	}
}

func awaitsHostnameClaim(refusals map[string]hostnameRefusal) bool {
	for _, refusal := range refusals {
		if refusal.reason == networkingv1alpha.HostnameInUseReason {
			return true
		}
	}
	return false
}

func awaitingHostnameClaimIndexFunc(obj client.Object) []string {
	if obj.GetAnnotations()[awaitingHostnameClaimAnnotation] != awaitingHostnameClaimValue {
		return nil
	}
	return []string{awaitingHostnameClaimValue}
}

func markAwaitingHostnameClaim(gateway *gatewayv1.Gateway, awaiting bool) {
	if awaiting {
		metav1.SetMetaDataAnnotation(&gateway.ObjectMeta, awaitingHostnameClaimAnnotation, awaitingHostnameClaimValue)
		return
	}
	delete(gateway.Annotations, awaitingHostnameClaimAnnotation)
}

// claimPrecedes reports whether claim a was made before claim b. Two claims
// made in the same second are ordered by name, so both sides agree.
func claimPrecedes(a, b *corev1.ConfigMap) bool {
	if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
		return a.CreationTimestamp.Before(&b.CreationTimestamp)
	}
	return a.Name < b.Name
}

// subtreeClaimConflict lists the hostnames other projects hold that a claim
// would overlap: wildcards above it, and for a wildcard, names beneath it.
type subtreeClaimConflict struct {
	above   []string
	beneath []string
}

func (c subtreeClaimConflict) found() bool {
	return len(c.above) > 0 || len(c.beneath) > 0
}

// subtreeClaimConflicts finds what a claim on hostname would overlap. The edge
// prefers an exact match over a wildcard, so a wildcard reserves every name
// beneath it at any depth. When the claim already exists, only claims made
// before it count, so two claims that raced settle on the older one.
func subtreeClaimConflicts(
	ctx context.Context,
	reader client.Reader,
	namespace, project, hostname string,
	existing *corev1.ConfigMap,
) (subtreeClaimConflict, error) {
	var conflict subtreeClaimConflict
	counts := func(other *corev1.ConfigMap) bool {
		if other.Labels[downstreamclient.UpstreamOwnerClusterNameLabel] == project {
			return false
		}
		return existing == nil || claimPrecedes(other, existing)
	}

	base, wildcard := strings.CutPrefix(hostname, "*.")
	for _, ancestor := range hostnameAncestors(base) {
		var above corev1.ConfigMap
		err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: hostnameClaimName("*." + ancestor)}, &above)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return conflict, fmt.Errorf("failed to look up wildcard claim above %s: %w", hostname, err)
		}
		if counts(&above) {
			conflict.above = append(conflict.above, claimedHostname(&above))
		}
	}

	if wildcard {
		var beneath corev1.ConfigMapList
		if err := reader.List(ctx, &beneath, client.InNamespace(namespace), client.MatchingFields{hostnameClaimAncestorIndex: base}); err != nil {
			return conflict, fmt.Errorf("failed to list claims beneath %s: %w", hostname, err)
		}
		for i := range beneath.Items {
			if counts(&beneath.Items[i]) {
				conflict.beneath = append(conflict.beneath, claimedHostname(&beneath.Items[i]))
			}
		}
		slices.Sort(conflict.beneath)
	}

	return conflict, nil
}

// subtreeConflictMessage explains a refused claim. It names hostnames under a
// domain the requester has proven it owns, and never the project holding them.
func subtreeConflictMessage(hostname string, conflict subtreeClaimConflict) string {
	const noOverride = "Taking names back from another project is not supported yet"
	if len(conflict.above) > 0 {
		return fmt.Sprintf("The hostname %q is beneath the wildcard %q, which another project has claimed. %s; that project must remove its wildcard first.",
			hostname, conflict.above[0], noOverride)
	}
	named, more := conflict.beneath, ""
	if len(named) > maxNamedClaimConflicts {
		more = fmt.Sprintf(" and %d more", len(named)-maxNamedClaimConflicts)
		named = named[:maxNamedClaimConflicts]
	}
	return fmt.Sprintf("The wildcard %q cannot be claimed while another project serves names beneath it: %s%s. %s; that project must remove them first.",
		hostname, strings.Join(named, ", "), more, noOverride)
}
