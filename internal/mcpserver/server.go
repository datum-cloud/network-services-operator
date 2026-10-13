// SPDX-License-Identifier: AGPL-3.0-only

// Package mcpserver is the plumbing shared by this repository's MCP servers:
// reading a caller's identity and project off a request, building a client that
// reads as that caller, refusing a deployment pointed at its own cluster, and
// serving the knowledge and skills a capability document names.
package mcpserver

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

const (
	// ProjectHeader names the project whose control plane a request reads. Set
	// by the caller, not by the model.
	ProjectHeader = "X-Datum-Project"

	readHeaderTimeout = 10 * time.Second

	// misconfiguredClientNote closes every error a request can fail with before
	// a read is attempted. All of them are the calling client's configuration,
	// so the sentence names the actor and rules out the two things the person
	// who asked would otherwise try.
	misconfiguredClientNote = "The person who asked did nothing wrong and re-authenticating will not " +
		"help: this is a configuration problem for whoever operates that client"
)

// CheckControlPlaneEndpoint refuses to start when the configuration resolved to
// the API server of the cluster this process runs in.
//
// In a pod with no kubeconfig, GetConfig falls back to in-cluster config, and
// ClientConfig then hangs a project control-plane path off the local API server.
// Every tool call fails with a 401 that reads like the caller's token is bad
// when the deployment is what is wrong; failing at boot puts the error where the
// mistake is.
//
// The check matches the SHAPE of the mistake, never an address: which control
// plane a deployment reads is deployment configuration, not this repo's.
func CheckControlPlaneEndpoint(cfg *rest.Config, binary string) error {
	local := localClusterEndpoint()
	if local == "" || !sameEndpoint(cfg.Host, local) {
		return nil
	}
	return fmt.Errorf(
		"control plane endpoint %s is this cluster's own API server: %s reads Datum project "+
			"control planes, not the cluster it runs in, so the in-cluster fallback is never "+
			"correct. Give the deployment an explicit control plane: mount a kubeconfig naming the "+
			"control plane's address and CA, and point KUBECONFIG at it (or pass --kubeconfig)",
		cfg.Host, binary)
}

// localClusterEndpoint returns the API server address in-cluster config resolves
// to, or "" outside a pod. It mirrors rest.InClusterConfig's own derivation
// rather than calling it, so the check still holds when the projected
// ServiceAccount token InClusterConfig also requires is not mounted.
func localClusterEndpoint() string {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return ""
	}
	return "https://" + net.JoinHostPort(host, port)
}

func sameEndpoint(a, b string) bool {
	return strings.TrimSuffix(a, "/") == strings.TrimSuffix(b, "/")
}

// CallerConfig returns a function resolving the REST config one request reads
// through. Resolution is deferred to call time so a request carrying no
// credentials fails as a tool error the model can report, rather than as a nil
// client panic deep in a handler.
//
// The project is taken from a header, never from a tool argument: arguments are
// chosen by the model, and a model that could name its own project would be one
// prompt injection away from another tenant's resources.
func CallerConfig(r *http.Request, base *rest.Config, userAgent string) func() (*rest.Config, error) {
	token := BearerToken(r)
	project := strings.TrimSpace(r.Header.Get(ProjectHeader))

	return func() (*rest.Config, error) {
		if token == "" {
			return nil, fmt.Errorf(
				"no credentials on this request: the client that called this tool did not forward "+
					"the user's identity. %s", misconfiguredClientNote)
		}
		if project == "" {
			return nil, fmt.Errorf(
				"no project on this request: the client that called this tool did not set the %s "+
					"header. %s", ProjectHeader, misconfiguredClientNote)
		}
		return ClientConfig(base, token, project, userAgent)
	}
}

func projectControlPlanePath(project string) string {
	return fmt.Sprintf("/apis/resourcemanager.miloapis.com/v1alpha1/projects/%s/control-plane", project)
}

// ClientConfig derives the REST config one request reads through: the caller's
// token, pointed at their project control plane. The base config supplies the
// endpoint and CA only — every credential field it might carry is cleared first,
// so the server's own identity can never leak into a caller's read.
func ClientConfig(base *rest.Config, token, project, userAgent string) (*rest.Config, error) {
	// The project arrives in a header and is interpolated into a URL path, so it
	// is validated before it can reshape that path into another API route.
	if errs := validation.IsDNS1123Subdomain(project); len(errs) > 0 {
		return nil, fmt.Errorf("invalid project %q on the %s header sent by the client that called "+
			"this tool: %s. %s", project, ProjectHeader, strings.Join(errs, "; "), misconfiguredClientNote)
	}

	cfg := rest.AnonymousClientConfig(rest.CopyConfig(base))
	cfg.BearerToken = token
	cfg.BearerTokenFile = ""
	cfg.UserAgent = userAgent

	host, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("parsing control plane host: %w", err)
	}
	host.Path = projectControlPlanePath(project)
	cfg.Host = host.String()

	return cfg, nil
}

// NewClient builds a client reading through cfg, which carries the caller's own
// credentials and their project.
func NewClient(cfg *rest.Config) (client.Client, error) {
	scheme, err := util.NewScheme()
	if err != nil {
		return nil, err
	}
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("building client for caller: %w", err)
	}
	return c, nil
}

// BearerToken extracts a bearer token from the Authorization header.
func BearerToken(r *http.Request) string {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) < len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(prefix):])
}

// Serve answers every URL a capability document names from one process: MCP at
// /mcp, the knowledge and skills from docs, and liveness at /healthz.
//
// The MCP handler is stateless and builds a server per request, bound to that
// caller's identity and project, so nothing is shared between callers.
func Serve(addr string, impl *mcp.Implementation, docs *KnowledgeHandler,
	register func(*mcp.Server, *http.Request)) error {
	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server {
			s := mcp.NewServer(impl, nil)
			register(s, r)
			return s
		},
		&mcp.StreamableHTTPOptions{Stateless: true},
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle(KnowledgePath, docs)
	mux.Handle(RunbookPrefix, docs)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving on %s: %w", addr, err)
	}
	return nil
}

// EnvOr returns the environment variable key, or fallback when it is unset.
func EnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
