// SPDX-License-Identifier: AGPL-3.0-only

package connectorbinding

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

func newKey(t *testing.T, seed byte) (string, ed25519.PrivateKey) {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(bytesOf(seed, ed25519.SeedSize))
	return hex.EncodeToString(priv.Public().(ed25519.PublicKey)), priv
}

func bytesOf(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func sign(priv ed25519.PrivateKey, uid, endpointID string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, Message(uid, endpointID)))
}

func TestMessage(t *testing.T) {
	assert.Equal(t, "datum-connector-binding/v1\nuid-1\nabcd", string(Message("uid-1", "abcd")))
}

func TestVerify(t *testing.T) {
	id, priv := newKey(t, 1)
	otherID, otherPriv := newKey(t, 2)
	const uid = "3b0f6a52-7f4c-4f0e-9a51-2d1c0e6f9a10"
	const otherUID = "9d2e1c40-1b7a-4a3e-8f0d-5c6b7a8e9f01"

	sigForOtherConnector := sign(priv, otherUID, id)
	short := hex.EncodeToString(bytesOf(7, 31))

	tests := []struct {
		name      string
		uid       string
		id        string
		signature string
		want      Verdict
	}{
		{name: "valid", uid: uid, id: id, signature: sign(priv, uid, id), want: Verified},
		{name: "no signature", uid: uid, id: id, signature: "", want: Missing},
		{name: "signature over another uid", uid: uid, id: id, signature: sigForOtherConnector, want: Invalid},
		{name: "signature copied to another connector", uid: otherUID, id: id, signature: sign(priv, uid, id), want: Invalid},
		{name: "signature by another key", uid: uid, id: id, signature: sign(otherPriv, uid, id), want: Invalid},
		{name: "valid signature presented for another endpoint id", uid: uid, id: otherID, signature: sign(priv, uid, id), want: Invalid},
		{name: "signature over another endpoint id string", uid: uid, id: id, signature: sign(priv, uid, otherID), want: Invalid},
		{name: "wrong length key", uid: uid, id: short, signature: sign(priv, uid, short), want: Invalid},
		{name: "non hex endpoint id", uid: uid, id: "not-hex", signature: sign(priv, uid, "not-hex"), want: Invalid},
		{name: "empty endpoint id", uid: uid, id: "", signature: sign(priv, uid, ""), want: Invalid},
		{name: "bad base64", uid: uid, id: id, signature: "!!!not base64!!!", want: Invalid},
		{name: "url base64", uid: uid, id: id, signature: base64.URLEncoding.EncodeToString(bytesOf(0xff, ed25519.SignatureSize)), want: Invalid},
		{name: "short signature", uid: uid, id: id, signature: base64.StdEncoding.EncodeToString(bytesOf(1, 32)), want: Invalid},
		{name: "zero signature", uid: uid, id: id, signature: base64.StdEncoding.EncodeToString(bytesOf(0, ed25519.SignatureSize)), want: Invalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Verify(tt.uid, tt.id, tt.signature))
		})
	}
}

func TestVerifyStatus(t *testing.T) {
	id, priv := newKey(t, 3)
	const uid = "uid-1"
	details := func(binding *networkingv1alpha1.ConnectorKeyBinding) *networkingv1alpha1.ConnectorStatus {
		return &networkingv1alpha1.ConnectorStatus{
			ConnectionDetails: &networkingv1alpha1.ConnectorConnectionDetails{
				Type:      networkingv1alpha1.PublicKeyConnectorConnectionType,
				PublicKey: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{Id: id, Binding: binding},
			},
		}
	}

	tests := []struct {
		name   string
		status *networkingv1alpha1.ConnectorStatus
		want   Verdict
	}{
		{name: "nil status", status: nil, want: Missing},
		{name: "no connection details", status: &networkingv1alpha1.ConnectorStatus{}, want: Missing},
		{name: "no binding", status: details(nil), want: Missing},
		{name: "empty signature", status: details(&networkingv1alpha1.ConnectorKeyBinding{}), want: Missing},
		{name: "valid", status: details(&networkingv1alpha1.ConnectorKeyBinding{Signature: sign(priv, uid, id)}), want: Verified},
		{name: "invalid", status: details(&networkingv1alpha1.ConnectorKeyBinding{Signature: sign(priv, "other", id)}), want: Invalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VerifyStatus(uid, tt.status))
		})
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": Observe, "observe": Observe, "enforce": Enforce} {
		got, err := ParseMode(in)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	_, err := ParseMode("Enforce")
	assert.Error(t, err)
	assert.True(t, Enforce.Enforcing())
	assert.False(t, Observe.Enforcing())
	assert.False(t, Mode("").Enforcing())
}

func TestVerifySharedVector(t *testing.T) {
	const (
		secret    = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
		id        = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
		uid       = "6b0f3c1e-2a4d-4e8b-9c7f-1d2e3f4a5b6c"
		signature = "pmdY3eHUNbxquZQw9swz5kxpfWoe1AnFU484kzICxgVRoO0Yn3NcyTJMc1QNzh+DDSU9UPI8V0T+U5Zk/cfuAg=="
	)

	seed, err := hex.DecodeString(secret)
	require.NoError(t, err)
	assert.Equal(t, id, hex.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)))
	assert.Equal(t, "datum-connector-binding/v1\n"+uid+"\n"+id, string(Message(uid, id)))

	assert.Equal(t, Verified, Verify(uid, id, signature))
	for _, other := range []string{"", "6b0f3c1e-2a4d-4e8b-9c7f-1d2e3f4a5b6d", "6B0F3C1E-2A4D-4E8B-9C7F-1D2E3F4A5B6C", uid + "\n"} {
		assert.Equal(t, Invalid, Verify(other, id, signature), "uid %q", other)
	}
}
