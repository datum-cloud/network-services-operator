package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestValidateEndpointSliceAddresses(t *testing.T) {
	tests := []struct {
		name      string
		addresses []string
		vouched   sets.Set[string]
		suffixes  []string
		fields    []string
	}{
		{name: "public", addresses: []string{"203.0.113.5", "origin.example.com"}},
		{name: "loopback", addresses: []string{"127.0.0.1"}, fields: []string{"endpoints[0].addresses[0]"}},
		{name: "second address", addresses: []string{"203.0.113.5", "fd00::1"}, fields: []string{"endpoints[0].addresses[1]"}},
		{name: "octal loopback", addresses: []string{"0177.0.0.1"}, fields: []string{"endpoints[0].addresses[0]"}},
		{name: "cluster name", addresses: []string{"api.kube-system.svc"}, fields: []string{"endpoints[0].addresses[0]"}},
		{name: "vouched", addresses: []string{"fd20::7"}, vouched: sets.New("fd20::7")},
		{name: "permitted suffix", addresses: []string{"origin.e2e.svc.cluster.local"}, suffixes: []string{"svc.cluster.local"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slice := &discoveryv1.EndpointSlice{Endpoints: []discoveryv1.Endpoint{{Addresses: tt.addresses}}}
			var fields []string
			for _, err := range ValidateEndpointSliceAddresses(slice, tt.vouched, tt.suffixes) {
				fields = append(fields, err.Field)
			}
			assert.Equal(t, tt.fields, fields)
		})
	}
}
