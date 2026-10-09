// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

const (
	internalDNSFinalizer = "networking.datumapis.com/internal-dns-access"

	InternalDNSContextNameAnnotation        = "networking.datumapis.com/internal-dns-context-name"
	InternalDNSContextUIDAnnotation         = "networking.datumapis.com/internal-dns-context-uid"
	InternalDNSAccessNameAnnotation         = "networking.datumapis.com/internal-dns-access-name"
	InternalDNSAccessUIDAnnotation          = "networking.datumapis.com/internal-dns-access-uid"
	InternalDNSWriterEpochAnnotation        = "networking.datumapis.com/internal-dns-writer-epoch"
	InternalDNSSequenceAnnotation           = "networking.datumapis.com/internal-dns-sequence"
	InternalDNSValidUntilAnnotation         = "networking.datumapis.com/internal-dns-valid-until"
	InternalDNSServiceDestinationAnnotation = "networking.datumapis.com/internal-dns-service-destination"
	InternalDNSSuffixAnnotation             = "networking.datumapis.com/internal-dns-suffix"
	InternalDNSRegionAnnotation             = "networking.datumapis.com/internal-dns-region"

	internalDNSOwnerNetworkUIDAnnotation = "networking.datumapis.com/internal-dns-network-uid"
	internalDNSOwnerProjectUIDAnnotation = "networking.datumapis.com/internal-dns-project-uid"

	internalDNSRegionTopologyKey = "topology.datum.net/region"
	internalDNSRenewBefore       = 45 * time.Second
	internalDNSRetryInterval     = 10 * time.Second
)

var (
	internalDNSContextGVK = schema.GroupVersionKind{
		Group: "dns.networking.miloapis.com", Version: "v1alpha1", Kind: "DNSResolverContext",
	}
	internalDNSAccessGVK = schema.GroupVersionKind{
		Group: "dns.networking.miloapis.com", Version: "v1alpha1", Kind: "DNSResolverAccessBinding",
	}
)

type ProjectIdentityResolver interface {
	ProjectUID(context.Context, string) (types.UID, error)
}

type APIProjectIdentityResolver struct {
	Reader client.Reader
}

func (r *APIProjectIdentityResolver) ProjectUID(ctx context.Context, name string) (types.UID, error) {
	project := &unstructured.Unstructured{}
	project.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "resourcemanager.miloapis.com", Version: "v1alpha1", Kind: "Project",
	})
	if err := r.Reader.Get(ctx, client.ObjectKey{Name: name}, project); err != nil {
		return "", err
	}
	if project.GetUID() == "" {
		return "", fmt.Errorf("project %q has no UID", name)
	}
	return project.GetUID(), nil
}

