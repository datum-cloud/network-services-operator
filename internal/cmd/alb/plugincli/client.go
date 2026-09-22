// SPDX-License-Identifier: AGPL-3.0-only

// Package plugincli holds the parts of the alb command that need the datumctl
// plugin host: credentials, the project and org flags, entitlement, and shell
// completion.
//
// They live apart from internal/cmd/alb/util so that util — and through it
// internal/cmd/alb/spec — stays free of go.datum.net/datumctl/plugin. Go
// imports are package-wide, so a single file importing the plugin SDK would
// pull the whole datumctl plugin runtime into every binary that reads an
// HTTPProxy through spec, including cmd/alb-mcp, which has no CLI at all.
package plugincli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.datum.net/datumctl/plugin"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func RestConfig(project string) (*rest.Config, error) {
	if project == "" {
		return nil, util.NewCLIError(util.ExitUsage, "no project set").
			WithFix("pass --project, or set a default with:\n       datumctl config set project <name>")
	}

	pctx := plugin.Context()
	if pctx.APIHost == "" {
		return nil, util.NewCLIError(util.ExitUnavailable, "DATUM_API_HOST is not set").
			WithFix("run this through datumctl:\n       datumctl alb ...")
	}

	token, err := plugin.Token()
	if err != nil {
		return nil, util.NewCLIError(util.ExitUnavailable, fmt.Sprintf("getting credentials: %v", err)).
			WithFix("re-run `datumctl login` and try again.").
			WithCause(err)
	}

	return &rest.Config{
		Host:            util.ProjectControlPlaneURL(pctx.APIHost, project),
		BearerToken:     token,
		UserAgent:       util.UserAgent(),
		TLSClientConfig: tlsClientConfig(),
		Timeout:         util.RequestTimeout,
	}, nil
}

func NewClient(project string) (client.Client, error) {
	cfg, err := RestConfig(project)
	if err != nil {
		return nil, err
	}

	scheme, err := util.NewScheme()
	if err != nil {
		return nil, err
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, util.NewCLIError(util.ExitUnavailable, fmt.Sprintf("building API client: %v", err)).WithCause(err)
	}
	return c, nil
}

func tlsClientConfig() rest.TLSClientConfig {
	return rest.TLSClientConfig{CAFile: os.Getenv(util.CAFileEnv)}
}

func ProjectFromCmd(cmd *cobra.Command) string {
	project, _ := cmd.Root().PersistentFlags().GetString("project")
	return project
}

func OrgFromCmd(cmd *cobra.Command) string {
	org, _ := cmd.Root().PersistentFlags().GetString("org")
	return org
}
