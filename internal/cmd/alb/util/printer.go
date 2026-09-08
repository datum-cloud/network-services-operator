// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

type OutputFormat string

const emDash = "—"

const (
	OutputTable OutputFormat = "table"
	OutputWide  OutputFormat = "wide"
	OutputJSON  OutputFormat = "json"
	OutputYAML  OutputFormat = "yaml"
	OutputName  OutputFormat = "name"
)

func AllOutputFormats() []OutputFormat {
	return []OutputFormat{OutputTable, OutputWide, OutputJSON, OutputYAML, OutputName}
}

func ParseOutputFormat(s string, allowed ...OutputFormat) (OutputFormat, error) {
	if len(allowed) == 0 {
		allowed = AllOutputFormats()
	}
	f := OutputFormat(s)
	for _, a := range allowed {
		if f == a {
			return f, nil
		}
	}

	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	return "", UsageErrorf("invalid output format %q — must be one of: %s", s, strings.Join(names, ", "))
}

func (f OutputFormat) IsMachine() bool {
	return f == OutputJSON || f == OutputYAML || f == OutputName
}

func PrintJSON(w io.Writer, obj any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	return nil
}

func PrintYAML(w io.Writer, obj any) error {
	b, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("encoding YAML: %w", err)
	}
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("writing YAML: %w", err)
	}
	return nil
}

func OrDash(s string) string {
	if s == "" {
		return emDash
	}
	return s
}

func OutputFromCmd(cmd *cobra.Command) string {
	if f := cmd.Root().PersistentFlags().Lookup("output"); f != nil {
		return f.Value.String()
	}
	return string(OutputTable)
}

func QuietFromCmd(cmd *cobra.Command) bool {
	quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
	return quiet
}