type InternalDNSProjectReconciler struct {
	Config   config.InternalDNSConfig
	Projects ProjectIdentityResolver
	Now      func() time.Time

	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks,verbs=get;list;watch
// +kubebuilder:rbac:groups=locations.miloapis.com,resources=locations,verbs=get;list;watch
// +kubebuilder:rbac:groups=dns.networking.miloapis.com,resources=dnsresolvercontexts;dnsresolveraccessbindings,verbs=get;list;watch;create;update;patch;delete

func (r *InternalDNSProjectReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	ctx = mccontext.WithCluster(ctx, req.ClusterName)
	return r.reconcile(ctx, cl.GetClient(), string(req.ClusterName), req.NamespacedName)
}

func (r *InternalDNSProjectReconciler) reconcile(
	ctx context.Context,
	cl client.Client,
	project string,
	key client.ObjectKey,
) (ctrl.Result, error) {
	networkContext := &networkingv1alpha.NetworkContext{}
	if err := cl.Get(ctx, key, networkContext); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	region, err := r.region(ctx, cl, networkContext)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !networkContext.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(networkContext, internalDNSFinalizer) {
			if err := r.cleanupRegion(ctx, cl, networkContext); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(networkContext, internalDNSFinalizer)
			if err := cl.Update(ctx, networkContext); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}
	previousRegion := networkContext.Annotations[InternalDNSRegionAnnotation]
	if previousRegion != "" && previousRegion != region {
		if err := r.cleanupRegion(ctx, cl, networkContext); err != nil {
			return ctrl.Result{}, err
		}
	}
	if previousRegion != region {
		patch := client.MergeFrom(networkContext.DeepCopy())
		if networkContext.Annotations == nil {
			networkContext.Annotations = map[string]string{}
		}
		if region == "" {
			delete(networkContext.Annotations, InternalDNSRegionAnnotation)
		} else {
			networkContext.Annotations[InternalDNSRegionAnnotation] = region
		}
		if err := cl.Patch(ctx, networkContext, patch); err != nil {
			return ctrl.Result{}, err
		}
	}

	network := &networkingv1alpha.Network{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: key.Namespace, Name: networkContext.Spec.Network.Name}, network); err != nil {
		return ctrl.Result{}, err
	}
	if network.Spec.DNS.Mode == networkingv1alpha.NetworkDNSModeDisabled {
		return ctrl.Result{}, r.disable(ctx, cl, networkContext, network)
	}
	if region == "" {
		return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, r.clearIntent(ctx, cl, networkContext)
	}

	if controllerutil.AddFinalizer(networkContext, internalDNSFinalizer) {
		if err := cl.Update(ctx, networkContext); err != nil {
			return ctrl.Result{}, err
		}
	}

	projectUID, err := r.Projects.ProjectUID(ctx, project)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve project identity: %w", err)
	}
	consumerID := string(projectUID) + "/" + string(network.UID)
	dnsContext, err := r.ensureContext(ctx, cl, network, projectUID, consumerID)
	if err != nil {
		return ctrl.Result{}, err
	}
	epoch, _, _ := unstructured.NestedInt64(dnsContext.Object, "status", "accessWriterEpoch")
	suffix, _, _ := unstructured.NestedString(dnsContext.Object, "status", "managedNamespace", "suffix")
	zoneName, _, _ := unstructured.NestedString(dnsContext.Object, "status", "managedNamespace", "dnsZoneRef", "name")
	zoneUID, _, _ := unstructured.NestedString(dnsContext.Object, "status", "managedNamespace", "dnsZoneRef", "uid")
	if dnsContext.GetUID() == "" || epoch < 1 || suffix == "" || zoneName == "" || zoneUID == "" {
		return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, r.clearIntent(ctx, cl, networkContext)
	}

	destination, err := internalDNSDestination(r.Config.ServicePrefix, dnsContext.GetUID())
	if err != nil {
		return ctrl.Result{}, err
	}
	binding, ready, err := r.ensureAccess(ctx, cl, network, projectUID, dnsContext, region, destination, epoch)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ready {
		return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, r.clearIntent(ctx, cl, networkContext)
	}

	sequence, _, _ := unstructured.NestedInt64(binding.Object, "spec", "authorization", "sequence")
	validUntil, err := objectTime(binding, "spec", "authorization", "validUntil")
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.publishIntent(ctx, cl, networkContext, map[string]string{
		InternalDNSContextNameAnnotation:        dnsContext.GetName(),
		InternalDNSContextUIDAnnotation:         string(dnsContext.GetUID()),
		InternalDNSAccessNameAnnotation:         binding.GetName(),
		InternalDNSAccessUIDAnnotation:          string(binding.GetUID()),
		InternalDNSWriterEpochAnnotation:        strconv.FormatInt(epoch, 10),
		InternalDNSSequenceAnnotation:           strconv.FormatInt(sequence, 10),
		InternalDNSValidUntilAnnotation:         validUntil.UTC().Format(time.RFC3339Nano),
		InternalDNSServiceDestinationAnnotation: destination,
		InternalDNSSuffixAnnotation:             suffix,
		InternalDNSRegionAnnotation:             region,
	}); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: renewalDelay(r.now(), validUntil)}, nil
}

