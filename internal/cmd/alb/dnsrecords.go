// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"fmt"
	"io"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

type pendingRecords struct {
	toPublish     []networkingv1alpha.HostnameDNSRecord
	awaitingDatum []networkingv1alpha.HostnameDNSRecord
	undelegated   []string
}

// collectPendingRecords reads the records each custom hostname still needs
// from its status, each record once however many hostnames share it.
func collectPendingRecords(proxy *networkingv1alpha.HTTPProxy, hostnames []string) pendingRecords {
	var pending pendingRecords
	statusByName := map[string]networkingv1alpha.HostnameStatus{}
	for _, hs := range proxy.Status.HostnameStatuses {
		statusByName[hs.Hostname] = hs
	}

	seen := map[string]bool{}
	for _, name := range hostnames {
		hs, ok := statusByName[name]
		if !ok {
			continue
		}
		if c := apimeta.FindStatusCondition(hs.Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed); c != nil &&
			c.Status == metav1.ConditionFalse && c.Reason == networkingv1alpha.DNSRecordReasonDNSAuthorityMissing {
			pending.undelegated = append(pending.undelegated, name)
		}
		for _, record := range hs.DNSRecords {
			if record.State != networkingv1alpha.HostnameDNSRecordMissing {
				continue
			}
			key := record.Name + "|" + record.Type + "|" + record.Content
			if seen[key] {
				continue
			}
			seen[key] = true
			if record.ManagedBy == networkingv1alpha.HostnameDNSRecordManagedByPlatform {
				pending.awaitingDatum = append(pending.awaitingDatum, record)
			} else {
				pending.toPublish = append(pending.toPublish, record)
			}
		}
	}
	return pending
}

func writePendingRecords(out io.Writer, pending pendingRecords) {
	if len(pending.toPublish) > 0 {
		_, _ = fmt.Fprintln(out, "DNS records to publish:")
		tw := util.NewTabWriter(out)
		_, _ = fmt.Fprintln(tw, "  NAME\tTYPE\tCONTENT\tPURPOSE")
		for _, r := range pending.toPublish {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", r.Name, r.Type, r.Content, r.Purpose)
		}
		_ = tw.Flush()
	}

	for _, r := range pending.awaitingDatum {
		_, _ = fmt.Fprintf(out, "Waiting on Datum DNS:  %s %s %s (%s)\n", r.Name, r.Type, r.Content, r.Purpose)
	}

	if len(pending.undelegated) == 0 {
		return
	}
	var certificate *networkingv1alpha.HostnameDNSRecord
	for i := range pending.toPublish {
		if pending.toPublish[i].Purpose == networkingv1alpha.HostnameDNSRecordPurposeCertificate {
			certificate = &pending.toPublish[i]
			break
		}
	}
	names := strings.Join(pending.undelegated, ", ")
	if certificate != nil {
		_, _ = fmt.Fprintf(out, "DNS not delegated: Datum DNS does not serve %s yet. Publish the certificate record %s at the DNS provider that does serve it, so the certificate issues before traffic moves.\n",
			names, certificate.Name)
		return
	}
	_, _ = fmt.Fprintf(out, "DNS not delegated: Datum DNS does not serve %s yet. Point the domain's NS records at its Datum DNS zone (see `datumctl dns`), or publish the routing record at the DNS provider that serves it.\n",
		names)
}

// hostnameProblem returns why a custom hostname is held back, when its
// ownership or claim was refused.
func hostnameProblem(hs networkingv1alpha.HostnameStatus) string {
	for _, condType := range []string{networkingv1alpha.HostnameConditionVerified, networkingv1alpha.HostnameConditionAvailable} {
		if c := apimeta.FindStatusCondition(hs.Conditions, condType); c != nil && c.Status == metav1.ConditionFalse && c.Message != "" {
			return c.Message
		}
	}
	return ""
}
