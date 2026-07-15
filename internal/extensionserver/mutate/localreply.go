package mutate

import (
	"fmt"
	"strings"

	accesslogv3 "github.com/envoyproxy/go-control-plane/envoy/config/accesslog/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/types/known/anypb"
)

// defaultMinStatusCode is the inclusive lower bound (HTTP status) above which an
// edge-generated local reply gets the branded body. 500 brands every server-side
// error class while leaving 4xx (client errors) untouched.
const defaultMinStatusCode uint32 = 500

// LocalReplyConfig carries the branded error-page settings sourced from the
// operator's GatewayConfig.ErrorPage field and the embedded/override HTML body.
// Construction (embed vs. mounted override) happens once at startup in
// internal/extensionserver/cmd/run.go; the mutation path only reads it.
type LocalReplyConfig struct {
	// Disabled disables all local-reply injection when true. An unconfigured or
	// empty BodyHTML is also treated as a no-op. This mirrors the CorazaConfig
	// convention so the two listener mutators behave consistently.
	Disabled bool
	// MinStatusCode is the inclusive lower bound for status codes that receive
	// the branded body. Defaults to 500 when zero.
	MinStatusCode uint32
	// RuntimeKey is the Envoy runtime key that gates the status-code comparison,
	// allowing the branding to be disabled at runtime without a redeploy
	// (e.g. "local_reply_5xx").
	RuntimeKey string
	// BodyHTML is the inline HTML served as the local-reply body. Envoy command
	// operators such as %RESPONSE_CODE% are substituted at response time.
	BodyHTML string
	// ContentType is the Content-Type set on the branded response
	// (e.g. "text/html; charset=UTF-8").
	ContentType string
}

// InjectLocalReplyConfig attaches a branded local_reply_config to every
// RDS-based HttpConnectionManager in every filter chain of the listener
// (including the default filter chain). It mirrors InjectCorazaListenerFilters:
// it walks the same filter-chain set, unmarshals each HCM, skips HCMs that are
// not RDS-based (EG's internal readiness listener uses an inline static
// route_config and must never be branded), and is idempotent — an HCM that
// already carries a local_reply_config is left untouched.
//
// The branded body replaces only the response body and content-type; the
// original HTTP status code is preserved (StatusCode left nil on the mapper).
// A status_code_filter gates the rewrite to responses >= MinStatusCode, backed
// by a runtime key so it can be disabled in an emergency.
//
// Returns the number of HCMs mutated.
//
// SAFETY: the downstream EG extension hook runs with failOpen:false, so a
// returned error blocks the entire xDS update fleet-wide. This function only
// errors on a genuinely malformed HCM (an HCM whose typed_config cannot be
// unmarshalled or re-marshalled) — exactly the same failure surface as the
// Coraza injector. A missing or empty body is a no-op, never an error, so the
// embedded-fallback guarantee holds: there is always a valid page and the data
// plane is never stalled by content problems.
func InjectLocalReplyConfig(l *listenerv3.Listener, cfg *LocalReplyConfig) (int, error) {
	// Unconfigured/empty => no-op (same convention as CorazaConfig). This is the
	// primary fail-safe: with no body there is simply nothing to inject.
	if cfg.Disabled || cfg.BodyHTML == "" {
		return 0, nil
	}

	lrc := buildLocalReplyConfig(cfg)

	chains := make([]*listenerv3.FilterChain, 0, len(l.GetFilterChains())+1)
	chains = append(chains, l.GetFilterChains()...)
	if dfc := l.GetDefaultFilterChain(); dfc != nil {
		chains = append(chains, dfc)
	}

	mutated := 0
	for _, fc := range chains {
		for _, f := range fc.GetFilters() {
			if f.GetName() != hcmFilterName {
				continue
			}
			tc := f.GetTypedConfig()
			if tc == nil {
				continue
			}
			hcm := &hcmv3.HttpConnectionManager{}
			if err := tc.UnmarshalTo(hcm); err != nil {
				return mutated, fmt.Errorf("unmarshal HCM in filter chain %q: %w", fc.GetName(), err)
			}
			// Only brand HCMs that use dynamic route discovery (RDS). EG's
			// internal listeners (e.g. the proxy readiness endpoint) use an
			// inline static route_config; branding those would attach the
			// error page to infrastructure/health endpoints. This check is
			// name-independent — it tests the route_specifier oneof, not the
			// listener name — matching InjectCorazaListenerFilters.
			if hcm.GetRds() == nil {
				continue
			}
			// Idempotent: do not overwrite an existing local_reply_config (ours
			// from a prior pass, or one EG/another extension already set).
			if hcm.GetLocalReplyConfig() != nil {
				continue
			}
			hcm.LocalReplyConfig = lrc
			newTC, err := anypb.New(hcm)
			if err != nil {
				return mutated, fmt.Errorf("marshal HCM in filter chain %q: %w", fc.GetName(), err)
			}
			f.ConfigType = &listenerv3.Filter_TypedConfig{TypedConfig: newTC}
			mutated++
		}
	}
	return mutated, nil
}

