package registrydata

import (
	"context"
	"time"
)

type ProviderLimiter interface {
	Acquire(ctx context.Context, provider string) (ok bool, retryAfter time.Duration, err error)
	BlockUntil(ctx context.Context, provider string, until time.Time) error
}
