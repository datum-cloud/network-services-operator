// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"io"
	"text/tabwriter"
)

func NewTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
}
