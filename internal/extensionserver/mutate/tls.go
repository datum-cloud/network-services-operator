package mutate

import (
	"crypto/tls"
	"crypto/x509"
	"time"

	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
)

// PruneInvalidTLSSecrets removes only the parts of a listener that use a broken
// certificate, so one customer's bad certificate can't stop the whole listener
// and take HTTPS down for every other hostname on it (issue #212).
//
// It is written so it can never make things worse:
//   - A listener is never left with nothing to serve. If every certificate on a
//     listener is broken, the listener is left exactly as it was and counted in
//     listenersLeftIntact, because handing back an empty listener would stop the
//     listener entirely — the very thing this guards against.
//   - Parts of a listener that don't serve a certificate are never touched.
//   - Anything it can't read is left alone, and it never reports an error, since
//     doing so would stop the update.
//
// now is a parameter so expiry can be tested with fixed times.
//
// It returns the secrets to hand back (the broken ones that were actually
// dropped are removed), how many parts and certificates were dropped, how many
// listeners were left untouched because everything on them was broken, and the
// hostnames that were dropped, for logging. The listeners are edited in place.
func PruneInvalidTLSSecrets(
	listeners []*listenerv3.Listener,
	secrets []*tlsv3.Secret,
	now time.Time,
) (kept []*tlsv3.Secret, prunedChains int, prunedSecrets int, listenersLeftIntact int, droppedNames []string) {
	// Find the broken certificates.
	badSecrets := make(map[string]struct{})
	for _, sec := range secrets {
		name := sec.GetName()
		if name == "" {
			continue
		}
		cert := sec.GetTlsCertificate()
		if cert == nil {
			// Only certificates are checked; leave anything else alone.
			continue
		}
		if !tlsCertificateUsable(cert, now) {
			badSecrets[name] = struct{}{}
		}
	}
	if len(badSecrets) == 0 {
		// Nothing broken, so change nothing.
		return secrets, 0, 0, 0, nil
	}

	// Remember which broken certificates were actually dropped, so only those
	// are removed from the secrets handed back.
	prunedBadNames := make(map[string]struct{})
	for _, l := range listeners {
		chains := l.GetFilterChains()
		if len(chains) == 0 {
			continue
		}

		// Work out which parts of the listener use a broken certificate.
		dropChain := make([]bool, len(chains))
		anyDrop := false
		keptCount := 0
		for i, fc := range chains {
			if chainReferencesBadSecret(fc, badSecrets) {
				dropChain[i] = true
				anyDrop = true
			} else {
				keptCount++
			}
		}
		if !anyDrop {
			continue
		}

		// If everything on the listener is broken, leave it untouched rather
		// than hand back a listener with nothing to serve.
		if keptCount == 0 {
			listenersLeftIntact++
			continue
		}

		// Keep the good parts, in their original order.
		newChains := make([]*listenerv3.FilterChain, 0, keptCount)
		for i, fc := range chains {
			if dropChain[i] {
				prunedChains++
				droppedNames = append(droppedNames, chainDropLabel(fc, badSecrets))
				for _, n := range chainBadSecretNames(fc, badSecrets) {
					prunedBadNames[n] = struct{}{}
				}
				continue
			}
			newChains = append(newChains, fc)
		}
		l.FilterChains = newChains
	}

	// Build kept secrets: input minus the bad secrets actually pruned from an
	// emitted listener. A bad secret still referenced only by an intact listener
	// is retained to preserve referential completeness.
	prunedSecrets = len(prunedBadNames)
	if prunedSecrets == 0 {
		// Bad secrets existed but none were pruned from an emitted listener
		// (e.g. only referenced by listeners left intact). Pass secrets through.
		return secrets, prunedChains, 0, listenersLeftIntact, droppedNames
	}
	kept = make([]*tlsv3.Secret, 0, len(secrets))
	for _, sec := range secrets {
		if _, dropped := prunedBadNames[sec.GetName()]; dropped {
			continue
		}
		kept = append(kept, sec)
	}
	return kept, prunedChains, prunedSecrets, listenersLeftIntact, droppedNames
}

// tlsCertificateUsable reports whether a certificate and its key match and are
// within their valid dates. A certificate we can't read is treated as usable, so
// only certificates we can prove are broken are ever dropped.
func tlsCertificateUsable(cert *tlsv3.TlsCertificate, now time.Time) bool {
	certPEM := cert.GetCertificateChain().GetInlineBytes()
	keyPEM := cert.GetPrivateKey().GetInlineBytes()
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		// Nothing we can read here, so don't touch it.
		return true
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		// The certificate and key don't match, or can't be read.
		return false
	}
	leaf := pair.Leaf
	if leaf == nil {
		if len(pair.Certificate) == 0 {
			return false
		}
		parsed, perr := x509.ParseCertificate(pair.Certificate[0])
		if perr != nil {
			return false
		}
		leaf = parsed
	}
	if now.Before(leaf.NotBefore) {
		return false // Not valid yet.
	}
	if !leaf.NotAfter.After(now) {
		return false // Expired.
	}
	return true
}

// chainReferencesBadSecret reports whether this part of the listener serves one
// of the broken certificates. Parts that serve no certificate are never dropped.
func chainReferencesBadSecret(fc *listenerv3.FilterChain, bad map[string]struct{}) bool {
	for _, n := range chainSDSSecretNames(fc) {
		if _, ok := bad[n]; ok {
			return true
		}
	}
	return false
}

// chainBadSecretNames returns the broken certificates this part of the listener serves.
func chainBadSecretNames(fc *listenerv3.FilterChain, bad map[string]struct{}) []string {
	var out []string
	for _, n := range chainSDSSecretNames(fc) {
		if _, ok := bad[n]; ok {
			out = appendUnique(out, n)
		}
	}
	return out
}

// chainSDSSecretNames returns the certificates this part of the listener serves.
// Anything it can't read returns nothing, so it is never matched or dropped.
func chainSDSSecretNames(fc *listenerv3.FilterChain) []string {
	ts := fc.GetTransportSocket()
	if ts == nil {
		return nil
	}
	tc := ts.GetTypedConfig()
	if tc == nil {
		return nil
	}
	dtc := &tlsv3.DownstreamTlsContext{}
	if err := tc.UnmarshalTo(dtc); err != nil {
		// Not something that serves a certificate; leave it alone.
		return nil
	}
	var names []string
	for _, sds := range dtc.GetCommonTlsContext().GetTlsCertificateSdsSecretConfigs() {
		if n := sds.GetName(); n != "" {
			names = appendUnique(names, n)
		}
	}
	return names
}

// chainDropLabel builds a human-readable label for a dropped chain for logging:
// the chain's SNI server name(s) when present, otherwise the bad SDS secret
// name(s) it referenced.
func chainDropLabel(fc *listenerv3.FilterChain, bad map[string]struct{}) string {
	if sn := fc.GetFilterChainMatch().GetServerNames(); len(sn) > 0 {
		return sn[0]
	}
	if names := chainBadSecretNames(fc, bad); len(names) > 0 {
		return names[0]
	}
	if n := fc.GetName(); n != "" {
		return n
	}
	return "<unknown>"
}
