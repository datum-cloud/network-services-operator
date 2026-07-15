package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.datum.net/network-services-operator/test/parity"
)

func TestBuildFetcher_URL(t *testing.T) {
	f, err := buildFetcher("http://127.0.0.1:19000", execTarget{}, true)
	require.NoError(t, err)
	h, ok := f.(parity.HTTPFetcher)
	require.True(t, ok)
	assert.Equal(t, "http://127.0.0.1:19000", h.BaseURL)
}

func TestBuildFetcher_PortForward(t *testing.T) {
	f, err := buildFetcher("", execTarget{pod: "envoy-abc", namespace: "envoy-gateway-system", container: "envoy"}, true)
	require.NoError(t, err)
	e, ok := f.(*pfFetcher)
	require.True(t, ok)
	assert.Equal(t, "19000", e.remotePort, "admin fetcher must port-forward the admin port")

	fExt, err := buildFetcher("", execTarget{pod: "ext-abc", namespace: "nso"}, false)
	require.NoError(t, err)
	eExt := fExt.(*pfFetcher)
	assert.Equal(t, "8080", eExt.remotePort, "ext fetcher must port-forward the health port")
}

func TestFreeLocalPort(t *testing.T) {
	p, err := freeLocalPort()
	require.NoError(t, err)
	assert.Greater(t, p, 0)
}

func TestBuildFetcher_NeitherErrors(t *testing.T) {
	_, err := buildFetcher("", execTarget{}, true)
	assert.Error(t, err)
}
