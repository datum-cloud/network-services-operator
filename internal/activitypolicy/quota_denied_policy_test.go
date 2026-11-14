// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// quotaDeniedAudit is the audit event Milo records when quota admission
// rejects a create: a 403 with a Status body and the outcome annotation.
func quotaDeniedAudit(name, outcome string) map[string]any {
	audit := map[string]any{
		"verb":       "create",
		"user":       map[string]any{"username": "alice@example.com"},
		"objectRef":  map[string]any{"name": name, "namespace": "default"},
		"requestURI": "/apis/networking.datumapis.com/v1alpha/namespaces/default/objects",
		"requestObject": map[string]any{
			"metadata": map[string]any{"name": name},
			"spec":     map[string]any{},
		},
		"responseStatus": map[string]any{"code": 403},
		"responseObject": map[string]any{"kind": "Status", "status": "Failure", "reason": "Forbidden", "code": 403},
	}
	if outcome != "" {
		audit["annotations"] = map[string]any{"quota.miloapis.com/outcome": outcome}
	}
	return audit
}

func TestPolicies_QuotaDenied(t *testing.T) {
	t.Parallel()
	policies := []struct {
		file, named, unnamed string
	}{
		{"httpproxy", "Alice couldn't create load balancer obj-1: quota reached", "Alice couldn't create a load balancer: quota reached"},
		{"connector", "Alice couldn't create connector obj-1: quota reached", "Alice couldn't create a connector: quota reached"},
		{"gateway", "Alice couldn't create gateway obj-1: quota reached", "Alice couldn't create a gateway: quota reached"},
		{"connectoradvertisement", "Alice couldn't create connector advertisement obj-1: quota reached", "Alice couldn't create a connector advertisement: quota reached"},
	}
	for _, p := range policies {
		t.Run(p.file, func(t *testing.T) {
			t.Parallel()
			pol := loadPolicy(t, "config/milo/activity/policies/"+p.file+"-policy.yaml")

			audit := quotaDeniedAudit("obj-1", "denied")
			require.Equal(t, "create-quota-denied", firstMatchingAuditRule(t, pol, audit))
			assert.Equal(t, p.named, auditSummary(t, pol, "create-quota-denied", audit))

			// generateName creates have no name yet.
			unnamed := quotaDeniedAudit("", "denied")
			require.Equal(t, "create-quota-denied", firstMatchingAuditRule(t, pol, unnamed))
			assert.Equal(t, p.unnamed, auditSummary(t, pol, "create-quota-denied", unnamed))

			for name, a := range map[string]map[string]any{
				// A permissions failure is also a 403, without the annotation.
				"forbidden without quota annotation": quotaDeniedAudit("obj-1", ""),
				// Timeouts and other failures are platform problems, not the
				// customer reaching a limit.
				"quota check timed out": quotaDeniedAudit("obj-1", "timeout"),
				"quota internal error":  quotaDeniedAudit("obj-1", "internal_error"),
			} {
				assert.Empty(t, firstMatchingAuditRule(t, pol, a), name)
			}

			system := quotaDeniedAudit("obj-1", "denied")
			system["user"] = map[string]any{"username": "system:control@networking.datumapis.com"}
			assert.Empty(t, firstMatchingAuditRule(t, pol, system), "controller-side denials stay out of the feed")
		})
	}
}
