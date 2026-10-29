// SPDX-License-Identifier: AGPL-3.0-only
package nlb

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/spf13/cobra"
	api "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/plugincli"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/cmd/nlb/spec"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func createCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "create <name>", Short: "Publish an existing service through a Network Load Balancer", Args: cobra.ExactArgs(1), RunE: runCreate}
	cmd.Flags().String("network", "", "Network containing the backend service")
	cmd.Flags().String("listener", "", "Name for the first listener")
	cmd.Flags().String("display-name", "", "Friendly display name")
	listenerFlags(cmd)
	addressFlags(cmd)
	backendFlags(cmd)
	mutationFlags(cmd)
	waitFlags(cmd)
	return cmd
}
func listenerFromFlags(cmd *cobra.Command, name string) (api.NetworkLoadBalancerListener, error) {
	var interval *api.PortRange
	if value := str(cmd, "port-range"); value != "" {
		var err error
		interval, err = spec.ParseRange(value)
		if err != nil {
			return api.NetworkLoadBalancerListener{}, err
		}
	}
	port, _ := cmd.Flags().GetInt32("port")
	addresses, _ := cmd.Flags().GetStringSlice("address")
	return spec.Listener(name, str(cmd, "protocol"), port, interval, addresses)
}
func backendFromFlags(cmd *cobra.Command) (api.NetworkLoadBalancerRouteBackend, error) {
	return spec.Backend(str(cmd, "network-service"), str(cmd, "backend-port"), str(cmd, "health-protocol"), str(cmd, "health-port"), str(cmd, "health-path"))
}
func addressFromFlags(cmd *cobra.Command, name string) (api.NetworkLoadBalancerAddressRequest, error) {
	locations, _ := cmd.Flags().GetStringSlice("location")
	return spec.Address(name, str(cmd, "families"), str(cmd, "location-source"), locations)
}
func preflight(cmd *cobra.Command, c client.Client, n *api.NetworkLoadBalancer, l api.NetworkLoadBalancerListener, b api.NetworkLoadBalancerRouteBackend) error {
	var network api.Network
	if err := c.Get(cmd.Context(), key(n.Spec.NetworkRef.Name), &network); err != nil {
		return fmt.Errorf("reading network: %w", err)
	}
	var service api.NetworkService
	if err := c.Get(cmd.Context(), key(b.NetworkService.Name), &service); err != nil {
		return fmt.Errorf("reading backend service: %w", err)
	}
	if err := spec.ValidateBackend(l, b, &service); err != nil {
		return util.UsageErrorf("%s", err)
	}
	// Match live interfaces rather than the service's capped status summary.
	selector, err := metav1.LabelSelectorAsSelector(&service.Spec.NetworkInterfaces.Selector)
	if err != nil {
		return err
	}
	var interfaces api.NetworkInterfaceList
	if err := c.List(cmd.Context(), &interfaces, client.InNamespace(spec.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return fmt.Errorf("checking backend network: %w", err)
	}
	for _, nic := range interfaces.Items {
		if nic.Status.Phase == api.NetworkInterfacePhaseBound && nic.Spec.Network.Name != n.Spec.NetworkRef.Name {
			return util.UsageErrorf("service %q selects interface %q on network %q, but the NLB uses %q", service.Name, nic.Name, nic.Spec.Network.Name, n.Spec.NetworkRef.Name)
		}
	}
	return nil
}
func createObject(cmd *cobra.Command, c client.Client, obj client.Object) error {
	opts := []client.CreateOption{client.FieldOwner(spec.ManagedBy)}
	if boolean(cmd, "dry-run") {
		opts = append(opts, client.DryRunAll)
	}
	return c.Create(cmd.Context(), obj, opts...)
}
func runCreate(cmd *cobra.Command, args []string) error {
	l, err := listenerFromFlags(cmd, str(cmd, "listener"))
	if err != nil {
		return util.UsageErrorf("%s", err)
	}
	if len(l.AddressRefs) != 1 {
		return util.UsageErrorf("create takes one address request; add more with nlb address add")
	}
	a, err := addressFromFlags(cmd, l.AddressRefs[0])
	if err != nil {
		return util.UsageErrorf("%s", err)
	}
	b, err := backendFromFlags(cmd)
	if err != nil {
		return util.UsageErrorf("%s", err)
	}
	n := &api.NetworkLoadBalancer{TypeMeta: metav1.TypeMeta{APIVersion: api.GroupVersion.String(), Kind: "NetworkLoadBalancer"}, ObjectMeta: metav1.ObjectMeta{Name: args[0], Namespace: spec.Namespace, Labels: map[string]string{"app.kubernetes.io/managed-by": spec.ManagedBy}}, Spec: api.NetworkLoadBalancerSpec{NetworkRef: api.LocalNetworkRef{Name: str(cmd, "network")}, Exposure: api.NetworkLoadBalancerExposure{Addresses: []api.NetworkLoadBalancerAddressRequest{a}}, Listeners: []api.NetworkLoadBalancerListener{l}}}
	if display := str(cmd, "display-name"); display != "" {
		n.Annotations = map[string]string{spec.DisplayName: display}
	}
	if err := spec.ValidateNLB(n); err != nil {
		return util.UsageErrorf("%s", err)
	}
	if err := checkTimeout(cmd); err != nil {
		return err
	}
	c, err := newClient(plugincli.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	if err := preflight(cmd, c, n, l, b); err != nil {
		return err
	}
	// A retry may finish a partially completed create, but never adopts other objects.
	existing := &api.NetworkLoadBalancer{}
	getErr := c.Get(cmd.Context(), key(n.Name), existing)
	resume := getErr == nil
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return getErr
	}
	if resume {
		if existing.Labels["app.kubernetes.io/managed-by"] != spec.ManagedBy || !reflect.DeepEqual(existing.Spec, n.Spec) || existing.DeletionTimestamp != nil {
			return util.UsageErrorf("load balancer %q already exists with different settings; use describe and update", n.Name)
		}
		n = existing
	}
	r := spec.NewRoute(n, l, b)
	var prior api.NetworkLoadBalancerRoute
	routeErr := c.Get(cmd.Context(), key(r.Name), &prior)
	if routeErr == nil {
		if !resume || !spec.Owned(&prior, n) || !reflect.DeepEqual(prior.Spec, r.Spec) {
			return util.UsageErrorf("route %q already exists; inspect it before retrying", r.Name)
		}
	} else if !apierrors.IsNotFound(routeErr) {
		return routeErr
	}
	if !resume {
		if err := createObject(cmd, c, n); err != nil {
			return err
		}
	}
	r = spec.NewRoute(n, l, b)
	if routeErr != nil {
		if err := createObject(cmd, c, r); err != nil {
			return partial(n.Name, "load balancer saved; route "+r.Name+" could not be created", err)
		}
	} else {
		r = &prior
	}
	if boolean(cmd, "dry-run") {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Preview only. Separate dry runs cannot fully check a route against a parent that has not been saved.")
	}
	if err := waitReady(cmd, c, n, l.Name, r); err != nil {
		return err
	}
	if !boolean(cmd, "dry-run") && !boolean(cmd, "no-wait") {
		if err := c.Get(cmd.Context(), key(n.Name), n); err != nil {
			return err
		}
		if err := c.Get(cmd.Context(), key(r.Name), r); err != nil {
			return err
		}
	}
	return printValue(cmd, detail{n, []api.NetworkLoadBalancerRoute{*r}}, func() error {
		verb := "Created"
		if resume {
			verb = "Resumed"
		}
		if boolean(cmd, "dry-run") {
			verb = "Validated"
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s NLB %q; route %q.\nNext: datumctl nlb endpoints %s\n", verb, n.Name, r.Name, n.Name)
		return err
	})
}
func partial(name, what string, err error) error {
	return util.ClassifyError(fmt.Errorf("%s: %w", what, err)).WithFix("inspect saved resources with datumctl nlb describe " + name + "; retry the same create/add command to finish without allocating another NLB")
}
func checkTimeout(cmd *cobra.Command) error {
	d, _ := cmd.Flags().GetDuration("timeout")
	if d <= 0 {
		return util.UsageErrorf("--timeout must be greater than zero")
	}
	return nil
}
func waitReady(cmd *cobra.Command, c client.Client, expectedNLB *api.NetworkLoadBalancer, listener string, expectedRoute *api.NetworkLoadBalancerRoute) error {
	if boolean(cmd, "dry-run") || boolean(cmd, "no-wait") {
		return nil
	}
	if err := checkTimeout(cmd); err != nil {
		return err
	}
	d, _ := cmd.Flags().GetDuration("timeout")
	ctx, cancel := context.WithTimeout(cmd.Context(), d)
	defer cancel()
	name := expectedNLB.Name
	previous := ""
	for {
		var n api.NetworkLoadBalancer
		if err := c.Get(ctx, key(name), &n); err != nil {
			if ctx.Err() != nil {
				break
			}
			return err
		}
		if n.UID != expectedNLB.UID || n.Generation != expectedNLB.Generation {
			return util.ClassifyError(fmt.Errorf("load balancer %q changed while waiting", name)).WithFix("inspect its current settings with datumctl nlb describe " + name)
		}
		var rs api.NetworkLoadBalancerRouteList
		if err := c.List(ctx, &rs, client.InNamespace(spec.Namespace)); err != nil {
			if ctx.Err() != nil {
				break
			}
			return err
		}
		ready, msg := spec.ReadyForRoute(&n, rs.Items, listener, expectedRoute)
		if ready {
			return nil
		}
		if msg != previous && !plugincli.QuietFromCmd(cmd) {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), msg)
			previous = msg
		}
		select {
		case <-ctx.Done():
			return waitError(name, fmt.Errorf("%s: %w", msg, ctx.Err()))
		case <-time.After(time.Second):
		}
	}
	return waitError(name, ctx.Err())
}
func waitError(name string, err error) error {
	return util.ClassifyError(fmt.Errorf("settings saved for %q; waiting ended: %w", name, err)).WithFix("check progress with datumctl nlb describe " + name)
}
func updateCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "update <name>", Short: "Change a load balancer's display name", Args: cobra.ExactArgs(1), ValidArgsFunction: completeNLB, RunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("display-name") {
			return util.UsageErrorf("set --display-name")
		}
		c, err := newClient(plugincli.ProjectFromCmd(cmd))
		if err != nil {
			return err
		}
		n, err := getNLB(cmd, c, args[0])
		if err != nil {
			return err
		}
		before := n.DeepCopy()
		if n.Annotations == nil {
			n.Annotations = map[string]string{}
		}
		n.Annotations[spec.DisplayName] = str(cmd, "display-name")
		if err := patch(cmd, c, before, n); err != nil {
			return err
		}
		return saved(cmd, n, n.Name)
	}}
	cmd.Flags().String("display-name", "", "Friendly display name")
	mutationFlags(cmd)
	return cmd
}
func saved(cmd *cobra.Command, value any, name string) error {
	return printValue(cmd, value, func() error {
		verb := "Saved"
		if boolean(cmd, "dry-run") {
			verb = "Validated"
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %q.\n", verb, name)
		return err
	})
}
func confirm(cmd *cobra.Command, message, name string) error {
	if boolean(cmd, "dry-run") || plugincli.AssumeYes(cmd) {
		return nil
	}
	ok, err := util.ConfirmTyped(cmd.InOrStdin(), cmd.ErrOrStderr(), message, name)
	if err != nil {
		return err
	}
	if !ok {
		return util.NewCLIError(util.ExitAborted, "aborted")
	}
	return nil
}
func deleteObject(cmd *cobra.Command, c client.Client, obj client.Object) error {
	uid, rv := obj.GetUID(), obj.GetResourceVersion()
	opts := []client.DeleteOption{client.Preconditions{UID: &uid, ResourceVersion: &rv}}
	if boolean(cmd, "dry-run") {
		opts = append(opts, client.DryRunAll)
	}
	return c.Delete(cmd.Context(), obj, opts...)
}

func deletionRoutes(cmd *cobra.Command, c client.Client, n *api.NetworkLoadBalancer) (owned, external []string, err error) {
	// A route can retain its owner reference after its parent attachments or
	// management label change. Inspect all routes, not only attached routes.
	var routes api.NetworkLoadBalancerRouteList
	if err := c.List(cmd.Context(), &routes, client.InNamespace(n.Namespace)); err != nil {
		return nil, nil, err
	}
	var unsafe []string
	for _, r := range routes.Items {
		switch {
		case spec.Owned(&r, n):
			owned = append(owned, r.Name)
		case spec.HasOwnerReference(&r, n):
			unsafe = append(unsafe, r.Name)
		case spec.Attached(&r, n, ""):
			external = append(external, r.Name)
		}
	}
	if len(unsafe) > 0 {
		sort.Strings(unsafe)
		return nil, nil, util.UsageErrorf("cannot delete NLB %q: routes %v are shared or managed separately but still depend on it for automatic deletion", n.Name, unsafe).
			WithFix("inspect these routes and explicitly resolve their ownership or delete them before retrying; no resources were deleted")
	}
	sort.Strings(owned)
	sort.Strings(external)
	return owned, external, nil
}

func deleteCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "delete <name>", Aliases: []string{"rm"}, Short: "Remove public access while preserving backend services", Args: cobra.ExactArgs(1), ValidArgsFunction: completeNLB, RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkTimeout(cmd); err != nil {
			return err
		}
		c, err := newClient(plugincli.ProjectFromCmd(cmd))
		if err != nil {
			return err
		}
		n, err := getNLB(cmd, c, args[0])
		if err != nil {
			return err
		}
		owned, external, err := deletionRoutes(cmd, c, n)
		if err != nil {
			return err
		}
		message := fmt.Sprintf("Delete NLB %q, its public IPs and DNS, and owned routes %v? Backend services are kept. External routes %v are kept.", n.Name, owned, external)
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), message)
		if err := confirm(cmd, message, n.Name); err != nil {
			return err
		}
		// Confirmation may take time. Refuse changes to the deletion plan instead
		// of proceeding with stale promises about which routes will be kept.
		currentOwned, currentExternal, err := deletionRoutes(cmd, c, n)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(owned, currentOwned) || !reflect.DeepEqual(external, currentExternal) {
			return util.UsageErrorf("route attachments changed while preparing deletion; inspect NLB %q and retry; no resources were deleted", n.Name)
		}
		if err := deleteObject(cmd, c, n); err != nil {
			return err
		}
		if !boolean(cmd, "dry-run") && !boolean(cmd, "no-wait") {
			d, _ := cmd.Flags().GetDuration("timeout")
			ctx, cancel := context.WithTimeout(cmd.Context(), d)
			defer cancel()
			for {
				var current api.NetworkLoadBalancer
				err := c.Get(ctx, key(n.Name), &current)
				if apierrors.IsNotFound(err) {
					break
				}
				if err != nil {
					return util.ClassifyError(err).WithFix("deletion was requested; check datumctl nlb describe " + n.Name)
				}
				select {
				case <-ctx.Done():
					return fmt.Errorf("deletion requested for %q; cleanup is still pending: %w", n.Name, ctx.Err())
				case <-time.After(time.Second):
				}
			}
		}
		state := "Deletion requested"
		if boolean(cmd, "dry-run") {
			state = "Deletion validated"
		} else if !boolean(cmd, "no-wait") {
			state = "Deleted"
		}
		return printValue(cmd, map[string]any{"name": n.Name, "status": state, "ownedRoutes": owned, "preservedRoutes": external}, func() error { _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", state, n.Name); return err })
	}}
	mutationFlags(cmd)
	waitFlags(cmd)
	return cmd
}
