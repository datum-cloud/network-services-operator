package mutate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
)

// fixedNow is the reference "now" used across the table; certs are minted
// relative to it so expiry is deterministic.
var fixedNow = time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)

// mintCert returns PEM-encoded (cert, key) for an ECDSA leaf valid in
// [notBefore, notAfter). The returned pair is internally consistent (key matches
// cert) unless mismatchKey replaces the key with an unrelated one.
func mintCert(t *testing.T, cn string, notBefore, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// validPair mints a cert valid around fixedNow.
func validPair(t *testing.T, cn string) (certPEM, keyPEM []byte) {
	t.Helper()
	return mintCert(t, cn, fixedNow.Add(-24*time.Hour), fixedNow.Add(24*time.Hour))
}

// expiredPair mints a cert that expired before fixedNow.
func expiredPair(t *testing.T, cn string) (certPEM, keyPEM []byte) {
	t.Helper()
	return mintCert(t, cn, fixedNow.Add(-48*time.Hour), fixedNow.Add(-24*time.Hour))
}

// tlsSecret builds a *tlsv3.Secret carrying inline cert/key bytes.
func tlsSecret(name string, certPEM, keyPEM []byte) *tlsv3.Secret {
	return &tlsv3.Secret{
		Name: name,
		Type: &tlsv3.Secret_TlsCertificate{
			TlsCertificate: &tlsv3.TlsCertificate{
				CertificateChain: &corev3.DataSource{
					Specifier: &corev3.DataSource_InlineBytes{InlineBytes: certPEM},
				},
				PrivateKey: &corev3.DataSource{
					Specifier: &corev3.DataSource_InlineBytes{InlineBytes: keyPEM},
				},
			},
		},
	}
}

// tlsChain builds a part of a listener that serves the named certificate for
// the given hostname.
func tlsChain(t *testing.T, sni, secretName string) *listenerv3.FilterChain {
	t.Helper()
	dtc := &tlsv3.DownstreamTlsContext{
		CommonTlsContext: &tlsv3.CommonTlsContext{
			TlsCertificateSdsSecretConfigs: []*tlsv3.SdsSecretConfig{
				{Name: secretName},
			},
		},
	}
	dtcAny, err := anypb.New(dtc)
	require.NoError(t, err)
	return &listenerv3.FilterChain{
		Name:             "fc-" + sni,
		FilterChainMatch: &listenerv3.FilterChainMatch{ServerNames: []string{sni}},
		TransportSocket: &corev3.TransportSocket{
			Name:       "envoy.transport_sockets.tls",
			ConfigType: &corev3.TransportSocket_TypedConfig{TypedConfig: dtcAny},
		},
	}
}

// plainChain builds a part of a listener that serves no certificate.
func plainChain(name string) *listenerv3.FilterChain {
	return &listenerv3.FilterChain{Name: name}
}

func listener(chains ...*listenerv3.FilterChain) *listenerv3.Listener {
	return &listenerv3.Listener{Name: "https", FilterChains: chains}
}

func chainNames(l *listenerv3.Listener) []string {
	out := make([]string, 0, len(l.GetFilterChains()))
	for _, fc := range l.GetFilterChains() {
		out = append(out, fc.GetName())
	}
	return out
}

func secretNames(secs []*tlsv3.Secret) []string {
	out := make([]string, 0, len(secs))
	for _, s := range secs {
		out = append(out, s.GetName())
	}
	return out
}

func TestPruneInvalidTLSSecrets_ExpiredChainDropped(t *testing.T) {
	goodCert, goodKey := validPair(t, "good.example.com")
	badCert, badKey := expiredPair(t, "bad.example.com")

	secrets := []*tlsv3.Secret{
		tlsSecret("good-secret", goodCert, goodKey),
		tlsSecret("bad-secret", badCert, badKey),
	}
	l := listener(tlsChain(t, "good.example.com", "good-secret"),
		tlsChain(t, "bad.example.com", "bad-secret"),
	)
	listeners := []*listenerv3.Listener{l}

	kept, prunedChains, prunedSecrets, intact, dropped :=
		PruneInvalidTLSSecrets(listeners, secrets, fixedNow)

	assert.Equal(t, 1, prunedChains)
	assert.Equal(t, 1, prunedSecrets)
	assert.Equal(t, 0, intact)
	assert.Equal(t, []string{"bad.example.com"}, dropped)

	// Good chain kept, listener still valid (non-empty).
	assert.Equal(t, []string{"fc-good.example.com"}, chainNames(l))
	// Bad secret removed from kept, good secret retained.
	assert.ElementsMatch(t, []string{"good-secret"}, secretNames(kept))
}

func TestPruneInvalidTLSSecrets_MismatchedKeyChainDropped(t *testing.T) {
	goodCert, goodKey := validPair(t, "good.example.com")
	// Mismatch: a valid cert paired with an unrelated valid key.
	mismatchCert, _ := validPair(t, "bad.example.com")
	_, otherKey := validPair(t, "other.example.com")

	secrets := []*tlsv3.Secret{
		tlsSecret("good-secret", goodCert, goodKey),
		tlsSecret("mismatch-secret", mismatchCert, otherKey),
	}
	l := listener(tlsChain(t, "good.example.com", "good-secret"),
		tlsChain(t, "bad.example.com", "mismatch-secret"),
	)

	kept, prunedChains, prunedSecrets, intact, dropped :=
		PruneInvalidTLSSecrets([]*listenerv3.Listener{l}, secrets, fixedNow)

	assert.Equal(t, 1, prunedChains)
	assert.Equal(t, 1, prunedSecrets)
	assert.Equal(t, 0, intact)
	assert.Equal(t, []string{"bad.example.com"}, dropped)
	assert.Equal(t, []string{"fc-good.example.com"}, chainNames(l))
	assert.ElementsMatch(t, []string{"good-secret"}, secretNames(kept))
}

func TestPruneInvalidTLSSecrets_AllBadListenerLeftIntact(t *testing.T) {
	bad1Cert, bad1Key := expiredPair(t, "bad1.example.com")
	bad2Cert, bad2Key := expiredPair(t, "bad2.example.com")

	secrets := []*tlsv3.Secret{
		tlsSecret("bad1-secret", bad1Cert, bad1Key),
		tlsSecret("bad2-secret", bad2Cert, bad2Key),
	}
	l := listener(tlsChain(t, "bad1.example.com", "bad1-secret"),
		tlsChain(t, "bad2.example.com", "bad2-secret"),
	)

	kept, prunedChains, prunedSecrets, intact, _ :=
		PruneInvalidTLSSecrets([]*listenerv3.Listener{l}, secrets, fixedNow)

	// Listener left INTACT (not emptied), nothing pruned from an emitted listener.
	assert.Equal(t, 0, prunedChains)
	assert.Equal(t, 0, prunedSecrets)
	assert.Equal(t, 1, intact)
	// Both chains still present — never emitted empty.
	assert.Equal(t, []string{"fc-bad1.example.com", "fc-bad2.example.com"}, chainNames(l))
	// Bad secrets retained because their (intact) listener still references them.
	assert.ElementsMatch(t, []string{"bad1-secret", "bad2-secret"}, secretNames(kept))
}

func TestPruneInvalidTLSSecrets_NonTLSChainNeverDropped(t *testing.T) {
	goodCert, goodKey := validPair(t, "good.example.com")
	badCert, badKey := expiredPair(t, "bad.example.com")

	secrets := []*tlsv3.Secret{
		tlsSecret("good-secret", goodCert, goodKey),
		tlsSecret("bad-secret", badCert, badKey),
	}
	// A non-TLS/default chain alongside one good and one bad TLS chain.
	l := listener(plainChain("tcp-passthrough"),
		tlsChain(t, "good.example.com", "good-secret"),
		tlsChain(t, "bad.example.com", "bad-secret"),
	)

	_, prunedChains, _, intact, _ :=
		PruneInvalidTLSSecrets([]*listenerv3.Listener{l}, secrets, fixedNow)

	assert.Equal(t, 1, prunedChains)
	assert.Equal(t, 0, intact)
	// Only the bad TLS chain removed; non-TLS chain and good TLS chain remain.
	assert.Equal(t, []string{"tcp-passthrough", "fc-good.example.com"}, chainNames(l))
}

func TestPruneInvalidTLSSecrets_GoodOnlyNoOp(t *testing.T) {
	good1Cert, good1Key := validPair(t, "a.example.com")
	good2Cert, good2Key := validPair(t, "b.example.com")

	secrets := []*tlsv3.Secret{
		tlsSecret("a-secret", good1Cert, good1Key),
		tlsSecret("b-secret", good2Cert, good2Key),
	}
	l := listener(tlsChain(t, "a.example.com", "a-secret"),
		tlsChain(t, "b.example.com", "b-secret"),
	)

	kept, prunedChains, prunedSecrets, intact, dropped :=
		PruneInvalidTLSSecrets([]*listenerv3.Listener{l}, secrets, fixedNow)

	assert.Equal(t, 0, prunedChains)
	assert.Equal(t, 0, prunedSecrets)
	assert.Equal(t, 0, intact)
	assert.Nil(t, dropped)
	// Same secrets passed through (identity of the fast path).
	assert.ElementsMatch(t, []string{"a-secret", "b-secret"}, secretNames(kept))
	assert.Len(t, kept, 2)
	// Both chains untouched.
	assert.Equal(t, []string{"fc-a.example.com", "fc-b.example.com"}, chainNames(l))
}

func TestPruneInvalidTLSSecrets_NonTLSSecretIgnored(t *testing.T) {
	// A secret with no TlsCertificate (e.g. a validation context) is never bad.
	vctxSecret := &tlsv3.Secret{
		Name: "ca-secret",
		Type: &tlsv3.Secret_ValidationContext{
			ValidationContext: &tlsv3.CertificateValidationContext{},
		},
	}
	goodCert, goodKey := validPair(t, "good.example.com")
	secrets := []*tlsv3.Secret{
		vctxSecret,
		tlsSecret("good-secret", goodCert, goodKey),
	}
	l := listener(tlsChain(t, "good.example.com", "good-secret"))

	kept, prunedChains, _, intact, _ :=
		PruneInvalidTLSSecrets([]*listenerv3.Listener{l}, secrets, fixedNow)

	assert.Equal(t, 0, prunedChains)
	assert.Equal(t, 0, intact)
	assert.ElementsMatch(t, []string{"ca-secret", "good-secret"}, secretNames(kept))
}

func TestPruneInvalidTLSSecrets_NotYetValidDropped(t *testing.T) {
	goodCert, goodKey := validPair(t, "good.example.com")
	// Not-yet-valid: starts after fixedNow.
	futureCert, futureKey := mintCert(t, "future.example.com",
		fixedNow.Add(24*time.Hour), fixedNow.Add(48*time.Hour))

	secrets := []*tlsv3.Secret{
		tlsSecret("good-secret", goodCert, goodKey),
		tlsSecret("future-secret", futureCert, futureKey),
	}
	l := listener(tlsChain(t, "good.example.com", "good-secret"),
		tlsChain(t, "future.example.com", "future-secret"),
	)

	_, prunedChains, prunedSecrets, _, dropped :=
		PruneInvalidTLSSecrets([]*listenerv3.Listener{l}, secrets, fixedNow)

	assert.Equal(t, 1, prunedChains)
	assert.Equal(t, 1, prunedSecrets)
	assert.Equal(t, []string{"future.example.com"}, dropped)
	assert.Equal(t, []string{"fc-good.example.com"}, chainNames(l))
}
