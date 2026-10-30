// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	cloudv1alpha1 "go.datum.net/galactic/api/cloud/v1alpha1"
	networkv1alpha1 "go.datum.net/galactic/api/network/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/dnsaccess"
	"go.miloapis.com/locations/pkg/locationidentity"
)

const (
	internalDNSConsumerVPCLabel  = "networking.datumapis.com/internal-dns-vpc-uid"
	internalDNSRouteContextLabel = "networking.datumapis.com/internal-dns-context-uid"
)

// errNoConsumerInterfaces means no interface on the network has an attachment
// yet, so there is no consumer VPC to program.
var errNoConsumerInterfaces = errors.New("no realized interface identifies the consumer VPC lifetime")

var internalDNSServiceProtocols = []networkv1alpha1.NetworkServiceProtocol{
	networkv1alpha1.NetworkServiceProtocolUDP, networkv1alpha1.NetworkServiceProtocolTCP,
}

type InternalDNSCellReconciler struct {
	Location    config.LocationConfig
	localReader client.Reader
	Config      config.InternalDNSConfig
	HubCluster  cluster.Cluster
	Now         func() time.Time

	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts;networkinterfaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=vpcattachments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=serviceendpoints;serviceroutepolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=serviceroutepolicies/status,verbs=get

func (r *InternalDNSCellReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	networkContext := &networkingv1alpha.NetworkContext{}
	if err := cl.GetClient().Get(ctx, req.NamespacedName, networkContext); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return r.reconcile(ctx, cl.GetClient(), networkContext)
}

func (r *InternalDNSCellReconciler) reconcile(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) (ctrl.Result, error) {
	// Legacy broad policies can deliver another location's context. Do not
	// program it or overwrite the owning cell's readiness with NoAttachments.
	identity, err := resolveLocationIdentity(ctx, r.localReader, r.Location)
	if err != nil {
		var unresolved *locationidentity.LocationUnresolved
		if errors.As(err, &unresolved) {
			return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, nil
		}
		return ctrl.Result{}, err
	}
	if identity.Reference.Name != networkContext.Spec.Location.Name {
		return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, nil
	}
	settings, err := internalDNSSettingsFromAnnotations(networkContext.Annotations, r.Config.Nameservers())
	serviceDestination := networkContext.Annotations[InternalDNSServiceDestinationAnnotation]
	region := networkContext.Annotations[InternalDNSRegionAnnotation]
	if err != nil || serviceDestination == "" || region == "" ||
		!networkContext.DeletionTimestamp.IsZero() {
		if cleanupErr := r.cleanup(ctx, cl, networkContext); cleanupErr != nil {
			return ctrl.Result{}, cleanupErr
		}
		return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, r.report(ctx, networkContext, nil, metav1.Condition{
			Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionFalse,
			Reason:  networkingv1alpha.NetworkContextDNSReadyReasonAccessAbsent,
			Message: "No DNS access configuration is available",
		})
	}

	vpc, consumerAttachments, err := r.consumerVPC(ctx, cl, networkContext)
	if errors.Is(err, errNoConsumerInterfaces) {
		return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, r.report(ctx, networkContext, nil, noAttachmentsCondition())
	}
	if err != nil {
		return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, r.report(ctx, networkContext, nil, metav1.Condition{
			Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionUnknown,
			Reason:  networkingv1alpha.NetworkContextDNSReadyReasonNetworkIdentityAbsent,
			Message: err.Error(),
		})
	}
	for _, attachment := range consumerAttachments {
		if attachment.Labels[internalDNSConsumerVPCLabel] == string(vpc.UID) {
			continue
		}
		patch := client.MergeFrom(attachment.DeepCopy())
		if attachment.Labels == nil {
			attachment.Labels = map[string]string{}
		}
		attachment.Labels[internalDNSConsumerVPCLabel] = string(vpc.UID)
		if err := cl.Patch(ctx, attachment, patch); err != nil {
			return ctrl.Result{}, err
		}
	}

	var attachments cloudv1alpha1.VPCAttachmentList
	if err := cl.List(ctx, &attachments); err != nil {
		return ctrl.Result{}, err
	}
	attachmentPointers := make([]*cloudv1alpha1.VPCAttachment, 0, len(attachments.Items))
	for i := range attachments.Items {
		attachmentPointers = append(attachmentPointers, &attachments.Items[i])
	}

	type servicePath struct {
		endpoint *networkv1alpha1.ServiceEndpoint
		policy   *networkv1alpha1.ServiceRoutePolicy
	}
	var paths []servicePath
	current := map[string]bool{}
	for index, resolver := range r.Config.EffectiveResolvers() {
		for _, protocol := range internalDNSServiceProtocols {
			endpoint, policy, err := r.ensureServicePath(
				ctx, cl, networkContext, vpc, index, resolver, protocol, serviceDestination, region,
			)
			if err != nil {
				return ctrl.Result{}, err
			}
			paths = append(paths, servicePath{endpoint: endpoint, policy: policy})
			current[endpoint.Name] = true
		}
	}
	if err := r.deleteServicePaths(ctx, cl, networkContext, current); err != nil {
		return ctrl.Result{}, err
	}

	for _, path := range paths {
		_, ready := policyProgrammed(path.policy, path.endpoint, attachmentPointers, vpc.Status.VPC, r.now())
		if !ready {
			condition := unreadyPathCondition(path.policy, path.endpoint, attachmentPointers, vpc.Status.VPC)
			if err := r.report(ctx, networkContext, nil, condition); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, nil
		}
	}

	access := &networkingv1alpha.NetworkDNSAccessStatus{
		ContextRef:         settings.ContextRef,
		AccessBindingRef:   settings.AccessBindingRef,
		ServiceDestination: serviceDestination,
		Nameservers:        r.Config.Nameservers(),
	}
	if err := r.report(ctx, networkContext, access, metav1.Condition{
		Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionTrue,
		Reason:  networkingv1alpha.NetworkContextDNSReadyReasonReady,
		Message: "DNS configuration and Galactic programming are current",
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: internalDNSRetryInterval}, nil
}

func (r *InternalDNSCellReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *InternalDNSCellReconciler) consumerVPC(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) (*cloudv1alpha1.VPC, []*cloudv1alpha1.VPCAttachment, error) {
	var interfaces networkingv1alpha.NetworkInterfaceList
	if err := cl.List(ctx, &interfaces, client.InNamespace(networkContext.Namespace)); err != nil {
		return nil, nil, err
	}
	attachments := []*cloudv1alpha1.VPCAttachment{}
	var vpc *cloudv1alpha1.VPC
	for i := range interfaces.Items {
		iface := &interfaces.Items[i]
		if iface.Status.NetworkContextRef == nil || iface.Status.NetworkContextRef.Name != networkContext.Name ||
			iface.Status.AttachmentRef == nil || iface.Status.AttachmentRef.APIGroup != "galactic.datumapis.com" ||
			iface.Status.AttachmentRef.Kind != "VPCAttachment" || !iface.DeletionTimestamp.IsZero() {
			continue
		}
		attachment := &cloudv1alpha1.VPCAttachment{}
		key := client.ObjectKey{Namespace: iface.Namespace, Name: iface.Status.AttachmentRef.Name}
		if err := cl.Get(ctx, key, attachment); err != nil {
			return nil, nil, fmt.Errorf("read VPC attachment %s: %w", key, err)
		}
		candidate := &cloudv1alpha1.VPC{}
		vpcKey := client.ObjectKey{Namespace: attachment.Namespace, Name: attachment.Spec.VPC.Name}
		if err := cl.Get(ctx, vpcKey, candidate); err != nil {
			return nil, nil, fmt.Errorf("read consumer VPC %s: %w", vpcKey, err)
		}
		if candidate.UID == "" || candidate.Status.VPC == "" {
			return nil, nil, fmt.Errorf("consumer VPC %s has no current network identity", vpcKey)
		}
		if vpc != nil && vpc.UID != candidate.UID {
			return nil, nil, errors.New("one network context resolves to more than one consumer VPC lifetime")
		}
		vpc = candidate
		attachments = append(attachments, attachment)
	}
	if vpc == nil || len(attachments) == 0 {
		return nil, nil, errNoConsumerInterfaces
	}
	return vpc, attachments, nil
}

func (r *InternalDNSCellReconciler) ensureServicePath(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
	vpc *cloudv1alpha1.VPC,
	index int,
	resolver config.InternalDNSResolver,
	protocol networkv1alpha1.NetworkServiceProtocol,
	destination string,
	region string,
) (*networkv1alpha1.ServiceEndpoint, *networkv1alpha1.ServiceRoutePolicy, error) {
	name := internalDNSServicePathName(networkContext, index, resolver.Frontend, protocol)
	endpoint := &networkv1alpha1.ServiceEndpoint{ObjectMeta: metav1.ObjectMeta{
		Namespace: networkContext.Namespace, Name: name,
	}}
	_, err := controllerutil.CreateOrUpdate(ctx, cl, endpoint, func() error {
		labelInternalDNSRoute(endpoint, networkContext)
		endpoint.Spec = networkv1alpha1.ServiceEndpointSpec{
			ServiceClass: "internal-dns", Address: destination, Port: 53, Protocol: protocol,
			Region: region, DeliveryMode: networkv1alpha1.ServiceEndpointDeliveryModePreferNodeLocal,
			AttachmentSelector: &metav1.LabelSelector{MatchLabels: resolver.ProducerAttachmentSelector},
		}
		return controllerutil.SetControllerReference(networkContext, endpoint, cl.Scheme())
	})
	if err != nil {
		return nil, nil, err
	}

	policy := &networkv1alpha1.ServiceRoutePolicy{ObjectMeta: metav1.ObjectMeta{
		Namespace: networkContext.Namespace, Name: name,
	}}
	_, err = controllerutil.CreateOrUpdate(ctx, cl, policy, func() error {
		labelInternalDNSRoute(policy, networkContext)
		policy.Spec = networkv1alpha1.ServiceRoutePolicySpec{
			ServiceRef:     networkv1alpha1.ServiceEndpointReference{Name: endpoint.Name},
			ConsumerVPCRef: &networkv1alpha1.ServiceRouteVPCReference{Name: vpc.Name, UID: string(vpc.UID)},
			Frontend:       &networkv1alpha1.ServiceRouteFrontend{Address: resolver.Frontend},
			Authorization:  &networkv1alpha1.ServiceRouteAuthorization{Persistent: true},
			AttachmentSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				internalDNSConsumerVPCLabel: string(vpc.UID),
			}},
			ProtocolPorts: []networkv1alpha1.ServiceRouteProtocolPort{{Protocol: protocol, Port: 53}},
			Region:        region,
		}
		return controllerutil.SetControllerReference(networkContext, policy, cl.Scheme())
	})
	if err != nil {
		return nil, nil, err
	}
	return endpoint, policy, nil
}

