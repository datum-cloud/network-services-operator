// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	cloudv1alpha1 "go.datum.net/galactic/api/cloud/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	consumerWatchNS       = "tenant"
	consumerWatchCluster  = "project-a"
	consumerWatchContext  = "context-a"
	consumerWatchInstance = "instance-0-eth0"
)

func consumerInterface(name, contextName, attachment string) *networkingv1alpha.NetworkInterface {
	iface := &networkingv1alpha.NetworkInterface{ObjectMeta: metav1.ObjectMeta{Namespace: consumerWatchNS, Name: name}}
	if contextName != "" {
		iface.Status.NetworkContextRef = &networkingv1alpha.LocalNetworkContextRef{Name: contextName}
	}
	if attachment != "" {
		iface.Status.AttachmentRef = &networkingv1alpha.NetworkInterfaceAttachmentRef{
			APIGroup: internalDNSAttachmentAPIGroup, Kind: internalDNSAttachmentKind, Name: attachment,
		}
	}
	return iface
}

func consumerWatchClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := networkingv1alpha.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := cloudv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestIsInternalDNSConsumer(t *testing.T) {
	t.Parallel()
	if !isInternalDNSConsumer(consumerInterface("a", consumerWatchContext, "a")) {
		t.Error("a bound interface with a Galactic attachment is a consumer")
	}
	if isInternalDNSConsumer(consumerInterface("a", "", "a")) {
		t.Error("an interface with no network context is not a consumer")
	}
	if isInternalDNSConsumer(consumerInterface("a", consumerWatchContext, "")) {
		t.Error("an interface with no attachment is not a consumer")
	}
	other := consumerInterface("a", consumerWatchContext, "a")
	other.Status.AttachmentRef.APIGroup = "compute.datumapis.com"
	if isInternalDNSConsumer(other) {
		t.Error("an attachment from another API group is not a consumer")
	}
}

// The context is reconciled for the interface that references an attachment,
// once per context, and not for interfaces of other attachments.
func TestInternalDNSContextsInNamespaceFiltersAndDedupes(t *testing.T) {
	t.Parallel()
	cl := consumerWatchClient(t,
		consumerInterface(consumerWatchInstance, consumerWatchContext, consumerWatchInstance),
		consumerInterface("instance-1-eth0", consumerWatchContext, "instance-1-eth0"),
		consumerInterface("instance-2-eth0", "context-b", "instance-2-eth0"),
		consumerInterface("unbound", "", ""),
	)
	names := func(keep func(*networkingv1alpha.NetworkInterface) bool) []string {
		requests := internalDNSContextsInNamespace(context.Background(), cl, consumerWatchCluster,
			consumerWatchNS, keep)
		got := make([]string, 0, len(requests))
		for _, r := range requests {
			if r.ClusterName != consumerWatchCluster || r.Namespace != consumerWatchNS {
				t.Fatalf("request went to %s %s", r.ClusterName, r.Namespace)
			}
			got = append(got, r.Name)
		}
		slices.Sort(got)
		return got
	}
	if got, want := names(func(*networkingv1alpha.NetworkInterface) bool { return true }),
		[]string{consumerWatchContext, "context-b"}; !slices.Equal(got, want) {
		t.Fatalf("every interface reached %v, want %v", got, want)
	}
	if got, want := names(func(i *networkingv1alpha.NetworkInterface) bool {
		return i.Status.AttachmentRef.Name == "instance-2-eth0"
	}), []string{"context-b"}; !slices.Equal(got, want) {
		t.Fatalf("one attachment reached %v, want %v", got, want)
	}
}

