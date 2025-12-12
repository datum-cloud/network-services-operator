package registrydata

import (
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/go-redis/redis/v7"
	"github.com/stretchr/testify/require"
)

func newTestRedis(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err)

	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = c.Close()
		mr.Close()
	})
	return mr, c
}
