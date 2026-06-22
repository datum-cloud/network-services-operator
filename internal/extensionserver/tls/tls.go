// Package tls provides mTLS helpers for the extension server's gRPC listener.
package tls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	extmetrics "go.datum.net/network-services-operator/internal/extensionserver/metrics"
)

// LoadServerTLSConfig builds a *tls.Config for the gRPC extension server:
//   - Presents the server certificate (certFile/keyFile) to EG clients via
//     GetCertificate, which re-reads the files on each handshake. This ensures
//     cert-manager certificate rotations (which update the mounted Secret) are
//     picked up without a pod restart. Without this, the process would serve the
//     original (possibly expired) cert until manually restarted.
//   - Verifies EG client certificates against clientCAFile using
//     RequireAndVerifyClientCert.
//
// STATE.md contract: the server presents extension-server-tls; EG presents
// extension-server-eg-client-tls signed by the same CA. Expected client
// identity: CN=envoy-gateway.
func LoadServerTLSConfig(certFile, keyFile, clientCAFile string) (*tls.Config, error) {
	// Validate that the cert and key can be loaded at startup to catch
	// misconfiguration immediately rather than on first handshake.
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return nil, fmt.Errorf("validate server keypair (%q, %q): %w", certFile, keyFile, err)
	}

	caData, err := os.ReadFile(clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read client CA %q: %w", clientCAFile, err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caData) {
		return nil, fmt.Errorf("no valid certificates found in %q", clientCAFile)
	}

	// GetCertificate re-reads certFile/keyFile on each TLS handshake.
	// cert-manager rotates the Secret mounted at /tls (updating the files in-place
	// ~30 days before expiry). Using GetCertificate means the running process picks
	// up the rotated cert automatically — no pod restart required.
	//
	// Trade-off: each handshake incurs a disk read. For the extension server this
	// happens only when EG reconnects (rare), so the overhead is negligible.
	//
	// Metrics: nso_extension_tls_reloads_total increments on every successful
	// reload (i.e. every EG reconnection). nso_extension_tls_reload_errors_total
	// increments when LoadX509KeyPair fails — this causes the TLS handshake to
	// fail and is the first signal that a cert-manager rotation is broken (file
	// permission error, corrupt PEM, etc.).
	getCert := func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			extmetrics.TLSReloadErrorsTotal.Inc()
			return nil, fmt.Errorf("reload server keypair (%q, %q): %w", certFile, keyFile, err)
		}
		extmetrics.TLSReloadsTotal.Inc()
		return &cert, nil
	}

	return &tls.Config{
		GetCertificate: getCert,
		ClientAuth:     tls.RequireAndVerifyClientCert,
		ClientCAs:      caPool,
		MinVersion:     tls.VersionTLS13,
	}, nil
}
