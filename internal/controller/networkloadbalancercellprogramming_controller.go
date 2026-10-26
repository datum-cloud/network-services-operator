// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"go.miloapis.com/locations/pkg/locationidentity"

	cloudv1alpha1 "go.datum.net/galactic/api/cloud/v1alpha1"
	networkv1alpha1 "go.datum.net/galactic/api/network/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

const (
	nlbProgrammingManagedByLabel      = "networking.datumapis.com/nlb-cell-intent"
	nlbProgrammingIntentNamespaceAnno = "networking.datumapis.com/nlb-cell-intent-namespace"
	nlbProgrammingBackendLabelPrefix  = "nlb.networking.datumapis.com/"
	nlbProgrammingBackendLabelValue   = "backend"
	nlbProgrammingRetry               = 30 * time.Second
)

type NetworkLoadBalancerCellProgrammingReconciler struct {
	Client           client.Client
	Location         config.LocationConfig
	GatewayNamespace string

	resolveLocation func(context.Context) (string, error)
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancercellintents,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=vpcattachments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=networkservices,verbs=get;list;watch;create;update;patch;delete

func (r *NetworkLoadBalancerCellProgrammingReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var intent networkingv1alpha.NetworkLoadBalancerCellIntent
	if err := r.Client.Get(ctx, req.NamespacedName, &intent); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.withdraw(ctx, req.NamespacedName)
		}
		return ctrl.Result{}, err
	}

	location, err := r.location(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	if location == "" || intent.Spec.Location != location || !intent.DeletionTimestamp.IsZero() ||
		intent.Spec.State != networkingv1alpha.NetworkLoadBalancerCellIntentValidate {
		return ctrl.Result{}, r.withdraw(ctx, req.NamespacedName)
	}

	backends, rejected, err := r.verifiedBackends(ctx, &intent)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, reason := range rejected {
		logger.Info("backend excluded", "intent", req.NamespacedName, "reason", reason)
	}
	service, reason := desiredNetworkLoadBalancerService(&intent, backends, r.GatewayNamespace)
	if service == nil {
		logger.Info("intent not served", "intent", req.NamespacedName, "reason", reason)
		if err := r.withdraw(ctx, req.NamespacedName); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: nlbProgrammingRetry}, nil
	}

	if err := r.labelBackends(ctx, &intent, backends); err != nil {
		return ctrl.Result{}, err
	}
	current := &networkv1alpha1.NetworkService{ObjectMeta: metav1.ObjectMeta{Namespace: service.Namespace, Name: service.Name}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, current, func() error {
		if current.Labels == nil {
			current.Labels = map[string]string{}
		}
		if current.Annotations == nil {
			current.Annotations = map[string]string{}
		}
		for key, value := range service.Labels {
			current.Labels[key] = value
		}
		for key, value := range service.Annotations {
			current.Annotations[key] = value
		}
		current.Spec = service.Spec
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("write gateway service %s: %w", service.Name, err)
	}
	return ctrl.Result{}, nil
}

