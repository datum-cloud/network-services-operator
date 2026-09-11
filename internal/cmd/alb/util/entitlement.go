// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"context"
	"fmt"
	"io"
	"time"

	"go.datum.net/datumctl/plugin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	networkingServiceIdentifier = "networking.datumapis.com"
	networkingServiceRef        = "networking-datumapis-com"
	networkingServiceRefLegacy  = "networking"
)

var networkingServiceRefAliases = map[string]bool{
	networkingServiceRef:       true,
	networkingServiceRefLegacy: true,
}

const (
	entitlementPhaseActive          = "Active"
	entitlementPhasePendingApproval = "PendingApproval"
	entitlementPhaseRejected        = "Rejected"
)

const entitlementWatchTimeout = 15 * time.Second

var serviceEntitlementGVK = schema.GroupVersionKind{
	Group:   "services.miloapis.com",
	Version: "v1alpha1",
	Kind:    "ServiceEntitlement",
}

func bestEntitlementPhase(list *unstructured.UnstructuredList) string {
	rank := map[string]int{
		entitlementPhaseRejected:        1,
		entitlementPhasePendingApproval: 2,
		entitlementPhaseActive:          3,
	}

	best, bestRank := "", 0
	for i := range list.Items {
		item := &list.Items[i]
		if !isNetworkingEntitlement(item) {
			continue
		}
		if phase := entitlementPhase(item); rank[phase] > bestRank {
			best, bestRank = phase, rank[phase]
		}
	}
	return best
}

func isNetworkingEntitlement(obj *unstructured.Unstructured) bool {
	return networkingServiceRefAliases[entitlementServiceRef(obj)]
}

func EnsureNetworkingEntitlement(ctx context.Context, project string, in io.Reader, out io.Writer) error {
	if project == "" {
		return nil
	}

	wc, err := newEntitlementClient(project)
	if err != nil {
		return err
	}

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(serviceEntitlementGVK.GroupVersion().WithKind(serviceEntitlementGVK.Kind + "List"))
	if err := wc.List(ctx, list); err != nil {
		if apimeta.IsNoMatchError(err) {
			return promptAndRequestEntitlement(ctx, project, wc, in, out)
		}
		if classified := ClassifyError(err); classified.Code() != ExitError {
			return classified
		}
		return NewCLIError(ExitUnavailable,
			fmt.Sprintf("checking the networking service entitlement for project %q: %v", project, err)).
			WithFix("verify you are logged in (datumctl login) and the project is reachable.").
			WithCause(err)
	}

	switch bestEntitlementPhase(list) {
	case entitlementPhaseActive:
		return nil
	case entitlementPhasePendingApproval:
		return pendingApprovalErr(project)
	case entitlementPhaseRejected:
		return NewCLIError(ExitForbidden,
			fmt.Sprintf("the networking entitlement request for project %q was rejected", project)).
			WithFix(fmt.Sprintf("submit a new request with:\n       datumctl services enable %s --wait", networkingServiceIdentifier))
	}

	return promptAndRequestEntitlement(ctx, project, wc, in, out)
}

