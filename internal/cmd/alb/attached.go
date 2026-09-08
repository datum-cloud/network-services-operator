// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func findTPPForProxy(ctx context.Context, c client.Client, proxyName string) (*networkingv1alpha.TrafficProtectionPolicy, error) {
	var list networkingv1alpha.TrafficProtectionPolicyList
	if err := c.List(ctx, &list, client.InNamespace(util.ResourceNamespace)); err != nil {
		return nil, util.ClassifyError(fmt.Errorf("listing traffic protection policies: %w", err))
	}
	for i := range list.Items {
		if spec.TPPTargetsProxy(&list.Items[i], proxyName) {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

func tppModeByProxy(ctx context.Context, c client.Client) map[string]string {
	modes := map[string]string{}
	var list networkingv1alpha.TrafficProtectionPolicyList
	if err := c.List(ctx, &list, client.InNamespace(util.ResourceNamespace)); err != nil {
		return modes
	}
	for i := range list.Items {
		policy := &list.Items[i]
		for _, ref := range policy.Spec.TargetRefs {
			if strings.EqualFold(string(ref.Kind), "Gateway") {
				modes[string(ref.Name)] = spec.TPPMode(policy)
			}
		}
	}
	return modes
}

func getSecurityPolicy(ctx context.Context, c client.Client, name string) (*envoygatewayv1alpha1.SecurityPolicy, error) {
	policy := &envoygatewayv1alpha1.SecurityPolicy{}
	err := c.Get(ctx, types.NamespacedName{Namespace: util.ResourceNamespace, Name: name}, policy)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, util.ClassifyError(fmt.Errorf("getting basic auth policy for %q: %w", name, err))
	}
	return policy, nil
}

func getBasicAuthSecret(ctx context.Context, c client.Client, proxyName string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{Namespace: util.ResourceNamespace, Name: spec.BasicAuthSecretName(proxyName)}, secret)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, util.ClassifyError(fmt.Errorf("getting basic auth secret for %q: %w", proxyName, err))
	}
	return secret, nil
}

func deleteIfExists(ctx context.Context, c client.Client, obj client.Object, dryRun bool) error {
	opts := util.DryRunDeleteOpts(dryRun)
	if err := c.Delete(ctx, obj, opts...); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func patchProxy(ctx context.Context, c client.Client, original, updated *networkingv1alpha.HTTPProxy, dryRun bool) error {
	opts := []client.PatchOption{client.FieldOwner(util.FieldManager)}
	if dryRun {
		opts = append(opts, client.DryRunAll)
	}
	return c.Patch(ctx, updated, client.MergeFrom(original), opts...)
}

func patchTPP(ctx context.Context, c client.Client, original, updated *networkingv1alpha.TrafficProtectionPolicy, dryRun bool) error {
	opts := []client.PatchOption{client.FieldOwner(util.FieldManager)}
	if dryRun {
		opts = append(opts, client.DryRunAll)
	}
	return c.Patch(ctx, updated, client.MergeFrom(original), opts...)
}

func patchOpts(dryRun bool) []client.PatchOption {
	opts := []client.PatchOption{client.FieldOwner(util.FieldManager)}
	if dryRun {
		opts = append(opts, client.DryRunAll)
	}
	return opts
}

func mutateProxy(
	cmd *cobra.Command,
	name string,
	mutate func(*networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error),
	success string,
) error {
	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	current, err := util.GetHTTPProxy(cmd.Context(), c, name)
	if err != nil {
		return err
	}
	updated, err := mutate(current)
	if err != nil {
		return err
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if err := patchProxy(cmd.Context(), c, current, updated, dryRun); err != nil {
		return util.ClassifyError(fmt.Errorf("updating application load balancer %q: %w", name, err))
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), success)
	return nil
}
