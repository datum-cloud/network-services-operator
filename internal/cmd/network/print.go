// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"go.datum.net/network-services-operator/internal/networkagent"
)

func whoShort(a networkagent.Actionability) string {
	switch a {
	case networkagent.ActionabilityUser:
		return "you"
	case networkagent.ActionabilityPlatform, networkagent.ActionabilityStalled:
		return "Datum"
	case networkagent.ActionabilityTransient:
		return "in progress"
	default:
		return "info"
	}
}
