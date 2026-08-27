// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"fmt"
	"net/url"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"

	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// IPAMClientFactory returns a client bound to one tenancy. Every IPAM request
// goes through one, so no request can reach IPAM without naming who it is for.
type IPAMClientFactory interface {
	// ClientForProject reaches IPAM on a consumer's behalf, inside their own
	// project. What it allocates is theirs, counts against their quota, and is
	// only unique among their own allocations.
	ClientForProject(project string) (client.Client, error)

	// ClientForPlatform reaches IPAM on the platform's own behalf, for values
	// that must be unique across every consumer and must not be gated on one
	// enabling the address service or draw on their quota.
	//
	// IPAM has no platform tenancy of its own, so today this is one project
	// control plane the platform owns. The seam is here so that when it gains
	// one, nothing above this line changes.
	ClientForPlatform() (client.Client, error)
}

// NewIPAMClientFactory builds tenancy-scoped clients from one connection. The
// clients are uncached, because a cache would watch every project served.
//
// platformProject names the control plane platform-owned allocations are made
// in. Empty means the deployment allocates nothing platform-scoped.
func NewIPAMClientFactory(base *rest.Config, scheme *runtime.Scheme, platformProject string) (IPAMClientFactory, error) {
	if base == nil {
		return nil, fmt.Errorf("a rest config is required")
	}
	return &projectPathIPAMClientFactory{
		base:            base,
		scheme:          scheme,
		platformProject: platformProject,
		clients:         map[string]client.Client{},
	}, nil
}

type projectPathIPAMClientFactory struct {
	base            *rest.Config
	scheme          *runtime.Scheme
	platformProject string

	mu      sync.Mutex
	clients map[string]client.Client
}

func (f *projectPathIPAMClientFactory) ClientForPlatform() (client.Client, error) {
	if f.platformProject == "" {
		return nil, errNoPlatformTenancy
	}
	return f.ClientForProject(f.platformProject)
}

func (f *projectPathIPAMClientFactory) ClientForProject(project string) (client.Client, error) {
	if project == "" {
		return nil, errNoProject
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if existing, ok := f.clients[project]; ok {
		return existing, nil
	}

	cfg, err := f.configForProject(project)
	if err != nil {
		return nil, err
	}

	cl, err := client.New(cfg, client.Options{Scheme: f.scheme})
	if err != nil {
		return nil, fmt.Errorf("failed building IPAM client for project %q: %w", project, err)
	}

	f.clients[project] = cl
	return cl, nil
}

// configForProject addresses the base connection at one project's control
// plane. The path names the project, so Milo authorizes the operator's own
// identity against that project rather than trusting a caller-supplied parent.
// Any path the base host already carries is replaced, not extended.
func (f *projectPathIPAMClientFactory) configForProject(project string) (*rest.Config, error) {
	cfg := rest.CopyConfig(f.base)

	host, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("failed parsing IPAM host %q: %w", cfg.Host, err)
	}
	host.Path = fmt.Sprintf("/apis/%s/v1alpha1/projects/%s/control-plane",
		resourcemanagerv1alpha1.GroupVersion.Group, project)
	cfg.Host = host.String()

	return cfg, nil
}

// IPAMScheme is the scheme a project-scoped IPAM client is built with.
func IPAMScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := ipamv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	return scheme, nil
}

var errNoProject = fmt.Errorf("no project")

// errNoPlatformTenancy says the deployment named no place for the platform to
// allocate what it owns, so it allocates none of it.
var errNoPlatformTenancy = fmt.Errorf("no platform tenancy is configured")

// projectFromNamespace reads the project a namespace belongs to. A namespace
// that names no project resolves to nothing, never to a default.
func projectFromNamespace(ns *corev1.Namespace) (string, error) {
	value, ok := ns.Labels[downstreamclient.UpstreamOwnerClusterNameLabel]
	if !ok || value == "" {
		return "", fmt.Errorf("namespace %q carries no %s label", ns.Name, downstreamclient.UpstreamOwnerClusterNameLabel)
	}

	project := downstreamclient.UpstreamClusterNameFromLabel(value)
	if project == "" {
		return "", fmt.Errorf("namespace %q has %s=%q, which names no project", ns.Name, downstreamclient.UpstreamOwnerClusterNameLabel, value)
	}

	return project, nil
}

func projectNamespaceFromNamespace(ns *corev1.Namespace) (string, error) {
	value, ok := ns.Labels[downstreamclient.UpstreamOwnerNamespaceLabel]
	if !ok || value == "" {
		return "", fmt.Errorf("namespace %q carries no %s label", ns.Name, downstreamclient.UpstreamOwnerNamespaceLabel)
	}
	return value, nil
}