func promptAndRequestEntitlement(ctx context.Context, project string, wc client.WithWatch, in io.Reader, out io.Writer) error {
	if NonInteractive(in) {
		return notEnabledErr(project)
	}

	_, _ = fmt.Fprintf(out, "Networking is not enabled for project %q.\n", project)
	_, _ = fmt.Fprint(out, "Would you like to enable it now? [y/N]: ")

	answer, err := readLine(in)
	if err != nil {
		return err
	}
	if !isAffirmative(answer) {
		return notEnabledErr(project)
	}

	_, _ = fmt.Fprintf(out, "Enabling networking for project %q...\n", project)

	entitlement := newEntitlementObject()
	if err := wc.Create(ctx, entitlement); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			if classified := ClassifyError(err); classified.Code() != ExitError {
				return classified
			}
			return NewCLIError(ExitUnavailable,
				fmt.Sprintf("enabling networking for project %q: %v", project, err)).
				WithCause(err)
		}
	}

	watchCtx, cancel := context.WithTimeout(ctx, entitlementWatchTimeout)
	defer cancel()

	watchList := &unstructured.UnstructuredList{}
	watchList.SetGroupVersionKind(serviceEntitlementGVK.GroupVersion().WithKind(serviceEntitlementGVK.Kind + "List"))
	watcher, err := wc.Watch(watchCtx, watchList)
	if err != nil {
		return pendingApprovalErr(project)
	}
	defer watcher.Stop()

	for {
		select {
		case <-watchCtx.Done():
			_, _ = fmt.Fprintf(out, "\nNetworking for project %q has been requested but is not active yet.\n", project)
			_, _ = fmt.Fprint(out, "Run your command again once it becomes active.\n\n")
			_, _ = fmt.Fprint(out, "Check status with: datumctl services list\n")
			return pendingApprovalErr(project)

		case event, open := <-watcher.ResultChan():
			if !open {
				return pendingApprovalErr(project)
			}
			if event.Type != watch.Modified && event.Type != watch.Added {
				continue
			}
			item, isUnstructured := event.Object.(*unstructured.Unstructured)
			if !isUnstructured || !isNetworkingEntitlement(item) {
				continue
			}
			switch entitlementPhase(item) {
			case entitlementPhaseActive:
				_, _ = fmt.Fprintf(out, "Networking enabled for project %q.\n\n", project)
				return nil
			case entitlementPhaseRejected:
				return NewCLIError(ExitForbidden,
					fmt.Sprintf("the networking entitlement request for project %q was rejected", project)).
					WithFix(fmt.Sprintf("submit a new request with:\n       datumctl services enable %s --wait", networkingServiceIdentifier))
			case entitlementPhasePendingApproval:
				_, _ = fmt.Fprintf(out, "\nNetworking for project %q has been requested but is not active yet.\n", project)
				_, _ = fmt.Fprint(out, "Check status with: datumctl services list\n")
				return pendingApprovalErr(project)
			}
		}
	}
}

func notEnabledErr(project string) *CLIError {
	return NewCLIError(ExitForbidden, fmt.Sprintf("Networking is not enabled for project %q", project)).
		WithFix(fmt.Sprintf("enable it with:\n       datumctl services enable %s --wait", networkingServiceIdentifier))
}

func pendingApprovalErr(project string) *CLIError {
	return NewCLIError(ExitForbidden, fmt.Sprintf("Networking for project %q is not active yet", project)).
		WithFix(fmt.Sprintf("wait for it to activate with:\n       datumctl services enable %s --wait\n"+
			"       or check the status with:\n       datumctl services list", networkingServiceIdentifier))
}

func newEntitlementObject() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(serviceEntitlementGVK)
	obj.SetName(networkingServiceRef)
	_ = unstructured.SetNestedField(obj.Object, networkingServiceRef, "spec", "serviceRef", "name")
	return obj
}

func entitlementServiceRef(obj *unstructured.Unstructured) string {
	name, _, _ := unstructured.NestedString(obj.Object, "spec", "serviceRef", "name")
	return name
}

func entitlementPhase(obj *unstructured.Unstructured) string {
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	return phase
}

func newEntitlementClient(project string) (client.WithWatch, error) {
	pluginCtx := plugin.Context()
	if pluginCtx.APIHost == "" {
		return nil, NewCLIError(ExitUnavailable,
			"cannot check the networking service entitlement: DATUM_API_HOST is not set").
			WithFix("run this through datumctl:\n       datumctl alb ...")
	}

	token, err := plugin.Token()
	if err != nil {
		return nil, NewCLIError(ExitUnavailable, fmt.Sprintf("getting credentials: %v", err)).
			WithFix("re-run `datumctl login` and try again.").
			WithCause(err)
	}

	cfg := &rest.Config{
		Host:            ProjectControlPlaneURL(pluginCtx.APIHost, project),
		BearerToken:     token,
		UserAgent:       UserAgent(),
		TLSClientConfig: tlsClientConfig(),
	}

	wc, err := client.NewWithWatch(cfg, client.Options{})
	if err != nil {
		return nil, NewCLIError(ExitUnavailable, fmt.Sprintf("building entitlement client: %v", err)).WithCause(err)
	}
	return wc, nil
}
