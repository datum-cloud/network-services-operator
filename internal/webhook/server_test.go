// SPDX-License-Identifier: AGPL-3.0-only

package webhook_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"

	networkingwebhook "go.datum.net/network-services-operator/internal/webhook"
)

func TestServerClosesEachConnectionDuringTheShutdownDelay(t *testing.T) {
	const delay = time.Second
	install := &envtest.WebhookInstallOptions{LocalServingHost: "127.0.0.1"}
	require.NoError(t, install.PrepWithoutInstalling())
	t.Cleanup(func() { require.NoError(t, install.Cleanup()) })
	server := networkingwebhook.NewClusterAwareWebhookServer(
		webhook.NewServer(webhook.Options{
			Host:    install.LocalServingHost,
			Port:    install.LocalServingPort,
			CertDir: install.LocalServingCertDir,
		}),
		multiclusterproviders.ProviderMilo,
	)
	server.Register("/validate", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := "https://" + net.JoinHostPort(install.LocalServingHost, strconv.Itoa(install.LocalServingPort)) + "/validate"
	client := http1Client(t, install.LocalServingCAData)

	shutdown, sigterm := context.WithCancel(t.Context())
	managerCtx, stopManager := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- server.Start(managerCtx) }()
	go server.CloseConnectionsThenStop(shutdown, delay, stopManager)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, server.StartedChecker()(nil))
	}, 10*time.Second, 20*time.Millisecond, "the webhook server serves")

	if resp := post(t, client, url); resp.Close {
		t.Fatal("a response before the signal asks the client to close its connection")
	}

	sigterm()
	signalled := time.Now()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, post(c, client, url).Close)
	}, 10*time.Second, 20*time.Millisecond, "a response carries Connection: close")
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

// http1Client calls the webhook server over HTTP/1.1 with keep-alive, as the
// API server calls a Service webhook.
func http1Client(t *testing.T, caData []byte) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(caData), "the serving CA parses")
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12},
			TLSNextProto:    map[string]func(string, *tls.Conn) http.RoundTripper{},
		},
	}
}
