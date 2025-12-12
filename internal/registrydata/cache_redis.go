package registrydata

import (
	"encoding/json"
	"time"

	redis "github.com/go-redis/redis/v7"
)

type redisCache struct {
	client redis.UniversalClient
	prefix string
}

func newRedisCache(client redis.UniversalClient, prefix string) *redisCache {
	return &redisCache{client: client, prefix: prefix}
}

func (c *redisCache) key(k string) string {
	if c.prefix == "" {
		return k
	}
	return c.prefix + k
}

func (c *redisCache) Get(key string, dst any) (bool, error) {
	val, err := c.client.Get(c.key(key)).Bytes()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(val, dst); err != nil {
		// treat as miss; delete the bad value
		_ = c.client.Del(c.key(key)).Err()
		return false, nil
	}
	return true, nil
}

func (c *redisCache) Set(key string, value any, ttl time.Duration) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(c.key(key), b, ttl).Err()
}