type verifiedNetworkLoadBalancerBackend struct {
	attachment *cloudv1alpha1.VPCAttachment
	port       int32
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) verifiedBackends(
	ctx context.Context,
	intent *networkingv1alpha.NetworkLoadBalancerCellIntent,
) ([]verifiedNetworkLoadBalancerBackend, []string, error) {
	var verified []verifiedNetworkLoadBalancerBackend
	var rejected []string
	for _, backend := range intent.Spec.Backends {
		attachment, reason, err := r.verifyBackend(ctx, intent, backend)
		if err != nil {
			return nil, nil, err
		}
		if reason != "" {
			rejected = append(rejected, fmt.Sprintf("%s/%s: %s", backend.SourceNamespace, backend.SourceName, reason))
			continue
		}
		verified = append(verified, verifiedNetworkLoadBalancerBackend{attachment: attachment, port: backend.Port})
	}
	return verified, rejected, nil
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) verifyBackend(
	ctx context.Context,
	intent *networkingv1alpha.NetworkLoadBalancerCellIntent,
	backend networkingv1alpha.NetworkLoadBalancerCellIntentBackend,
) (*cloudv1alpha1.VPCAttachment, string, error) {
	if backend.SourceNamespace != intent.Namespace {
		return nil, "interface is outside the intent's project namespace", nil
	}
	if backend.Family != networkingv1alpha.IPv6Protocol {
		return nil, "backend is not IPv6", nil
	}
	var iface networkingv1alpha.NetworkInterface
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: backend.SourceNamespace, Name: backend.SourceName}, &iface); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, "interface does not exist", nil
		}
		return nil, "", fmt.Errorf("read interface %s/%s: %w", backend.SourceNamespace, backend.SourceName, err)
	}
	switch {
	case string(iface.UID) != backend.SourceUID:
		return nil, "interface UID changed", nil
	case iface.Labels[networkingv1alpha.NetworkInterfaceProjectionLabel] == "true":
		return nil, "interface is a projection", nil
	case iface.Spec.Network.Name != intent.Spec.NetworkName:
		return nil, "interface is on another network", nil
	case iface.Labels[networkingv1alpha.NetworkInterfaceLocationLabel] != intent.Spec.Location:
		return nil, "interface is in another location", nil
	case iface.Status.Phase != networkingv1alpha.NetworkInterfacePhaseBound || !isServiceMemberHealthy(&iface):
		return nil, "interface is not bound to an available holder", nil
	case iface.Status.VPC == "" || iface.Status.AttachmentRef == nil || iface.Status.AttachmentRef.Name == "":
		return nil, "interface has no attachment", nil
	case !slices.Contains(interfaceBackhaulAddresses(&iface), backend.Address):
		return nil, "interface no longer holds the backend address", nil
	}

	var attachment cloudv1alpha1.VPCAttachment
	key := client.ObjectKey{Namespace: iface.Namespace, Name: iface.Status.AttachmentRef.Name}
	if err := r.Client.Get(ctx, key, &attachment); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, "attachment does not exist", nil
		}
		return nil, "", fmt.Errorf("read attachment %s: %w", key, err)
	}
	addresses := make([]string, 0, len(attachment.Spec.Interface.Addresses))
	for _, address := range attachment.Spec.Interface.Addresses {
		host, _, _ := strings.Cut(string(address), "/")
		addresses = append(addresses, host)
	}
	switch {
	case !attachment.DeletionTimestamp.IsZero():
		return nil, "attachment is being deleted", nil
	case iface.Status.AttachmentRef.UID != "" && attachment.UID != iface.Status.AttachmentRef.UID:
		return nil, "attachment UID does not match the interface", nil
	case !metav1.IsControlledBy(&attachment, &iface):
		return nil, "attachment is not controlled by the interface", nil
	case attachment.Spec.InterfaceRef == nil || attachment.Spec.InterfaceRef.Name != iface.Name:
		return nil, "attachment names another interface", nil
	case attachment.Status.VPC == "" || attachment.Status.VPC != iface.Status.VPC:
		return nil, "attachment VPC does not match the interface", nil
	case attachment.Status.Node == "":
		return nil, "attachment is not placed on a node", nil
	case attachment.Status.ObservedGeneration != attachment.Generation ||
		!apimeta.IsStatusConditionTrue(attachment.Status.Conditions, cloudv1alpha1.ConditionTypeProgrammed):
		return nil, "attachment is not programmed", nil
	case !slices.Contains(addresses, backend.Address):
		return nil, "attachment does not hold the backend address", nil
	}
	return &attachment, "", nil
}

func desiredNetworkLoadBalancerService(
	intent *networkingv1alpha.NetworkLoadBalancerCellIntent,
	backends []verifiedNetworkLoadBalancerBackend,
	namespace string,
) (*networkv1alpha1.NetworkService, string) {
	if len(backends) == 0 {
		return nil, "no backend passed verification"
	}
	if intent.Spec.VIPFamily != networkingv1alpha.IPv6Protocol {
		return nil, "only IPv6 addresses are served"
	}
	var protocol networkv1alpha1.NetworkServiceProtocol
	switch intent.Spec.Protocol {
	case networkingv1alpha.NetworkLoadBalancerProtocolTCP:
		protocol = networkv1alpha1.NetworkServiceProtocolTCP
	case networkingv1alpha.NetworkLoadBalancerProtocolUDP:
		protocol = networkv1alpha1.NetworkServiceProtocolUDP
	default:
		return nil, fmt.Sprintf("protocol %q is not served", intent.Spec.Protocol)
	}
	vpc, port := backends[0].attachment.Status.VPC, backends[0].port
	for _, backend := range backends[1:] {
		if backend.attachment.Status.VPC != vpc {
			return nil, "backends span more than one VPC"
		}
		if backend.port != port {
			return nil, "backends listen on different ports"
		}
	}
	return &networkv1alpha1.NetworkService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Name:        intent.Name,
			Labels:      map[string]string{nlbProgrammingManagedByLabel: intent.Name},
			Annotations: map[string]string{nlbProgrammingIntentNamespaceAnno: intent.Namespace},
		},
		Spec: networkv1alpha1.NetworkServiceSpec{
			VPCRef:       vpc,
			VIPAddresses: []string{intent.Spec.VIP},
			Protocol:     protocol,
			Port:         intent.Spec.ListenerPort,
			BackendPort:  port,
			BackendSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				networkLoadBalancerBackendLabel(intent.Name): nlbProgrammingBackendLabelValue,
			}},
		},
	}, ""
}

