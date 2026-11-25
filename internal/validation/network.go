package validation

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

type internalRange struct {
	prefix netip.Prefix
	name   string
}

var internalRanges = []internalRange{
	{netip.MustParsePrefix("0.0.0.0/8"), "unspecified"},
	{netip.MustParsePrefix("10.0.0.0/8"), "private"},
	{netip.MustParsePrefix("100.64.0.0/10"), "carrier-grade NAT"},
	{netip.MustParsePrefix("127.0.0.0/8"), "loopback"},
	{netip.MustParsePrefix("169.254.0.0/16"), "link-local"},
	{netip.MustParsePrefix("172.16.0.0/12"), "private"},
	{netip.MustParsePrefix("192.168.0.0/16"), "private"},
	{netip.MustParsePrefix("224.0.0.0/4"), "multicast"},
	{netip.MustParsePrefix("240.0.0.0/4"), "reserved"},
	{netip.MustParsePrefix("::/128"), "unspecified"},
	{netip.MustParsePrefix("::1/128"), "loopback"},
	{netip.MustParsePrefix("::/96"), "IPv4-compatible"},
	{netip.MustParsePrefix("64:ff9b::/96"), "NAT64"},
	{netip.MustParsePrefix("64:ff9b:1::/48"), "local-use NAT64"},
	{netip.MustParsePrefix("fc00::/7"), "unique local"},
	{netip.MustParsePrefix("fe80::/10"), "link-local"},
	{netip.MustParsePrefix("fec0::/10"), "site-local"},
	{netip.MustParsePrefix("ff00::/8"), "multicast"},
}

var internalDomainSuffixes = []string{
	"cluster.local",
	"svc",
	"pod",
	"local",
	"localhost",
	"internal",
	"home.arpa",
}

func validateExternalHost(fldPath *field.Path, host string, permittedSuffixes ...string) field.ErrorList {
	name := strings.TrimSuffix(host, ".")

	if addr, err := netip.ParseAddr(name); err == nil {
		return validateExternalAddr(fldPath, host, addr)
	}

	if addr, ok := parseLegacyIPv4(name); ok {
		return field.ErrorList{field.Invalid(fldPath, host, fmt.Sprintf(
			"is the IPv4 address %s in a non-canonical spelling; write addresses as four decimal octets", addr))}
	}

	if errs := validation.IsFullyQualifiedDomainName(fldPath, host); len(errs) > 0 {
		return errs
	}

	name = strings.ToLower(name)
	for _, suffix := range permittedSuffixes {
		if hasDomainSuffix(name, strings.ToLower(strings.Trim(suffix, "."))) {
			return nil
		}
	}

	for _, suffix := range internalDomainSuffixes {
		if hasDomainSuffix(name, suffix) {
			return field.ErrorList{field.Invalid(fldPath, host, fmt.Sprintf(
				"is under %q, which resolves only inside a cluster or private network; use a public DNS name", suffix))}
		}
	}

	tld := name[strings.LastIndex(name, ".")+1:]
	if _, icann := publicsuffix.PublicSuffix(tld); !icann {
		return field.ErrorList{field.Invalid(fldPath, host, fmt.Sprintf(
			"ends in %q, which is not a public top-level domain, so the resolver search path can expand it to a cluster-internal name; use a public DNS name", tld))}
	}

	return nil
}

func validateExternalAddr(fldPath *field.Path, host string, addr netip.Addr) field.ErrorList {
	if addr.Zone() != "" {
		return field.ErrorList{field.Invalid(fldPath, host, "may not carry an IPv6 zone")}
	}

	if addr.Is4In6() {
		if r, ok := internalRangeOf(addr.Unmap()); ok {
			return field.ErrorList{field.Invalid(fldPath, host, fmt.Sprintf(
				"is an IPv4-mapped form of %s, which is in the %s range %s; tenant upstreams must be publicly routable", addr.Unmap(), r.name, r.prefix))}
		}
		return nil
	}

	if r, ok := internalRangeOf(addr); ok {
		return field.ErrorList{field.Invalid(fldPath, host, fmt.Sprintf(
			"is in the %s range %s; tenant upstreams must be publicly routable", r.name, r.prefix))}
	}

	return nil
}

func internalRangeOf(addr netip.Addr) (internalRange, bool) {
	for _, r := range internalRanges {
		if r.prefix.Contains(addr) {
			return r, true
		}
	}
	return internalRange{}, false
}

func hasDomainSuffix(name, suffix string) bool {
	return name == suffix || strings.HasSuffix(name, "."+suffix)
}

func parseLegacyIPv4(name string) (netip.Addr, bool) {
	parts := strings.Split(name, ".")
	if len(parts) > 4 {
		return netip.Addr{}, false
	}

	values := make([]uint64, len(parts))
	for i, part := range parts {
		v, ok := parseLegacyIPv4Part(part)
		if !ok {
			return netip.Addr{}, false
		}
		values[i] = v
	}

	last := len(values) - 1
	for _, v := range values[:last] {
		if v > 0xff {
			return netip.Addr{}, false
		}
	}
	if values[last] >= 1<<(8*(4-last)) {
		return netip.Addr{}, false
	}

	var ip uint64
	for i, v := range values[:last] {
		ip |= v << (8 * (3 - i))
	}
	ip |= values[last]

	return netip.AddrFrom4([4]byte{byte(ip >> 24), byte(ip >> 16), byte(ip >> 8), byte(ip)}), true
}

func parseLegacyIPv4Part(part string) (uint64, bool) {
	if part == "" {
		return 0, false
	}
	digits, base := part, 10
	switch {
	case len(part) > 2 && (part[:2] == "0x" || part[:2] == "0X"):
		digits, base = part[2:], 16
	case len(part) > 1 && part[0] == '0':
		digits, base = part[1:], 8
	}
	v, err := strconv.ParseUint(digits, base, 32)
	if err != nil {
		return 0, false
	}
	return v, true
}

// validateExternalHTTPSURL validates a free-form URL string that the shared data
// plane will fetch on a tenant's behalf (e.g. OIDC issuer/endpoints, remote
// JWKS). The URL must be a valid absolute https URL with no userinfo component
// and a host that passes validateExternalHost. An empty value is treated as
// valid (callers decide whether the field is required).
func validateExternalHTTPSURL(fldPath *field.Path, raw string) field.ErrorList {
	allErrs := field.ErrorList{}

	if raw == "" {
		return allErrs
	}

	u, err := url.Parse(raw)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, raw, fmt.Sprintf("invalid URL: %s", err)))
		return allErrs
	}

	if u.Scheme != schemeHTTPS {
		allErrs = append(allErrs, field.NotSupported(fldPath.Key("scheme"), u.Scheme, []string{schemeHTTPS}))
	}

	if u.User != nil {
		allErrs = append(allErrs, field.Invalid(fldPath.Key("userinfo"), fmt.Sprintf("%s:redacted", u.User.Username()), "must not have a userinfo component"))
	}

	host := u.Hostname()
	if host == "" {
		allErrs = append(allErrs, field.Required(fldPath.Key("host"), "must have a host component"))
		return allErrs
	}

	allErrs = append(allErrs, validateExternalHost(fldPath.Key("host"), host)...)

	return allErrs
}
