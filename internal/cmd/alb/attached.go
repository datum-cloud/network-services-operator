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
	return c.Patch(ctx, updated, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}), patchOpts(dryRun)...)
}

func patchProxyWithRetry(
	ctx context.Context,
	c client.Client,
	name string,
	mutate func(*networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error),
	dryRun bool,
) error {
	for attempt := 0; ; attempt++ {
		current, err := util.GetHTTPProxy(ctx, c, name)
		if err != nil {
			return err
		}
		updated, err := mutate(current)
		if err != nil {
			return err
		}
		err = patchProxy(ctx, c, current, updated, dryRun)
		if err == nil {
			return nil
		}
		if apierrors.IsConflict(err) && attempt == 0 {
			continue
		}
		return err
	}
}

func patchTPP(ctx context.Context, c client.Client, original, updated *networkingv1alpha.TrafficProtectionPolicy, dryRun bool) error {
	return c.Patch(ctx, updated, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}), patchOpts(dryRun)...)
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
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if err := patchProxyWithRetry(cmd.Context(), c, name, mutate, dryRun); err != nil {
		return classifyProxyPatchError(name, err)
	}
	if dryRun {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Application load balancer %q validated.\n", name)
		return nil
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), success)
	return nil
}

func classifyProxyPatchError(name string, err error) error {
	if tooMany := tooManyBackendsError(err); tooMany != nil {
		return tooMany
	}
	return util.ClassifyError(fmt.Errorf("updating application load balancer %q: %w", name, err))
}

func tooManyBackendsError(err error) *util.CLIError {
	if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "backends: Too many") {
		return nil
	}
	return util.NewCLIError(util.ExitInvalid, "the API currently allows one origin per route").
		WithFix("keep one --endpoint or --network-service per route until multi-origin pools are enabled").
		WithCause(err)
}
