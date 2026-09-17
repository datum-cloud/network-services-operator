// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// networkInterfaceEgressInterval paces the pass that reads an attachment
// nothing here watches. The attachment's kind is named by the reference rather
// than known when this is built, so there is no informer to wake on the
// provider publishing an address, and this interval is what carries one.
const networkInterfaceEgressInterval = time.Minute

// jsonKeyEgress and the keys below are the path the provider publishes egress
// under on the attachment it owns.
const (
	jsonKeyEgress          = "egress"
	jsonKeyInternet        = "internet"
	jsonKeySourceAddresses = "sourceAddresses"
	jsonKeyAddress         = "address"
	jsonKeyFamily          = "family"
	jsonKeyStability       = "stability"
)

// NetworkInterfaceEgressReconciler reports the source addresses an interface's
// outbound traffic leaves on, read off the attachment realizing that interface
// in the cell.
//
// The attachment belongs to whichever provider realizes the interface, and
// status.attachmentRef names its group and kind rather than a type this
// operator compiles against. It is read unstructured, through the cluster's
// REST mapper, so a provider's API group stays independent of this one.
//
// Nothing is derived here. The stability a consumer reads is the provider's,
// resolved from the class serving the network, and an attachment publishing no
// address leaves the interface reporting none: a wrong egress address is worse
// than an absent one, because a consumer allow-lists it at their destination.
type NetworkInterfaceEgressReconciler struct {
	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloud.datumapis.com,resources=vpcattachments,verbs=get;list;watch

func (r *NetworkInterfaceEgressReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	return r.report(ctx, cl.GetClient(), cl.GetRESTMapper(), req.NamespacedName)
}

func (r *NetworkInterfaceEgressReconciler) report(
	ctx context.Context,
	cl client.Client,
	mapper meta.RESTMapper,
	key client.ObjectKey,
) (ctrl.Result, error) {
	var iface networkingv1alpha.NetworkInterface
	if err := cl.Get(ctx, key, &iface); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// A published copy is never read back, and an interface on its way out is
	// releasing what it reported rather than reporting anything new.
	if isProjection(&iface) || !iface.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	reported, err := readAttachmentEgress(ctx, cl, mapper, &iface)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !apiequality.Semantic.DeepEqual(iface.Status.Egress, reported) {
		iface.Status.Egress = reported
		if err := cl.Status().Update(ctx, &iface); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed reporting interface egress: %w", err)
		}
	}

	if iface.Status.AttachmentRef == nil {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: networkInterfaceEgressInterval}, nil
}

// readAttachmentEgress returns what the attachment publishes, or nil when it
// publishes nothing. An attachment that is not there yet, or gone, reports
// nothing rather than an error: the reference is written by a provider and an
// interface can outlive what realized it.
func readAttachmentEgress(
	ctx context.Context,
	cl client.Client,
	mapper meta.RESTMapper,
	iface *networkingv1alpha.NetworkInterface,
) (*networkingv1alpha.NetworkInterfaceEgressStatus, error) {
	ref := iface.Status.AttachmentRef
	if ref == nil {
		return nil, nil
	}

	mapping, err := mapper.RESTMapping(schema.GroupKind{Group: ref.APIGroup, Kind: ref.Kind})
	if err != nil {
		// A cell that does not serve the provider's kind cannot be asked about
		// an attachment of it, which is a cluster this interface is not
		// realized on rather than a failure to read one.
		log.FromContext(ctx).V(1).Info("the cell serves no attachment of this kind",
			"apiGroup", ref.APIGroup, jsonKeyKind, ref.Kind)
		return nil, nil
	}

	attachment := &unstructured.Unstructured{}
	attachment.SetGroupVersionKind(mapping.GroupVersionKind)
	if err := cl.Get(ctx, client.ObjectKey{Namespace: iface.Namespace, Name: ref.Name}, attachment); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed reading attachment %s/%s: %w", ref.Kind, ref.Name, err)
	}

	addresses, err := attachmentSourceAddresses(attachment)
	if err != nil {
		return nil, fmt.Errorf("failed reading egress off attachment %s/%s: %w", ref.Kind, ref.Name, err)
	}
	if len(addresses) == 0 {
		return nil, nil
	}

	return &networkingv1alpha.NetworkInterfaceEgressStatus{
		Internet: &networkingv1alpha.NetworkInterfaceInternetEgressStatus{
			SourceAddresses: addresses,
		},
	}, nil
}

// attachmentSourceAddresses reads status.egress.internet.sourceAddresses off an
// attachment.
//
// Entries are keyed on family rather than taken by position, because the
// publisher stores them as a map keyed that way and neither order nor count is
// promised. An entry missing a field is a broken publisher rather than a
// partial answer to complete: filling one in would hand a consumer an address
// to allow-list that nothing said was theirs.
func attachmentSourceAddresses(
	attachment *unstructured.Unstructured,
) ([]networkingv1alpha.InternetEgressSourceAddress, error) {
	published, found, err := unstructured.NestedSlice(
		attachment.Object, jsonKeyStatus, jsonKeyEgress, jsonKeyInternet, jsonKeySourceAddresses)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}

	byFamily := map[string]networkingv1alpha.InternetEgressSourceAddress{}
	for _, item := range published {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("a source address is %T rather than an object", item)
		}

		family, err := requiredString(entry, jsonKeyFamily)
		if err != nil {
			return nil, err
		}
		address, err := requiredString(entry, jsonKeyAddress)
		if err != nil {
			return nil, err
		}
		stability, err := requiredString(entry, jsonKeyStability)
		if err != nil {
			return nil, err
		}

		if _, repeated := byFamily[family]; repeated {
			return nil, fmt.Errorf("two source addresses published for family %q", family)
		}
		byFamily[family] = networkingv1alpha.InternetEgressSourceAddress{
			Family:    networkingv1alpha.IPFamily(family),
			Address:   address,
			Stability: networkingv1alpha.InternetEgressAddressStability(stability),
		}
	}

	families := slices.Sorted(maps.Keys(byFamily))
	addresses := make([]networkingv1alpha.InternetEgressSourceAddress, 0, len(families))
	for _, family := range families {
		addresses = append(addresses, byFamily[family])
	}
	return addresses, nil
}

func requiredString(entry map[string]any, key string) (string, error) {
	value, found, err := unstructured.NestedString(entry, key)
	if err != nil {
		return "", err
	}
	if !found || value == "" {
		return "", fmt.Errorf("a source address carries no %s", key)
	}
	return value, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NetworkInterfaceEgressReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkInterface{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("networkinterface_egress").
		Complete(r)
}
