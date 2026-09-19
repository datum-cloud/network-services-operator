// Package assets holds static content compiled into the extension server.
//
// The default branded 5xx error page is embedded here so the extension server
// always has a valid page to serve, even when no override ConfigMap is mounted.
// The canonical content is expected to come from a mounted ConfigMap (managed in
// the infrastructure GitOps repo); this embed is the always-valid fallback.
package assets

import _ "embed"

// DefaultError5xxHTML is the compiled-in branded HTML served for edge-generated
// 5xx responses when no override page is mounted. Keep it small and
// self-contained (inline CSS, no external assets) so it renders standalone.
//
//go:embed error-5xx-default.html
var DefaultError5xxHTML string

// DefaultErrorOfflineHTML is the compiled-in branded HTML served when the edge
// has no healthy upstream to send a request to, which is what a NetworkService
// with no serving members looks like from the data plane. Same fallback rules
// as DefaultError5xxHTML.
//
//go:embed error-offline-default.html
var DefaultErrorOfflineHTML string
