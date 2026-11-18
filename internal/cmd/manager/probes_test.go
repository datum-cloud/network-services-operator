// SPDX-License-Identifier: AGPL-3.0-only

package managercmd

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"

	networkingwebhook "go.datum.net/network-services-operator/internal/webhook"
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
	install := &envtest.WebhookInstallOptions{LocalServingHost: "127.0.0.1"}
	require.NoError(t, install.PrepWithoutInstalling())
	t.Cleanup(func() { require.NoError(t, install.Cleanup()) })
	probes := &recordedProbes{
		webhookServer: networkingwebhook.NewClusterAwareWebhookServer(
			webhook.NewServer(webhook.Options{
				Host:    install.LocalServingHost,
				Port:    install.LocalServingPort,
				CertDir: install.LocalServingCertDir,
			}),
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

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, probes.ready())
	}, 10*time.Second, 20*time.Millisecond, "ready once the webhook server serves")

	cancel()
	if err := <-stopped; err != nil {
		t.Fatalf("webhook server stopped with an error: %v", err)
	}
	if err := probes.ready(); err == nil {
		t.Fatal("the manager is ready after its webhook server stopped serving")
	}
}
