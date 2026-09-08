// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func deleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Delete an Application Load Balancer",
		Long: `Delete an Application Load Balancer and its attached traffic protection
and basic auth configuration.`,
		Example: `  datumctl alb delete my-app
  datumctl alb delete my-app --yes`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runDelete,
	}
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without deleting")
	return cmd
}

func runDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	if !util.AssumeYes(cmd) && !dryRun {
		ok, err := util.ConfirmTyped(
			cmd.InOrStdin(),
			cmd.ErrOrStderr(),
			fmt.Sprintf("This will delete application load balancer %q and its WAF and basic auth configuration.", name),
			name,
		)
		if err != nil {
			return err
		}
		if !ok {
			return util.NewCLIError(util.ExitAborted, "aborted")
		}
	}

	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: util.ResourceNamespace},
	}
	if err := c.Delete(ctx, proxy, util.DryRunDeleteOpts(dryRun)...); err != nil {
		if apierrors.IsNotFound(err) {
			return util.NewCLIError(util.ExitNotFound, fmt.Sprintf("application load balancer %q not found", name)).
				WithFix("list load balancers with:\n       datumctl alb list").
				WithCause(err)
		}
		return util.ClassifyError(fmt.Errorf("deleting application load balancer %q: %w", name, err))
	}

	if tpp, err := findTPPForProxy(ctx, c, name); err != nil {
		return err
	} else if tpp != nil {
		if err := deleteIfExists(ctx, c, tpp, dryRun); err != nil {
			return util.ClassifyError(fmt.Errorf("deleting traffic protection for %q: %w", name, err))
		}
	}

	secpol := &envoygatewayv1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: util.ResourceNamespace},
	}
	if err := deleteIfExists(ctx, c, secpol, dryRun); err != nil {
		return util.ClassifyError(fmt.Errorf("deleting basic auth policy for %q: %w", name, err))
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: spec.BasicAuthSecretName(name), Namespace: util.ResourceNamespace},
	}
	if err := deleteIfExists(ctx, c, secret, dryRun); err != nil {
		return util.ClassifyError(fmt.Errorf("deleting basic auth secret for %q: %w", name, err))
	}

	if dryRun {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Application load balancer %q validated for deletion.\n", name)
		return nil
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Application load balancer %q deleted.\n", name)
	return nil
}
