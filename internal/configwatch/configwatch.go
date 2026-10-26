package configwatch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"

	"go.miloapis.com/instrumentation/metrics"
)

const DefaultInterval = 10 * time.Second

var ErrChanged = errors.New("server config changed")

var rejected = metrics.NewGauge(prometheus.GaugeOpts{
	Name: "nso_server_config_rejected",
	Help: "1 while the server config file cannot be read or holds a config this process refuses to load, so the process keeps running on the config it started with.",
})

type Watcher struct {
	Path   string
	Loaded []byte
	// Changed reports whether data loads to a config that differs from the
	// running one. An error rejects data and keeps the running config.
	Changed  func(data []byte) (bool, error)
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

	seen := w.Loaded
	readFailing := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		data, err := os.ReadFile(w.Path)
		if err != nil {
			if !readFailing {
				w.Log.Error(err, "unable to read server config, keeping the running config", "path", w.Path)
			}
			readFailing = true
			rejected.Set(1)
			continue
		}
		if readFailing {
			readFailing = false
			seen = nil
		}
		if seen != nil && bytes.Equal(data, seen) {
			continue
		}
		seen = data

		changed, err := w.Changed(data)
		if err != nil {
			w.Log.Error(err, "server config changed but cannot be loaded, keeping the running config", "path", w.Path)
			rejected.Set(1)
			continue
		}
		rejected.Set(0)
		if !changed {
			w.Log.Info("server config file changed with no effect on the running config", "path", w.Path)
			continue
		}
		w.Log.Info("server config changed, stopping so the new config takes effect", "path", w.Path)
		return ErrChanged
	}
}