func internalDNSServicePathName(
	networkContext *networkingv1alpha.NetworkContext,
	index int,
	frontend string,
	protocol networkv1alpha1.NetworkServiceProtocol,
) string {
	identity := string(networkContext.UID) + "/" + string(protocol)
	if index > 0 {
		if address, err := netip.ParseAddr(frontend); err == nil {
			frontend = address.String()
		}
		identity += "/" + frontend
	}
	return boundedDNSName(networkContext.Name+"-dns-"+string(protocol), identity)
}

func labelInternalDNSRoute(obj client.Object, networkContext *networkingv1alpha.NetworkContext) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[internalDNSRouteContextLabel] = string(networkContext.UID)
	obj.SetLabels(labels)
}

func (r *InternalDNSCellReconciler) deleteServicePaths(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
	keep map[string]bool,
) error {
	var errs []error
	inContext := []client.ListOption{
		client.InNamespace(networkContext.Namespace),
		client.MatchingLabels{internalDNSRouteContextLabel: string(networkContext.UID)},
	}
	var policies networkv1alpha1.ServiceRoutePolicyList
	if err := cl.List(ctx, &policies, inContext...); err != nil {
		errs = append(errs, err)
	} else {
		for i := range policies.Items {
			policy := &policies.Items[i]
			if keep[policy.Name] || !metav1.IsControlledBy(policy, networkContext) {
				continue
			}
			if err := deleteOwnedDNSRoute(ctx, cl, policy, networkContext); err != nil {
				errs = append(errs, err)
			}
		}
	}
	var endpoints networkv1alpha1.ServiceEndpointList
	if err := cl.List(ctx, &endpoints, inContext...); err != nil {
		errs = append(errs, err)
	} else {
		for i := range endpoints.Items {
			endpoint := &endpoints.Items[i]
			if keep[endpoint.Name] || !metav1.IsControlledBy(endpoint, networkContext) {
				continue
			}
			if err := deleteOwnedDNSRoute(ctx, cl, endpoint, networkContext); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// unreadyPathCondition explains why a DNS service path is not ready. With
// nothing attached there is no path to acknowledge, so it says that instead
// of reporting a programming failure.
func unreadyPathCondition(
	policy *networkv1alpha1.ServiceRoutePolicy,
	endpoint *networkv1alpha1.ServiceEndpoint,
	attachments []*cloudv1alpha1.VPCAttachment,
	consumerVPCIdentity string,
) metav1.Condition {
	if _, err := dnsaccess.ProgrammingPlan(policy, endpoint, attachments, consumerVPCIdentity); errors.Is(err, dnsaccess.ErrNoEligibleConsumers) {
		return noAttachmentsCondition()
	}
	return metav1.Condition{
		Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionUnknown,
		Reason:  networkingv1alpha.NetworkContextDNSReadyReasonNetworkPathNotReady,
		Message: "Galactic has not acknowledged the current DNS service path on every required node",
	}
}

func noAttachmentsCondition() metav1.Condition {
	return metav1.Condition{
		Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionFalse,
		Reason:  networkingv1alpha.NetworkContextDNSReadyReasonNoAttachments,
		Message: "Nothing is attached to this network in this location",
	}
}

func policyProgrammed(
	policy *networkv1alpha1.ServiceRoutePolicy,
	endpoint *networkv1alpha1.ServiceEndpoint,
	attachments []*cloudv1alpha1.VPCAttachment,
	consumerVPCIdentity string,
	now time.Time,
) (time.Time, bool) {
	if policy.UID == "" || endpoint.UID == "" || policy.Status.ObservedGeneration != policy.Generation ||
		!apimeta.IsStatusConditionTrue(policy.Status.Conditions, networkv1alpha1.ConditionTypeServiceRouteAccepted) ||
		policy.Spec.Authorization == nil || !policy.Spec.Authorization.Persistent {
		return time.Time{}, false
	}
	plan, err := dnsaccess.ProgrammingPlan(policy, endpoint, attachments, consumerVPCIdentity)
	if err != nil {
		return time.Time{}, false
	}
	validUntil := now.Add(time.Minute)
	for _, node := range plan.Nodes {
		matched := 0
		for _, report := range policy.Status.Nodes {
			if report.NodeName != node {
				continue
			}
			matched++
			if !report.Ready || report.PolicyUID != string(policy.UID) ||
				report.ObservedGeneration != policy.Generation || report.InputDigest != plan.InputDigest ||
				!report.ValidUntil.After(now) {
				return time.Time{}, false
			}
			validUntil = minTime(validUntil, report.ValidUntil.Time)
		}
		if matched != 1 {
			return time.Time{}, false
		}
	}
	return validUntil, true
}

func (r *InternalDNSCellReconciler) report(
	ctx context.Context,
	networkContext *networkingv1alpha.NetworkContext,
	access *networkingv1alpha.NetworkDNSAccessStatus,
	condition metav1.Condition,
) error {
	hub := r.HubCluster.GetClient()
	current := &networkingv1alpha.NetworkContext{}
	key := client.ObjectKeyFromObject(networkContext)
	if err := hub.Get(ctx, key, current); err != nil {
		return client.IgnoreNotFound(err)
	}
	patch := client.MergeFrom(current.DeepCopy())
	if access != nil || condition.Status != metav1.ConditionUnknown {
		current.Status.DNSAccess = access
	}
	condition.ObservedGeneration = current.Generation
	apimeta.SetStatusCondition(&current.Status.Conditions, condition)
	return hub.Status().Patch(ctx, current, patch)
}

func (r *InternalDNSCellReconciler) cleanup(
	ctx context.Context,
	cl client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) error {
	var errs []error
	var interfaces networkingv1alpha.NetworkInterfaceList
	if err := cl.List(ctx, &interfaces, client.InNamespace(networkContext.Namespace)); err != nil {
		errs = append(errs, err)
	} else {
		for i := range interfaces.Items {
			iface := &interfaces.Items[i]
			if iface.Status.NetworkContextRef == nil || iface.Status.NetworkContextRef.Name != networkContext.Name ||
				iface.Status.AttachmentRef == nil || iface.Status.AttachmentRef.APIGroup != "galactic.datumapis.com" ||
				iface.Status.AttachmentRef.Kind != "VPCAttachment" {
				continue
			}
			attachment := &cloudv1alpha1.VPCAttachment{}
			key := client.ObjectKey{Namespace: iface.Namespace, Name: iface.Status.AttachmentRef.Name}
			if err := cl.Get(ctx, key, attachment); err != nil {
				if !apierrors.IsNotFound(err) {
					errs = append(errs, err)
				}
				continue
			}
			if _, present := attachment.Labels[internalDNSConsumerVPCLabel]; !present {
				continue
			}
			patch := client.MergeFrom(attachment.DeepCopy())
			delete(attachment.Labels, internalDNSConsumerVPCLabel)
			if err := cl.Patch(ctx, attachment, patch); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, protocol := range internalDNSServiceProtocols {
		name := internalDNSServicePathName(networkContext, 0, "", protocol)
		policy := &networkv1alpha1.ServiceRoutePolicy{ObjectMeta: metav1.ObjectMeta{
			Namespace: networkContext.Namespace, Name: name,
		}}
		if err := deleteOwnedDNSRoute(ctx, cl, policy, networkContext); err != nil {
			errs = append(errs, err)
		}
		endpoint := &networkv1alpha1.ServiceEndpoint{ObjectMeta: metav1.ObjectMeta{
			Namespace: networkContext.Namespace, Name: name,
		}}
		if err := deleteOwnedDNSRoute(ctx, cl, endpoint, networkContext); err != nil {
			errs = append(errs, err)
		}
	}
	if err := r.deleteServicePaths(ctx, cl, networkContext, nil); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func deleteOwnedDNSRoute(ctx context.Context, cl client.Client, obj client.Object, owner *networkingv1alpha.NetworkContext) error {
	if err := cl.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(obj, owner) {
		return fmt.Errorf("DNS route %s/%s belongs to another context lifetime", obj.GetNamespace(), obj.GetName())
	}
	uid, version := obj.GetUID(), obj.GetResourceVersion()
	return client.IgnoreNotFound(cl.Delete(ctx, obj, client.Preconditions{UID: &uid, ResourceVersion: &version}))
}

func (r *InternalDNSCellReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.HubCluster == nil {
		return errors.New("a federation hub cluster is required")
	}
	r.mgr = mgr
	r.localReader = mgr.GetLocalManager().GetClient()
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkContext{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("internaldns-cell").
		Complete(r)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
