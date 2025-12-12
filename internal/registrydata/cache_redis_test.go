package registrydata

import (
	"testing"
	"time"

	redis "github.com/go-redis/redis/v7"
	"github.com/stretchr/testify/require"
)

func TestRedisCache_SetGetAndExpire(t *testing.T) {
	t.Parallel()

	mr, client := newTestRedis(t)
	c := newRedisCache(client, "pfx:")

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

	mr.FastForward(11 * time.Second)
	var got2 payload
	found, err = c.Get("k", &got2)
	require.NoError(t, err)
	require.False(t, found)
}

func TestRedisCache_BadJSONTreatedAsMissAndDeleted(t *testing.T) {
	t.Parallel()

	_, client := newTestRedis(t)
	c := newRedisCache(client, "pfx:")

	// write an invalid JSON value
	require.NoError(t, client.Set("pfx:bad", []byte("{not-json"), 10*time.Second).Err())

	var dst map[string]any
	found, err := c.Get("bad", &dst)
	require.NoError(t, err)
	require.False(t, found)

	// should have deleted the bad value
	require.Equal(t, int64(0), client.Exists("pfx:bad").Val())
}

func TestRedisCache_Get_MissingIsNotError(t *testing.T) {
	t.Parallel()

	_, client := newTestRedis(t)
	c := newRedisCache(client, "")

	var dst any
	found, err := c.Get("missing", &dst)
	require.NoError(t, err)
	require.False(t, found)

	// Ensure we didn't accidentally create the key.
	require.Equal(t, redis.Nil, client.Get("missing").Err())
}
