package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// The two generation gauges were set inside the hook call, so an edge that missed a rebuild left them equal and
// hid the miss. Each policy target's Programmed condition reports it instead.
func TestTPPGenerationGaugesAreNotRegistered(t *testing.T) {
	for _, name := range []string{"nso_extension_tpp_cache_generation", "nso_extension_tpp_applied_generation"} {
		probe := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: "probe"}, []string{"namespace", "name"})
		if err := prometheus.DefaultRegisterer.Register(probe); err != nil {
			t.Errorf("%s is still registered: %v", name, err)
			continue
		}
		prometheus.DefaultRegisterer.Unregister(probe)
	}
}
