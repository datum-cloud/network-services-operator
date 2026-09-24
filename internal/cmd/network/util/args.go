// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NoPositionalArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%q accepts no positional arguments, got %v", cmd.CommandPath(), args)
	}
	return nil
}
