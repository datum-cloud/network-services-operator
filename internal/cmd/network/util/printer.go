// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"encoding/json"
	"fmt"
	"io"

	"sigs.k8s.io/yaml"
)

type OutputFormat string

const (
	OutputTable OutputFormat = "table"
	OutputWide  OutputFormat = "wide"
	OutputJSON  OutputFormat = "json"
	OutputYAML  OutputFormat = "yaml"
)

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
	_, err = w.Write(b)
	return err
}
