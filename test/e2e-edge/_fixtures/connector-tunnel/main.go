// connect-proxy is a stand-in for the real connector tunnel. It exercises the
// proxy-side CONNECT wiring and the 503<->200 liveness swap, but not real tunnel
// establishment or NAT traversal.
//
// When a connector is online, the extension server points its backend at a path
// that issues an HTTP CONNECT toward the connector's target, which in the test
// is this pod's Service. On a CONNECT, this proxy dials the configured upstream
// (the echo backend) and blindly splices bytes both ways — a minimal forward
// proxy.
//
// Liveness is driven entirely by the connector's annotation on the control plane
// (see _steps/flip-connector-liveness.yaml); this proxy is always up. It exists
// so an online request can only succeed via the tunnel, never via a direct
// fallback route — which is what makes the 503->200 transition meaningful.
package main

import (
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	listen := envOr("LISTEN_ADDR", ":8080")
	// Where CONNECT requests are forwarded. Default to the echo backend Service.
	// The proxy ignores the CONNECT target host and always dials this upstream,
	// so the test controls the destination via UPSTREAM_ADDR rather than the
	// host the proxy sends.
	upstream := envOr("UPSTREAM_ADDR", "echo-backend.default.svc.cluster.local:8080")

	srv := &http.Server{
		Addr:        listen,
		ReadTimeout: 0, // tunnels are long-lived; no read deadline on the hijacked conn
		Handler:     &proxy{upstream: upstream},
	}
	log.Printf("connect-proxy listening on %s, forwarding CONNECT -> %s", listen, upstream)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}

type proxy struct {
	upstream string
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		// A plain GET is handy as a liveness/readiness probe.
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "connect-proxy ready\n")
		return
	}

	dst, err := net.DialTimeout("tcp", p.upstream, 10*time.Second)
	if err != nil {
		log.Printf("CONNECT %s: dial upstream %s failed: %v", r.Host, p.upstream, err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer dst.Close()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		log.Printf("CONNECT %s: hijack failed: %v", r.Host, err)
		return
	}
	defer client.Close()

	// Tell the client the tunnel is established, then splice bytes both ways.
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		log.Printf("CONNECT %s: write 200 failed: %v", r.Host, err)
		return
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(dst, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, dst); done <- struct{}{} }()
	<-done
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
