/*
Copyright © 2026 Datum Technology, Inc. All rights reserved.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	cloudv1alpha1 "go.datum.net/galactic/api/cloud/v1alpha1"
	galacticv1alpha1 "go.datum.net/galactic/api/v1alpha1"
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/galactic/galactic"
	"go.datum.net/network-services-operator/internal/galactic/identifier"
)

const (
	// LabelVPC records the base62 VPC identifier a NAD attaches to.
	LabelVPC = "galactic.datumapis.com/vpc"

	// LabelVPCAttachment records the base62 attachment identifier a NAD holds.
	// The NAD is the allocation record for that identifier.
	LabelVPCAttachment      = "galactic.datumapis.com/vpc-attachment"
	attachmentKind          = "VPCAttachment"
	reasonAPIGroupMigration = "APIGroupMigration"

	// AnnotationHostInterface records the host device this attachment will get,
	// on the NAD, at the moment the NAD is written. The name is derived from the
	// VPC and the attachment identifier, so it is known here — well before the
	// CNI ADD that creates the device. A runtime that must name the device when
	// it asks for an interface, rather than learn it from the ADD result, has
	// nowhere else to read it in time. galactic writes the same key during ADD
	// from the same inputs, so the two always agree.
	AnnotationHostInterface = "k8s.v1.cni.cncf.io/host-interface"

	// ConditionTypePrepared reports that the data plane's pre-Pod artifacts exist.
	// Unlike Programmed, which only becomes true at CNI ADD, it is safe to gate
	// Pod creation on. network-services-operator is adding the type in parallel.
	ConditionTypePrepared = "Prepared"

	consumerLabelPrefix = "compute.datumapis.com/"
)

// maxIdentifierAttempts bounds the probe for an unused identifier.
const maxIdentifierAttempts = 100

// NetworkInterfaceReconciler realizes a fulfilled NetworkInterface claim on the
// galactic data plane.
//
// It creates the VPCAttachment and the NetworkAttachmentDefinition in one pass,
// so it already holds every render input and never has to look sideways. Both
// objects are per-interface, which keeps the attachment identifier and the tap
// device name stable across instance replacement, and it publishes the
// annotations a workload must carry so no infrastructure provider has to know
// what a NAD is.
type NetworkInterfaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// APIReader bypasses the cache when listing allocated identifiers, so a NAD
	// written moments ago cannot be missed and its identifier reissued.
	APIReader client.Reader

	// AttachmentMode is how guests in this cell consume an interface. It is
	// required configuration standing in for a capability class that does not
	// exist yet, so a cell states what it is rather than defaulting. It covers
	// every interface that states no mode of its own.
	AttachmentMode cloudv1alpha1.VPCAttachmentInterfaceMode

	// LabeledNamespaces are the namespaces whose interfaces carry their consumer
	// labels onto their attachments. Attachments elsewhere keep whatever labels
	// they have, so a tenant's claim labels never reach a selector that picks
	// platform producers.
	LabeledNamespaces []string

	// TrafficPolicyEnforced states that the cell's nodes enforce traffic
	// policy, which is the only way a disabled network's interfaces can be
	// reported as blocked.
	TrafficPolicyEnforced bool

	// HypervisorHostOnlyPorts are the TCP ports a discovered hypervisor guest
	// serves only to its node, such as a unikernel's exec agent. Declared
	// guests run their own agent over a channel the network never carries.
	HypervisorHostOnlyPorts []uint16
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaceclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaceclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=vpcattachments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=vpcattachments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=k8s.cni.cncf.io,resources=network-attachment-definitions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts,verbs=get;list;watch
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=trafficpolicies;trafficpolicybindings,verbs=get;list;watch;create;update;patch;delete

func (r *NetworkInterfaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var networkInterface networkingv1alpha.NetworkInterface
	if err := r.Get(ctx, req.NamespacedName, &networkInterface); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !networkInterface.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if !claimFulfilled(&networkInterface) {
		return ctrl.Result{}, r.clearConsumerLabels(ctx, &networkInterface)
	}

	var vpc cloudv1alpha1.VPC
	vpcKey := types.NamespacedName{
		Namespace: networkInterface.Namespace,
		Name:      networkInterface.Status.NetworkContextRef.Name,
	}
	if err := r.Get(ctx, vpcKey, &vpc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, r.markPrepared(ctx, &networkInterface,
				metav1.ConditionFalse, "AwaitingVPC", fmt.Sprintf("VPC %s does not exist yet", vpcKey.Name))
		}
		return ctrl.Result{}, fmt.Errorf("get VPC %s: %w", vpcKey, err)
	}
	if vpc.Status.VPC == "" {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, r.markPrepared(ctx, &networkInterface,
			metav1.ConditionFalse, "AwaitingVPCIdentifier",
			fmt.Sprintf("VPC %s has no identifier yet", vpc.Name))
	}

	var networkContext networkingv1alpha.NetworkContext
	if err := r.Get(ctx, vpcKey, &networkContext); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, r.markPrepared(ctx, &networkInterface,
				metav1.ConditionFalse, "AwaitingNetworkContext",
				fmt.Sprintf("NetworkContext %s does not exist yet", vpcKey.Name))
		}
		return ctrl.Result{}, fmt.Errorf("get NetworkContext %s: %w", vpcKey, err)
	}
	mode := internetEgressMode(&networkContext)

	attachment, err := r.reconcileAttachment(ctx, &networkInterface, &vpc)
	if err != nil {
		return ctrl.Result{}, err
	}
	policies, err := r.reconcileTrafficPolicy(ctx, &networkInterface, attachment, mode)
	if err != nil {
		return ctrl.Result{}, err
	}
	nad, err := r.reconcileNAD(ctx, attachment, &vpc, &networkInterface)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.publishAttachmentStatus(ctx, attachment, &vpc, nad); err != nil {
		return ctrl.Result{}, err
	}
	r.setInternetEgressCondition(&networkInterface, attachment, policies, mode)
	return ctrl.Result{}, r.publishToInterface(ctx, &networkInterface, attachment, &vpc)
}

// claimFulfilled reports whether an interface is bound to a claim and holds
// every address it must carry. Nothing can be rendered before that.
func claimFulfilled(networkInterface *networkingv1alpha.NetworkInterface) bool {
	if networkInterface.Status.Phase != networkingv1alpha.NetworkInterfacePhaseBound {
		return false
	}
	if networkInterface.Status.NetworkContextRef == nil {
		return false
	}
	return meta.IsStatusConditionTrue(networkInterface.Status.Conditions,
		networkingv1alpha.NetworkInterfaceAllocated)
}

// reconcileAttachment creates the VPCAttachment for an interface. The controller
// owns this object, not the infrastructure provider: it is the only component
// that speaks both the workload vocabulary and the data plane's.
func (r *NetworkInterfaceReconciler) reconcileAttachment(
	ctx context.Context, networkInterface *networkingv1alpha.NetworkInterface, vpc *cloudv1alpha1.VPC,
) (*cloudv1alpha1.VPCAttachment, error) {
	attachment := &cloudv1alpha1.VPCAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: networkInterface.Name, Namespace: networkInterface.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, attachment, func() error {
		if slices.Contains(r.LabeledNamespaces, networkInterface.Namespace) {
			attachment.Labels = withConsumerLabels(attachment.Labels, networkInterface.Labels)
		}
		attachment.Spec.VPC = cloudv1alpha1.VPCRef{Name: vpc.Name}
		attachment.Spec.InterfaceRef = &cloudv1alpha1.NetworkInterfaceRef{Name: networkInterface.Name}
		attachment.Spec.Interface.Name = networkInterface.Spec.InterfaceName
		attachment.Spec.Interface.Mode = r.attachmentMode(networkInterface)
		attachment.Spec.Interface.Addresses = interfaceAddresses(networkInterface)
		return controllerutil.SetControllerReference(networkInterface, attachment, r.Scheme)
	}); err != nil {
		return nil, fmt.Errorf("reconcile VPC attachment %s: %w", attachment.Name, err)
	}
	return attachment, nil
}

// clearConsumerLabels deselects the attachment of an interface no claim holds,
// such as one a Retain policy kept after its Pod went away. Otherwise a
// replacement Pod's attachment and the retained one would both match the same
// producer selector on one node.
func (r *NetworkInterfaceReconciler) clearConsumerLabels(
	ctx context.Context, networkInterface *networkingv1alpha.NetworkInterface,
) error {
	if networkInterface.Status.Phase == networkingv1alpha.NetworkInterfacePhaseBound ||
		!slices.Contains(r.LabeledNamespaces, networkInterface.Namespace) {
		return nil
	}
	var attachment cloudv1alpha1.VPCAttachment
	if err := r.Get(ctx, client.ObjectKeyFromObject(networkInterface), &attachment); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(&attachment, networkInterface) {
		return nil
	}
	cleared := withConsumerLabels(attachment.Labels, nil)
	if len(cleared) == len(attachment.Labels) {
		return nil
	}
	patch := client.MergeFrom(attachment.DeepCopy())
	attachment.Labels = cleared
	if err := r.Patch(ctx, &attachment, patch); err != nil {
		return fmt.Errorf("clear consumer labels on VPC attachment %s: %w", attachment.Name, err)
	}
	return nil
}

func withConsumerLabels(current, source map[string]string) map[string]string {
	updated := map[string]string{}
	for key, value := range current {
		if !strings.HasPrefix(key, consumerLabelPrefix) {
			updated[key] = value
		}
	}
	for key, value := range source {
		if strings.HasPrefix(key, consumerLabelPrefix) {
			updated[key] = value
		}
	}
	if len(updated) == 0 {
		return nil
	}
	return updated
}

// reconcileNAD creates or updates the NAD the attachment owns.
func (r *NetworkInterfaceReconciler) reconcileNAD(
	ctx context.Context,
	attachment *cloudv1alpha1.VPCAttachment,
	vpc *cloudv1alpha1.VPC,
	networkInterface *networkingv1alpha.NetworkInterface,
) (*nadv1.NetworkAttachmentDefinition, error) {
	nad := &nadv1.NetworkAttachmentDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: attachment.Name, Namespace: attachment.Namespace},
	}

	attachmentID := ""
	if err := r.Get(ctx, client.ObjectKeyFromObject(nad), nad); err == nil {
		attachmentID = nad.Labels[LabelVPCAttachment]
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get NetworkAttachmentDefinition %s: %w", nad.Name, err)
	}
	if attachmentID == "" {
		allocated, err := r.allocateAttachmentIdentifier(ctx, vpc.Status.VPC, client.ObjectKeyFromObject(nad))
		if err != nil {
			return nil, err
		}
		attachmentID = allocated
	}

	addresses := make([]galactic.Address, 0, len(networkInterface.Spec.Addresses))
	for _, address := range networkInterface.Spec.Addresses {
		addresses = append(addresses, galactic.Address{
			Address: address.Address,
			Gateway: address.Gateway,
		})
	}
	config, err := galactic.ConflistJSON(attachment.Name, masterPlugin(attachment.Spec.Interface.Mode),
		vpc.Status.VPC, attachmentID, networkInterface.Spec.MTU, addresses,
		declaresDevice(attachment.Spec.Interface.Mode), r.hostOnlyPorts(attachment.Spec.Interface.Mode))
	if err != nil {
		return nil, err
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, nad, func() error {
		if nad.Labels == nil {
			nad.Labels = map[string]string{}
		}
		nad.Labels[LabelVPC] = vpc.Status.VPC
		nad.Labels[LabelVPCAttachment] = attachmentID
		if nad.Annotations == nil {
			nad.Annotations = map[string]string{}
		}
		nad.Annotations[AnnotationHostInterface] = galactic.HostInterfaceName(vpc.Status.VPC, attachmentID)
		nad.Spec.Config = config
		return controllerutil.SetControllerReference(attachment, nad, r.Scheme)
	}); err != nil {
		return nil, fmt.Errorf("reconcile NetworkAttachmentDefinition %s: %w", nad.Name, err)
	}
	return nad, nil
}

// attachmentMode resolves how one guest consumes its interface. An interface
// that states a mode carries the workload's own requirement, so it wins. The
// cell-wide mode covers everything else, which is every interface written
// before a workload could state one.
func (r *NetworkInterfaceReconciler) attachmentMode(
	networkInterface *networkingv1alpha.NetworkInterface,
) cloudv1alpha1.VPCAttachmentInterfaceMode {
	switch networkInterface.Spec.AttachmentMode {
	case networkingv1alpha.NetworkInterfaceAttachmentModeNetns:
		return cloudv1alpha1.VPCAttachmentInterfaceModeNetns
	case networkingv1alpha.NetworkInterfaceAttachmentModeHypervisor:
		return cloudv1alpha1.VPCAttachmentInterfaceModeHypervisor
	case networkingv1alpha.NetworkInterfaceAttachmentModeHypervisorDeclared:
		return cloudv1alpha1.VPCAttachmentInterfaceModeHypervisorDeclared
	default:
		return r.AttachmentMode
	}
}

// masterPlugin translates how a guest consumes an interface into the galactic
// binary that realizes it. This is the only place the two vocabularies meet.
func masterPlugin(mode cloudv1alpha1.VPCAttachmentInterfaceMode) string {
	switch mode {
	case cloudv1alpha1.VPCAttachmentInterfaceModeHypervisor,
		cloudv1alpha1.VPCAttachmentInterfaceModeHypervisorDeclared:
		return galactic.PluginTap
	default:
		return galactic.PluginVeth
	}
}

// declaresDevice reports whether the tap plugin describes the device to the
// hypervisor instead of leaving the hypervisor to discover it.
func declaresDevice(mode cloudv1alpha1.VPCAttachmentInterfaceMode) bool {
	return mode == cloudv1alpha1.VPCAttachmentInterfaceModeHypervisorDeclared
}

// hostOnlyPorts returns the ports a guest attached in mode serves only to its
// node.
func (r *NetworkInterfaceReconciler) hostOnlyPorts(mode cloudv1alpha1.VPCAttachmentInterfaceMode) []uint16 {
	if mode != cloudv1alpha1.VPCAttachmentInterfaceModeHypervisor {
		return nil
	}
	return r.HypervisorHostOnlyPorts
}

// interfaceAddresses copies the addresses NSO allocated onto the attachment, so
// the attachment describes itself without a second read.
func interfaceAddresses(networkInterface *networkingv1alpha.NetworkInterface) []cloudv1alpha1.IPAddress {
	addresses := make([]cloudv1alpha1.IPAddress, 0, len(networkInterface.Spec.Addresses))
	for _, address := range networkInterface.Spec.Addresses {
		addresses = append(addresses, cloudv1alpha1.IPAddress(address.Address))
	}
	return addresses
}

// allocateAttachmentIdentifier derives an identifier unused within the VPC from
// the NAD's namespace and name. A recreated interface normally derives the
// identifier it held before, which a running workload may still be attached
// under, so its own previous NAD awaiting garbage collection is not a collision. Other interfaces
// land on unrelated identifiers, so a freed one is not handed to a different
// workload while its BGPAdvertisement is still being garbage collected.
func (r *NetworkInterfaceReconciler) allocateAttachmentIdentifier(
	ctx context.Context, vpc string, key types.NamespacedName,
) (string, error) {
	used := make(map[string]struct{})
	var nads nadv1.NetworkAttachmentDefinitionList
	if err := r.APIReader.List(ctx, &nads, client.MatchingLabels{LabelVPC: vpc}); err != nil {
		return "", fmt.Errorf("list NetworkAttachmentDefinitions for VPC %s: %w", vpc, err)
	}
	for _, nad := range nads.Items {
		if nad.Namespace == key.Namespace && nad.Name == key.Name {
			continue
		}
		if id := nad.Labels[LabelVPCAttachment]; id != "" {
			used[id] = struct{}{}
		}
	}

	for attempt := range maxIdentifierAttempts {
		candidate, err := identifier.VPCAttachmentBase62For(key.String(), attempt)
		if err != nil {
			return "", err
		}
		if _, taken := used[candidate]; !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no unused attachment identifier found in VPC %s after %d attempts",
		vpc, maxIdentifierAttempts)
}

// publishAttachmentStatus records the allocated identifiers on the attachment.
func (r *NetworkInterfaceReconciler) publishAttachmentStatus(
	ctx context.Context,
	attachment *cloudv1alpha1.VPCAttachment,
	vpc *cloudv1alpha1.VPC,
	nad *nadv1.NetworkAttachmentDefinition,
) error {
	attachment.Status.VPC = vpc.Status.VPC
	attachment.Status.VPCAttachment = nad.Labels[LabelVPCAttachment]
	attachment.Status.NetworkAttachmentDefinition = nad.Name
	attachment.Status.ObservedGeneration = attachment.Generation
	meta.SetStatusCondition(&attachment.Status.Conditions, metav1.Condition{
		Type:               cloudv1alpha1.ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             "AttachmentDefinitionReady",
		Message:            fmt.Sprintf("NetworkAttachmentDefinition %s is ready for use", nad.Name),
		ObservedGeneration: attachment.Generation,
	})
	if err := r.Status().Update(ctx, attachment); err != nil {
		return fmt.Errorf("update VPC attachment %s status: %w", client.ObjectKeyFromObject(attachment), err)
	}
	return nil
}

// publishToInterface records what realizes the interface and which VPC it landed
// in, then reports Prepared so compute can release the Pod.
func (r *NetworkInterfaceReconciler) publishToInterface(
	ctx context.Context,
	networkInterface *networkingv1alpha.NetworkInterface,
	attachment *cloudv1alpha1.VPCAttachment,
	vpc *cloudv1alpha1.VPC,
) error {
	ref := &networkingv1alpha.NetworkInterfaceAttachmentRef{
		APIGroup: cloudv1alpha1.GroupVersion.Group,
		Kind:     attachmentKind,
		Name:     attachment.Name,
	}
	if old := networkInterface.Status.AttachmentRef; old != nil && old.APIGroup != ref.APIGroup {
		// NSO preserves the provider-owned Programmed condition. A condition
		// earned by the former API group cannot certify this new attachment;
		// the new BGPAdvertisement reconciler will report it again.
		meta.SetStatusCondition(&networkInterface.Status.Conditions, metav1.Condition{
			Type:               networkingv1alpha.NetworkInterfaceProgrammed,
			Status:             metav1.ConditionUnknown,
			Reason:             reasonAPIGroupMigration,
			Message:            "waiting for the Galactic attachment to be programmed",
			ObservedGeneration: networkInterface.Generation,
		})
	}
	networkInterface.Status.AttachmentRef = ref
	networkInterface.Status.VPC = vpc.Status.VPC
	return r.markPrepared(ctx, networkInterface, metav1.ConditionTrue, "AttachmentReady",
		fmt.Sprintf("VPCAttachment %s and its attachment definition exist", attachment.Name))
}

// markPrepared reports whether the pre-Pod artifacts exist, on the interface and
// on the claim holding it, which is what compute gates Pod creation on.
func (r *NetworkInterfaceReconciler) markPrepared(
	ctx context.Context,
	networkInterface *networkingv1alpha.NetworkInterface,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	condition := metav1.Condition{
		Type:               ConditionTypePrepared,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: networkInterface.Generation,
	}
	meta.SetStatusCondition(&networkInterface.Status.Conditions, condition)
	if err := r.Status().Update(ctx, networkInterface); err != nil {
		return fmt.Errorf("update network interface %s status: %w",
			client.ObjectKeyFromObject(networkInterface), err)
	}

	if networkInterface.Spec.ClaimRef == nil {
		return nil
	}
	var claim networkingv1alpha.NetworkInterfaceClaim
	key := types.NamespacedName{Namespace: networkInterface.Namespace, Name: networkInterface.Spec.ClaimRef.Name}
	if err := r.Get(ctx, key, &claim); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !claim.DeletionTimestamp.IsZero() {
		return nil
	}
	claimCondition := condition
	claimCondition.ObservedGeneration = claim.Generation
	meta.SetStatusCondition(&claim.Status.Conditions, claimCondition)
	if internet := meta.FindStatusCondition(networkInterface.Status.Conditions,
		networkingv1alpha.NetworkInterfaceInternetEgressReady); internet != nil {
		claimInternet := *internet
		claimInternet.ObservedGeneration = claim.Generation
		meta.SetStatusCondition(&claim.Status.Conditions, claimInternet)
	}
	// A claim can have Programmed and Ready from the old attachment even after
	// its interface points at Galactic. Invalidate both until the new group's
	// advertisement reports that the replacement attachment is carrying traffic.
	if programmed := meta.FindStatusCondition(networkInterface.Status.Conditions,
		networkingv1alpha.NetworkInterfaceProgrammed); programmed != nil &&
		programmed.Status == metav1.ConditionUnknown && programmed.Reason == reasonAPIGroupMigration {
		claimProgrammed := *programmed
		claimProgrammed.Type = networkingv1alpha.NetworkInterfaceClaimProgrammed
		claimProgrammed.ObservedGeneration = claim.Generation
		meta.SetStatusCondition(&claim.Status.Conditions, claimProgrammed)
		meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
			Type:               networkingv1alpha.NetworkInterfaceClaimReady,
			Status:             metav1.ConditionUnknown,
			Reason:             "NotProgrammed",
			Message:            "waiting for the Galactic attachment to be programmed",
			ObservedGeneration: claim.Generation,
		})
	}
	if err := r.Status().Update(ctx, &claim); err != nil {
		return fmt.Errorf("update network interface claim %s status: %w", key, err)
	}
	return nil
}

// SetupWithManager registers the reconciler with the manager.
func (r *NetworkInterfaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// A NAD is owned by the attachment rather than the interface, but all three
	// share a name and namespace, so mapping one back is an identity.
	nadToInterface := func(_ context.Context, obj client.Object) []ctrl.Request {
		return []ctrl.Request{{NamespacedName: client.ObjectKeyFromObject(obj)}}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkInterface{}).
		Owns(&cloudv1alpha1.VPCAttachment{}).
		Owns(&galacticv1alpha1.TrafficPolicy{}).
		Owns(&galacticv1alpha1.TrafficPolicyBinding{}).
		Watches(&nadv1.NetworkAttachmentDefinition{}, handler.EnqueueRequestsFromMapFunc(nadToInterface)).
		Watches(&networkingv1alpha.NetworkContext{}, handler.EnqueueRequestsFromMapFunc(r.interfacesForNetworkContext)).
		Named("galactic-networkinterface").
		Complete(r)
}