// The predicates pass the events that can change labeling and drop the status
// writes a running instance makes constantly.
func TestInternalDNSConsumerPredicates(t *testing.T) {
	t.Parallel()
	bare := consumerInterface(consumerWatchInstance, "", "")
	bound := consumerInterface(consumerWatchInstance, consumerWatchContext, consumerWatchInstance)
	ifaces := internalDNSInterfaceChanged()
	if !ifaces.Update(event.UpdateEvent{ObjectOld: bare, ObjectNew: bound}) {
		t.Error("an interface being bound did not pass")
	}
	unrelated := bound.DeepCopy()
	unrelated.Status.Conditions = []metav1.Condition{{Type: "Programmed", Status: metav1.ConditionTrue}}
	if ifaces.Update(event.UpdateEvent{ObjectOld: bound, ObjectNew: unrelated}) {
		t.Error("an unrelated interface status write passed")
	}
	deleting := bound.DeepCopy()
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	if !ifaces.Update(event.UpdateEvent{ObjectOld: bound, ObjectNew: deleting}) {
		t.Error("an interface starting deletion did not pass")
	}
	if !ifaces.Create(event.CreateEvent{Object: bound}) || !ifaces.Delete(event.DeleteEvent{Object: bound}) {
		t.Error("an interface being created or deleted did not pass")
	}

	attachment := &cloudv1alpha1.VPCAttachment{ObjectMeta: metav1.ObjectMeta{
		Namespace: consumerWatchNS, Name: consumerWatchInstance,
	}}
	status := attachment.DeepCopy()
	status.Status.HostInterface = "tap0"
	attachments := internalDNSAttachmentChanged()
	if attachments.Update(event.UpdateEvent{ObjectOld: attachment, ObjectNew: status}) {
		t.Error("an attachment status write passed")
	}
	labeled := attachment.DeepCopy()
	labeled.Labels = map[string]string{internalDNSConsumerVPCLabel: "uid"}
	if !attachments.Update(event.UpdateEvent{ObjectOld: labeled, ObjectNew: attachment}) {
		t.Error("the consumer label being removed did not pass")
	}
	if !attachments.Create(event.CreateEvent{Object: attachment}) {
		t.Error("an attachment being created did not pass")
	}

	vpc := &cloudv1alpha1.VPC{ObjectMeta: metav1.ObjectMeta{Namespace: consumerWatchNS, Name: "default"}}
	identified := vpc.DeepCopy()
	identified.Status.VPC = "1a2b"
	vpcs := internalDNSVPCIdentityChanged()
	if !vpcs.Update(event.UpdateEvent{ObjectOld: vpc, ObjectNew: identified}) {
		t.Error("a VPC gaining its identity did not pass")
	}
	if vpcs.Update(event.UpdateEvent{ObjectOld: identified, ObjectNew: identified.DeepCopy()}) {
		t.Error("a VPC update that keeps its identity passed")
	}
}

// An interface whose attachment is not visible yet is skipped rather than
// failing the whole context: the attachment's create event requeues it.
func TestConsumerVPCSkipsAnAttachmentNotVisibleYet(t *testing.T) {
	t.Parallel()
	vpc := &cloudv1alpha1.VPC{ObjectMeta: metav1.ObjectMeta{
		Namespace: consumerWatchNS, Name: "default", UID: "vpc-uid",
	}}
	vpc.Status.VPC = "1a2b"
	attachment := &cloudv1alpha1.VPCAttachment{ObjectMeta: metav1.ObjectMeta{
		Namespace: consumerWatchNS, Name: consumerWatchInstance,
	}}
	attachment.Spec.VPC.Name = vpc.Name
	cl := consumerWatchClient(t, vpc, attachment,
		consumerInterface(consumerWatchInstance, consumerWatchContext, consumerWatchInstance),
		consumerInterface("instance-1-eth0", consumerWatchContext, "instance-1-eth0"),
	)
	networkContext := &networkingv1alpha.NetworkContext{ObjectMeta: metav1.ObjectMeta{
		Namespace: consumerWatchNS, Name: consumerWatchContext,
	}}

	got, attachments, err := (&InternalDNSCellReconciler{}).consumerVPC(context.Background(), cl, networkContext)
	if err != nil {
		t.Fatalf("consumerVPC: %v", err)
	}
	if got.Name != vpc.Name || len(attachments) != 1 || attachments[0].Name != consumerWatchInstance {
		t.Fatalf("got VPC %s and %d attachments, want %s and the one visible attachment", got.Name, len(attachments), vpc.Name)
	}
}
