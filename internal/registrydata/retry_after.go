package registrydata

import (
	"strings"
	"time"
)

// parseRetryAfterHeader parses Retry-After per RFC: either delta-seconds or HTTP-date.
func parseRetryAfterHeader(val string) time.Duration {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0
	}
	// delta-seconds
	if secs, err := time.ParseDuration(val + "s"); err == nil {
		if secs < 0 {
			return 0
		}
		return secs
	}
	// HTTP-date
	if t, err := time.Parse(time.RFC1123, val); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	if t, err := time.Parse(time.RFC1123Z, val); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}
