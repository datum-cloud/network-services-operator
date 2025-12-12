package registrydata

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemoryCache_SetGetAndExpire(t *testing.T) {
	t.Parallel()

	c := newMemoryCache()
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	type payload struct {
		A string `json:"a"`
		B int    `json:"b"`
	}

	require.NoError(t, c.Set("k", payload{A: "x", B: 7}, 10*time.Second))

	var got payload
	found, err := c.Get("k", &got)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, payload{A: "x", B: 7}, got)

	now = now.Add(11 * time.Second)
	var got2 payload
	found, err = c.Get("k", &got2)
	require.NoError(t, err)
	require.False(t, found)
}

func TestMemoryCache_BadJSONTreatedAsMissAndDeleted(t *testing.T) {
	t.Parallel()

	c := newMemoryCache()
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	c.mu.Lock()
	c.entries["bad"] = memEntry{b: []byte("{not-json"), expiresAt: now.Add(1 * time.Hour)}
	c.mu.Unlock()

	var dst map[string]any
	found, err := c.Get("bad", &dst)
	require.NoError(t, err)
	require.False(t, found)

	c.mu.RLock()
	_, ok := c.entries["bad"]
	c.mu.RUnlock()
	require.False(t, ok)
}
