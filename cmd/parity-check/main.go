// Command parity-check is the command-line wrapper around the parity package. It
// reads the configuration the extension server intended to program, reads the
// proxy's live configuration, compares them, prints the result as JSON, and
// exits non-zero on any mismatch.
//
// The proxy and extension server can be reached either by a direct URL or, when
// their containers ship without any shell or HTTP client, by forwarding a port
// to the pod and fetching through it from this process. The flags below select
// which.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.datum.net/network-services-operator/test/parity"
)

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "parity-check: %v\n", err)
		os.Exit(2) // 2 = could not fetch or parse; 1 = mismatch found.
	}
}

type config struct {
	corazaFilter string
	timeout      time.Duration

	adminURL string
	admin    execTarget

	extURL string
	ext    execTarget
	// extSelector matches all extension server replicas. Only one replica
	// actually builds configuration at a time, so the check must query every
	// replica and use the authoritative one. Mutually exclusive with
	// --ext-exec-pod / --ext-url.
	extSelector string

	// expectMinBuildID, when set, requires the build count to be at least this
	// value, proving a fresh build happened. Capture the count before a change,
	// then require a higher one after.
	expectMinBuildID uint64
	jsonOut          bool

	// printBuildID, when true, resolves the authoritative replica and prints only
	// its build count, then exits without comparing anything. Used to capture the
	// count before a change and to wait for configuration to settle.
	printBuildID bool
}

type execTarget struct {
	pod       string
	namespace string
	container string
	kubeCtx   string
}

func (e execTarget) set() bool { return e.pod != "" }

func parseFlags() config {
	var c config
	flag.StringVar(&c.corazaFilter, "coraza-filter", "coraza-waf",
		"Coraza HTTP filter name (identifies WAF HCM injection)")
	flag.DurationVar(&c.timeout, "timeout", 30*time.Second, "overall fetch timeout")

	flag.StringVar(&c.adminURL, "admin-url", "",
		"Envoy admin base URL (e.g. http://127.0.0.1:19000); use this OR --admin-exec-pod")
	flag.StringVar(&c.admin.pod, "admin-exec-pod", "", "proxy pod to kubectl-exec into for admin access")
	flag.StringVar(&c.admin.namespace, "admin-exec-namespace", "envoy-gateway-system", "namespace of the proxy pod")
	flag.StringVar(&c.admin.container, "admin-exec-container", "envoy", "container in the proxy pod")
	flag.StringVar(&c.admin.kubeCtx, "admin-exec-context", "", "kubeconfig context for admin exec (optional)")

	flag.StringVar(&c.extURL, "ext-url", "",
		"ext-server health base URL (e.g. http://127.0.0.1:8080); use this OR --ext-exec-pod / --ext-exec-selector")
	flag.StringVar(&c.ext.pod, "ext-exec-pod", "", "single ext-server pod to kubectl-exec into")
	flag.StringVar(&c.extSelector, "ext-exec-selector", "",
		"label selector for ALL ext-server replicas; picks the authoritative one (preferred over --ext-exec-pod)")
	flag.StringVar(&c.ext.namespace, "ext-exec-namespace", "", "namespace of the ext-server pod(s)")
	flag.StringVar(&c.ext.container, "ext-exec-container", "", "container in the ext-server pod")
	flag.StringVar(&c.ext.kubeCtx, "ext-exec-context", "", "kubeconfig context for ext exec (optional)")

	flag.Uint64Var(&c.expectMinBuildID, "expect-min-build-id", 0, "require programmed-set BuildID >= this (STALE oracle)")
	flag.BoolVar(&c.jsonOut, "json", true, "emit the ParityReport as JSON")
	flag.BoolVar(&c.printBuildID, "print-build-id", false,
		"resolve the authoritative ext-server replica and print ONLY its BuildID, then exit")
	flag.Parse()
	return c
}

func run(c config) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	// Resolve the authoritative replica and emit only its build count, without
	// fetching or comparing the proxy's configuration.
	if c.printBuildID {
		expected, _, err := resolveExpected(ctx, c)
		if err != nil {
			return err
		}
		fmt.Printf("%d\n", expected.BuildID)
		return nil
	}

	adminFetcher, err := buildFetcher(c.adminURL, c.admin, true)
	if err != nil {
		return fmt.Errorf("admin fetcher: %w", err)
	}

	// Resolve the intended configuration and the fetcher for the authoritative
	// replica. With a selector this queries every replica and picks the active
	// one; otherwise it uses the single configured endpoint.
	expected, extFetcher, err := resolveExpected(ctx, c)
	if err != nil {
		return err
	}
	if c.expectMinBuildID > 0 && expected.BuildID < c.expectMinBuildID {
		return fmt.Errorf("STALE: programmed-set BuildID %d < required %d (ext-server did not re-translate)",
			expected.BuildID, c.expectMinBuildID)
	}

	droppedSecrets := expected.Keys[parity.FamilyTLSPrune]
	actual, _, err := parity.FetchActual(ctx, adminFetcher, extFetcher, c.corazaFilter, droppedSecrets)
	if err != nil {
		return err
	}

	report := parity.Compare(expected, actual)

	if c.jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	}
	fmt.Fprintln(os.Stderr, report.String())

	if !report.OK() {
		os.Exit(1) // mismatch found — distinct from a fetch or parse failure (exit 2).
	}
	return nil
}

