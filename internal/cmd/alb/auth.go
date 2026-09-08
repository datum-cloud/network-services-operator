// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/display"
)

func authCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage basic authentication on an Application Load Balancer",
	}
	cmd.AddCommand(authSetCommand(), authUnsetCommand(), authListCommand())
	return cmd
}

func authSetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Enable or update basic authentication",
		Example: `  echo 'secret' | datumctl alb auth set my-app --user admin --password-stdin
  datumctl alb auth set my-app --user admin --user editor --password-stdin`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runAuthSet,
	}
	cmd.Flags().StringArray("user", nil, "Username to admit (repeatable)")
	cmd.Flags().Bool("password-stdin", false, "Read the password for every --user from stdin")
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without updating")
	_ = cmd.MarkFlagRequired("user")
	return cmd
}

func authUnsetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "unset <name>",
		Aliases:           []string{"disable", "rm"},
		Short:             "Disable basic authentication",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runAuthUnset,
	}
	cmd.Flags().Bool("dry-run", false, "Submit for server-side validation without deleting")
	return cmd
}

func authListCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "list <name>",
		Aliases:           []string{"ls"},
		Short:             "List basic auth usernames",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: util.CompleteALBNames,
		RunE:              runAuthList,
	}
}

func runAuthSet(cmd *cobra.Command, args []string) error {
	name := args[0]
	usernames, _ := cmd.Flags().GetStringArray("user")
	passwordStdin, _ := cmd.Flags().GetBool("password-stdin")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	if !passwordStdin {
		return util.UsageErrorf("pass --password-stdin and provide the password on stdin").
			WithFix("echo 'secret' | datumctl alb auth set " + name + " --user admin --password-stdin")
	}

	password, err := readPassword(cmd.InOrStdin())
	if err != nil {
		return err
	}

	users := make([]spec.BasicAuthUser, 0, len(usernames))
	for _, username := range usernames {
		users = append(users, spec.BasicAuthUser{Username: username, Password: password})
	}

	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	proxy, err := util.GetHTTPProxy(ctx, c, name)
	if err != nil {
		return err
	}

	secret, err := spec.BuildBasicAuthSecret(name, users)
	if err != nil {
		return err
	}
	policy := spec.BuildSecurityPolicy(name, display.HTTPProxyDisplayName(proxy))

	opts := []client.CreateOption{client.FieldOwner(util.FieldManager)}
	if dryRun {
		opts = append(opts, client.DryRunAll)
	}

	existingSecret, err := getBasicAuthSecret(ctx, c, name)
	if err != nil {
		return err
	}
	if existingSecret == nil {
		if err := c.Create(ctx, secret, opts...); err != nil {
			return util.ClassifyError(fmt.Errorf("creating basic auth secret for %q: %w", name, err))
		}
	} else {
		updated := existingSecret.DeepCopy()
		updated.Data = secret.Data
		updated.Type = secret.Type
		if err := c.Patch(ctx, updated, client.MergeFrom(existingSecret), patchOpts(dryRun)...); err != nil {
			return util.ClassifyError(fmt.Errorf("updating basic auth secret for %q: %w", name, err))
		}
	}

	existingPolicy, err := getSecurityPolicy(ctx, c, name)
	if err != nil {
		return err
	}
	if existingPolicy == nil {
		if err := c.Create(ctx, policy, opts...); err != nil && !apierrors.IsAlreadyExists(err) {
			return util.ClassifyError(fmt.Errorf("enabling basic auth on %q: %w", name, err))
		}
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Basic authentication enabled on %q (%d user(s)).\n", name, len(users))
	return nil
}

func runAuthUnset(cmd *cobra.Command, args []string) error {
	name := args[0]
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	if _, err := util.GetHTTPProxy(ctx, c, name); err != nil {
		return err
	}

	policy, err := getSecurityPolicy(ctx, c, name)
	if err != nil {
		return err
	}
	if policy != nil {
		if err := deleteIfExists(ctx, c, policy, dryRun); err != nil {
			return util.ClassifyError(fmt.Errorf("disabling basic auth on %q: %w", name, err))
		}
	}
	secret, err := getBasicAuthSecret(ctx, c, name)
	if err != nil {
		return err
	}
	if secret != nil {
		if err := deleteIfExists(ctx, c, secret, dryRun); err != nil {
			return util.ClassifyError(fmt.Errorf("removing basic auth secret for %q: %w", name, err))
		}
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Basic authentication disabled on %q.\n", name)
	return nil
}

func runAuthList(cmd *cobra.Command, args []string) error {
	c, err := newClient(util.ProjectFromCmd(cmd))
	if err != nil {
		return err
	}
	if _, err := util.GetHTTPProxy(cmd.Context(), c, args[0]); err != nil {
		return err
	}
	policy, err := getSecurityPolicy(cmd.Context(), c, args[0])
	if err != nil {
		return err
	}
	if policy == nil {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Basic authentication: off")
		return nil
	}
	secret, err := getBasicAuthSecret(cmd.Context(), c, args[0])
	if err != nil {
		return err
	}
	usernames := spec.ParseHtpasswdUsernames(secret)
	if len(usernames) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Basic authentication: enabled (no usernames readable)")
		return nil
	}
	for _, username := range usernames {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), username)
	}
	return nil
}

func readPassword(in io.Reader) (string, error) {
	if in == nil {
		in = os.Stdin
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return "", util.NewCLIError(util.ExitUsage, fmt.Sprintf("reading password: %v", err)).WithCause(err)
	}
	password := strings.TrimRight(string(data), "\r\n")
	if password == "" {
		return "", util.UsageErrorf("password is empty")
	}
	return password, nil
}
