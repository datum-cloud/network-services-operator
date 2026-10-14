// SPDX-License-Identifier: AGPL-3.0-only

package controller

// fieldOwner names the field manager that records one controller's saves.
func fieldOwner(controller string) string {
	return "network-services/" + controller
}