func (r *InternalDNSProjectReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *InternalDNSProjectReconciler) region(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) (string, error) {
	location := &locationsv1alpha1.Location{}
	if err := cl.Get(ctx, client.ObjectKey{Name: networkContext.Spec.Location.Name}, location); err != nil {
		return "", client.IgnoreNotFound(err)
	}
	return location.Spec.Topology[internalDNSRegionTopologyKey], nil
}

func (r *InternalDNSProjectReconciler) ensureContext(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
	projectUID types.UID,
	consumerID string,
) (*unstructured.Unstructured, error) {
	name := internalDNSResourceName(network.Name, network.UID)
	current := newInternalDNSObject(internalDNSContextGVK, network.Namespace, name)
	err := cl.Get(ctx, client.ObjectKeyFromObject(current), current)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		ownedNetwork := current.GetAnnotations()[internalDNSOwnerNetworkUIDAnnotation]
		existingConsumer, _, _ := unstructured.NestedString(current.Object, "spec", "consumerID")
		if ownedNetwork != string(network.UID) || existingConsumer != consumerID {
			return nil, fmt.Errorf("DNSResolverContext %s/%s belongs to another network lifetime", network.Namespace, name)
		}
		return current, nil
	}

	desired := newInternalDNSObject(internalDNSContextGVK, network.Namespace, name)
	desired.SetAnnotations(map[string]string{
		internalDNSOwnerNetworkUIDAnnotation: string(network.UID),
		internalDNSOwnerProjectUIDAnnotation: string(projectUID),
	})
	desired.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: networkingv1alpha.GroupVersion.String(), Kind: "Network", Name: network.Name,
		UID: network.UID, Controller: boolPtr(true), BlockOwnerDeletion: boolPtr(true),
	}})
	desired.Object["spec"] = map[string]any{
		"consumerID":       consumerID,
		"managedNamespace": map[string]any{"enabled": true},
	}
	if err := cl.Create(ctx, desired); err != nil {
		return nil, err
	}
	return desired, nil
}

