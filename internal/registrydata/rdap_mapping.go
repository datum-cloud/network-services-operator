package registrydata

import (
	"net/http"
	"strings"
	"time"

	"github.com/openrdap/rdap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// rdapSuggestedDelay inspects an RDAP response's HTTP details and returns:
// - the minimal parsed Retry-After duration across 429/503 responses (zero if none present)
// - a boolean indicating whether any 429 (Too Many Requests) was observed
func rdapSuggestedDelay(resp *rdap.Response) (time.Duration, bool) {
	var delay time.Duration
	limited := false
	if resp != nil && len(resp.HTTP) > 0 {
		for _, hr := range resp.HTTP {
			if hr == nil || hr.Response == nil {
				continue
			}
			code := hr.Response.StatusCode
			if code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable {
				if code == http.StatusTooManyRequests {
					limited = true
				}
				if ra := parseRetryAfterHeader(hr.Response.Header.Get("Retry-After")); ra > 0 {
					if delay == 0 || ra < delay {
						delay = ra
					}
				}
			}
		}
	}
	return delay, limited
}

// mapRDAPDomainToRegistration maps a raw RDAP domain into our Registration model.
func mapRDAPDomainToRegistration(d rdap.Domain) networkingv1alpha.Registration {
	reg := networkingv1alpha.Registration{}

	reg.Domain = d.LDHName
	reg.Handle = d.Handle

	// IDs & statuses
	reg.RegistryDomainID = pickRegistryDomainID(d.PublicIDs)
	copyStatuses(&reg, d.Status)

	// Lifecycle timestamps
	applyLifecycleFromEvents(&reg, d.Events)

	// DNSSEC
	if d.SecureDNS != nil {
		reg.DNSSEC = buildDNSSEC(d.SecureDNS)
	}

	// Contacts & abuse
	if contacts, abuse := buildContacts(d.Entities); contacts != nil || abuse != nil {
		reg.Contacts = contacts
		reg.Abuse = abuse
	}

	// Registrar
	if ri := buildRegistrarInfo(d.Entities); ri != nil {
		reg.Registrar = ri
	}

	return reg
}

func pickRegistryDomainID(publicIDs []rdap.PublicID) string {
	for _, pid := range publicIDs {
		if pid.Identifier == "" {
			continue
		}
		pt := strings.ToLower(pid.Type)
		// Skip registrar/iana IDs
		if strings.Contains(pt, "iana") || strings.Contains(pt, "registrar") {
			continue
		}
		// Prefer domain/roid-ish types
		if strings.Contains(pt, "roid") || strings.Contains(pt, "domain") {
			return pid.Identifier
		}
	}
	return ""
}

func copyStatuses(reg *networkingv1alpha.Registration, st []string) {
	if len(st) > 0 {
		reg.Statuses = append(reg.Statuses, st...)
	}
}

func applyLifecycleFromEvents(reg *networkingv1alpha.Registration, events []rdap.Event) {
	for _, ev := range events {
		date := parseRFC3339Ptr(ev.Date)
		if date == nil {
			continue
		}
		switch strings.ToLower(ev.Action) {
		case "registration":
			reg.CreatedAt = date
		case "last changed":
			reg.UpdatedAt = date
		case "expiration":
			reg.ExpiresAt = date
		}
	}
}

func parseRFC3339Ptr(s string) *metav1.Time {
	if s == "" {
		return nil
	}
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	t := metav1.NewTime(tt)
	return &t
}

func buildDNSSEC(sd *rdap.SecureDNS) *networkingv1alpha.DNSSECInfo {
	out := &networkingv1alpha.DNSSECInfo{}
	if sd.DelegationSigned != nil {
		enabled := *sd.DelegationSigned
		out.Enabled = &enabled
	}
	for _, ds := range sd.DS {
		var keyTag uint16
		if ds.KeyTag != nil {
			keyTag = uint16(*ds.KeyTag)
		}
		var alg, dt uint8
		if ds.Algorithm != nil {
			alg = *ds.Algorithm
		}
		if ds.DigestType != nil {
			dt = *ds.DigestType
		}
		out.DS = append(out.DS, networkingv1alpha.DSRecord{
			KeyTag:     keyTag,
			Algorithm:  alg,
			DigestType: dt,
			Digest:     ds.Digest,
		})
	}
	return out
}

func buildContacts(entities []rdap.Entity) (*networkingv1alpha.ContactSet, *networkingv1alpha.AbuseContact) {
	cs := &networkingv1alpha.ContactSet{}
	var abuse *networkingv1alpha.AbuseContact

	for _, e := range entities {
		name, email, phone := extractVCard(e.VCard)
		for _, role := range e.Roles {
			switch strings.ToLower(role) {
			case "registrant":
				cs.Registrant = &networkingv1alpha.Contact{Organization: name, Email: email, Phone: phone}
			case "administrative":
				cs.Admin = &networkingv1alpha.Contact{Organization: name, Email: email, Phone: phone}
			case "technical":
				cs.Tech = &networkingv1alpha.Contact{Organization: name, Email: email, Phone: phone}
			case "abuse":
				abuse = &networkingv1alpha.AbuseContact{Email: email, Phone: phone}
			}
		}
	}

	// normalize nil when empty
	if cs.Registrant == nil && cs.Admin == nil && cs.Tech == nil {
		cs = nil
	}
	return cs, abuse
}

func buildRegistrarInfo(entities []rdap.Entity) *networkingv1alpha.RegistrarInfo {
	for _, e := range entities {
		if !hasRole(e.Roles, "registrar") {
			continue
		}
		name, _, _ := extractVCard(e.VCard)
		ri := &networkingv1alpha.RegistrarInfo{Name: name}

		for _, pid := range e.PublicIDs {
			if strings.Contains(strings.ToLower(pid.Type), "iana") && pid.Identifier != "" {
				ri.IANAID = pid.Identifier
				break
			}
		}
		for _, l := range e.Links {
			if l.Href != "" {
				ri.URL = l.Href
				break
			}
		}
		return ri
	}
	return nil
}

func hasRole(roles []string, want string) bool {
	for _, r := range roles {
		if strings.EqualFold(r, want) {
			return true
		}
	}
	return false
}

func extractVCard(vc *rdap.VCard) (name, email, phone string) {
	if vc == nil {
		return "", "", ""
	}
	if n := vc.Name(); n != "" {
		name = n
	}
	if e := vc.Email(); e != "" {
		email = e
	}
	if t := vc.Tel(); t != "" {
		phone = t
	}
	if p := vc.GetFirst("org"); p != nil {
		vals := p.Values()
		if len(vals) > 0 && vals[0] != "" {
			name = vals[0]
		}
	}
	return
}
