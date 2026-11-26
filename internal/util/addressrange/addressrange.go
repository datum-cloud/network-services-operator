// SPDX-License-Identifier: AGPL-3.0-only

package addressrange

import "net/netip"

type Range struct {
	Prefix netip.Prefix
	Name   string
}

var NonPublic = []Range{
	{netip.MustParsePrefix("0.0.0.0/8"), "unspecified"},
	{netip.MustParsePrefix("10.0.0.0/8"), "private"},
	{netip.MustParsePrefix("100.64.0.0/10"), "carrier-grade NAT"},
	{netip.MustParsePrefix("127.0.0.0/8"), "loopback"},
	{netip.MustParsePrefix("169.254.0.0/16"), "link-local"},
	{netip.MustParsePrefix("172.16.0.0/12"), "private"},
	{netip.MustParsePrefix("192.0.0.0/24"), "IETF protocol assignments"},
	{netip.MustParsePrefix("192.0.2.0/24"), "documentation"},
	{netip.MustParsePrefix("192.168.0.0/16"), "private"},
	{netip.MustParsePrefix("198.18.0.0/15"), "benchmarking"},
	{netip.MustParsePrefix("198.51.100.0/24"), "documentation"},
	{netip.MustParsePrefix("203.0.113.0/24"), "documentation"},
	{netip.MustParsePrefix("224.0.0.0/4"), "multicast"},
	{netip.MustParsePrefix("240.0.0.0/4"), "reserved"},
	{netip.MustParsePrefix("::/128"), "unspecified"},
	{netip.MustParsePrefix("::1/128"), "loopback"},
	{netip.MustParsePrefix("::/96"), "IPv4-compatible"},
	{netip.MustParsePrefix("::ffff:0:0/96"), "IPv4-mapped"},
	{netip.MustParsePrefix("100::/64"), "discard-only"},
	{netip.MustParsePrefix("2001::/32"), "Teredo"},
	{netip.MustParsePrefix("2001:db8::/32"), "documentation"},
	{netip.MustParsePrefix("2002::/16"), "6to4"},
	{netip.MustParsePrefix("fc00::/7"), "unique local"},
	{netip.MustParsePrefix("fe80::/10"), "link-local"},
	{netip.MustParsePrefix("fec0::/10"), "site-local"},
	{netip.MustParsePrefix("ff00::/8"), "multicast"},
}

var (
	NAT64         = Range{netip.MustParsePrefix("64:ff9b::/96"), "NAT64"}
	LocalUseNAT64 = Range{netip.MustParsePrefix("64:ff9b:1::/48"), "local-use NAT64"}

	LocalUseNAT64Translation = netip.MustParsePrefix("64:ff9b:1::/96")
)

func NAT64TranslationPrefixes() []netip.Prefix {
	return []netip.Prefix{NAT64.Prefix, LocalUseNAT64Translation}
}

func NonPublicOf(addr netip.Addr) (Range, bool) {
	addr = addr.WithZone("")
	for _, r := range NonPublic {
		if r.Prefix.Contains(addr) {
			return r, true
		}
	}
	return Range{}, false
}

func RefusedAtEdge(addr netip.Addr) (Range, bool) {
	addr = addr.WithZone("")
	for _, p := range NAT64TranslationPrefixes() {
		if p.Contains(addr) {
			b := addr.As16()
			return NonPublicOf(netip.AddrFrom4([4]byte(b[12:])))
		}
	}
	if LocalUseNAT64.Prefix.Contains(addr) {
		return LocalUseNAT64, true
	}
	return NonPublicOf(addr)
}