func (r *InternalDNSProjectReconciler) ensureAccess(
	ctx context.Context,
	cl client.Client,
	network *networkingv1alpha.Network,
	projectUID types.UID,
	dnsContext *unstructured.Unstructured,
	region string,
	destination string,
	epoch int64,
) (*unstructured.Unstructured, bool, error) {
	name := internalDNSRegionalName(dnsContext.GetName(), region)
	current := newInternalDNSObject(internalDNSAccessGVK, network.Namespace, name)
	err := cl.Get(ctx, client.ObjectKeyFromObject(current), current)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, false, err
	}
	now := r.now()
	sequence := int64(1)
	if err == nil {
		if current.GetAnnotations()[internalDNSOwnerNetworkUIDAnnotation] != string(network.UID) {
			return nil, false, fmt.Errorf("DNSResolverAccessBinding %s/%s belongs to another network lifetime", network.Namespace, name)
		}
		if accessBindingMatches(current, dnsContext, region, destination, epoch) {
			sequence, _, _ = unstructured.NestedInt64(current.Object, "spec", "authorization", "sequence")
			validUntil, _ := objectTime(current, "spec", "authorization", "validUntil")
			if bindingReady(current, sequence) && validUntil.After(now.Add(internalDNSRenewBefore)) {
				return current, true, nil
			}
			if validUntil.After(now) && !bindingReady(current, sequence) {
				return current, false, nil
			}
			sequence++
		} else {
			currentEpoch, _, _ := unstructured.NestedInt64(current.Object, "spec", "authorization", "writerEpoch")
			currentContextUID, _, _ := unstructured.NestedString(current.Object, "spec", "contextRef", "uid")
			if currentContextUID == string(dnsContext.GetUID()) && currentEpoch > epoch {
				return nil, false, fmt.Errorf("DNSResolverAccessBinding %s/%s has writer epoch %d ahead of context epoch %d",
					network.Namespace, name, currentEpoch, epoch)
			}
		}
	}

	validUntil := metav1.NewTime(now.Add(r.Config.AccessLeaseDuration.Duration).UTC())
	desiredSpec := map[string]any{
		"contextRef":    map[string]any{"name": dnsContext.GetName(), "uid": string(dnsContext.GetUID())},
		"region":        region,
		"queryIdentity": map[string]any{"type": "DestinationAddress", "value": destination},
		"port":          int64(53),
		"transports":    []any{"UDP", "TCP"},
		"authorization": map[string]any{
			"writerEpoch": epoch,
			"sequence":    sequence,
			"validUntil":  validUntil.Format(time.RFC3339Nano),
		},
	}
	annotations := current.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[internalDNSOwnerNetworkUIDAnnotation] = string(network.UID)
	annotations[internalDNSOwnerProjectUIDAnnotation] = string(projectUID)
	annotations[InternalDNSContextUIDAnnotation] = string(dnsContext.GetUID())
	annotations[InternalDNSRegionAnnotation] = region
	current.SetAnnotations(annotations)
	current.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: internalDNSContextGVK.GroupVersion().String(), Kind: internalDNSContextGVK.Kind,
		Name: dnsContext.GetName(), UID: dnsContext.GetUID(), Controller: boolPtr(true), BlockOwnerDeletion: boolPtr(true),
	}})
	if apierrors.IsNotFound(err) {
		current.Object["spec"] = desiredSpec
		if err := cl.Create(ctx, current); err != nil {
			return nil, false, err
		}
		return current, false, nil
	}
	if err := unstructured.SetNestedMap(current.Object, desiredSpec, "spec"); err != nil {
		return nil, false, err
	}
	if err := cl.Update(ctx, current); err != nil {
		return nil, false, err
	}
	return current, false, nil
}

func accessBindingMatches(
	binding *unstructured.Unstructured,
	dnsContext *unstructured.Unstructured,
	region string,
	destination string,
	epoch int64,
) bool {
	contextName, _, _ := unstructured.NestedString(binding.Object, "spec", "contextRef", "name")
	contextUID, _, _ := unstructured.NestedString(binding.Object, "spec", "contextRef", "uid")
	boundRegion, _, _ := unstructured.NestedString(binding.Object, "spec", "region")
	identityType, _, _ := unstructured.NestedString(binding.Object, "spec", "queryIdentity", "type")
	identityValue, _, _ := unstructured.NestedString(binding.Object, "spec", "queryIdentity", "value")
	writerEpoch, _, _ := unstructured.NestedInt64(binding.Object, "spec", "authorization", "writerEpoch")
	return contextName == dnsContext.GetName() && contextUID == string(dnsContext.GetUID()) &&
		boundRegion == region && identityType == "DestinationAddress" && identityValue == destination && writerEpoch == epoch
}

