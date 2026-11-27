// SPDX-License-Identifier: AGPL-3.0-only

// Package connectorbinding verifies that a Connector is bound to the endpoint
// key it advertises: the agent signs the Connector's UID and endpoint ID with
// the endpoint's ed25519 private key.
package connectorbinding

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

const messagePrefix = "datum-connector-binding/v1"

type Verdict string

const (
	Verified Verdict = "verified"
	Missing  Verdict = "missing"
	Invalid  Verdict = "invalid"
)

var Verdicts = []Verdict{Verified, Missing, Invalid}

type Mode string

const (
	Observe Mode = "observe"
	Enforce Mode = "enforce"
)

func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case Observe, Enforce:
		return Mode(s), nil
	case "":
		return Observe, nil
	}
	return "", fmt.Errorf("connector key binding mode must be %q or %q, got %q", Observe, Enforce, s)
}

func (m Mode) Enforcing() bool {
	return m == Enforce
}

func Message(uid, endpointID string) []byte {
	return []byte(messagePrefix + "\n" + uid + "\n" + endpointID)
}

func Verify(uid, endpointID, signature string) Verdict {
	if signature == "" {
		return Missing
	}
	key, err := hex.DecodeString(endpointID)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return Invalid
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Invalid
	}
	if !ed25519.Verify(ed25519.PublicKey(key), Message(uid, endpointID), sig) {
		return Invalid
	}
	return Verified
}

func VerifyStatus(uid string, status *networkingv1alpha1.ConnectorStatus) Verdict {
	if status == nil || status.ConnectionDetails == nil || status.ConnectionDetails.PublicKey == nil {
		return Missing
	}
	pk := status.ConnectionDetails.PublicKey
	if pk.Binding == nil {
		return Missing
	}
	return Verify(uid, pk.Id, pk.Binding.Signature)
}
