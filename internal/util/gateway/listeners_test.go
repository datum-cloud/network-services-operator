package gateway

import (
	"testing"

	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
)

func listenerNames(gateway *gatewayv1.Gateway) []string {
	names := make([]string, 0, len(gateway.Spec.Listeners))
	for _, l := range gateway.Spec.Listeners {
		names = append(names, string(l.Name))
	}
	return names
}

func TestRestoreMissingDefaultListeners(t *testing.T) {
	cfg := config.GatewayConfig{}

	scenarios := map[string]struct {
		listeners     []gatewayv1.Listener
		expectedNames []string
		expectHost    map[string]string
	}{
		"both defaults stripped are restored": {
			listeners: []gatewayv1.Listener{
				{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80, Hostname: ptr.To(gatewayv1.Hostname("appauth.example.com"))},
			},
			expectedNames: []string{"http", DefaultHTTPListenerName, DefaultHTTPSListenerName},
		},
		"an existing default keeps its controller-assigned hostname": {
			listeners: []gatewayv1.Listener{
				{Name: DefaultHTTPListenerName, Protocol: gatewayv1.HTTPProtocolType, Port: 80, Hostname: ptr.To(gatewayv1.Hostname("uid.datumproxy.net"))},
			},
			expectedNames: []string{DefaultHTTPListenerName, DefaultHTTPSListenerName},
			expectHost:    map[string]string{DefaultHTTPListenerName: "uid.datumproxy.net"},
		},
		"nothing to do when both defaults are present": {
			listeners: []gatewayv1.Listener{
				{Name: DefaultHTTPListenerName, Protocol: gatewayv1.HTTPProtocolType, Port: 80, Hostname: ptr.To(gatewayv1.Hostname("uid.datumproxy.net"))},
				{Name: DefaultHTTPSListenerName, Protocol: gatewayv1.HTTPSProtocolType, Port: 443, Hostname: ptr.To(gatewayv1.Hostname("uid.datumproxy.net"))},
			},
			expectedNames: []string{DefaultHTTPListenerName, DefaultHTTPSListenerName},
			expectHost: map[string]string{
				DefaultHTTPListenerName:  "uid.datumproxy.net",
				DefaultHTTPSListenerName: "uid.datumproxy.net",
			},
		},
		"a gateway left with no listeners regains both": {
			listeners:     nil,
			expectedNames: []string{DefaultHTTPListenerName, DefaultHTTPSListenerName},
		},
	}

	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			gateway := &gatewayv1.Gateway{Spec: gatewayv1.GatewaySpec{Listeners: scenario.listeners}}
			RestoreMissingDefaultListeners(gateway, cfg)

			got := listenerNames(gateway)
			if len(got) != len(scenario.expectedNames) {
				t.Fatalf("expected listeners %v, got %v", scenario.expectedNames, got)
			}
			for i, want := range scenario.expectedNames {
				if got[i] != want {
					t.Errorf("listener %d: expected %q, got %q", i, want, got[i])
				}
			}
			for listenerName, wantHost := range scenario.expectHost {
				l := GetListenerByName(gateway.Spec.Listeners, gatewayv1.SectionName(listenerName))
				if l == nil || l.Hostname == nil {
					t.Fatalf("listener %q lost its hostname", listenerName)
				}
				if string(*l.Hostname) != wantHost {
					t.Errorf("listener %q: expected hostname %q, got %q", listenerName, wantHost, string(*l.Hostname))
				}
			}
		})
	}
}
