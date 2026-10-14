// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"context"
	"fmt"
	"io"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

// requireVerifiedHostnames refuses hostnames whose domain is not verified yet.
// The platform would accept the change, then leave the load balancer stuck in
// Programming until the domain verifies, so stop before writing anything.
//
// The gateway controller stays the authority. When the caller cannot list
// Domains, the check is skipped with a warning rather than blocking the change.
func requireVerifiedHostnames(ctx context.Context, c client.Client, warn io.Writer, hostnames []string) error {
	if len(hostnames) == 0 {
		return nil
	}

	var domains networkingv1alpha.DomainList
	if err := c.List(ctx, &domains, client.InNamespace(util.ResourceNamespace)); err != nil {
		if apierrors.IsForbidden(err) {
			_, _ = fmt.Fprintln(warn, "Warning: not allowed to list domains, so hostname verification was not checked.")
			return nil
		}
		return util.ClassifyError(fmt.Errorf("listing domains: %w", err))
	}

	unverified := spec.UnverifiedHostnames(hostnames, domains.Items)
	if len(unverified) == 0 {
		return nil
	}

	msg := fmt.Sprintf("hostname %q is not on a verified domain: %s", unverified[0].Hostname, unverified[0].Reason)
	if len(unverified) > 1 {
		lines := make([]string, 0, len(unverified))
		for _, u := range unverified {
			lines = append(lines, fmt.Sprintf("  %s: %s", u.Hostname, u.Reason))
		}
		msg = "hostnames are not on a verified domain:\n" + strings.Join(lines, "\n")
	}
	return util.NewCLIError(util.ExitInvalid, msg).
		WithFix("verify the domain, then retry. Check verification state with:\n       datumctl get domains")
}
