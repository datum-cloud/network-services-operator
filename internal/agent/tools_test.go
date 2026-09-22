// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
)

func depsFor(r Reader) DepsFor {
	return func(context.Context) (ToolDeps, error) {
		return ToolDeps{Reader: r, Namespace: "default"}, nil
	}
}

func pinClock(t *testing.T) {
	t.Helper()
	prev := nowFunc
	nowFunc = func() time.Time { return testNow }
	t.Cleanup(func() { nowFunc = prev })
}

// TestToolsAreReadOnly makes "this service publishes no mutating tool" a
// property of the types rather than a promise in a comment. Every handler
// reaches the cluster through Reader, which has no Create, Update, Patch or
// Delete on it, so adding a write would mean widening that interface — a
// reviewable diff rather than a quiet change inside a handler.
func TestToolsAreReadOnly(t *testing.T) {
	rt := reflect.TypeOf((*Reader)(nil)).Elem()
	for i := 0; i < rt.NumMethod(); i++ {
		name := rt.Method(i).Name
		for _, verb := range []string{"Create", "Update", "Patch", "Delete", "Apply", "Write", "Set"} {
			assert.False(t, strings.HasPrefix(name, verb),
				"Reader.%s looks like a write; changing a load balancer belongs in the assistant's "+
					"own plan and apply path, which holds the confirmation step", name)
		}
	}
	assert.Equal(t, 6, rt.NumMethod(),
		"Reader grew a method; if it is a write, it does not belong here")
}

// TestGetReturnsUsernamesNeverHashes pins the one read in this package that
// could leak. The stored value is an htpasswd hash, and a hash is still a
// credential's shadow.
func TestGetReturnsUsernamesNeverHashes(t *testing.T) {
	pinClock(t)

	const hash = "{SHA}W6ph5Mm5Pz8GgiULbPgzG37mj9g="
	r := &fakeReader{
		proxies: []networkingv1alpha.HTTPProxy{healthyProxy()},
		secrets: map[string]*corev1.Secret{
			spec.BasicAuthSecretName("my-app"): {
				ObjectMeta: metav1.ObjectMeta{Name: spec.BasicAuthSecretName("my-app")},
				Data:       map[string][]byte{".htpasswd": []byte("admin:" + hash + "\n")},
			},
		},
	}

	_, out, err := albGet(depsFor(r))(context.Background(), nil, GetInput{Name: "my-app"})
	require.NoError(t, err)

	assert.True(t, out.LoadBalancer.BasicAuth.Enabled)
	assert.Equal(t, []string{"admin"}, out.LoadBalancer.BasicAuth.Usernames)

	encoded, err := json.Marshal(out)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), hash, "a password hash must never leave this service")
	assert.NotContains(t, string(encoded), "htpasswd")
}

// TestListPutsTheWorstFirst pins the point of a fleet view.
func TestListPutsTheWorstFirst(t *testing.T) {
	pinClock(t)

	healthy := healthyProxy()
	healthy.Name = "fine"

	broken := healthyProxy()
	broken.Name = "broken"
	broken.Status.Conditions = []metav1.Condition{
		cond(networkingv1alpha.HTTPProxyConditionProgrammed,
			networkingv1alpha.HTTPProxyReasonNetworkServiceBackendNotFound,
			metav1.ConditionFalse, ago(time.Hour)),
	}

	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{healthy, broken}}
	_, out, err := albList(depsFor(r))(context.Background(), nil, ListInput{})
	require.NoError(t, err)
	require.Len(t, out.LoadBalancers, 2)

	assert.Equal(t, "broken", out.LoadBalancers[0].Name)
	assert.Equal(t, ScopeAllTraffic, out.LoadBalancers[0].Scope)
	assert.Equal(t, ActionabilityUser, out.LoadBalancers[0].Actionability)

	assert.Equal(t, "fine", out.LoadBalancers[1].Name)
	assert.Equal(t, ConfidenceUnverified, out.LoadBalancers[1].Confidence,
		"a load balancer with no reported fault is still unverified")
}