func networkLoadBalancerBackendLabel(intentName string) string {
	return nlbProgrammingBackendLabelPrefix + intentName
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) labelBackends(
	ctx context.Context,
	intent *networkingv1alpha.NetworkLoadBalancerCellIntent,
	backends []verifiedNetworkLoadBalancerBackend,
) error {
	label := networkLoadBalancerBackendLabel(intent.Name)
	wanted := map[types.NamespacedName]bool{}
	for _, backend := range backends {
		attachment := backend.attachment
		wanted[client.ObjectKeyFromObject(attachment)] = true
		if attachment.Labels[label] == nlbProgrammingBackendLabelValue {
			continue
		}
		patch := client.MergeFrom(attachment.DeepCopy())
		if attachment.Labels == nil {
			attachment.Labels = map[string]string{}
		}
		attachment.Labels[label] = nlbProgrammingBackendLabelValue
		if err := r.Client.Patch(ctx, attachment, patch); err != nil {
			return fmt.Errorf("select attachment %s: %w", attachment.Name, err)
		}
	}
	return r.unlabelBackends(ctx, intent.Namespace, label, wanted)
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) unlabelBackends(
	ctx context.Context, namespace, label string, keep map[types.NamespacedName]bool,
) error {
	var attachments cloudv1alpha1.VPCAttachmentList
	if err := r.Client.List(ctx, &attachments, client.InNamespace(namespace), client.HasLabels{label}); err != nil {
		return fmt.Errorf("list selected attachments: %w", err)
	}
	for i := range attachments.Items {
		attachment := &attachments.Items[i]
		if keep[client.ObjectKeyFromObject(attachment)] {
			continue
		}
		patch := client.MergeFrom(attachment.DeepCopy())
		delete(attachment.Labels, label)
		if err := r.Client.Patch(ctx, attachment, patch); err != nil {
			return fmt.Errorf("deselect attachment %s: %w", attachment.Name, err)
		}
	}
	return nil
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) withdraw(ctx context.Context, key types.NamespacedName) error {
	service := &networkv1alpha1.NetworkService{}
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.GatewayNamespace, Name: key.Name}, service)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return fmt.Errorf("read gateway service %s: %w", key.Name, err)
	case service.Labels[nlbProgrammingManagedByLabel] != key.Name ||
		service.Annotations[nlbProgrammingIntentNamespaceAnno] != key.Namespace:
		return fmt.Errorf("gateway service %s/%s is not managed for intent %s", r.GatewayNamespace, key.Name, key)
	default:
		if err := r.Client.Delete(ctx, service); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("delete gateway service %s: %w", key.Name, err)
		}
	}
	return r.unlabelBackends(ctx, key.Namespace, networkLoadBalancerBackendLabel(key.Name), nil)
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) location(ctx context.Context) (string, error) {
	if r.resolveLocation != nil {
		return r.resolveLocation(ctx)
	}
	identity, err := resolveLocationIdentity(ctx, r.Client, r.Location)
	if err != nil {
		var unresolved *locationidentity.LocationUnresolved
		if errors.As(err, &unresolved) {
			return "", nil
		}
		return "", err
	}
	return identity.Reference.Name, nil
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) intentsInNamespace(ctx context.Context, obj client.Object) []reconcile.Request {
	var intents networkingv1alpha.NetworkLoadBalancerCellIntentList
	if err := r.Client.List(ctx, &intents, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(intents.Items))
	for _, intent := range intents.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&intent)})
	}
	return requests
}

func intentForGatewayService(_ context.Context, obj client.Object) []reconcile.Request {
	name := obj.GetLabels()[nlbProgrammingManagedByLabel]
	namespace := obj.GetAnnotations()[nlbProgrammingIntentNamespaceAnno]
	if name == "" || namespace == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}}
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.GatewayNamespace == "" {
		return errors.New("a gateway namespace is required")
	}
	r.Client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkLoadBalancerCellIntent{}).
		Watches(&networkingv1alpha.NetworkInterface{}, handler.EnqueueRequestsFromMapFunc(r.intentsInNamespace)).
		Watches(&cloudv1alpha1.VPCAttachment{}, handler.EnqueueRequestsFromMapFunc(r.intentsInNamespace)).
		Watches(&networkv1alpha1.NetworkService{}, handler.EnqueueRequestsFromMapFunc(intentForGatewayService)).
		Named("networkloadbalancercellprogramming").
		Complete(r)
}
