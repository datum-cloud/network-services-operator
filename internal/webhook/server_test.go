// SPDX-License-Identifier: AGPL-3.0-only

package webhook_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"

	networkingwebhook "go.datum.net/network-services-operator/internal/webhook"
	"go.datum.net/network-services-operator/internal/webhook/webhooktest"
)

func TestServerClosesEachConnectionDuringTheShutdownDelay(t *testing.T) {
	const delay = time.Second
	serving := webhooktest.NewServing(t)
	server := networkingwebhook.NewClusterAwareWebhookServer(
		webhook.NewServer(serving.Options),
		multiclusterproviders.ProviderMilo,
	)
	server.Register("/validate", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := serving.URL("/validate")
	client := serving.HTTP1Client(t)

	shutdown, sigterm := context.WithCancel(t.Context())
	managerCtx, stopManager := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- server.Start(managerCtx) }()
	go server.CloseConnectionsThenStop(shutdown, delay, stopManager)
	webhooktest.WaitFor(t, "the webhook server serves", func(c *assert.CollectT) {
		assert.NoError(c, server.StartedChecker()(nil))
	})

	if resp := post(t, client, url); resp.Close {
		t.Fatal("a response before the signal asks the client to close its connection")
	}

	sigterm()
	signalled := time.Now()
	webhooktest.WaitFor(t, "a response carries Connection: close", func(c *assert.CollectT) {
		assert.True(c, post(c, client, url).Close)
	})
	if took := time.Since(signalled); took >= delay {
		t.Fatalf("the first response with Connection: close came %v after the signal, not in the delay", took)
	}

	if err := <-stopped; err != nil {
		t.Fatalf("webhook server stopped with an error: %v", err)
	}
	if took := time.Since(signalled); took < delay {
		t.Fatalf("webhook server stopped %v after the signal, before the %v delay", took, delay)
	}
	if _, err := client.Post(url, "application/json", strings.NewReader("{}")); err == nil {
		t.Fatal("webhook server still answers after it stopped")
	}
}

func post(t require.TestingT, client *http.Client, url string) *http.Response {
	resp, err := client.Post(url, "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp
}