// TestListCountsWorkingHostnames pins the "2/3" column, which needs the fan-out
// rather than any single field.
func TestListCountsWorkingHostnames(t *testing.T) {
	pinClock(t)

	p := healthyProxy()
	p = withHostname(p, networkingv1alpha.HostnameStatus{
		Hostname: "good.example.com",
		Conditions: []metav1.Condition{
			cond(networkingv1alpha.HostnameConditionAvailable, networkingv1alpha.HostnameAvailableReasonClaimed, metav1.ConditionTrue, ago(time.Hour)),
			cond(networkingv1alpha.HostnameConditionDNSRecordProgrammed, networkingv1alpha.DNSRecordReasonCreated, metav1.ConditionTrue, ago(time.Hour)),
			cond(networkingv1alpha.HostnameConditionCertificateReady, networkingv1alpha.CertificateReadyReasonCertificateIssued, metav1.ConditionTrue, ago(time.Hour)),
		},
	})
	p = withHostname(p, networkingv1alpha.HostnameStatus{
		Hostname: "bad.example.com",
		Conditions: []metav1.Condition{
			cond(networkingv1alpha.HostnameConditionAvailable, networkingv1alpha.HostnameAvailableReasonInUse, metav1.ConditionFalse, ago(time.Hour)),
		},
	})

	r := &fakeReader{
		proxies: []networkingv1alpha.HTTPProxy{p},
		domains: []networkingv1alpha.Domain{verifiedDomain()},
	}
	_, out, err := albList(depsFor(r))(context.Background(), nil, ListInput{})
	require.NoError(t, err)
	require.Len(t, out.LoadBalancers, 1)

	assert.Equal(t, "1/2", out.LoadBalancers[0].CustomHostnames)
	assert.Equal(t, "bad.example.com", out.LoadBalancers[0].RootCauseHostname,
		"the fleet row has to name the hostname, because the aggregate conditions never do")
}

// TestReasonExplainSaysWhenAReasonIsAmbiguous is the tool-level half of the
// reason-uniqueness problem. Answering "Pending" with one meaning would be
// confidently wrong two times in three.
func TestReasonExplainSaysWhenAReasonIsAmbiguous(t *testing.T) {
	handler := albReasonExplain(depsFor(&fakeReader{}))

	_, out, err := handler(context.Background(), nil, ReasonExplainInput{Reason: "Pending"})
	require.NoError(t, err)
	assert.True(t, out.Ambiguous)
	assert.Nil(t, out.Reason, "an ambiguous reason must not be answered with one meaning")
	assert.Greater(t, len(out.Reasons), 1)
	assert.Contains(t, out.Note, "do not assume the first")

	_, out2, err := handler(context.Background(), nil, ReasonExplainInput{
		Reason:        "Pending",
		ConditionType: networkingv1alpha.HostnameConditionCertificateReady,
	})
	require.NoError(t, err)
	assert.False(t, out2.Ambiguous)
	require.NotNil(t, out2.Reason)
	assert.Contains(t, out2.Reason.Explanation, "certificate")

	_, _, err = handler(context.Background(), nil, ReasonExplainInput{Reason: "NoSuchReason"})
	assert.Error(t, err)
}

// TestEveryToolIsRegisteredAndDescribed pins that a tool cannot ship without
// telling the model what it does and that it changes nothing.
func TestEveryToolIsRegisteredAndDescribed(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	RegisterTools(s, depsFor(&fakeReader{}))

	want := map[string]bool{
		ToolList: false, ToolGet: false, ToolDiagnose: false, ToolReasonExplain: false,
	}
	for _, tool := range serverTools(t, s) {
		if _, ok := want[tool.Name]; !ok {
			t.Errorf("unexpected tool %q registered", tool.Name)
			continue
		}
		want[tool.Name] = true
		assert.True(t, strings.HasPrefix(tool.Name, "alb_"),
			"%s needs the alb_ prefix so it cannot collide with another service's tool", tool.Name)
		assert.Contains(t, tool.Description, "Read-only.",
			"%s must say it changes nothing", tool.Name)
	}
	for name, found := range want {
		assert.True(t, found, "%s was never registered", name)
	}
}

// serverTools drives a real tools/list over an in-memory transport, so the test
// sees what a client would see rather than what the registration code intended.
func serverTools(t *testing.T, s *mcp.Server) []*mcp.Tool {
	t.Helper()
	ctx := context.Background()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	res, err := clientSession.ListTools(ctx, nil)
	require.NoError(t, err)
	return res.Tools
}