// resolveExpected returns the intended configuration and the fetcher for the
// authoritative replica. With a selector it queries every replica and picks the
// active one; otherwise it uses the single configured endpoint. The chosen
// fetcher is reused later so other signals also come from the active replica.
func resolveExpected(ctx context.Context, c config) (parity.Expected, parity.Fetcher, error) {
	if c.extSelector != "" {
		pods, err := resolvePods(ctx, c.ext.kubeCtx, c.ext.namespace, c.extSelector)
		if err != nil {
			return parity.Expected{}, nil, fmt.Errorf("resolve ext-server pods: %w", err)
		}
		if len(pods) == 0 {
			return parity.Expected{}, nil, fmt.Errorf(
				"no ext-server pods matched selector %q in namespace %q", c.extSelector, c.ext.namespace)
		}
		fetchers := make(map[string]parity.Fetcher, len(pods))
		for _, pod := range pods {
			et := c.ext
			et.pod = pod
			fetchers[pod] = &pfFetcher{target: et, remotePort: "8080"}
		}
		src, perReplicaErrs, err := parity.FetchExpectedFromMany(ctx, fetchers)
		if err != nil {
			return parity.Expected{}, nil, err
		}
		// Report per-replica fetch errors and the chosen replica for diagnosis;
		// they are not fatal as long as one replica answered.
		for pod, e := range perReplicaErrs {
			fmt.Fprintf(os.Stderr, "parity-check: ext replica %s unreachable: %v\n", pod, e)
		}
		fmt.Fprintf(os.Stderr, "parity-check: authoritative ext replica %s (BuildID %d) of %d\n",
			src.Replica, src.Expected.BuildID, len(pods))
		return src.Expected, fetchers[src.Replica], nil
	}

	// Single-endpoint fallback.
	extFetcher, err := buildFetcher(c.extURL, c.ext, false)
	if err != nil {
		return parity.Expected{}, nil, fmt.Errorf("ext fetcher: %w", err)
	}
	exp, err := parity.FetchExpected(ctx, extFetcher)
	if err != nil {
		return parity.Expected{}, nil, err
	}
	return exp, extFetcher, nil
}

// resolvePods returns the names of the running pods matching selector, used to
// enumerate the extension server replicas.
func resolvePods(ctx context.Context, kubeCtx, namespace, selector string) ([]string, error) {
	args := []string{}
	if kubeCtx != "" {
		args = append(args, "--context", kubeCtx)
	}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "get", "pods", "-l", selector,
		"--field-selector=status.phase=Running",
		"-o", "jsonpath={.items[*].metadata.name}")

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get pods -l %s: %w: %s", selector, err, strings.TrimSpace(stderr.String()))
	}
	return strings.Fields(string(out)), nil
}

// buildFetcher returns a direct HTTP fetcher when a URL is given, otherwise a
// port-forward fetcher to the given pod. admin selects which remote port to
// reach. Port-forwarding is used rather than running a command inside the pod
// because the containers ship without a shell or HTTP client.
func buildFetcher(url string, et execTarget, admin bool) (parity.Fetcher, error) {
	switch {
	case url != "":
		return parity.HTTPFetcher{BaseURL: url}, nil
	case et.set():
		port := "8080"
		if admin {
			port = "19000"
		}
		return &pfFetcher{target: et, remotePort: port}, nil
	default:
		return nil, fmt.Errorf("either a URL or an exec pod must be provided")
	}
}

// pfFetcher fetches a path by forwarding a local port to the pod and making the
// request from this process. The forward is set up and torn down per call, on a
// fresh local port each time so concurrent fetches don't collide.
type pfFetcher struct {
	target     execTarget
	remotePort string
}

func (p *pfFetcher) Fetch(ctx context.Context, path string) ([]byte, error) {
	localPort, err := freeLocalPort()
	if err != nil {
		return nil, fmt.Errorf("allocate local port: %w", err)
	}

	args := []string{}
	if p.target.kubeCtx != "" {
		args = append(args, "--context", p.target.kubeCtx)
	}
	if p.target.namespace != "" {
		args = append(args, "-n", p.target.namespace)
	}
	args = append(args, "port-forward", "pod/"+p.target.pod,
		fmt.Sprintf("%d:%s", localPort, p.remotePort))

	// The forward must outlive the single request but be torn down right after.
	pfCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(pfCtx, "kubectl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start port-forward: %w", err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()

	// Wait until the forward is listening before fetching.
	if err := waitForwardReady(pfCtx, stdout); err != nil {
		return nil, fmt.Errorf("port-forward not ready: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	url := fmt.Sprintf("http://127.0.0.1:%d%s", localPort, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s via port-forward: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return body, nil
}

// freeLocalPort asks the operating system for an unused port. Another process
// could claim it in the brief gap before we bind, but that window is small and
// the caller retries.
func freeLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitForwardReady blocks until the forward reports that its local socket is
// listening, or the context is done.
func waitForwardReady(ctx context.Context, stdout io.Reader) error {
	ready := make(chan struct{}, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "Forwarding from") {
				ready <- struct{}{}
				return
			}
		}
	}()
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(15 * time.Second):
		return fmt.Errorf("timed out waiting for port-forward")
	}
}
