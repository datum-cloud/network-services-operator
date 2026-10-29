// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
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
	nlbProgrammingBackendLabelPrefix = "nlb.networking.datumapis.com/"
	nlbProgrammingBackendLabelValue  = "backend"
	nlbProgrammingRetry              = 30 * time.Second
)

var nlbPartialBackendReasons = map[string]bool{"BackendsUnhealthy": true, "BackendsUnresolved": true}

type NetworkLoadBalancerCellProgrammingReconciler struct {
	HubCluster             cluster.Cluster
	Client                 client.Client
	APIReader              client.Reader
	Scheme                 *runtime.Scheme
	Location               config.LocationConfig
	MaxExposedPorts        int32
	SharedAddressListeners bool
	UDPPortRanges          bool
	IPv4VIPs               bool

	resolveLocation func(context.Context) (string, error)
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancercellintents,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancercellintents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=vpcattachments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=networkgateways,verbs=get;list;watch
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=networkserviceaddresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=galactic.datumapis.com,resources=networkservices,verbs=get;list;watch;create;update;patch;delete

func (r *NetworkLoadBalancerCellProgrammingReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var intent networkingv1alpha.NetworkLoadBalancerCellIntent
	if err := r.Client.Get(ctx, req.NamespacedName, &intent); err != nil {
		if apierrors.IsNotFound(err) {
			return r.withdrawResult(ctx, req.NamespacedName)
		}
		return ctrl.Result{}, err
	}

	if intent.Spec.Allocation != nil {
		return r.reconcileAllocationAuthority(ctx, &intent)
	}
	location, err := r.location(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	if location == "" || intent.Spec.Location != location {
		return r.withdrawResult(ctx, req.NamespacedName)
	}
	if !intent.DeletionTimestamp.IsZero() || !cellIntentRequestsServing(&intent) {
		if done, err := r.withdraw(ctx, req.NamespacedName); err != nil || !done {
			return ctrl.Result{RequeueAfter: nlbProgrammingRetry}, err
		}
		return ctrl.Result{}, r.reportNotServed(ctx, &intent, "Withdrawn", "The intent is withdrawn")
	}

	if reason, err := r.requiredCapabilities(ctx, &intent); err != nil {
		return ctrl.Result{}, err
	} else if reason != "" {
		if done, err := r.withdraw(ctx, req.NamespacedName); err != nil || !done {
			return ctrl.Result{RequeueAfter: nlbProgrammingRetry}, err
		}
		return ctrl.Result{RequeueAfter: nlbProgrammingRetry}, r.reportNotServed(ctx, &intent, "UnsupportedCapability", reason)
	}
	backends, rejected, err := r.verifiedBackends(ctx, &intent)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, reason := range rejected {
		logger.Info("backend excluded", "intent", req.NamespacedName, "reason", reason)
	}
	service, reason := desiredNetworkLoadBalancerService(&intent, backends, r.IPv4VIPs)
	if service == nil {
		logger.Info("intent not served", "intent", req.NamespacedName, "reason", reason)
		if done, err := r.withdraw(ctx, req.NamespacedName); err != nil || !done {
			return ctrl.Result{RequeueAfter: nlbProgrammingRetry}, err
		}
		if err := r.reportNotServed(ctx, &intent, "NotServed", reason); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: nlbProgrammingRetry}, nil
	}

	if err := r.ensureAddress(ctx, service); err != nil {
		if done, withdrawErr := r.withdraw(ctx, req.NamespacedName); withdrawErr != nil || !done {
			return ctrl.Result{RequeueAfter: nlbProgrammingRetry}, withdrawErr
		}
		return ctrl.Result{RequeueAfter: nlbProgrammingRetry}, r.reportNotServed(ctx, &intent, "AddressInventoryConflict", err.Error())
	}
	if err := r.labelBackends(ctx, &intent, backends); err != nil {
		return ctrl.Result{}, err
	}
	current := &networkv1alpha1.NetworkService{ObjectMeta: metav1.ObjectMeta{Namespace: service.Namespace, Name: service.Name}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, current, func() error {
		if !current.CreationTimestamp.IsZero() && !metav1.IsControlledBy(current, &intent) {
			return fmt.Errorf("gateway service %s belongs to another owner", current.Name)
		}
		if r.APIReader != nil {
			var latest networkingv1alpha.NetworkLoadBalancerCellIntent
			if err := r.APIReader.Get(ctx, req.NamespacedName, &latest); err != nil {
				return err
			}
			if latest.UID != intent.UID || !latest.DeletionTimestamp.IsZero() || !equality.Semantic.DeepEqual(latest.Spec, intent.Spec) {
				return fmt.Errorf("serving intent changed during verification")
			}
		}
		current.Spec = service.Spec
		return controllerutil.SetControllerReference(&intent, current, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("write gateway service %s: %w", service.Name, err)
	}
	return ctrl.Result{}, r.reportServed(ctx, &intent, current)
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) reportServed(
	ctx context.Context,
	intent *networkingv1alpha.NetworkLoadBalancerCellIntent,
	service *networkv1alpha1.NetworkService,
) error {
	programmed := servedProgrammedCondition(service)
	announced := gatewayServiceCondition(service, "Advertised")
	if intent.Spec.VIPFamily == networkingv1alpha.IPv4Protocol {
		announced = gatewayServiceCondition(service, "IPv4Served")
	}
	return r.reportStatus(ctx, intent, programmed, announced)
}

func servedProgrammedCondition(service *networkv1alpha1.NetworkService) metav1.Condition {
	ready := gatewayServiceCondition(service, "Ready")
	if ready.Status != metav1.ConditionFalse || !nlbPartialBackendReasons[ready.Reason] {
		return ready
	}
	loaded := gatewayServiceCondition(service, "Programmed")
	backends := gatewayServiceCondition(service, "BackendsReady")
	if loaded.Status == metav1.ConditionTrue && backends.Status == metav1.ConditionFalse && nlbPartialBackendReasons[backends.Reason] {
		return metav1.Condition{Status: metav1.ConditionTrue, Reason: backends.Reason, Message: backends.Message}
	}
	return ready
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) reportNotServed(
	ctx context.Context,
	intent *networkingv1alpha.NetworkLoadBalancerCellIntent,
	reason, message string,
) error {
	condition := metav1.Condition{Status: metav1.ConditionFalse, Reason: reason, Message: message}
	return r.reportStatus(ctx, intent, condition, condition)
}

func gatewayServiceCondition(service *networkv1alpha1.NetworkService, conditionType string) metav1.Condition {
	reported := apimeta.FindStatusCondition(service.Status.Conditions, conditionType)
	if reported == nil || reported.ObservedGeneration != service.Generation {
		return metav1.Condition{Status: metav1.ConditionUnknown, Reason: "Pending", Message: "The gateways have not reported this service"}
	}
	return metav1.Condition{Status: reported.Status, Reason: reported.Reason, Message: reported.Message}
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) reportStatus(
	ctx context.Context,
	intent *networkingv1alpha.NetworkLoadBalancerCellIntent,
	programmed, announced metav1.Condition,
) error {
	before := intent.DeepCopy()
	intent.Status.ObservedGeneration = intent.Generation
	programmed.Type = networkingv1alpha.NetworkLoadBalancerCellIntentProgrammed
	announced.Type = networkingv1alpha.NetworkLoadBalancerCellIntentAnnounced
	for _, condition := range []metav1.Condition{programmed, announced} {
		condition.ObservedGeneration = intent.Generation
		apimeta.SetStatusCondition(&intent.Status.Conditions, condition)
	}
	if equality.Semantic.DeepEqual(before.Status, intent.Status) {
		return nil
	}
	if err := r.Client.Status().Patch(ctx, intent, client.MergeFrom(before)); err != nil {
		return fmt.Errorf("report intent %s status: %w", intent.Name, err)
	}
	return nil
}

type verifiedNetworkLoadBalancerBackend struct {
	attachment *cloudv1alpha1.VPCAttachment
	port       int32
	portRange  *networkingv1alpha.PortRange
	probe      networkingv1alpha.NetworkLoadBalancerCellIntentProbe
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
		verified = append(verified, verifiedNetworkLoadBalancerBackend{attachment: attachment, port: backend.Port, portRange: backend.PortRange, probe: backend.Probe})
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
	ipv4VIPs bool,
) (*networkv1alpha1.NetworkService, string) {
	if reason := validateAddressGroup(intent); reason != "" {
		return nil, reason
	}
	if len(backends) == 0 {
		return nil, "no backend passed verification"
	}
	switch intent.Spec.VIPFamily {
	case networkingv1alpha.IPv6Protocol:
	case networkingv1alpha.IPv4Protocol:
		if !ipv4VIPs {
			return nil, "IPv4 addresses are not served in this location"
		}
	default:
		return nil, fmt.Sprintf("address family %q is not served", intent.Spec.VIPFamily)
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
	probe := backends[0].probe
	if probe.Protocol != "TCP" || probe.Port < 1 || probe.Port > 65535 || probe.Path != "" {
		return nil, "serving requires a valid TCP health probe; HTTP and HTTPS probes are not supported by this dataplane"
	}
	frontend := networkingv1alpha.PortInterval(intent.Spec.ListenerPort, intent.Spec.ListenerPortRange)
	if !frontend.Valid() {
		return nil, "invalid frontend port interval"
	}
	if !frontend.Singleton() && (protocol != networkv1alpha1.NetworkServiceProtocolUDP || intent.Spec.AddressGroup == nil) {
		return nil, "UDP ranges require a complete address inventory"
	}
	for _, backend := range backends {
		interval := networkingv1alpha.PortInterval(backend.port, backend.portRange)
		if !interval.Valid() || (frontend.Singleton() && !interval.Singleton()) || (!frontend.Singleton() && interval != frontend) {
			return nil, "backend port interval does not match the forwarding contract"
		}
	}
	vpc, port := backends[0].attachment.Status.VPC, networkingv1alpha.PortInterval(backends[0].port, backends[0].portRange).Start
	for _, backend := range backends[1:] {
		if backend.probe != probe {
			return nil, "backends require different health probes"
		}
		if backend.attachment.Status.VPC != vpc {
			return nil, "backends span more than one VPC"
		}
		if networkingv1alpha.PortInterval(backend.port, backend.portRange).Start != port {
			return nil, "backends listen on different ports"
		}
	}
	service := &networkv1alpha1.NetworkService{
		ObjectMeta: metav1.ObjectMeta{Namespace: intent.Namespace, Name: intent.Name},
		Spec: networkv1alpha1.NetworkServiceSpec{
			VPCRef:          vpc,
			VIPAddresses:    []string{intent.Spec.VIP},
			Protocol:        protocol,
			Port:            frontend.Start,
			AddressGroup:    galacticAddressGroup(intent.Spec.AddressGroup),
			BackendPort:     port,
			SessionAffinity: networkv1alpha1.NetworkServiceSessionAffinityNone,
			HealthCheck: &networkv1alpha1.NetworkServiceHealthCheck{
				FailurePolicy: networkv1alpha1.NetworkServiceHealthFailClosed, Port: probe.Port, IntervalSeconds: 5, TimeoutSeconds: 2, HealthyThreshold: 2, UnhealthyThreshold: 1,
			},
			BackendSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				networkLoadBalancerBackendLabel(intent.Name): nlbProgrammingBackendLabelValue,
			}},
		},
	}
	if !frontend.Singleton() {
		service.Spec.Port = 0
		service.Spec.BackendPort = 0
		service.Spec.PortRange = &networkv1alpha1.PortRange{Start: frontend.Start, End: frontend.End}
	}
	return service, ""
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

func (r *NetworkLoadBalancerCellProgrammingReconciler) withdraw(ctx context.Context, key types.NamespacedName) (bool, error) {
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	service := &networkv1alpha1.NetworkService{}
	err := reader.Get(ctx, key, service)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.unlabelBackends(ctx, key.Namespace, networkLoadBalancerBackendLabel(key.Name), nil); err != nil {
			return false, err
		}
		return r.cleanupAddresses(ctx, key.Namespace)
	case err != nil:
		return false, fmt.Errorf("read gateway service %s: %w", key, err)
	case !ownedByCellIntent(service, key.Name):
		return false, fmt.Errorf("gateway service %s is not owned by its cell intent", key)
	}
	if service.DeletionTimestamp.IsZero() {
		if err := r.Client.Delete(ctx, service, client.Preconditions{UID: &service.UID}); client.IgnoreNotFound(err) != nil {
			return false, fmt.Errorf("delete gateway service %s: %w", key, err)
		}
	}
	// A successful delete request does not acknowledge gateway teardown.
	// Retain selectors and status until Galactic's finalizers have completed.
	if err := reader.Get(ctx, key, &networkv1alpha1.NetworkService{}); apierrors.IsNotFound(err) {
		if err := r.unlabelBackends(ctx, key.Namespace, networkLoadBalancerBackendLabel(key.Name), nil); err != nil {
			return false, err
		}
		return r.cleanupAddresses(ctx, key.Namespace)
	} else if err != nil {
		return false, err
	}
	return false, nil
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) withdrawResult(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	done, err := r.withdraw(ctx, key)
	if err != nil || !done {
		return ctrl.Result{RequeueAfter: nlbProgrammingRetry}, err
	}
	return ctrl.Result{}, nil
}

func ownedByCellIntent(service *networkv1alpha1.NetworkService, intentName string) bool {
	owner := metav1.GetControllerOf(service)
	return owner != nil && owner.Kind == "NetworkLoadBalancerCellIntent" && owner.Name == intentName
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

func (r *NetworkLoadBalancerCellProgrammingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.HubCluster == nil {
		return fmt.Errorf("NLB serving requires an authoritative federation hub client")
	}
	r.Client = mgr.GetClient()
	r.APIReader = mgr.GetAPIReader()
	r.Scheme = mgr.GetScheme()
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkLoadBalancerCellIntent{}).
		Watches(&networkingv1alpha.NetworkInterface{}, handler.EnqueueRequestsFromMapFunc(r.intentsInNamespace)).
		Watches(&cloudv1alpha1.VPCAttachment{}, handler.EnqueueRequestsFromMapFunc(r.intentsInNamespace)).
		Watches(&networkv1alpha1.NetworkServiceAddress{}, handler.EnqueueRequestsFromMapFunc(r.intentsInNamespace)).
		Watches(&networkv1alpha1.NetworkGateway{}, handler.EnqueueRequestsFromMapFunc(r.allIntents)).
		Owns(&networkv1alpha1.NetworkService{}).
		Named("networkloadbalancercellprogramming").
		Complete(r)
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) allIntents(ctx context.Context, _ client.Object) []reconcile.Request {
	var intents networkingv1alpha.NetworkLoadBalancerCellIntentList
	if err := r.Client.List(ctx, &intents); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(intents.Items))
	for _, intent := range intents.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&intent)})
	}
	return requests
}
