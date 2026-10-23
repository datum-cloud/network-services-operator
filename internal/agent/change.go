// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

// Every change tool runs in two calls, and only the second writes.
//
// The first, with dryRun, works the change out from the load balancer as it is
// now, has the platform validate it without keeping it, and returns the load
// balancer as it would be plus a confirm value. That value hashes the exact
// patch, and the patch carries the resourceVersion it was computed against.
//
// The second repeats the call with that confirm value. It works the change out
// again and refuses unless it hashes the same, so what is written is exactly
// what was previewed against exactly the load balancer that was read, or
// nothing at all. A change made in between, by anyone, means previewing again.
//
// The confirm value binds a preview to its apply; it is not a credential. The
// write still runs as the caller, who could make it some other way.

// ChangeOutput is what one change tool call reports.
type ChangeOutput struct {
	// DryRun is true for a preview, which changed nothing.
	DryRun bool `json:"dryRun"`
	// Changed is false when the load balancer already says what was asked, in
	// which case nothing was or will be written and there is nothing to confirm.
	Changed bool `json:"changed"`
	// Summary says what the change does, in the customer's terms.
	Summary string `json:"summary"`
	// Confirm is returned by a preview. Pass it back, unchanged, to make the
	// change once the person has agreed to it.
	Confirm string `json:"confirm,omitempty"`
	// Notes are things the person should know that did not stop the change.
	Notes []string `json:"notes,omitempty"`
	// LoadBalancer is the load balancer as it will be after a preview, or as
	// the platform returned it after the change.
	LoadBalancer LoadBalancerView `json:"loadBalancer"`
}

// proxyChange is one change to a load balancer, described before anything is
// read so that a preview and its apply derive it the same way.
type proxyChange struct {
	name    string
	summary string
	// mutate works the change out from the load balancer as it is now.
	mutate func(*networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error)
	// services are origins naming a network service, which must exist and
	// declare the port named.
	services []spec.BackendInput
	// hostnames are being attached, and must sit on a verified domain.
	hostnames []string
}

var errNoWriter = errors.New("this server cannot change load balancers")

func runChange(ctx context.Context, deps DepsFor, c proxyChange, dryRun bool, confirm string) (ChangeOutput, error) {
	d, err := deps(ctx)
	if err != nil {
		return ChangeOutput{}, err
	}
	if d.Writer == nil {
		return ChangeOutput{}, errNoWriter
	}
	if c.name == "" {
		return ChangeOutput{}, fmt.Errorf("name is required")
	}
	if !dryRun && confirm == "" {
		return ChangeOutput{}, fmt.Errorf("preview the change first: call again with dryRun true, show " +
			"the person what it does, and once they agree pass back the confirm value it returns")
	}

	current, err := d.Reader.GetProxy(ctx, d.Namespace, c.name)
	if err != nil {
		return ChangeOutput{}, err
	}
	updated, err := c.mutate(current)
	if err != nil {
		return ChangeOutput{}, toolError(err)
	}

	unlocked, err := client.MergeFrom(current).Data(updated)
	if err != nil {
		return ChangeOutput{}, fmt.Errorf("working out the change: %w", err)
	}
	if string(unlocked) == "{}" {
		return ChangeOutput{
			DryRun:       dryRun,
			Summary:      "Nothing to change: the load balancer already does this.",
			LoadBalancer: buildView(ctx, d.Reader, d.Namespace, current, nowFunc()),
		}, nil
	}

	if err := checkServices(ctx, d, c.services); err != nil {
		return ChangeOutput{}, toolError(err)
	}
	notes, err := checkHostnames(ctx, d, c.hostnames)
	if err != nil {
		return ChangeOutput{}, toolError(err)
	}

	patch, err := lockedPatch(current).Data(updated)
	if err != nil {
		return ChangeOutput{}, fmt.Errorf("working out the change: %w", err)
	}
	token := confirmToken(c.name, patch)
	if !dryRun && confirm != token {
		return ChangeOutput{}, errMoved
	}

	if err := d.Writer.PatchProxy(ctx, current, updated, dryRun); err != nil {
		if apierrors.IsConflict(err) {
			return ChangeOutput{}, errMoved
		}
		return ChangeOutput{}, toolError(util.ClassifyProxyPatchError(c.name, err))
	}

	out := ChangeOutput{
		DryRun:       dryRun,
		Changed:      true,
		Summary:      c.summary,
		Notes:        notes,
		LoadBalancer: buildView(ctx, d.Reader, d.Namespace, updated, nowFunc()),
	}
	if dryRun {
		out.Confirm = token
	}
	return out, nil
}

var errMoved = errors.New("the load balancer changed since this was previewed, or this is not the " +
	"change that was previewed. Nothing was written. Preview it again and show the person the new result")

// confirmToken stands for one patch to one load balancer. The patch includes
// the resourceVersion it was computed against, so any change in between gives
// a different token.
func confirmToken(name string, patch []byte) string {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte{0})
	h.Write(patch)
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// checkServices refuses an origin naming a network service that does not
// exist or does not declare the port. The platform accepts either, and the
// whole load balancer then stays unpublished.
func checkServices(ctx context.Context, d ToolDeps, backends []spec.BackendInput) error {
	for _, b := range backends {
		if b.NetworkService == "" {
			continue
		}
		service, err := d.Reader.GetNetworkService(ctx, d.Namespace, b.NetworkService)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return util.NewCLIError(util.ExitNotFound, fmt.Sprintf("network service %q not found", b.NetworkService)).
					WithFix("it has to exist before a route can send traffic to it. This service never creates one.")
			}
			return err
		}
		if err := util.EnsureNetworkServicePort(service, b.Port); err != nil {
			return err
		}
	}
	return nil
}

// checkHostnames refuses a hostname no verified domain covers yet. One that is
// attached anyway sits unpublished until the domain verifies. A caller not
// allowed to list domains is let through with a note, as the CLI does.
func checkHostnames(ctx context.Context, d ToolDeps, hostnames []string) ([]string, error) {
	if len(hostnames) == 0 {
		return nil, nil
	}
	domains, err := d.Reader.ListDomains(ctx, d.Namespace)
	if err != nil {
		if apierrors.IsForbidden(err) {
			return []string{"Not allowed to list domains, so whether the hostname's domain is verified was not checked."}, nil
		}
		return nil, err
	}
	unverified := spec.UnverifiedHostnames(hostnames, domains)
	if len(unverified) == 0 {
		return nil, nil
	}
	return nil, util.NewCLIError(util.ExitInvalid,
		fmt.Sprintf("hostname %q is not on a verified domain: %s", unverified[0].Hostname, unverified[0].Reason)).
		WithFix("the domain has to be verified first; load the domain-verification skill for the record to create")
}

// toolError keeps the fix a CLI error carries, which is otherwise lost when
// the error is reduced to its message.
func toolError(err error) error {
	var cliErr *util.CLIError
	if errors.As(err, &cliErr) && cliErr.Fix() != "" {
		return fmt.Errorf("%s. Fix: %s", cliErr.Error(), cliErr.Fix())
	}
	return err
}
