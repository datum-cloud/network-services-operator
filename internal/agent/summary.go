// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"fmt"
	"strings"
)

// confidence decides how much the answer is worth.
//
// The hard case is an all-green load balancer. Datum publishes no signal for
// whether its edge can actually serve one — a load balancer reports every
// condition true within seconds of being created, while the edge is still
// catching up, and nothing anywhere says when that finishes. So "nothing is
// wrong" is reported as unverified rather than as working, and the summary says
// what would settle it.
//
// No convergence window is applied. One is not published anywhere, and a
// guessed one would be exactly the unfalsifiable claim the rest of this package
// exists to avoid: it would make a load balancer that never serves look healthy
// the moment the guess elapsed.
func confidence(d *Diagnosis) Confidence {
	if d.RootCause != nil {
		if len(d.Unread) > 0 {
			return ConfidencePartial
		}
		return ConfidenceReported
	}
	if len(d.Unread) > 0 {
		return ConfidencePartial
	}
	return ConfidenceUnverified
}

// summarise writes the sentence a person reads and the steps they take next.
func summarise(d *Diagnosis) (string, []string) {
	name := d.LoadBalancer
	if d.DisplayName != "" {
		name = fmt.Sprintf("%s (%s)", d.DisplayName, d.LoadBalancer)
	}

	if d.RootCause == nil {
		return healthySummary(d, name)
	}

	c := d.RootCause
	var b strings.Builder
	fmt.Fprintf(&b, "%s is not working. %s", name, c.Explanation)
	if c.Hostname != "" && !strings.Contains(c.Explanation, c.Hostname) {
		fmt.Fprintf(&b, " This affects %s.", c.Hostname)
	}
	if c.InStateFor != "" {
		fmt.Fprintf(&b, " It has been like this for %s.", c.InStateFor)
	}
	if c.TimeDiscarded != "" {
		fmt.Fprintf(&b, " %s", c.TimeDiscarded)
	}

	next := []string{}
	if c.Remediation != "" {
		next = append(next, c.Remediation)
	}
	if c.Hostname != "" {
		if dom := domainOf(d, c.Hostname); dom != nil && dom.VerificationRecord != nil && !dom.Verified {
			next = append(next, fmt.Sprintf(
				"Create this DNS record at your provider: name %q, type %s, value %q.",
				dom.VerificationRecord.Name, dom.VerificationRecord.Type, dom.VerificationRecord.Content))
		}
	}
	if c.Skill != "" {
		next = append(next, "The full procedure is in the "+c.Skill+" skill.")
	}
	for _, u := range d.Unread {
		next = append(next, "Not everything could be checked: "+u)
	}
	return b.String(), next
}

func healthySummary(d *Diagnosis, name string) (string, []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s reports no faults.", name)

	if d.Confidence == ConfidenceUnverified {
		b.WriteString(" That means its settings have been published and nothing is reporting a " +
			"problem — it does not mean a request has been served through it, because Datum " +
			"publishes no signal for that.")
		if d.ObjectAge != "" {
			fmt.Fprintf(&b, " It was created %s ago.", d.ObjectAge)
		}
	}

	next := []string{}
	if d.GeneratedHostname != "" {
		next = append(next, fmt.Sprintf(
			"Confirm it end to end with a request: curl -sSI https://%s", d.GeneratedHostname))
		next = append(next, "Or check whether requests are arriving at all with the traffic summary.")
	}
	if !d.Protection.Attached {
		next = append(next, "Nothing is inspecting traffic to this load balancer: no traffic "+
			"protection is attached to it. That is worth turning on before it takes real traffic.")
	}
	for _, u := range d.Unread {
		next = append(next, "Not everything could be checked: "+u)
	}
	return b.String(), next
}

func domainOf(d *Diagnosis, hostname string) *DomainView {
	for i := range d.Hostnames {
		if d.Hostnames[i].Hostname == hostname {
			return d.Hostnames[i].Domain
		}
	}
	return nil
}