func (r *InternalDNSProjectReconciler) disable(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
	network *networkingv1alpha.Network,
) error {
	var errs []error
	if err := r.cleanupRegion(ctx, cl, networkContext); err != nil {
		errs = append(errs, err)
	}
	contextObj := newInternalDNSObject(internalDNSContextGVK, network.Namespace, internalDNSResourceName(network.Name, network.UID))
	if err := cl.Delete(ctx, contextObj); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, err)
	}
	if err := r.clearIntent(ctx, cl, networkContext); err != nil {
		errs = append(errs, err)
	}
	if controllerutil.RemoveFinalizer(networkContext, internalDNSFinalizer) {
		if err := cl.Update(ctx, networkContext); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *InternalDNSProjectReconciler) cleanupRegion(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) error {
	region := networkContext.Annotations[InternalDNSRegionAnnotation]
	contextName := networkContext.Annotations[InternalDNSContextNameAnnotation]
	if region == "" || contextName == "" {
		return nil
	}
	var contexts networkingv1alpha.NetworkContextList
	if err := cl.List(ctx, &contexts, client.InNamespace(networkContext.Namespace), client.MatchingLabels{
		networkingv1alpha.NetworkUIDLabel: networkContext.Labels[networkingv1alpha.NetworkUIDLabel],
	}); err != nil {
		return err
	}
	for i := range contexts.Items {
		other := &contexts.Items[i]
		if other.UID == networkContext.UID || !other.DeletionTimestamp.IsZero() {
			continue
		}
		otherRegion := other.Annotations[InternalDNSRegionAnnotation]
		if otherRegion == "" {
			var err error
			otherRegion, err = r.region(ctx, cl, other)
			if err != nil {
				return err
			}
		}
		if otherRegion == region {
			return nil
		}
	}
	binding := newInternalDNSObject(internalDNSAccessGVK, networkContext.Namespace, internalDNSRegionalName(contextName, region))
	if err := cl.Delete(ctx, binding); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (r *InternalDNSProjectReconciler) publishIntent(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
	values map[string]string,
) error {
	patch := client.MergeFrom(networkContext.DeepCopy())
	if networkContext.Annotations == nil {
		networkContext.Annotations = map[string]string{}
	}
	changed := false
	for key, value := range values {
		if networkContext.Annotations[key] != value {
			networkContext.Annotations[key] = value
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return cl.Patch(ctx, networkContext, patch)
}

func (r *InternalDNSProjectReconciler) clearIntent(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) error {
	patch := client.MergeFrom(networkContext.DeepCopy())
	changed := false
	for _, key := range internalDNSIntentAnnotations() {
		if _, ok := networkContext.Annotations[key]; ok {
			delete(networkContext.Annotations, key)
			changed = true
		}
	}
	if networkContext.Spec.DNS != nil {
		networkContext.Spec.DNS = nil
		changed = true
	}
	if !changed {
		return nil
	}
	return cl.Patch(ctx, networkContext, patch)
}

func (r *InternalDNSProjectReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.Projects == nil {
		return errors.New("a project identity resolver is required")
	}
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkContext{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("internaldns-project").
		Complete(r)
}

type InternalDNSReadinessReconciler struct {
	Config   config.InternalDNSConfig
	Projects ProjectClusterResolver
	Now      func() time.Time

	hub client.Client
}

func (r *InternalDNSReadinessReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	hubContext := &networkingv1alpha.NetworkContext{}
	if err := r.hub.Get(ctx, req.NamespacedName, hubContext); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	routing, err := resolveProjectRouting(ctx, r.hub, req.Namespace)
	if err != nil {
		var unresolved *projectUnresolvable
		if errors.As(err, &unresolved) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	projectClient, err := r.Projects.ClientForProject(ctx, routing.project)
	if err != nil {
		return ctrl.Result{}, err
	}
	projectContext := &networkingv1alpha.NetworkContext{}
	key := client.ObjectKey{Namespace: routing.projectNamespace, Name: hubContext.Name}
	if err := projectClient.Get(ctx, key, projectContext); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return r.publish(ctx, projectClient, projectContext, hubContext)
}

func (r *InternalDNSReadinessReconciler) publish(
	ctx context.Context,
	cl client.Client,
	projectContext *networkingv1alpha.NetworkContext,
	hubContext *networkingv1alpha.NetworkContext,
) (ctrl.Result, error) {
	intent, err := internalDNSSettingsFromAnnotations(projectContext.Annotations, r.Config.Frontend)
	if err != nil {
		return ctrl.Result{}, r.updateProject(ctx, cl, projectContext, nil, metav1.Condition{
			Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionFalse,
			Reason: networkingv1alpha.NetworkContextDNSReadyReasonPending, Message: err.Error(),
		})
	}

	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	access := hubContext.Status.DNSAccess
	ready := apimeta.IsStatusConditionTrue(hubContext.Status.Conditions, networkingv1alpha.NetworkContextDNSReady)
	if !ready || !dnsAccessMatches(intent, access) || access == nil || !access.ValidUntil.After(now) {
		err := r.updateProject(ctx, cl, projectContext, nil, metav1.Condition{
			Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionFalse,
			Reason:  networkingv1alpha.NetworkContextDNSReadyReasonNetworkPathNotReady,
			Message: "The current DNS authorization has no complete, unexpired network programming proof",
		})
		return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, err
	}
	intent.ValidUntil = access.ValidUntil
	err = r.updateProject(ctx, cl, projectContext, intent, metav1.Condition{
		Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionTrue,
		Reason:  networkingv1alpha.NetworkContextDNSReadyReasonReady,
		Message: "DNS authorization and private network programming are current",
	})
	return ctrl.Result{RequeueAfter: renewalDelay(now, access.ValidUntil.Time)}, err
}

func (r *InternalDNSReadinessReconciler) updateProject(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
	settings *networkingv1alpha.NetworkDNSSettings,
	condition metav1.Condition,
) error {
	var errs []error
	if !equalDNSSettings(networkContext.Spec.DNS, settings) {
		patch := client.MergeFrom(networkContext.DeepCopy())
		networkContext.Spec.DNS = settings
		if err := cl.Patch(ctx, networkContext, patch); err != nil {
			return err
		}
	}
	condition.ObservedGeneration = networkContext.Generation
	if apimeta.SetStatusCondition(&networkContext.Status.Conditions, condition) {
		if err := cl.Status().Update(ctx, networkContext); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *InternalDNSReadinessReconciler) SetupWithManager(mgr manager.Manager) error {
	if r.Projects == nil {
		return errors.New("a project cluster resolver is required")
	}
	r.hub = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkContext{}).
		Named("internaldns-readiness").
		Complete(r)
}

func internalDNSSettingsFromAnnotations(
	annotations map[string]string,
	frontend string,
) (*networkingv1alpha.NetworkDNSSettings, error) {
	contextName := annotations[InternalDNSContextNameAnnotation]
	contextUID := annotations[InternalDNSContextUIDAnnotation]
	accessName := annotations[InternalDNSAccessNameAnnotation]
	accessUID := annotations[InternalDNSAccessUIDAnnotation]
	epoch, epochErr := strconv.ParseInt(annotations[InternalDNSWriterEpochAnnotation], 10, 64)
	sequence, sequenceErr := strconv.ParseInt(annotations[InternalDNSSequenceAnnotation], 10, 64)
	validUntil, timeErr := time.Parse(time.RFC3339Nano, annotations[InternalDNSValidUntilAnnotation])
	if contextName == "" || contextUID == "" || accessName == "" || accessUID == "" ||
		epochErr != nil || sequenceErr != nil || timeErr != nil {
		return nil, errors.New("DNS access intent is incomplete")
	}
	settings := &networkingv1alpha.NetworkDNSSettings{
		ContextRef:       networkingv1alpha.NetworkDNSObjectReference{Name: contextName, UID: types.UID(contextUID)},
		AccessBindingRef: networkingv1alpha.NetworkDNSObjectReference{Name: accessName, UID: types.UID(accessUID)},
		WriterEpoch:      epoch,
		Sequence:         sequence,
		ValidUntil:       metav1.NewTime(validUntil),
		Nameservers:      []string{frontend},
	}
	if suffix := annotations[InternalDNSSuffixAnnotation]; suffix != "" {
		settings.Searches = []string{suffix}
	}
	return settings, nil
}

func dnsAccessMatches(settings *networkingv1alpha.NetworkDNSSettings, access *networkingv1alpha.NetworkDNSAccessStatus) bool {
	return settings != nil && access != nil &&
		settings.ContextRef == access.ContextRef &&
		settings.AccessBindingRef == access.AccessBindingRef &&
		settings.WriterEpoch == access.WriterEpoch &&
		settings.Sequence == access.Sequence
}

func equalDNSSettings(a, b *networkingv1alpha.NetworkDNSSettings) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ContextRef == b.ContextRef && a.AccessBindingRef == b.AccessBindingRef &&
		a.WriterEpoch == b.WriterEpoch && a.Sequence == b.Sequence && a.ValidUntil.Equal(&b.ValidUntil) &&
		strings.Join(a.Nameservers, "\x00") == strings.Join(b.Nameservers, "\x00") &&
		strings.Join(a.Searches, "\x00") == strings.Join(b.Searches, "\x00")
}

func internalDNSDestination(prefixText string, uid types.UID) (string, error) {
	prefix, err := netip.ParsePrefix(prefixText)
	if err != nil {
		return "", err
	}
	bytes := prefix.Masked().Addr().As16()
	digest := sha256.Sum256([]byte(uid))
	copy(bytes[8:], digest[:8])
	address := netip.AddrFrom16(bytes)
	if !prefix.Contains(address) {
		return "", fmt.Errorf("allocated DNS destination %s falls outside %s", address, prefix)
	}
	return address.String(), nil
}

func internalDNSResourceName(network string, uid types.UID) string {
	return boundedDNSName("dns-"+network, string(uid))
}

func internalDNSRegionalName(contextName, region string) string {
	return boundedDNSName(contextName+"-"+region, contextName+"/"+region)
}

func boundedDNSName(prefix, identity string) string {
	sum := sha256.Sum256([]byte(identity))
	suffix := hex.EncodeToString(sum[:6])
	maxPrefix := 253 - len(suffix) - 1
	prefix = strings.Trim(prefix, "-")
	if len(prefix) > maxPrefix {
		prefix = strings.TrimRight(prefix[:maxPrefix], "-")
	}
	return prefix + "-" + suffix
}

func bindingReady(binding *unstructured.Unstructured, sequence int64) bool {
	observed, found, _ := unstructured.NestedInt64(binding.Object, "status", "observedSequence")
	bindingUID, _, _ := unstructured.NestedString(binding.Object, "status", "bindingRef", "uid")
	return found && observed == sequence && bindingUID != "" && unstructuredConditionTrue(binding, "Ready")
}

func unstructuredConditionTrue(obj *unstructured.Unstructured, conditionType string) bool {
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, _ := raw.(map[string]any)
		if condition["type"] == conditionType && condition["status"] == "True" {
			return true
		}
	}
	return false
}

func objectTime(obj *unstructured.Unstructured, fields ...string) (time.Time, error) {
	text, found, err := unstructured.NestedString(obj.Object, fields...)
	if err != nil || !found {
		return time.Time{}, fmt.Errorf("%s has no %s", obj.GetName(), strings.Join(fields, "."))
	}
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s has invalid %s: %w", obj.GetName(), strings.Join(fields, "."), err)
	}
	return value, nil
}

func newInternalDNSObject(gvk schema.GroupVersionKind, namespace, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	return obj
}

func renewalDelay(now time.Time, validUntil time.Time) time.Duration {
	delay := validUntil.Sub(now) - internalDNSRenewBefore
	if delay < time.Second {
		return time.Second
	}
	return delay
}

func internalDNSIntentAnnotations() []string {
	return []string{
		InternalDNSContextNameAnnotation,
		InternalDNSContextUIDAnnotation,
		InternalDNSAccessNameAnnotation,
		InternalDNSAccessUIDAnnotation,
		InternalDNSWriterEpochAnnotation,
		InternalDNSSequenceAnnotation,
		InternalDNSValidUntilAnnotation,
		InternalDNSServiceDestinationAnnotation,
		InternalDNSSuffixAnnotation,
	}
}

func boolPtr(value bool) *bool { return &value }
