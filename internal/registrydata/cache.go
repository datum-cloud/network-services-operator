package registrydata

import (
	"time"
)

type Cache interface {
	Get(key string, dst any) (found bool, err error)
	Set(key string, value any, ttl time.Duration) error
}
