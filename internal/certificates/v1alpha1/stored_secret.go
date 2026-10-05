// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"crypto/sha256"
	"encoding/hex"

	"k8s.io/apimachinery/pkg/types"
)

// StoredSecretName returns the name of the kubernetes.io/tls Secret holding the
// issued key pair for the TLSCertificate with the given UID. The Secret lives in
// the certificate service's namespace on the service cluster, and its name
// depends on nothing but the UID.
func StoredSecretName(uid types.UID) string {
	sum := sha256.Sum256([]byte(uid))
	return "tc-" + hex.EncodeToString(sum[:16])
}
