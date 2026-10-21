// SPDX-License-Identifier: AGPL-3.0-only

package publicudp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/netip"
	"reflect"
	"time"

	api "go.datum.net/network-services-operator/api/v1alpha"
	core "k8s.io/api/core/v1"
	errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const Finalizer = "networking.datumapis.com/public-udp-withdraw"
const OwnerLabel = "networking.datumapis.com/public-udp-owner"
const Ready = "PublicReady"

type Reconciler struct {
	Project                                client.Client
	Cell                                   client.Client
	ProjectName, Location, FabricNamespace string
}

func object(kind, ns, name string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(schema.GroupVersionKind{Group: "galactic.datumapis.com", Version: "v1alpha1", Kind: kind})
	o.SetNamespace(ns)
	o.SetName(name)
	return o
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var s api.NetworkService
	if err := r.Project.Get(ctx, req.NamespacedName, &s); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if s.Spec.PublicUDP == nil && !controllerutil.ContainsFinalizer(&s, Finalizer) {
		return ctrl.Result{}, nil
	}
	if s.Spec.PublicUDP != nil && s.Spec.PublicUDP.Location != r.Location {
		return ctrl.Result{}, nil
	}
	if s.UID == "" {
		return ctrl.Result{}, fmt.Errorf("service has no UID")
	}
	if s.DeletionTimestamp.IsZero() && controllerutil.AddFinalizer(&s, Finalizer) {
		if err := r.Project.Update(ctx, &s); err != nil {
			return ctrl.Result{}, err
		}
	}
	sum := sha256.Sum256([]byte(string(s.UID)))
	name := fmt.Sprintf("public-udp-%x", sum[:12])
	rule := object("NetworkService", r.FabricNamespace, name)
	err := r.Cell.Get(ctx, client.ObjectKeyFromObject(rule), rule)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	if err == nil && rule.GetLabels()[OwnerLabel] != string(s.UID) {
		return ctrl.Result{}, fmt.Errorf("public UDP resource ownership conflict")
	}
	before := s.DeepCopy()
	report := func(ready bool, reason string) (ctrl.Result, error) {
		status := metav1.ConditionFalse
		if ready {
			status = metav1.ConditionTrue
		}
		meta.SetStatusCondition(&s.Status.Conditions, metav1.Condition{Type: Ready, Status: status, Reason: reason, Message: reason, ObservedGeneration: s.Generation})
		if !reflect.DeepEqual(before.Status, s.Status) {
			if err := r.Project.Status().Patch(ctx, &s, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	cleanup := func() (bool, error) {
		if rule.GetResourceVersion() != "" {
			return false, client.IgnoreNotFound(r.Cell.Delete(ctx, rule, client.Preconditions{UID: ptr.To(rule.GetUID())}))
		}
		if pending, err := r.bindingsRemain(ctx, name); err != nil || pending {
			return false, err
		}
		var ifaces api.NetworkInterfaceList
		if err := r.Cell.List(ctx, &ifaces); err != nil {
			return false, err
		}
		hold := api.NetworkInterfacePublicUDPHoldPrefix + fmt.Sprintf("%x", sum[:8])
		for i := range ifaces.Items {
			n := &ifaces.Items[i]
			if controllerutil.RemoveFinalizer(n, hold) {
				if err := r.Cell.Update(ctx, n); err != nil {
					return false, err
				}
			}
		}
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{Group: "galactic.datumapis.com", Version: "v1alpha1", Kind: "VPCAttachmentList"})
		key := "networking.datumapis.com/udp-" + fmt.Sprintf("%x", sum[:8])
		if err := r.Cell.List(ctx, list, client.MatchingLabels{key: "true"}); err != nil {
			return false, err
		}
		for i := range list.Items {
			a := &list.Items[i]
			base := a.DeepCopy()
			labels := a.GetLabels()
			delete(labels, key)
			a.SetLabels(labels)
			if err := r.Cell.Patch(ctx, a, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	if !s.DeletionTimestamp.IsZero() || s.Spec.PublicUDP == nil {
		done, err := cleanup()
		if err != nil {
			return ctrl.Result{}, err
		}
		if !done {
			return report(false, "Withdrawing")
		}
		controllerutil.RemoveFinalizer(&s, Finalizer)
		return ctrl.Result{}, r.Project.Update(ctx, &s)
	}
	source, attachment, vip, err := r.resolve(ctx, &s)
	if err != nil {
		if _, e := cleanup(); e != nil {
			return ctrl.Result{}, e
		}
		s.Status.PublicAddresses = nil
		return report(false, "BackendUnavailable")
	}
	keyForService := "networking.datumapis.com/udp-" + fmt.Sprintf("%x", sum[:8])
	stale := &unstructured.UnstructuredList{}
	stale.SetGroupVersionKind(schema.GroupVersionKind{Group: "galactic.datumapis.com", Version: "v1alpha1", Kind: "VPCAttachmentList"})
	if err := r.Cell.List(ctx, stale, client.MatchingLabels{keyForService: "true"}); err != nil {
		return ctrl.Result{}, err
	}
	for _, a := range stale.Items {
		if a.GetUID() != attachment.GetUID() {
			if _, err := cleanup(); err != nil {
				return ctrl.Result{}, err
			}
			return report(false, "BackendChanging")
		}
	}
	hold := api.NetworkInterfacePublicUDPHoldPrefix + fmt.Sprintf("%x", sum[:8])
	if controllerutil.AddFinalizer(source, hold) {
		if err := r.Cell.Update(ctx, source); err != nil {
			return ctrl.Result{}, err
		}
	}
	key := "networking.datumapis.com/udp-" + fmt.Sprintf("%x", sum[:8])
	base := attachment.DeepCopy()
	labels := attachment.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[key] = "true"
	attachment.SetLabels(labels)
	if !reflect.DeepEqual(base.GetLabels(), labels) {
		if err := r.Cell.Patch(ctx, attachment, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
	}
	desired := map[string]interface{}{
		"vpcRef":          source.Status.VPC,
		"vipAddresses":    []interface{}{vip},
		"protocol":        "udp",
		"port":            int64(s.Spec.Ports[0].Port),
		"backendPort":     int64(s.Spec.Ports[0].Port),
		"backendSelector": map[string]interface{}{"matchLabels": map[string]interface{}{key: "true"}},
	}
	if rule.GetDeletionTimestamp() != nil {
		return report(false, "Withdrawing")
	}
	if rule.GetResourceVersion() == "" {
		if pending, err := r.bindingsRemain(ctx, name); err != nil {
			return ctrl.Result{}, err
		} else if pending {
			return report(false, "Withdrawing")
		}
		rule.SetLabels(map[string]string{OwnerLabel: string(s.UID)})
		rule.Object["spec"] = desired
		if err := r.Cell.Create(ctx, rule); err != nil {
			return ctrl.Result{}, err
		}
	} else if !reflect.DeepEqual(rule.Object["spec"], desired) {
		if _, err := cleanup(); err != nil {
			return ctrl.Result{}, err
		}
		return report(false, "BackendChanging")
	}
	s.Status.PublicAddresses = []string{vip}
	conditions, _, _ := unstructured.NestedSlice(rule.Object, "status", "conditions")
	for _, raw := range conditions {
		c, ok := raw.(map[string]interface{})
		if ok && c["type"] == "Ready" && c["status"] == "True" && c["observedGeneration"] == rule.GetGeneration() {
			return report(true, "Programmed")
		}
	}
	return report(false, "Programming")
}

func (r *Reconciler) resolve(ctx context.Context, s *api.NetworkService) (*api.NetworkInterface, *unstructured.Unstructured, string, error) {
	fail := func() (*api.NetworkInterface, *unstructured.Unstructured, string, error) {
		return nil, nil, "", fmt.Errorf("public UDP requires one healthy allocated interface in its project and location")
	}
	if len(s.Spec.Ports) != 1 || s.Spec.Ports[0].Protocol != api.NetworkServiceProtocolUDP || s.Spec.Ports[0].AppProtocol != nil {
		return fail()
	}
	selector, err := metav1.LabelSelectorAsSelector(&s.Spec.NetworkInterfaces.Selector)
	if err != nil || selector.Empty() {
		return fail()
	}
	var matches api.NetworkInterfaceList
	if err := r.Project.List(ctx, &matches, client.InNamespace(s.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, nil, "", err
	}
	if len(matches.Items) != 1 {
		return fail()
	}
	p := &matches.Items[0]
	if p.Labels[api.NetworkInterfaceLocationLabel] != r.Location || p.Labels[api.NetworkInterfaceProjectionLabel] != "true" {
		return fail()
	}
	var ns core.Namespace
	if err := r.Cell.Get(ctx, client.ObjectKey{Name: p.Labels[api.NetworkInterfaceSourceNamespaceLabel]}, &ns); err != nil {
		return nil, nil, "", err
	}
	if ns.Labels["meta.datumapis.com/upstream-cluster-name"] != "cluster-"+r.ProjectName || ns.Labels["meta.datumapis.com/upstream-namespace"] != s.Namespace {
		return fail()
	}
	var n api.NetworkInterface
	if err := r.Cell.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: p.Name}, &n); err != nil {
		return nil, nil, "", err
	}
	if n.Annotations[api.NetworkInterfaceAddressReleaseStarted] != "" || !n.DeletionTimestamp.IsZero() || n.Spec.Network != p.Spec.Network || !selector.Matches(labels.Set(n.Labels)) || n.Status.Phase != api.NetworkInterfacePhaseBound || n.Status.VPC == "" || n.Status.AttachmentRef == nil {
		return fail()
	}
	if n.Spec.ClaimRef == nil {
		return fail()
	}
	var claim api.NetworkInterfaceClaim
	if err := r.Cell.Get(ctx, client.ObjectKey{Namespace: n.Namespace, Name: n.Spec.ClaimRef.Name}, &claim); err != nil {
		return nil, nil, "", err
	}
	if !claim.DeletionTimestamp.IsZero() {
		return fail()
	}
	for _, kind := range []string{api.NetworkInterfaceAllocated, api.NetworkInterfaceProgrammed, api.NetworkInterfaceHolderAvailable} {
		c := meta.FindStatusCondition(n.Status.Conditions, kind)
		if c == nil || c.Status != metav1.ConditionTrue || c.ObservedGeneration != n.Generation {
			return fail()
		}
	}
	class := s.Spec.PublicUDP.AddressClass
	if class == "" {
		class = "public-ipv4"
	}
	vip := ""
	for _, a := range n.Spec.ExternalAddresses {
		if a.Class == class {
			ip, e := netip.ParseAddr(a.Address)
			if e != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
				return fail()
			}
			vip = ip.String()
		}
	}
	if vip == "" || n.Status.AttachmentRef.APIGroup != "galactic.datumapis.com" || n.Status.AttachmentRef.Kind != "VPCAttachment" {
		return fail()
	}
	a := object("VPCAttachment", n.Namespace, n.Status.AttachmentRef.Name)
	if err := r.Cell.Get(ctx, client.ObjectKeyFromObject(a), a); err != nil {
		return nil, nil, "", err
	}
	ref, _, _ := unstructured.NestedString(a.Object, "spec", "interfaceRef", "name")
	vpc, _, _ := unstructured.NestedString(a.Object, "status", "vpc")
	node, _, _ := unstructured.NestedString(a.Object, "status", "node")
	if !metav1.IsControlledBy(a, &n) || a.GetDeletionTimestamp() != nil || ref != n.Name || vpc != n.Status.VPC || node == "" {
		return fail()
	}
	return &n, a, vip, nil
}

func (r *Reconciler) bindingsRemain(ctx context.Context, name string) (bool, error) {
	bindings := &unstructured.UnstructuredList{}
	bindings.SetGroupVersionKind(object("ServiceVIPBindingList", "", "").GroupVersionKind())
	if err := r.Cell.List(ctx, bindings, client.InNamespace(r.FabricNamespace)); err != nil {
		return false, err
	}
	for _, b := range bindings.Items {
		for _, owner := range b.GetOwnerReferences() {
			if owner.APIVersion == "galactic.datumapis.com/v1alpha1" && owner.Kind == "NetworkService" && owner.Name == name {
				return true, nil
			}
		}
	}
	return false, nil
}
