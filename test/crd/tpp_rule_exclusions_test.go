// SPDX-License-Identifier: AGPL-3.0-only

package crd

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// The controller writes every excluded rule ID as SecRuleRemoveById. The WAF refuses to load a directive set whose
// SecRuleRemoveById names a negative ID, so the API must refuse such an ID at admission rather than let the policy
// reach the WAF. A range is written the same way and is refused by the WAF when its start exceeds its end.

type (
	exclusions = networkingv1alpha.OWASPRuleExclusions
	idRange    = networkingv1alpha.OWASPIDRange
)

func tppWithExclusions(name string, ex exclusions) *networkingv1alpha.TrafficProtectionPolicy {
	return &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{gatewayTargetRef("gw")},
			RuleSets: []networkingv1alpha.TrafficProtectionPolicyRuleSet{{
				Type:             networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
				OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{RuleExclusions: &ex},
			}},
		},
	}
}

func TestTPPRuleExclusionIDsAdmission(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		ex    exclusions
		admit bool
	}{
		{"an ordinary id", exclusions{IDs: []int{942100}}, true},
		{"id zero", exclusions{IDs: []int{0}}, true},
		{"the largest id", exclusions{IDs: []int{math.MaxInt64}}, true},
		{"a negative id", exclusions{IDs: []int{-1}}, false},
		{"a negative id among valid ones", exclusions{IDs: []int{942100, -942100}}, false},
		{"an ordinary range", exclusions{IDRanges: []idRange{"942100-942999"}}, true},
		{"the widest range", exclusions{IDRanges: []idRange{"0-9999999999"}}, true},
		{"a range that runs backwards", exclusions{IDRanges: []idRange{"942999-942100"}}, false},
		{"a negative range start", exclusions{IDRanges: []idRange{"-1-5"}}, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpp := tppWithExclusions(fmt.Sprintf("exclusions-%d", i), tc.ex)
			err := cl.Create(ctx, tpp)
			if err == nil {
				t.Cleanup(func() { _ = cl.Delete(ctx, tpp) })
			}
			if tc.admit {
				require.NoError(t, err, "the API should admit %+v", tc.ex)
			} else {
				require.Error(t, err, "the API admitted %+v, which the WAF refuses or the controller cannot read", tc.ex)
			}
		})
	}
}

// A JSON number past int64 cannot be built with the typed client, so it goes to the API server as raw JSON. Admitted,
// it would be stored where the controller's typed client cannot decode it.
func TestTPPRuleExclusionIDsOverflowAdmission(t *testing.T) {
	requireEnv(t)
	cfg := rest.CopyConfig(testConfig)
	httpClient, err := rest.HTTPClientFor(cfg)
	require.NoError(t, err)

	body := `{"apiVersion":"networking.datumapis.com/v1alpha","kind":"TrafficProtectionPolicy",` +
		`"metadata":{"name":"exclusions-overflow","namespace":"default"},` +
		`"spec":{"targetRefs":[{"group":"gateway.networking.k8s.io","kind":"Gateway","name":"gw"}],` +
		`"ruleSets":[{"type":"OWASPCoreRuleSet","owaspCoreRuleSet":{"ruleExclusions":{"ids":[9223372036854775808]}}}]}}`
	url := strings.TrimSuffix(cfg.Host, "/") +
		"/apis/networking.datumapis.com/v1alpha/namespaces/default/trafficprotectionpolicies"
	resp, err := httpClient.Post(url, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	msg, _ := io.ReadAll(resp.Body)
	require.NotEqual(t, http.StatusCreated, resp.StatusCode, "the API admitted an id past int64")
	t.Logf("refused with %d: %.200s", resp.StatusCode, msg)
}
