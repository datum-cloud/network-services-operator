// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"fmt"
	"io"

	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/networkagent"
)

func whoShort(a networkagent.Actionability) string {
	switch a {
	case networkagent.ActionabilityUser:
		return "you"
	case networkagent.ActionabilityPlatform, networkagent.ActionabilityStalled:
		return "Datum"
	case networkagent.ActionabilityTransient:
		return "in progress"
	default:
		return "info"
	}
}

func whoLong(c *networkagent.Cause) string {
	switch c.Actionability {
	case networkagent.ActionabilityUser:
		return "you — a change on your side fixes this"
	case networkagent.ActionabilityPlatform:
		return "Datum — nothing you change will clear it; raise it with Datum"
	case networkagent.ActionabilityStalled:
		return "Datum — this normally clears on its own but has held longer than expected; raise it with Datum"
	case networkagent.ActionabilityTransient:
		if c.ExpectedWithin != "" {
			return "nobody yet — this is normal and should clear within " + c.ExpectedWithin
		}
		return "nobody yet — this is normal and clears on its own"
	default:
		return "nobody"
	}
}

func printCauses(w io.Writer, root *networkagent.Cause, others []networkagent.Cause, unreadable []string) {
	if root != nil {
		_, _ = fmt.Fprintln(w, "\nCause:")
		tw := util.NewTabWriter(w)
		_, _ = fmt.Fprintf(tw, "  On:\t%s\n", root.Object)
		if root.Location != "" {
			_, _ = fmt.Fprintf(tw, "  Location:\t%s\n", root.Location)
		}
		if root.Workload != "" {
			_, _ = fmt.Fprintf(tw, "  Workload:\t%s\n", root.Workload)
		}
		reason := fmt.Sprintf("%s=%s (%s)", root.ConditionType, root.Reason, root.Status)
		if root.InStateFor != "" {
			reason += ", for " + root.InStateFor
		}
		_, _ = fmt.Fprintf(tw, "  Reason:\t%s\n", reason)
		_, _ = fmt.Fprintf(tw, "  Who acts:\t%s\n", whoLong(root))
		if root.Remediation != "" {
			_, _ = fmt.Fprintf(tw, "  What to do:\t%s\n", root.Remediation)
		}
		if root.Message != "" {
			_, _ = fmt.Fprintf(tw, "  Message:\t%s\n", root.Message)
		}
		_ = tw.Flush()
	}
	if len(others) > 0 {
		_, _ = fmt.Fprintln(w, "\nAlso found:")
		for _, c := range others {
			_, _ = fmt.Fprintf(w, "  - %s: %s (%s)\n", c.Object, c.Reason, whoShort(c.Actionability))
		}
	}
	if len(unreadable) > 0 {
		_, _ = fmt.Fprintln(w, "\nCould not read (so this is not a clean bill of health):")
		for _, u := range unreadable {
			_, _ = fmt.Fprintf(w, "  - %s\n", u)
		}
	}
}
