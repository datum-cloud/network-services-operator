// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsIPFamilyHostname(t *testing.T) {
	assert.True(t, isIPFamilyHostname("v4.example.com"))
	assert.True(t, isIPFamilyHostname("v6.example.com"))
	assert.False(t, isIPFamilyHostname("example.com"))
	assert.False(t, isIPFamilyHostname("v4example.com"))
	assert.False(t, isIPFamilyHostname("vv4.example.com"))
}

func TestGatewayDNSEndpointEntries_OmitsIPFamilyAliases(t *testing.T) {
	v4IPs := []any{"203.0.113.10"}
	v6IPs := []any{"2001:db8::1"}
	hostnames := []string{
		"aabbccddeeff00112233445566778899.gateways.test.local",
		"v4.aabbccddeeff00112233445566778899.gateways.test.local",
		"v6.aabbccddeeff00112233445566778899.gateways.test.local",
	}

	endpoints := gatewayDNSEndpointEntries(hostnames, v4IPs, v6IPs)
	require.Len(t, endpoints, 2, "only apex A and AAAA; v4./v6. aliases must not get ExternalDNS endpoints")

	seen := map[string]string{}
	for _, ep := range endpoints {
		m, ok := ep.(map[string]any)
		require.True(t, ok)
		name, _ := m["dnsName"].(string)
		rtype, _ := m["recordType"].(string)
		seen[rtype] = name
		assert.Equal(t, hostnames[0], name)
		assert.False(t, isIPFamilyHostname(name))
	}
	assert.Equal(t, hostnames[0], seen["A"])
	assert.Equal(t, hostnames[0], seen["AAAA"])
}

func TestGatewayDNSEndpointEntries_EmptyWhenOnlyIPFamilyHostnames(t *testing.T) {
	endpoints := gatewayDNSEndpointEntries(
		[]string{"v4.example.com", "v6.example.com"},
		[]any{"203.0.113.10"},
		[]any{"2001:db8::1"},
	)
	assert.Empty(t, endpoints)
}
