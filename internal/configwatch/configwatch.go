package configwatch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"time"

	"github.com/go-logr/logr"
)

const DefaultInterval = 10 * time.Second

var ErrChanged = errors.New("server config changed")

type Watcher struct {
	Path     string
	Loaded   []byte
	Validate func([]byte) error
	Interval time.Duration
	Log      logr.Logger
}

func (w *Watcher) Start(ctx context.Context) error {
	interval := w.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var rejected []byte
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		data, err := os.ReadFile(w.Path)
		if err != nil {
			w.Log.Error(err, "unable to read server config, keeping the current config", "path", w.Path)
			continue
		}
		if bytes.Equal(data, w.Loaded) || (rejected != nil && bytes.Equal(data, rejected)) {
			continue
		}
		if w.Validate != nil {
			if err := w.Validate(data); err != nil {
				w.Log.Error(err, "server config changed but is invalid, keeping the current config", "path", w.Path)
				rejected = data
				continue
			}
		}
		w.Log.Info("server config changed, stopping so the new config takes effect", "path", w.Path)
		return ErrChanged
	}
}
