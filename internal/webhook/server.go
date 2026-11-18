package webhook

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"
)

type ClusterAwareWebhookServer struct {
	webhook.Server
	discoveryMode multiclusterproviders.Provider
	closing       atomic.Bool
}

var _ webhook.Server = &ClusterAwareWebhookServer{}

func (s *ClusterAwareWebhookServer) Register(path string, hook http.Handler) {
	if h, ok := hook.(*admission.Webhook); ok {
		orig := h.Handler
		h.Handler = admission.HandlerFunc(func(ctx context.Context, req admission.Request) admission.Response {
			c := clusterFromExtra(req.UserInfo.Extra)
			ctx = mccontext.WithCluster(ctx, multicluster.ClusterName(c))
			return orig.Handle(ctx, req)
		})
	}

	s.Server.Register(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.closing.Load() {
			w.Header().Set("Connection", "close")
		}
		hook.ServeHTTP(w, r)
	}))
}

func (s *ClusterAwareWebhookServer) CloseConnectionsThenStop(shutdown context.Context, delay time.Duration, stopManager context.CancelFunc) {
	<-shutdown.Done()
	logf.Log.WithName("webhook").Info("closing each connection before shutdown", "delay", delay)
	s.closing.Store(true)
	time.Sleep(delay)
	stopManager()
}

func NewClusterAwareWebhookServer(server webhook.Server, mode multiclusterproviders.Provider) *ClusterAwareWebhookServer {
	return &ClusterAwareWebhookServer{
		Server:        server,
		discoveryMode: mode,
	}
}
