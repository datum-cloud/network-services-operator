package gatewayapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"

	"golang.org/x/net/http2"
	"sigs.k8s.io/gateway-api/conformance/utils/config"
	"sigs.k8s.io/gateway-api/conformance/utils/roundtripper"
	"sigs.k8s.io/gateway-api/conformance/utils/tlog"
)

// NodePortRoundTripper is the default implementation of a RoundTripper. It will
// be used if a custom implementation is not specified.
type NodePortRoundTripper struct {
	Debug         bool
	TimeoutConfig config.TimeoutConfig
	DialContext   func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (d *NodePortRoundTripper) httpTransport(request roundtripper.Request) (http.RoundTripper, error) {
	transport := &http.Transport{
		DialContext: d.DialContext,
		// We disable keep-alives so that we don't leak established TCP connections.
		// Leaking TCP connections is bad because we could eventually hit the
		// threshold of maximum number of open TCP connections to a specific
		// destination. Keep-alives are not presently utilized so disabling this has
		// no adverse affect.
		//
		// Ref. https://github.com/kubernetes-sigs/gateway-api/issues/2357
		DisableKeepAlives: true,
	}
	// In gateway-api conformance v1.5.1, Server/CertPem/KeyPem were renamed:
	//   Server → ServerName, CertPem → ServerCertificate (server CA cert only, no key).
	if request.ServerName != "" && len(request.ServerCertificate) > 0 {
		tlsConfig, err := tlsClientConfig(request.ServerName, request.ServerCertificate)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsConfig
	}

	return transport, nil
}

func (d *NodePortRoundTripper) h2cPriorKnowledgeTransport(request roundtripper.Request) (http.RoundTripper, error) {
	if request.ServerName != "" && len(request.ServerCertificate) > 0 {
		return nil, errors.New("request has configured TLS but h2 prior knowledge is not encrypted")
	}

	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return d.DialContext(ctx, network, addr)
		},
	}

	return transport, nil
}

// CaptureRoundTrip makes a request with the provided parameters and returns the
// captured request and response from echoserver. An error will be returned if
// there is an error running the function but not if an HTTP error status code
// is received.
func (d *NodePortRoundTripper) CaptureRoundTrip(request roundtripper.Request) (*roundtripper.CapturedRequest, *roundtripper.CapturedResponse, error) {
	var transport http.RoundTripper
	var err error

	switch request.Protocol {
	case roundtripper.H2CPriorKnowledgeProtocol:
		transport, err = d.h2cPriorKnowledgeTransport(request)
	default:
		transport, err = d.httpTransport(request)
	}

	if err != nil {
		return nil, nil, err
	}

	return d.defaultRoundTrip(request, transport)
}

func (d *NodePortRoundTripper) defaultRoundTrip(request roundtripper.Request, transport http.RoundTripper) (*roundtripper.CapturedRequest, *roundtripper.CapturedResponse, error) {
	client := &http.Client{}

	if request.UnfollowRedirect {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	client.Transport = transport

	method := "GET"
	if request.Method != "" {
		method = request.Method
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.TimeoutConfig.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, request.URL.String(), nil)
	if err != nil {
		return nil, nil, err
	}

	if request.Host != "" {
		req.Host = request.Host
	}

	if request.Headers != nil {
		for name, value := range request.Headers {
			req.Header.Set(name, value[0])
		}
	}

	if d.Debug {
		var dump []byte
		dump, err = httputil.DumpRequestOut(req, true)
		if err != nil {
			return nil, nil, err
		}

		tlog.Logf(request.T, "Sending Request:\n%s\n\n", formatDump(dump, "< "))
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	// nolint:errcheck
	defer resp.Body.Close()

	if d.Debug {
		var dump []byte
		dump, err = httputil.DumpResponse(resp, true)
		if err != nil {
			return nil, nil, err
		}

		tlog.Logf(request.T, "Received Response:\n%s\n\n", formatDump(dump, "< "))
	}

	cReq := &roundtripper.CapturedRequest{}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	// we cannot assume the response is JSON
	if resp.Header.Get("Content-type") == "application/json" {
		err = json.Unmarshal(body, cReq)
		if err != nil {
			return nil, nil, fmt.Errorf("unexpected error reading response: %w", err)
		}
	} else {
		cReq.Method = method // assume it made the right request if the service being called isn't echoing
	}

	cRes := &roundtripper.CapturedResponse{
		StatusCode:    resp.StatusCode,
		ContentLength: resp.ContentLength,
		Protocol:      resp.Proto,
		Headers:       resp.Header,
	}

	if resp.TLS != nil {
		cRes.PeerCertificates = resp.TLS.PeerCertificates
	}

	if roundtripper.IsRedirect(resp.StatusCode) {
		redirectURL, err := resp.Location()
		if err != nil {
			return nil, nil, err
		}
		cRes.RedirectRequest = &roundtripper.RedirectRequest{
			Scheme: redirectURL.Scheme,
			Host:   redirectURL.Hostname(),
			Port:   redirectURL.Port(),
			Path:   redirectURL.Path,
		}
	}

	return cReq, cRes, nil
}

// tlsClientConfig builds a TLS client config that trusts the given server CA certificate.
// In gateway-api conformance v1.5.1 the Request no longer carries a client key/cert;
// client certificates are injected via Request.GetClientCertificateHook if needed.
func tlsClientConfig(server string, serverCertificate []byte) (*tls.Config, error) {
	if server == "" {
		return nil, fmt.Errorf("unexpected error, server name required for TLS")
	}

	// Add the provided cert as a trusted CA
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(serverCertificate) {
		return nil, fmt.Errorf("unexpected error adding trusted CA from ServerCertificate")
	}

	// Create the tls Config for this provided host and trusted CA.
	// Disable G402: TLS MinVersion too low. (gosec)
	// #nosec G402
	return &tls.Config{
		ServerName: server,
		RootCAs:    certPool,
	}, nil
}

var startLineRegex = regexp.MustCompile(`(?m)^`)

func formatDump(data []byte, prefix string) string {
	data = startLineRegex.ReplaceAllLiteral(data, []byte(prefix))
	return string(data)
}
