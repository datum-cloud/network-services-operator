package registrydata

import (
	"net/http"
	"testing"
	"time"

	"github.com/openrdap/rdap"
	"github.com/stretchr/testify/require"
)

func TestParseRetryAfterHeader_DeltaSeconds(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0*time.Second, parseRetryAfterHeader(""))
	require.Equal(t, 0*time.Second, parseRetryAfterHeader("   "))
	require.Equal(t, 120*time.Second, parseRetryAfterHeader("120"))
	require.Equal(t, 0*time.Second, parseRetryAfterHeader("-5"))
}

func TestParseRetryAfterHeader_HTTPDate(t *testing.T) {
	t.Parallel()

	target := time.Now().Add(5 * time.Second).UTC().Truncate(time.Second)
	d := parseRetryAfterHeader(target.Format(time.RFC1123))
	require.GreaterOrEqual(t, d, 3*time.Second)
	require.LessOrEqual(t, d, 6*time.Second)
}

func TestRDAPSuggestedDelay_MinimumAcrossResponses(t *testing.T) {
	t.Parallel()

	r1 := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"10"}},
	}
	r2 := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Retry-After": []string{"2"}},
	}
	r3 := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
	}

	resp := &rdap.Response{
		HTTP: []*rdap.HTTPResponse{
			{Response: r1},
			{Response: r2},
			{Response: r3},
		},
	}

	delay, limited := rdapSuggestedDelay(resp)
	require.True(t, limited)
	require.Equal(t, 2*time.Second, delay)
}
