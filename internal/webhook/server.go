package webhook

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"

	"go.datum.net/network-services-operator/internal/providers"
)

type clusterAwareWebhookServer struct {
	webhook.Server
	discoveryMode providers.Provider
}

var _ webhook.Server = &clusterAwareWebhookServer{}

func (s *clusterAwareWebhookServer) Register(path string, hook http.Handler) {
	if h, ok := hook.(*admission.Webhook); ok {
		h.WithContextFunc = func(ctx context.Context, req *http.Request) context.Context {
			clusterName, err := url.QueryUnescape(req.PathValue("cluster_name"))
			if err != nil {
				return ctx
			}
			return mccontext.WithCluster(ctx, clusterName)
		}
	}

	if s.discoveryMode != providers.ProviderSingle {
		path = fmt.Sprintf("/clusters/{cluster_name}%s", path)
	}

	s.Server.Register(path, hook)
}

func NewClusterAwareWebhookServer(server webhook.Server, mode providers.Provider) webhook.Server {
	return &clusterAwareWebhookServer{
		Server:        server,
		discoveryMode: mode,
	}
}
