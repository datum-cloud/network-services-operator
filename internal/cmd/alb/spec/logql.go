// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"fmt"
	"regexp"
	"strings"
)

var lokiLabelEscape = regexp.MustCompile(`[.*+?^${}()|[\]\\]`)

func BuildAlbLogQL(proxyName string, methods, codes []string) string {
	pin := fmt.Sprintf(`route_name=~"%s"`, escapeLogQLQuoted(albRouteNameRegexp(proxyName)))
	extras := make([]string, 0, len(methods)+len(codes))
	extras = append(extras, logQLMatchers("method", methods)...)
	extras = append(extras, logQLMatchers("response_code", codes)...)
	if len(extras) == 0 {
		return "{" + pin + "}"
	}
	return "{" + pin + ", " + strings.Join(extras, ", ") + "}"
}

func albRouteNameRegexp(proxyName string) string {
	return `httproute/[^/]+/` + escapeLogQLRegexp(proxyName) + `/.*`
}

func logQLMatchers(label string, values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		cleaned = append(cleaned, v)
	}
	switch len(cleaned) {
	case 0:
		return nil
	case 1:
		return []string{fmt.Sprintf(`%s="%s"`, label, escapeLogQLQuoted(cleaned[0]))}
	default:
		parts := make([]string, len(cleaned))
		for i, v := range cleaned {
			parts[i] = escapeLogQLRegexp(v)
		}
		return []string{fmt.Sprintf(`%s=~"%s"`, label, strings.Join(parts, "|"))}
	}
}

func escapeLogQLQuoted(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
}

func escapeLogQLRegexp(value string) string {
	return lokiLabelEscape.ReplaceAllStringFunc(value, func(m string) string {
		return `\` + m
	})
}
