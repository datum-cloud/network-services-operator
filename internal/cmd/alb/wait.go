// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

var (
	waitInterval    = 2 * time.Second
	defaultWaitTime = 2 * time.Minute
)

func waitForCanonicalHostname(ctx context.Context, c client.Client, name string, timeout time.Duration) (string, error) {
	var hostname string
	err := wait.PollUntilContextTimeout(ctx, waitInterval, timeout, true, func(ctx context.Context) (bool, error) {
		proxy := &networkingv1alpha.HTTPProxy{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: util.ResourceNamespace, Name: name}, proxy); err != nil {
			return false, err
		}
		if proxy.Status.CanonicalHostname != "" {
			hostname = proxy.Status.CanonicalHostname
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		if wait.Interrupted(err) {
			return "", util.NewCLIError(util.ExitUnavailable,
				fmt.Sprintf("timed out waiting for a hostname on %q", name)).
				WithFix("check status with:\n       datumctl alb describe " + name).
				WithCause(err)
		}
		return "", util.ClassifyError(fmt.Errorf("waiting for hostname: %w", err))
	}
	return hostname, nil
}
