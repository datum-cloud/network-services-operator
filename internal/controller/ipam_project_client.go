// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
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

// IPAMClientFactory returns a client bound to one project. Every IPAM request
// goes through one, so no request can reach IPAM without naming a project.
type IPAMClientFactory interface {
	ClientForProject(project string) (client.Client, error)
}

// NewIPAMClientFactory builds project-scoped clients from one connection. The
// clients are uncached, because a cache would watch every project served.
func NewIPAMClientFactory(base *rest.Config, scheme *runtime.Scheme) (IPAMClientFactory, error) {
	if base == nil {
		return nil, fmt.Errorf("a rest config is required")
	}
	return &projectPathIPAMClientFactory{
		base:    base,
		scheme:  scheme,
		clients: map[string]client.Client{},
	}, nil
}

type projectPathIPAMClientFactory struct {
	base   *rest.Config
	scheme *runtime.Scheme

	mu      sync.Mutex
	clients map[string]client.Client
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

// requireProjectNamespace confirms the namespace addresses land in exists. The
// name is the upstream namespace the claim's own objects live in, inside the
// project's control plane, so it is already there; the read only turns a
// missing namespace into a clear failure instead of a rejected claim. The
// operator never creates it, because that would be writing into a customer's
// project on their behalf.
func requireProjectNamespace(ctx context.Context, cl client.Client, name string) error {
	var existing corev1.Namespace
	return cl.Get(ctx, client.ObjectKey{Name: name}, &existing)
}
