// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	serveReturnTimeout = 10 * time.Second
	// Longer than the certificate watcher's poll interval, which is the
	// fallback when a file event is missed.
	rotationTimeout = 20 * time.Second
)

// TestServesTLSWhenGivenACertificate is the reason the option exists: the
// assistant refuses to send a caller's token over plain HTTP, so a server given
// a certificate has to answer HTTPS and nothing else.
func TestServesTLSWhenGivenACertificate(t *testing.T) {
	opts, roots := writeServingCertificate(t, t.TempDir(), 1)
	addr, stop := startServing(t, opts)
	defer stop()

	resp, err := httpsClient(roots).Get("https://" + addr + "/healthz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, resp.TLS)
	assert.GreaterOrEqual(t, resp.TLS.Version, uint16(tls.VersionTLS12))

	plain, err := http.Get("http://" + addr + "/healthz")
	require.NoError(t, err, "Go answers plaintext on a TLS port with a 400 rather than a reset")
	defer func() { _ = plain.Body.Close() }()
	assert.Equal(t, http.StatusBadRequest, plain.StatusCode, "plain HTTP must not be served on a TLS listener")
}

// TestPicksUpARotatedCertificateWithoutARestart pins why the certificate is
// watched rather than loaded once: the CSI driver renews it in place.
func TestPicksUpARotatedCertificateWithoutARestart(t *testing.T) {
	dir := t.TempDir()
	opts, _ := writeServingCertificate(t, dir, 1)
	addr, stop := startServing(t, opts)
	defer stop()

	_, rotatedRoots := writeServingCertificate(t, dir, 2)

	require.Eventually(t, func() bool {
		resp, err := httpsClient(rotatedRoots).Get("https://" + addr + "/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, rotationTimeout, 100*time.Millisecond, "the rotated certificate was never served")
}

// TestServesPlainHTTPWithoutACertificate keeps local runs and e2e working.
func TestServesPlainHTTPWithoutACertificate(t *testing.T) {
	addr, stop := startServing(t, TLSOptions{})
	defer stop()

	resp, err := http.Get("http://" + addr + "/healthz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Nil(t, resp.TLS)
}

// TestHalfACertificateFailsAtStartup: falling back to plaintext here would
// start a server the assistant silently drops every tool of.
func TestHalfACertificateFailsAtStartup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    TLSOptions
		missing string
	}{
		{"certificate without key", TLSOptions{CertFile: "/tls/tls.crt"}, "--tls-key-file"},
		{"key without certificate", TLSOptions{KeyFile: "/tls/tls.key"}, "--tls-cert-file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Serve(context.Background(), "127.0.0.1:0", tc.opts, nil, nil, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "without "+tc.missing)
		})
	}
}

func TestAnUnreadableCertificateFailsAtStartup(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	missing := filepath.Join(t.TempDir(), "absent")
	err = serve(context.Background(), listener, TLSOptions{CertFile: missing, KeyFile: missing}, http.NotFoundHandler())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading serving certificate")
}

func TestTLSFlagsDefaultFromTheBinarysEnvironment(t *testing.T) {
	t.Setenv("TEST_MCP_TLS_CERT_FILE", "/env/tls.crt")
	t.Setenv("TEST_MCP_TLS_KEY_FILE", "/env/tls.key")

	var fromEnv TLSOptions
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fromEnv.BindFlags(fs, "TEST_MCP")
	require.NoError(t, fs.Parse(nil))
	assert.Equal(t, TLSOptions{CertFile: "/env/tls.crt", KeyFile: "/env/tls.key"}, fromEnv)
	assert.Equal(t, "https", fromEnv.Scheme())

	var fromFlags TLSOptions
	fs = flag.NewFlagSet("test", flag.ContinueOnError)
	fromFlags.BindFlags(fs, "TEST_MCP")
	require.NoError(t, fs.Parse([]string{"--tls-cert-file=/flag/tls.crt", "--tls-key-file=/flag/tls.key"}))
	assert.Equal(t, TLSOptions{CertFile: "/flag/tls.crt", KeyFile: "/flag/tls.key"}, fromFlags)

	assert.Equal(t, "http", TLSOptions{}.Scheme())
}

func startServing(t *testing.T, opts TLSOptions) (addr string, stop func()) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, listener, opts, mux) }()

	return listener.Addr().String(), func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err, "a cancelled context is a clean shutdown")
		case <-time.After(serveReturnTimeout):
			t.Error("serve did not return after its context was cancelled")
		}
	}
}

func httpsClient(roots *x509.CertPool) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			DisableKeepAlives: true,
		},
	}
}

// writeServingCertificate writes a throwaway self-signed certificate for
// 127.0.0.1 into dir, replacing any already there, and returns a pool trusting
// only it.
func writeServingCertificate(t *testing.T, dir string, serial int64) (TLSOptions, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: "mcpserver-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	opts := TLSOptions{CertFile: filepath.Join(dir, "tls.crt"), KeyFile: filepath.Join(dir, "tls.key")}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	require.NoError(t, os.WriteFile(opts.CertFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(opts.KeyFile,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(certPEM))
	return opts, roots
}
