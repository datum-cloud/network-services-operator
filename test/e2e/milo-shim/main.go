// Command milo-shim stands in for Milo's project control-plane front door so
// the e2e environment can exercise path-scoped IPAM access.
//
// TEST SCAFFOLDING. It is deployed only by Taskfile.test-infra.yml, ships in no
// production overlay, and is not built into the operator image.
//
// In production the operator addresses IPAM at
//
//	/apis/resourcemanager.miloapis.com/v1alpha1/projects/<project>/control-plane/...
//
// and Milo resolves the project from that path, injects the three
// iam.miloapis.com/parent-* extras, and authorizes the operator's own identity
// against the named project. The e2e environment has no Milo: IPAM is an
// aggregated apiserver in a kind cluster and nothing there serves
// resourcemanager.miloapis.com, so the operator's requests 404 at the
// aggregator before IPAM is ever reached.
//
// This process fills that gap and nothing more. It terminates the project
// control-plane path, derives the project from the path segment, and re-issues
// the remainder against the kind apiserver carrying the tenancy that path
// named.
//
// What it deliberately does NOT reproduce: Milo authorizes the CALLER per
// project, whereas this shim asserts a single fixed tenant identity once it has
// established the project. The e2e environment has no per-project RBAC universe
// to authorize against. So these suites prove that a request lands in the
// project its path named — they do not prove the per-project authorization
// model that motivated the path-scoping change.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// The tenant identity the shim asserts downstream. IPAM authorizes through a
// delegated SubjectAccessReview against the host apiserver, so this must be a
// subject with ordinary RBAC of its own — it is bound to nso-ipam-tenant in
// config/dependencies/ipam/overlay/rbac.yaml. That binding is also the shim's
// blast radius: whatever path a caller asks for is re-issued as this user, so
// it can reach nothing the tenant role does not already allow.
const tenantUser = "nso-ipam-agent"

// The project kind, as Milo's IAM extras spell it. Milo exports no constant.
const parentKind = "Project"

// A path this shim serves, e.g.
// /apis/resourcemanager.miloapis.com/v1alpha1/projects/project-alpha/control-plane/apis/...
// The trailing remainder is what gets re-issued; it may be empty.
var projectPath = regexp.MustCompile(
	`^/apis/` + regexp.QuoteMeta(resourcemanagerv1alpha1.GroupVersion.Group) +
		`/v1alpha1/projects/([^/]+)/control-plane(/.*)?$`)

// Project names travel into impersonation headers, so they are held to the
// shape a real project name has rather than passed through.
var projectName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(log); err != nil {
		log.Error("milo-shim exiting", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}

	target, err := url.Parse(cfg.Host)
	if err != nil {
		return fmt.Errorf("parsing apiserver host %q: %w", cfg.Host, err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("building kubernetes client: %w", err)
	}

	s := &shim{
		log:        log,
		cfg:        cfg,
		target:     target,
		authn:      clientset,
		transports: map[string]http.RoundTripper{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/", s)

	srv := &http.Server{
		Addr:              ":8443",
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Info("serving project control-plane paths", "addr", srv.Addr, "upstream", cfg.Host)
	if err := srv.ListenAndServeTLS("/etc/tls/tls.crt", "/etc/tls/tls.key"); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type shim struct {
	log    *slog.Logger
	cfg    *rest.Config
	target *url.URL
	authn  kubernetes.Interface

	mu         sync.Mutex
	transports map[string]http.RoundTripper
}

// transportFor returns a round tripper that carries the shim's own credential
// and asserts one project's tenancy.
//
// The impersonation is left to client-go rather than written as headers here.
// The extra keys contain a "/", which is not a legal HTTP header-name
// character, so client-go escapes them on the way out and the apiserver
// unescapes them; hand-built headers are rejected outright by net/http. Using
// its round tripper also means the shim asserts the tenancy in exactly the
// encoding the operator used to.
func (s *shim) transportFor(project string) (http.RoundTripper, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.transports[project]; ok {
		return existing, nil
	}

	cfg := rest.CopyConfig(s.cfg)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: tenantUser,
		Extra: map[string][]string{
			iamv1alpha1.ParentAPIGroupExtraKey: {resourcemanagerv1alpha1.GroupVersion.Group},
			iamv1alpha1.ParentKindExtraKey:     {parentKind},
			iamv1alpha1.ParentNameExtraKey:     {project},
		},
	}

	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, err
	}
	s.transports[project] = transport
	return transport, nil
}

func (s *shim) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	match := projectPath.FindStringSubmatch(r.URL.Path)
	if match == nil {
		// Only project control-plane paths are served. Anything else would be
		// an unscoped request, which is the thing this shim exists to prevent.
		http.Error(w, "not a project control-plane path", http.StatusNotFound)
		return
	}

	project, remainder := match[1], match[2]
	if !projectName.MatchString(project) {
		http.Error(w, "malformed project name", http.StatusBadRequest)
		return
	}
	if remainder == "" {
		remainder = "/"
	}

	// Milo authenticates before it resolves anything. Requiring the caller to
	// present a token the cluster recognises keeps the shim from being an
	// anonymous relay into the tenant role.
	if err := s.authenticate(r); err != nil {
		s.log.Warn("rejecting unauthenticated caller", "project", project, "error", err)
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	transport, err := s.transportFor(project)
	if err != nil {
		s.log.Error("building transport", "project", project, "error", err)
		http.Error(w, "cannot reach the project's control plane", http.StatusInternalServerError)
		return
	}

	proxy := &httputil.ReverseProxy{
		// Watches and other long-lived reads must not sit in a buffer.
		FlushInterval: -1,
		Transport:     transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = s.target.Scheme
			pr.Out.URL.Host = s.target.Host
			pr.Out.Host = s.target.Host
			pr.Out.URL.Path = remainder

			// Both headers must be absent for the transport above to set them:
			// client-go leaves an Authorization or Impersonate-User header
			// alone if one is already present. Forwarding either would send
			// the CALLER's credential and the CALLER's choice of project,
			// which is exactly what the path is meant to decide.
			pr.Out.Header.Del("Authorization")
			for name := range pr.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "impersonate-") {
					pr.Out.Header.Del(name)
				}
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.log.Error("upstream request failed", "project", project, "path", remainder, "error", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, r)
}

// authenticate confirms the caller holds a token the cluster recognises. It
// does not gate on WHO the caller is: the operator's ServiceAccount holds no
// IPAM grant of its own by design, so an identity check here would have nothing
// to check against.
func (s *shim) authenticate(r *http.Request) error {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return errors.New("no bearer token")
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	review, err := s.authn.AuthenticationV1().TokenReviews().Create(ctx, &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{Token: token},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("token review: %w", err)
	}
	if !review.Status.Authenticated {
		return fmt.Errorf("token not authenticated: %s", review.Status.Error)
	}
	return nil
}
