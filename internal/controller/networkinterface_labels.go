// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"maps"
	"strings"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// consumerLabelPrefixes are the prefixes a claim's own labels travel to its
// interface under. Whatever creates a claim describes it: compute stamps keys
// such as compute.datumapis.com/workload-name on the claims its workloads hold.
// A consumer selects the members of a network service by those keys, so they
// have to reach the interface, which is what a service selects and what is
// published to the consumer's project.
//
// It is an allow-list rather than a blanket copy so a key a consumer can write
// cannot land on a key the platform reads.
var consumerLabelPrefixes = []string{"compute.datumapis.com/"}

// platformLabelPrefixes are the prefixes whose keys on a published copy of an
// interface are the platform's to write. A copy is converged onto what its
// source says, so a key under one of these that the source no longer carries is
// removed rather than left behind.
var platformLabelPrefixes = []string{
	"networking.datumapis.com/",
	"compute.datumapis.com/",
	"meta.datumapis.com/",
}

func hasAnyPrefix(key string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// propagatedInterfaceLabels are the labels an interface carries on behalf of
// the claim holding it: the claim's allow-listed keys, and the location whose
// cell holds the interface.
func propagatedInterfaceLabels(
	claim *networkingv1alpha.NetworkInterfaceClaim,
	location networkingv1alpha.LocationReference,
) map[string]string {
	propagated := map[string]string{}
	for key, value := range claim.Labels {
		if hasAnyPrefix(key, consumerLabelPrefixes) {
			propagated[key] = value
		}
	}
	if location.Name != "" {
		propagated[networkingv1alpha.NetworkInterfaceLocationLabel] = location.Name
	}
	return propagated
}

// replaceOwnedLabels converges the owned part of a label set onto what is
// desired. Merging alone would leave a key whose source dropped it in place
// forever, and a stale key is a key a selector still matches.
func replaceOwnedLabels(
	current map[string]string,
	desired map[string]string,
	ownedPrefixes []string,
	ownedKeys []string,
) (map[string]string, bool) {
	updated := map[string]string{}
	maps.Copy(updated, current)

	for key := range current {
		if _, wanted := desired[key]; wanted {
			continue
		}
		if hasAnyPrefix(key, ownedPrefixes) {
			delete(updated, key)
			continue
		}
		for _, owned := range ownedKeys {
			if key == owned {
				delete(updated, key)
				break
			}
		}
	}
	maps.Copy(updated, desired)

	return updated, !maps.Equal(updated, current)
}
