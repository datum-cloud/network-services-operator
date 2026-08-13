// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"

	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// Milo exports no constant for a kind name, so the project kind is spelled
// here.
const ipamParentType = "Project"

// IPAMClientFactory returns a client bound to one project. Every IPAM request
// goes through one, so no request can reach IPAM without naming a project.
type IPAMClientFactory interface {
	ClientForProject(project string) (client.Client, error)
}

// NewIPAMClientFactory builds project-scoped clients from one connection. The
// clients are uncached, because a cache would watch every project served.
func NewIPAMClientFactory(base *rest.Config, scheme *runtime.Scheme, actAsUsername string) (IPAMClientFactory, error) {
	if base == nil {
		return nil, fmt.Errorf("a rest config is required")
	}
	if actAsUsername == "" {
		return nil, fmt.Errorf("an impersonation username is required")
	}
	return &impersonatingIPAMClientFactory{
		base:          base,
		scheme:        scheme,
		actAsUsername: actAsUsername,
		clients:       map[string]client.Client{},
	}, nil
}

type impersonatingIPAMClientFactory struct {
	base          *rest.Config
	scheme        *runtime.Scheme
	actAsUsername string

	mu      sync.Mutex
	clients map[string]client.Client
}

func (f *impersonatingIPAMClientFactory) ClientForProject(project string) (client.Client, error) {
	if project == "" {
		return nil, errNoProject
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if existing, ok := f.clients[project]; ok {
		return existing, nil
	}

	cfg := rest.CopyConfig(f.base)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: f.actAsUsername,
		Extra: map[string][]string{
			iamv1alpha1.ParentAPIGroupExtraKey: {resourcemanagerv1alpha1.GroupVersion.Group},
			iamv1alpha1.ParentKindExtraKey:     {ipamParentType},
			iamv1alpha1.ParentNameExtraKey:     {project},
		},
	}

	cl, err := client.New(cfg, client.Options{Scheme: f.scheme})
	if err != nil {
		return nil, fmt.Errorf("failed building IPAM client for project %q: %w", project, err)
	}

	f.clients[project] = cl
	return cl, nil
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

func ensureProjectNamespace(ctx context.Context, cl client.Client, name string) error {
	var existing corev1.Namespace
	err := cl.Get(ctx, client.ObjectKey{Name: name}, &existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	namespace := &corev1.Namespace{}
	namespace.Name = name
	if err := cl.Create(ctx, namespace); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}