// buildLocalReplyConfig builds the Envoy local_reply_config carrying a single
// response mapper: match any response with status >= MinStatusCode (gated by a
// runtime key) and override the body with the branded HTML. StatusCode is left
// nil on the mapper so the original response code is preserved.
func buildLocalReplyConfig(cfg *LocalReplyConfig) *hcmv3.LocalReplyConfig {
	minStatus := cfg.MinStatusCode
	if minStatus == 0 {
		minStatus = defaultMinStatusCode
	}
	return &hcmv3.LocalReplyConfig{
		Mappers: []*hcmv3.ResponseMapper{{
			Filter: &accesslogv3.AccessLogFilter{
				FilterSpecifier: &accesslogv3.AccessLogFilter_StatusCodeFilter{
					StatusCodeFilter: &accesslogv3.StatusCodeFilter{
						Comparison: &accesslogv3.ComparisonFilter{
							Op: accesslogv3.ComparisonFilter_GE,
							Value: &corev3.RuntimeUInt32{
								DefaultValue: minStatus,
								RuntimeKey:   cfg.RuntimeKey,
							},
						},
					},
				},
			},
			// StatusCode left nil => preserve the original code; only the body
			// and content-type are replaced.
			BodyFormatOverride: &corev3.SubstitutionFormatString{
				ContentType: cfg.ContentType,
				Format: &corev3.SubstitutionFormatString_TextFormatSource{
					TextFormatSource: &corev3.DataSource{
						Specifier: &corev3.DataSource_InlineString{InlineString: escapeEnvoyFormatLiterals(cfg.BodyHTML)},
					},
				},
			},
		}},
	}
}

// envoyBodyAllowedCommands is the allowlist of Envoy substitution command
// operators permitted to pass through unescaped in an error-page body. It is an
// allowlist, not a grammar match: only operators we actually template are
// preserved, because any *other* command-shaped token (e.g. a literal
// "%COMPLETE%" in an override page) is not a valid Envoy operator and would
// re-trigger the exact listener rejection this escaper exists to prevent
// (issue #243). Keep entries longest-first so matching is unambiguous.
var envoyBodyAllowedCommands = []string{
	"%START_TIME(%Y-%m-%d %H:%M:%S UTC)%",
	"%RESPONSE_CODE_DETAILS%",
	"%RESPONSE_CODE%",
}

// matchAllowedCommand returns the allowlisted command operator that prefixes s,
// or "" if none does.
func matchAllowedCommand(s string) string {
	for _, cmd := range envoyBodyAllowedCommands {
		if strings.HasPrefix(s, cmd) {
			return cmd
		}
	}
	return ""
}

// escapeEnvoyFormatLiterals makes an arbitrary string safe to embed as the
// text_format of an Envoy SubstitutionFormatString. Envoy parses that string as
// a format template, so any bare '%' is read as the start of a command operator
// and an unrecognized one is rejected — which, on the failOpen:false downstream
// hook, NACKs the whole listener xDS update fleet-wide (see issue #243: the
// branded error page's CSS "height: 100%" took the listener down).
//
// Bare '%' are escaped to '%%' (Envoy's literal-percent escape) while
// already-escaped '%%' and allowlisted operators (envoyBodyAllowedCommands,
// e.g. the intended %RESPONSE_CODE%) are preserved unchanged. Every other
// command-shaped token is treated as a literal and escaped, so an unrecognized
// operator can never survive into the pushed config. The transform is
// idempotent.
func escapeEnvoyFormatLiterals(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '%' {
			b.WriteString("%%")
			i += 2
			continue
		}
		if cmd := matchAllowedCommand(s[i:]); cmd != "" {
			b.WriteString(cmd)
			i += len(cmd)
			continue
		}
		b.WriteString("%%")
		i++
	}
	return b.String()
}

// assertEnvoyFormatSafe returns an error if s still contains a bare '%' that
// Envoy would misparse as an incomplete or unrecognized command operator. It is
// the inverse invariant of escapeEnvoyFormatLiterals and backs the startup
// validation in ValidateLocalReplyConfig.
func assertEnvoyFormatSafe(s string) error {
	for i := 0; i < len(s); {
		if s[i] != '%' {
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '%' {
			i += 2
			continue
		}
		if cmd := matchAllowedCommand(s[i:]); cmd != "" {
			i += len(cmd)
			continue
		}
		return fmt.Errorf("bare %% at byte offset %d", i)
	}
	return nil
}

// ValidateLocalReplyConfig assembles the local_reply_config and asserts it is
// safe to push, so a malformed body is caught in the operator process at startup
// instead of NACKing the xDS update fleet-wide on the failOpen:false downstream
// hook (issue #243). It round-trips the config through proto marshalling and
// verifies the injected body carries no bare '%'. A disabled or empty config is
// vacuously valid (injection is a no-op).
func ValidateLocalReplyConfig(cfg *LocalReplyConfig) error {
	if cfg.Disabled || cfg.BodyHTML == "" {
		return nil
	}
	lrc := buildLocalReplyConfig(cfg)
	if _, err := anypb.New(lrc); err != nil {
		return fmt.Errorf("marshal local_reply_config: %w", err)
	}
	body := lrc.GetMappers()[0].GetBodyFormatOverride().GetTextFormatSource().GetInlineString()
	if err := assertEnvoyFormatSafe(body); err != nil {
		return fmt.Errorf("assembled error-page body is not Envoy-safe: %w", err)
	}
	return nil
}
