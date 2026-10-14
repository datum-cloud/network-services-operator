// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildAlbLogQL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `{route_name=~"httproute/[^/]+/my-app/.*"}`, BuildAlbLogQL([]string{"my-app"}, nil, nil))
	assert.Equal(t,
		`{route_name=~"httproute/[^/]+/gw\\.1/.*", method="GET"}`,
		BuildAlbLogQL([]string{"gw.1"}, []string{"GET"}, nil),
	)
	assert.Equal(t,
		`{route_name=~"httproute/[^/]+/my-app/.*", method=~"GET|POST", response_code=~"500|502"}`,
		BuildAlbLogQL([]string{"my-app"}, []string{"GET", "POST"}, []string{"500", "502"}),
	)
	assert.Equal(t,
		`{route_name=~"httproute/[^/]+/gw\"1/.*"}`,
		BuildAlbLogQL([]string{`gw"1`}, nil, nil),
	)
	assert.Equal(t, `{route_name=~"httproute/[^/]+/[^/]+/.*"}`, BuildAlbLogQL(nil, nil, nil))
	assert.Equal(t,
		`{route_name=~"httproute/[^/]+/(a|b\\.c)/.*", response_code="500"}`,
		BuildAlbLogQL([]string{"a", "b.c", "a", " "}, nil, []string{"500"}),
	)
}
