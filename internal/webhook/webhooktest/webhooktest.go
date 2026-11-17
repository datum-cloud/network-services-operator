// SPDX-License-Identifier: AGPL-3.0-only

package webhooktest

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

const (
	waitTimeout   = 10 * time.Second
	pollInterval  = 20 * time.Millisecond
	clientTimeout = 5 * time.Second
	servingHost   = "127.0.0.1"
)

type Serving struct {
	Options webhook.Options
	caData  []byte
}

func NewServing(t testing.TB) Serving {
	t.Helper()
	install := &envtest.WebhookInstallOptions{LocalServingHost: servingHost}
	require.NoError(t, install.PrepWithoutInstalling())
	t.Cleanup(func() { require.NoError(t, install.Cleanup()) })
	return Serving{
		Options: webhook.Options{
			Host:    install.LocalServingHost,
			Port:    install.LocalServingPort,
			CertDir: install.LocalServingCertDir,
		},
		caData: install.LocalServingCAData,
	}
}

func (s Serving) URL(path string) string {
	return "https://" + s.Options.Host + ":" + strconv.Itoa(s.Options.Port) + path
}

func (s Serving) HTTP1Client(t testing.TB) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(s.caData), "the serving CA parses")
	return &http.Client{
		Timeout: clientTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12},
			TLSNextProto:    map[string]func(string, *tls.Conn) http.RoundTripper{},
		},
	}
}

func WaitFor(t testing.TB, what string, condition func(c *assert.CollectT)) {
	t.Helper()
	require.EventuallyWithT(t, condition, waitTimeout, pollInterval, "timed out waiting: %s", what)
}
