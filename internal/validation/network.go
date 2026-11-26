package validation

import (
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"go.datum.net/network-services-operator/internal/util/addressrange"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

var internalRanges = append(slices.Clone(addressrange.NonPublic), addressrange.NAT64, addressrange.LocalUseNAT64)

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
				"is an IPv4-mapped form of %s, which is in the %s range %s; tenant upstreams must be publicly routable", addr.Unmap(), r.Name, r.Prefix))}
		}
		return nil
	}

	if r, ok := internalRangeOf(addr); ok {
		return field.ErrorList{field.Invalid(fldPath, host, fmt.Sprintf(
			"is in the %s range %s; tenant upstreams must be publicly routable", r.Name, r.Prefix))}
	}

	return nil
}

func NonPublicAddressRange(host string) (addressrange.Range, bool) {
	name := strings.TrimSuffix(host, ".")
	addr, err := netip.ParseAddr(name)
	if err != nil {
		legacy, ok := parseLegacyIPv4(name)
		if !ok {
			return addressrange.Range{}, false
		}
		addr = legacy
	}
	addr = addr.WithZone("")
	if addr.Is4In6() {
		return internalRangeOf(addr.Unmap())
	}
	return internalRangeOf(addr)
}

func internalRangeOf(addr netip.Addr) (addressrange.Range, bool) {
	for _, r := range internalRanges {
		if r.Prefix.Contains(addr) {
			return r, true
		}
	}
	return addressrange.Range{}, false
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
