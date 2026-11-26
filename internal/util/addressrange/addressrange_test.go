// SPDX-License-Identifier: AGPL-3.0-only

package addressrange

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNonPublicCoversBothFormerLists(t *testing.T) {
	for _, addr := range []string{
		"192.0.2.1",
		"198.51.100.1",
		"203.0.113.1",
		"2001:db8::1",
		"100::1",
		"::ffff:8.8.8.8",
		"0.0.0.1",
		"10.1.2.3",
		"100.64.0.1",
		"127.0.0.1",
		"169.254.169.254",
		"172.16.0.1",
		"192.0.0.1",
		"192.168.0.1",
		"198.18.0.1",
		"224.0.0.1",
		"255.255.255.255",
		"::",
		"::1",
		"::7f00:1",
		"2001::1",
		"2002::1",
		"fd00::1",
		"fe80::1",
		"fec0::1",
		"ff02::1",
	} {
		_, ok := NonPublicOf(netip.MustParseAddr(addr))
		assert.True(t, ok, "%s must be non-public", addr)
	}
}

func TestRefusedAtEdge(t *testing.T) {
	tests := []struct {
		addr string
		name string
	}{
		{"10.0.0.5", "private"},
		{"203.0.113.10", "documentation"},
		{"fe80::1%eth0", "link-local"},
		{"64:ff9b::a00:1", "private"},
		{"64:ff9b:1::c000:201", "documentation"},
		{"64:ff9b:1:1::1", "local-use NAT64"},
		{"8.8.8.8", ""},
		{"64:ff9b::808:808", ""},
		{"64:ff9b:1::808:808", ""},
		{"2606:4700::1", ""},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			r, ok := RefusedAtEdge(netip.MustParseAddr(tc.addr))
			assert.Equal(t, tc.name != "", ok)
			assert.Equal(t, tc.name, r.Name)
		})
	}
}
