// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"crypto/tls"
	"flag"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
)

const (
	tlsCertFileFlag = "tls-cert-file"
	tlsKeyFileFlag  = "tls-key-file"
)

// TLSOptions names the serving certificate. Both paths set serves HTTPS, both
// empty serves plain HTTP, and one without the other is refused: a deployment
// that lost half its certificate must not quietly fall back to plaintext, which
// the assistant will not send a caller's token over.
type TLSOptions struct {
	CertFile string
	KeyFile  string
}

// BindFlags registers --tls-cert-file and --tls-key-file, defaulting each to
// <envPrefix>_TLS_CERT_FILE and <envPrefix>_TLS_KEY_FILE.
func (o *TLSOptions) BindFlags(fs *flag.FlagSet, envPrefix string) {
	fs.StringVar(&o.CertFile, tlsCertFileFlag, EnvOr(envPrefix+"_TLS_CERT_FILE", ""),
		"path to the PEM serving certificate; set together with --"+tlsKeyFileFlag+" to serve HTTPS")
	fs.StringVar(&o.KeyFile, tlsKeyFileFlag, EnvOr(envPrefix+"_TLS_KEY_FILE", ""),
		"path to the PEM private key; set together with --"+tlsCertFileFlag+" to serve HTTPS")
}

// Enabled reports whether the server serves HTTPS.
func (o TLSOptions) Enabled() bool {
	return o.CertFile != "" && o.KeyFile != ""
}

// Scheme is the URL scheme the server answers on.
func (o TLSOptions) Scheme() string {
	if o.Enabled() {
		return "https"
	}
	return "http"
}

// Validate refuses a certificate without its key, or a key without its
// certificate.
func (o TLSOptions) Validate() error {
	if (o.CertFile == "") == (o.KeyFile == "") {
		return nil
	}
	set, missing := tlsCertFileFlag, tlsKeyFileFlag
	if o.CertFile == "" {
		set, missing = tlsKeyFileFlag, tlsCertFileFlag
	}
	return fmt.Errorf("--%s is set without --%s: set both to serve HTTPS, or neither to serve plain HTTP",
		set, missing)
}

// watchedConfig serves whatever certificate is on disk now, not the one read at
// startup: cert-manager's CSI driver rewrites the files in place on renewal,
// and a server that kept the first one would expire with it.
func (o TLSOptions) watchedConfig() (*tls.Config, *certwatcher.CertWatcher, error) {
	watcher, err := certwatcher.New(o.CertFile, o.KeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("loading serving certificate: %w", err)
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: watcher.GetCertificate,
	}, watcher, nil
}
