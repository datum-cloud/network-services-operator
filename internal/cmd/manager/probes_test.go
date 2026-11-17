// SPDX-License-Identifier: AGPL-3.0-only

package managercmd

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"

	networkingwebhook "go.datum.net/network-services-operator/internal/webhook"
	"go.datum.net/network-services-operator/internal/webhook/webhooktest"
)

type recordedProbes struct {
	webhookServer webhook.Server
	readyz        map[string]healthz.Checker
}

func (r *recordedProbes) AddHealthzCheck(string, healthz.Checker) error { return nil }

func (r *recordedProbes) AddReadyzCheck(name string, check healthz.Checker) error {
	r.readyz[name] = check
	return nil
}

func (r *recordedProbes) GetWebhookServer() webhook.Server { return r.webhookServer }

func (r *recordedProbes) ready() error {
	req, _ := http.NewRequest(http.MethodGet, "/readyz", nil)
	var errs []error
	for name, check := range r.readyz {
		if err := check(req); err != nil {
			errs = append(errs, errors.New(name+": "+err.Error()))
		}
	}
	return errors.Join(errs...)
}

func TestManagerIsReadyOnlyWhileTheWebhookServerServes(t *testing.T) {
	probes := &recordedProbes{
		webhookServer: networkingwebhook.NewClusterAwareWebhookServer(
			webhook.NewServer(webhooktest.NewServing(t).Options),
			multiclusterproviders.ProviderMilo,
		),
		readyz: map[string]healthz.Checker{},
	}
	if err := addProbes(probes); err != nil {
		t.Fatal(err)
	}

	if err := probes.ready(); err == nil {
		t.Fatal("the manager is ready before its webhook server serves")
	}

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- probes.webhookServer.Start(ctx) }()

	webhooktest.WaitFor(t, "ready once the webhook server serves", func(c *assert.CollectT) {
		assert.NoError(c, probes.ready())
	})

	cancel()
	if err := <-stopped; err != nil {
		t.Fatalf("webhook server stopped with an error: %v", err)
	}
	if err := probes.ready(); err == nil {
		t.Fatal("the manager is ready after its webhook server stopped serving")
	}
}
