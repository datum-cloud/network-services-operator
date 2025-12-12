package registrydata

import (
	"context"
	"fmt"
	"strconv"
	"time"

	redis "github.com/go-redis/redis/v7"
)

type redisProviderLimiter struct {
	client   redis.UniversalClient
	prefix   string
	limits   RateLimits
	stateTTL time.Duration

	acquireScript *redis.Script
	blockScript   *redis.Script
}

func newRedisProviderLimiter(client redis.UniversalClient, prefix string, limits RateLimits) *redisProviderLimiter {
	return &redisProviderLimiter{
		client:   client,
		prefix:   prefix,
		limits:   limits,
		stateTTL: 30 * time.Minute,
		acquireScript: redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local burst = tonumber(ARGV[3])
local defaultBlock = tonumber(ARGV[4])
local stateTTL = tonumber(ARGV[5])

local tokens = tonumber(redis.call('HGET', key, 'tokens'))
if tokens == nil then tokens = burst end

local lastRefill = tonumber(redis.call('HGET', key, 'last_refill'))
if lastRefill == nil then lastRefill = now end

local blockedUntil = tonumber(redis.call('HGET', key, 'blocked_until'))
if blockedUntil == nil then blockedUntil = 0 end

if blockedUntil > now then
  return {0, blockedUntil - now}
end

local delta = now - lastRefill
if delta < 0 then delta = 0 end

tokens = math.min(burst, tokens + (delta * rate / 1000.0))

if tokens >= 1.0 then
  tokens = tokens - 1.0
  redis.call('HSET', key, 'tokens', tokens, 'last_refill', now)
  redis.call('PEXPIRE', key, stateTTL)
  return {1, 0}
else
  local blockUntil = now + defaultBlock
  redis.call('HSET', key, 'tokens', tokens, 'last_refill', now, 'blocked_until', blockUntil)
  redis.call('PEXPIRE', key, stateTTL)
  return {0, defaultBlock}
end
`),
		blockScript: redis.NewScript(`
local key = KEYS[1]
local blockUntil = tonumber(ARGV[1])
local stateTTL = tonumber(ARGV[2])
local cur = tonumber(redis.call('HGET', key, 'blocked_until'))
if cur == nil then cur = 0 end
if blockUntil > cur then
  redis.call('HSET', key, 'blocked_until', blockUntil)
end
redis.call('PEXPIRE', key, stateTTL)
return 1
`),
	}
}

func (l *redisProviderLimiter) key(provider string) string {
	if provider == "" {
		provider = "default"
	}
	base := "rl:" + provider
	if l.prefix == "" {
		return base
	}
	return l.prefix + base
}

func (l *redisProviderLimiter) Acquire(ctx context.Context, provider string) (bool, time.Duration, error) {
	_ = ctx // go-redis/v7 is context-less
	now := time.Now()
	nowMs := now.UnixMilli()
	stateTTLms := l.stateTTL.Milliseconds()
	if stateTTLms <= 0 {
		stateTTLms = (30 * time.Minute).Milliseconds()
	}
	defaultBlock := l.limits.DefaultBlock
	if defaultBlock <= 0 {
		defaultBlock = 2 * time.Second
	}
	res, err := l.acquireScript.Run(l.client, []string{l.key(provider)}, nowMs,
		l.limits.DefaultRatePerSec, l.limits.DefaultBurst, defaultBlock.Milliseconds(), stateTTLms).Result()
	if err != nil {
		return false, 0, err
	}
	arr, ok := res.([]interface{})
	if !ok || len(arr) < 2 {
		return false, 0, fmt.Errorf("unexpected limiter response: %T", res)
	}
	okNum, ok := toInt64(arr[0])
	if !ok {
		return false, 0, fmt.Errorf("unexpected limiter ok type: %T", arr[0])
	}
	if okNum == 0 {
		retryMs, ok := toInt64(arr[1])
		if !ok {
			return false, 0, fmt.Errorf("unexpected limiter retry type: %T", arr[1])
		}
		return false, time.Duration(retryMs) * time.Millisecond, nil
	}
	return true, 0, nil
}

func (l *redisProviderLimiter) BlockUntil(ctx context.Context, provider string, until time.Time) error {
	_ = ctx // go-redis/v7 is context-less
	stateTTLms := l.stateTTL.Milliseconds()
	if stateTTLms <= 0 {
		stateTTLms = (30 * time.Minute).Milliseconds()
	}
	_, err := l.blockScript.Run(l.client, []string{l.key(provider)}, until.UnixMilli(), stateTTLms).Result()
	return err
}

func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	case float64:
		return int64(x), true
	case []byte:
		n, err := strconv.ParseInt(string(x), 10, 64)
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}
