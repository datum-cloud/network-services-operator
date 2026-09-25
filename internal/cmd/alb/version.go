// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

var Version = "dev"

type versionInfo struct {
	Version    string `json:"version" yaml:"version"`
	APIGroup   string `json:"apiGroup" yaml:"apiGroup"`
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	PluginAPI  int    `json:"pluginApiVersion" yaml:"pluginApiVersion"`
	GoVersion  string `json:"goVersion" yaml:"goVersion"`
	Platform   string `json:"platform" yaml:"platform"`
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the plugin version",
		Long: "Print the plugin version.\n\n" +
			"Runs entirely offline: no credentials, no API call, and no project or\n" +
			"entitlement required, so it still answers when everything else does not.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: "  datumctl alb version\n" +
			"  datumctl alb version -o json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := versionInfo{
				Version:    Version,
				APIGroup:   networkingv1alpha.GroupVersion.Group,
				APIVersion: networkingv1alpha.GroupVersion.Version,
				PluginAPI:  pluginAPIVersion,
				GoVersion:  runtime.Version(),
				Platform:   runtime.GOOS + "/" + runtime.GOARCH,
			}

			format, err := util.ParseOutputFormat(util.OutputFromCmd(cmd),
				util.OutputTable, util.OutputWide, util.OutputJSON, util.OutputYAML)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			switch format {
			case util.OutputJSON:
				return util.PrintJSON(out, info)
			case util.OutputYAML:
				return util.PrintYAML(out, info)
			case util.OutputWide:
				_, _ = fmt.Fprintf(out, "datumctl-alb %s (Networking API %s)\n", info.Version, networkingv1alpha.GroupVersion)
				_, _ = fmt.Fprintf(out, "  plugin API %d\n", info.PluginAPI)
				_, _ = fmt.Fprintf(out, "  %s %s\n", info.GoVersion, info.Platform)
				return nil
			default:
				_, _ = fmt.Fprintf(out, "datumctl-alb %s (Networking API %s)\n", info.Version, networkingv1alpha.GroupVersion)
				return nil
			}
		},
	}
}
