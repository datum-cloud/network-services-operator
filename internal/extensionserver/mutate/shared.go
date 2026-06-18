// Package mutate implements the xDS mutation families for the extension server.
// It is a CR-driven port of test/perf/extserver/internal/mutate, replacing
// blanket-regex targeting with PolicyIndex lookups derived from the informer
// cache. See internal/extensionserver/cache for the index types.
package mutate

import (
	"slices"
	"strings"
)

// sanitizeID turns an xDS resource name into a label-safe id fragment.
// Ported from test/perf/extserver/internal/mutate/connector.go.
func sanitizeID(s string) string {
	r := strings.NewReplacer("/", "-", " ", "-", ":", "-")
	return r.Replace(s)
}

// appendUnique appends v to s only if v is not already present.
func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}
