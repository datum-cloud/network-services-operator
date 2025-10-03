package gateway

import (
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
)

const (
	DefaultHTTPPort  = 80
	DefaultHTTPSPort = 443

	DefaultHTTPListenerName  = "default-http"
	DefaultHTTPSListenerName = "default-https"
)

func IsDefaultListener(l gatewayv1.Listener) bool {
	return l.Name == DefaultHTTPListenerName || l.Name == DefaultHTTPSListenerName
}

func GetListenerByName(listeners []gatewayv1.Listener, name gatewayv1.SectionName) *gatewayv1.Listener {
	for i, l := range listeners {
		if l.Name == name {
			return &listeners[i]
		}
	}
	return nil
}

// SetListener will insert a listener into the gateway, or replace the existing
// listener of the same name with the provided listener.
func SetListener(gateway *gatewayv1.Gateway, listener gatewayv1.Listener) {
	for i, l := range gateway.Spec.Listeners {
		if l.Name == listener.Name {
			gateway.Spec.Listeners[i] = listener
			return
		}
	}

	gateway.Spec.Listeners = append(gateway.Spec.Listeners, listener)
}

func SetDefaultListeners(gateway *gatewayv1.Gateway, gatewayConfig config.GatewayConfig) {
	SetListener(gateway, gatewayv1.Listener{
		Name:     DefaultHTTPListenerName,
		Protocol: gatewayv1.HTTPProtocolType,
		Port:     DefaultHTTPPort,
		AllowedRoutes: &gatewayv1.AllowedRoutes{
			Namespaces: &gatewayv1.RouteNamespaces{
				From: ptr.To(gatewayv1.NamespacesFromSame),
			},
		},
	})

	SetListener(gateway, gatewayv1.Listener{
		Name:     DefaultHTTPSListenerName,
		Protocol: gatewayv1.HTTPSProtocolType,
		Port:     DefaultHTTPSPort,
		AllowedRoutes: &gatewayv1.AllowedRoutes{
			Namespaces: &gatewayv1.RouteNamespaces{
				From: ptr.To(gatewayv1.NamespacesFromSame),
			},
		},
		TLS: &gatewayv1.GatewayTLSConfig{
			Mode:    ptr.To(gatewayv1.TLSModeTerminate),
			Options: gatewayConfig.ListenerTLSOptions,
		},
	})
}
