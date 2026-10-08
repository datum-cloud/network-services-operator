// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToKebabCase(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "customer-api", toKebabCase("Customer API"))
	assert.Equal(t, "my-app", toKebabCase("MyApp"))
	assert.Equal(t, "gw-1", toKebabCase("gw.1"))
	assert.Equal(t, "foo-bar", toKebabCase("  foo__bar  "))
	assert.Equal(t, "", toKebabCase("!!!"))
}

func TestNameFromDisplayName(t *testing.T) {
	t.Parallel()

	restore := randomNameSuffix
	t.Cleanup(func() { randomNameSuffix = restore })
	randomNameSuffix = func(n int) (string, error) {
		return strings.Repeat("a", n), nil
	}

	got, err := NameFromDisplayName("Customer API")
	require.NoError(t, err)
	assert.Equal(t, "customer-api-aaaaaa", got)

	got, err = NameFromDisplayName(strings.Repeat("x", 40))
	require.NoError(t, err)
	assert.LessOrEqual(t, len(got), generatedNameMaxLength)
	assert.True(t, strings.HasSuffix(got, "-aaaaaa"))
	assert.True(t, isDNSLabel(got))

	_, err = NameFromDisplayName("")
	require.Error(t, err)
	_, err = NameFromDisplayName("!!!")
	require.Error(t, err)
}
