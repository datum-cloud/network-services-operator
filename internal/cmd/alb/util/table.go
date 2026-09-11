// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"io"
	"text/tabwriter"
)

func NewTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
}

func TruncateCell(s string, max int) (string, bool) {
	if max <= 0 {
		return s, false
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s, false
	}
	return string(runes[:max-1]) + "…", true
}
