package registrydata

import (
	"context"
	"strings"
	"time"

	whois "github.com/domainr/whois"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// whoisFetchAtHost performs a WHOIS query at a specific host for a query label and returns the body as string.
func whoisFetchAtHost(ctx context.Context, query, host string) (string, error) {
	req, err := whois.NewRequest(query)
	if err != nil {
		return "", err
	}
	req.Host = host
	resp, err := whois.DefaultClient.FetchContext(ctx, req)
	if err != nil {
		return "", err
	}
	return string(resp.Body), nil
}

// findWhoisValue scans WHOIS body for a key (case-insensitive) of the form "Key: value".
// It tolerates variable spacing/tabs around ':' and ignores inline content after the first colon.
func findWhoisValue(body string, keys []string) string {
	keySet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keySet[strings.ToLower(strings.TrimSpace(k))] = struct{}{}
	}
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		idx := strings.IndexByte(l, ':')
		if idx <= 0 {
			continue
		}
		left := strings.ToLower(strings.TrimSpace(l[:idx]))
		right := strings.TrimSpace(l[idx+1:])
		if _, ok := keySet[left]; ok {
			return right
		}
	}
	return ""
}

// parseTimeFlex tries several common WHOIS time formats.
func parseTimeFlex(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05-0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseWhoisRegistration(apex, body string) *networkingv1alpha.Registration {
	reg := &networkingv1alpha.Registration{Domain: apex}
	// Registry Domain ID
	if v := findWhoisValue(body, []string{"Registry Domain ID", "Domain ID", "roid"}); v != "" {
		reg.RegistryDomainID = strings.TrimSpace(v)
	}
	// Registrar name and IANA ID
	if v := findWhoisValue(body, []string{"Registrar", "Sponsoring Registrar"}); v != "" {
		name := strings.TrimSpace(v)
		if name != "" {
			reg.Registrar = &networkingv1alpha.RegistrarInfo{Name: name}
		}
	}
	if v := findWhoisValue(body, []string{"Registrar IANA ID"}); v != "" {
		if reg.Registrar == nil {
			reg.Registrar = &networkingv1alpha.RegistrarInfo{}
		}
		reg.Registrar.IANAID = strings.TrimSpace(v)
	}
	if v := findWhoisValue(body, []string{"Registrar URL"}); v != "" {
		if reg.Registrar == nil {
			reg.Registrar = &networkingv1alpha.RegistrarInfo{}
		}
		reg.Registrar.URL = strings.TrimSpace(v)
	}
	// Dates
	if v := findWhoisValue(body, []string{"Creation Date", "Created On", "Registered"}); v != "" {
		if t, ok := parseTimeFlex(v); ok {
			mt := metav1.NewTime(t)
			reg.CreatedAt = &mt
		}
	}
	if v := findWhoisValue(body, []string{"Updated Date", "Last Updated On"}); v != "" {
		if t, ok := parseTimeFlex(v); ok {
			mt := metav1.NewTime(t)
			reg.UpdatedAt = &mt
		}
	}
	if v := findWhoisValue(body, []string{"Registry Expiry Date",
		"Expiration Date",
		"Expiry Date",
		"Expires",
		"Registrar Registration Expiration Date"}); v != "" {
		if t, ok := parseTimeFlex(v); ok {
			mt := metav1.NewTime(t)
			reg.ExpiresAt = &mt
		}
	}
	// Abuse
	if email := strings.TrimSpace(findWhoisValue(body, []string{"Registrar Abuse Contact Email"})); email != "" {
		if reg.Abuse == nil {
			reg.Abuse = &networkingv1alpha.AbuseContact{}
		}
		reg.Abuse.Email = email
	}
	if phone := strings.TrimSpace(findWhoisValue(body, []string{"Registrar Abuse Contact Phone"})); phone != "" {
		if reg.Abuse == nil {
			reg.Abuse = &networkingv1alpha.AbuseContact{}
		}
		reg.Abuse.Phone = phone
	}
	// Statuses
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		ll := strings.ToLower(l)
		if strings.HasPrefix(ll, strings.ToLower("Domain Status:")) {
			val := strings.TrimSpace(strings.TrimPrefix(l, "Domain Status:"))
			if val != "" {
				reg.Statuses = append(reg.Statuses, strings.Fields(val)[0])
			}
		} else if strings.HasPrefix(ll, strings.ToLower("Status:")) {
			val := strings.TrimSpace(strings.TrimPrefix(l, "Status:"))
			if val != "" {
				reg.Statuses = append(reg.Statuses, strings.Fields(val)[0])
			}
		}
	}
	// DNSSEC
	if v := findWhoisValue(body, []string{"DNSSEC"}); v != "" {
		vv := strings.ToLower(strings.TrimSpace(v))
		enabled := vv != "unsigned" && vv != "no" && !strings.Contains(vv, "unsigned")
		reg.DNSSEC = &networkingv1alpha.DNSSECInfo{Enabled: &enabled}
	}
	// Contacts
	redact := func(s string) string {
		s = strings.TrimSpace(s)
		if s == "" {
			return ""
		}
		up := strings.ToUpper(s)
		if up == "REDACTED" || strings.Contains(up, "REDACTED") {
			return ""
		}
		return s
	}
	setContact := func(orgKey, emailKey, phoneKey string) *networkingv1alpha.Contact {
		org := redact(findWhoisValue(body, []string{orgKey}))
		email := redact(findWhoisValue(body, []string{emailKey}))
		phone := redact(findWhoisValue(body, []string{phoneKey}))
		if org == "" && email == "" && phone == "" {
			return nil
		}
		return &networkingv1alpha.Contact{Organization: org, Email: email, Phone: phone}
	}
	registrant := setContact("Registrant Organization", "Registrant Email", "Registrant Phone")
	admin := setContact("Admin Organization", "Admin Email", "Admin Phone")
	tech := setContact("Tech Organization", "Tech Email", "Tech Phone")
	if registrant != nil || admin != nil || tech != nil {
		reg.Contacts = &networkingv1alpha.ContactSet{Registrant: registrant, Admin: admin, Tech: tech}
	}
	return reg
}
