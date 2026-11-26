// SPDX-License-Identifier: AGPL-3.0-only

package admissionpolicy_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/kustomize/api/krusty"
	kustypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"

	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	operator  = "system:serviceaccount:network-services-operator-system:controller-manager"
	provider  = "system:serviceaccount:fleet-system:fleet-provider"
	realizer  = "system:serviceaccount:physical-realizer:realizer"
	compute   = "system:serviceaccount:compute-system:compute"
	breakGlas = "physical-attachment-break-glass"

	customerNamespace = "ns-customer-example"
	providerNamespace = "ns-provider-example"
	unlabelled        = "ns-unlabelled"

	componentDir = "../../config/components/physical-attachment-admission"
)

type env struct {
	t      *testing.T
	ctx    context.Context
	cfg    *rest.Config
	admin  client.Client
	scheme *runtime.Scheme
}

func startEnv(t *testing.T) *env {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest assets are required")
	}

	names := []string{"networkinterfaceclaims", "networkinterfaces"}
	crds := make([]*apiextensionsv1.CustomResourceDefinition, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "networking.datumapis.com_"+name+".yaml"))
		require.NoError(t, err)
		var crd apiextensionsv1.CustomResourceDefinition
		require.NoError(t, yaml.Unmarshal(data, &crd))
		crds = append(crds, &crd)
	}

	testEnv := &envtest.Environment{CRDs: crds}
	cfg, err := testEnv.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, testEnv.Stop()) })

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	admin, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	e := &env{t: t, ctx: context.Background(), cfg: cfg, admin: admin, scheme: scheme}

	for name, project := range map[string]string{
		customerNamespace: "cluster-customer-example",
		providerNamespace: "cluster-_provider-example",
		unlabelled:        "",
	} {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		if project != "" {
			ns.Labels = map[string]string{"meta.datumapis.com/upstream-cluster-name": project}
		}
		require.NoError(t, admin.Create(e.ctx, ns))
	}

	require.NoError(t, admin.Create(e.ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "everyone-is-admin"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "cluster-admin"},
		Subjects: []rbacv1.Subject{
			{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: operator},
			{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: provider},
			{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: realizer},
			{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: compute},
			{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: "an-operator"},
		},
	}))

	e.applyComponent()
	return e
}

func (e *env) applyComponent() {
	e.t.Helper()
	for _, file := range []string{"physical-attachment-policy.yaml"} {
		data, err := os.ReadFile(filepath.Join(componentDir, file))
		require.NoError(e.t, err)
		decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
		for {
			obj := &unstructured.Unstructured{}
			if err := decoder.Decode(&obj.Object); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(e.t, err)
			}
			if len(obj.Object) == 0 {
				continue
			}
			require.NoError(e.t, e.admin.Create(e.ctx, obj))
		}
	}
}

// setLists replaces the policy's list variables the way a deployment's patch
// would, by index.
func (e *env) setLists(lists map[int]string) {
	e.t.Helper()
	policy := &unstructured.Unstructured{}
	policy.SetAPIVersion("admissionregistration.k8s.io/v1")
	policy.SetKind("ValidatingAdmissionPolicy")
	require.NoError(e.t, e.admin.Get(e.ctx, client.ObjectKey{Name: "physical-attachment-writers"}, policy))
	variables, found, err := unstructured.NestedSlice(policy.Object, "spec", "variables")
	require.NoError(e.t, err)
	require.True(e.t, found)
	for index, expression := range lists {
		variables[index].(map[string]any)["expression"] = expression
	}
	require.NoError(e.t, unstructured.SetNestedSlice(policy.Object, variables, "spec", "variables"))
	require.NoError(e.t, e.admin.Update(e.ctx, policy))
}

func (e *env) as(user string, groups ...string) client.Client {
	e.t.Helper()
	cfg := rest.CopyConfig(e.cfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: append([]string{"system:authenticated"}, groups...)}
	cl, err := client.New(cfg, client.Options{Scheme: e.scheme})
	require.NoError(e.t, err)
	return cl
}

func physicalClaim(namespace, name string) *networkingv1alpha.NetworkInterfaceClaim {
	return &networkingv1alpha.NetworkInterfaceClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: networkingv1alpha.NetworkInterfaceClaimSpec{
			Network:        networkingv1alpha.LocalNetworkRef{Name: "app"},
			AttachmentMode: networkingv1alpha.NetworkInterfaceAttachmentModePhysical,
			AttachmentPoint: &networkingv1alpha.NetworkInterfaceAttachmentPoint{
				MACAddress: "00:00:5e:00:53:01", Switch: "rack-a1-tor-1", Port: "Ethernet1/12",
			},
		},
	}
}

func virtualClaim(namespace, name string) *networkingv1alpha.NetworkInterfaceClaim {
	return &networkingv1alpha.NetworkInterfaceClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       networkingv1alpha.NetworkInterfaceClaimSpec{Network: networkingv1alpha.LocalNetworkRef{Name: "app"}},
	}
}

// A refusal is attributed to this policy by name, so a schema rejection of the
// same request cannot pass for one.
func refusedByPolicy(err error) bool {
	return err != nil && strings.Contains(err.Error(), "ValidatingAdmissionPolicy 'physical-attachment-writers'")
}

func requireRefused(t *testing.T, err error) {
	t.Helper()
	require.True(t, refusedByPolicy(err), "expected the physical attachment policy to refuse, got %v", err)
}

// The policy takes effect a moment after it is created, so the first refusal
// is waited for rather than assumed.
func (e *env) waitForPolicy() {
	e.t.Helper()
	intruder := e.as(compute)
	require.Eventually(e.t, func() bool {
		claim := physicalClaim(customerNamespace, "policy-probe")
		err := intruder.Create(e.ctx, claim)
		if err == nil {
			_ = e.admin.Delete(e.ctx, claim)
		}
		return refusedByPolicy(err)
	}, 30*time.Second, 200*time.Millisecond)
}

func (e *env) configure() {
	e.t.Helper()
	e.waitForPolicy()
	e.setLists(map[int]string{
		1: "['" + provider + "']",
		2: "['" + realizer + "']",
		3: "['" + breakGlas + "']",
		4: "['provider-example']",
	})
	probe := e.as(provider)
	require.Eventually(e.t, func() bool {
		claim := physicalClaim(customerNamespace, "lists-probe")
		if err := probe.Create(e.ctx, claim); err != nil {
			return false
		}
		require.NoError(e.t, e.admin.Delete(e.ctx, claim))
		return true
	}, 30*time.Second, 200*time.Millisecond)
}

func TestOnlyProvidersWritePhysicalClaims(t *testing.T) {
	e := startEnv(t)
	e.configure()

	require.NoError(t, e.as(provider).Create(e.ctx, physicalClaim(customerNamespace, "by-provider")))
	requireRefused(t, e.as(compute).Create(e.ctx, physicalClaim(customerNamespace, "by-compute")))
	requireRefused(t, e.as(realizer).Create(e.ctx, physicalClaim(customerNamespace, "by-realizer")))
	requireRefused(t, e.as("an-operator", breakGlas).Create(e.ctx, physicalClaim(customerNamespace, "by-breakglass")))

	require.NoError(t, e.as(compute).Create(e.ctx, virtualClaim(customerNamespace, "virtual-by-compute")),
		"a claim in any other mode is admitted as before")

	var claim networkingv1alpha.NetworkInterfaceClaim
	require.NoError(t, e.admin.Get(e.ctx, client.ObjectKey{Namespace: customerNamespace, Name: "by-provider"}, &claim))
	claim.Labels = map[string]string{"example.com/touched": "true"}
	requireRefused(t, e.as(compute).Update(e.ctx, &claim))
}

func TestPhysicalClaimsAreRefusedUntilConfigured(t *testing.T) {
	e := startEnv(t)
	e.waitForPolicy()

	requireRefused(t, e.as(provider).Create(e.ctx, physicalClaim(customerNamespace, "before-configured")))
	require.NoError(t, e.as(operator).Create(e.ctx, physicalClaim(customerNamespace, "by-operator")),
		"the operator is listed by default")
}

// The policy matches only physical objects and the detach finalizer, so a list
// left wrong can only stop physical attachment.
func TestOtherClaimsAreLeftAlone(t *testing.T) {
	e := startEnv(t)
	e.waitForPolicy()

	require.NoError(t, e.as(compute).Create(e.ctx, virtualClaim(customerNamespace, "virtual")))

	withDetach := virtualClaim(customerNamespace, "virtual-with-detach")
	withDetach.Finalizers = []string{networkingv1alpha.NetworkInterfaceClaimDetachFinalizer}
	requireRefused(t, e.as(compute).Create(e.ctx, withDetach))
}

func TestRealizerAndBreakGlassMayUpdateButNotCreate(t *testing.T) {
	e := startEnv(t)
	e.configure()

	require.NoError(t, e.as(provider).Create(e.ctx, physicalClaim(customerNamespace, "touched")))
	key := client.ObjectKey{Namespace: customerNamespace, Name: "touched"}

	for _, cl := range []client.Client{e.as(realizer), e.as("an-operator", breakGlas)} {
		var claim networkingv1alpha.NetworkInterfaceClaim
		require.NoError(t, e.admin.Get(e.ctx, key, &claim))
		claim.Labels = map[string]string{"example.com/seen": time.Now().Format("150405.000000")}
		require.NoError(t, cl.Update(e.ctx, &claim))
	}
}

func TestOnlyTheRealizerRemovesTheDetachFinalizer(t *testing.T) {
	e := startEnv(t)
	e.configure()

	seed := func(name string) client.ObjectKey {
		require.NoError(t, e.as(provider).Create(e.ctx, physicalClaim(customerNamespace, name)))
		key := client.ObjectKey{Namespace: customerNamespace, Name: name}
		var claim networkingv1alpha.NetworkInterfaceClaim
		require.NoError(t, e.admin.Get(e.ctx, key, &claim))
		claim.Finalizers = append(claim.Finalizers, networkingv1alpha.NetworkInterfaceClaimDetachFinalizer)
		require.NoError(t, e.as(operator).Update(e.ctx, &claim))
		return key
	}
	strip := func(cl client.Client, key client.ObjectKey) error {
		var claim networkingv1alpha.NetworkInterfaceClaim
		require.NoError(t, e.admin.Get(e.ctx, key, &claim))
		claim.Finalizers = nil
		return cl.Update(e.ctx, &claim)
	}

	requireRefused(t, strip(e.as(provider), seed("provider-strips")))
	requireRefused(t, strip(e.as(compute), seed("compute-strips")))
	require.NoError(t, strip(e.as(realizer), seed("realizer-strips")))
	require.NoError(t, strip(e.as("an-operator", breakGlas), seed("breakglass-strips")))
	require.NoError(t, strip(e.as(operator), seed("operator-strips")))
}

func TestOnlyTheOperatorAddsTheDetachFinalizer(t *testing.T) {
	e := startEnv(t)
	e.configure()

	withFinalizer := physicalClaim(customerNamespace, "born-with-finalizer")
	withFinalizer.Finalizers = []string{networkingv1alpha.NetworkInterfaceClaimDetachFinalizer}
	requireRefused(t, e.as(provider).Create(e.ctx, withFinalizer))

	require.NoError(t, e.as(provider).Create(e.ctx, physicalClaim(customerNamespace, "gains-finalizer")))
	var claim networkingv1alpha.NetworkInterfaceClaim
	require.NoError(t, e.admin.Get(e.ctx, client.ObjectKey{Namespace: customerNamespace, Name: "gains-finalizer"}, &claim))
	claim.Finalizers = []string{networkingv1alpha.NetworkInterfaceClaimDetachFinalizer}
	requireRefused(t, e.as(realizer).Update(e.ctx, &claim))
}

// The operator adds both finalizers in one write when it first sees a physical
// claim, and removes the detach finalizer itself from a deleted claim no port
// ever carried. Both must stay admitted under a configured policy.
func TestTheOperatorsOwnFinalizerWritesAreAdmitted(t *testing.T) {
	e := startEnv(t)
	e.configure()

	require.NoError(t, e.as(provider).Create(e.ctx, physicalClaim(customerNamespace, "operator-finalizers")))
	key := client.ObjectKey{Namespace: customerNamespace, Name: "operator-finalizers"}
	var claim networkingv1alpha.NetworkInterfaceClaim
	require.NoError(t, e.admin.Get(e.ctx, key, &claim))
	claim.Finalizers = []string{
		"networking.datumapis.com/networkinterfaceclaim-release",
		networkingv1alpha.NetworkInterfaceClaimDetachFinalizer,
	}
	require.NoError(t, e.as(operator).Update(e.ctx, &claim))

	require.NoError(t, e.as(provider).Delete(e.ctx, &claim))
	require.NoError(t, e.admin.Get(e.ctx, key, &claim))
	require.False(t, claim.DeletionTimestamp.IsZero())
	claim.Finalizers = []string{"networking.datumapis.com/networkinterfaceclaim-release"}
	require.NoError(t, e.as(operator).Update(e.ctx, &claim))
	claim.Finalizers = nil
	require.NoError(t, e.as(operator).Update(e.ctx, &claim))
}

func TestBootClaimsOnlyInProviderProjects(t *testing.T) {
	e := startEnv(t)
	e.configure()

	boot := func(namespace, name string) *networkingv1alpha.NetworkInterfaceClaim {
		claim := physicalClaim(namespace, name)
		claim.Spec.Purpose = networkingv1alpha.NetworkInterfacePurposeBoot
		return claim
	}

	require.NoError(t, e.as(provider).Create(e.ctx, boot(providerNamespace, "boot-in-provider")),
		"the label decodes to the listed project")
	requireRefused(t, e.as(provider).Create(e.ctx, boot(customerNamespace, "boot-in-customer")))
	requireRefused(t, e.as(provider).Create(e.ctx, boot(unlabelled, "boot-unlabelled")))
	require.NoError(t, e.as(provider).Create(e.ctx, physicalClaim(customerNamespace, "network-in-customer")))
}

func TestOnlyTheOperatorWritesPhysicalInterfaces(t *testing.T) {
	e := startEnv(t)
	e.configure()

	iface := func(name string) *networkingv1alpha.NetworkInterface {
		return &networkingv1alpha.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{Namespace: customerNamespace, Name: name},
			Spec: networkingv1alpha.NetworkInterfaceSpec{
				Network:        networkingv1alpha.LocalNetworkRef{Name: "app"},
				AttachmentMode: networkingv1alpha.NetworkInterfaceAttachmentModePhysical,
				AttachmentPoint: &networkingv1alpha.NetworkInterfaceAttachmentPoint{
					MACAddress: "00:00:5e:00:53:01", Switch: "rack-a1-tor-1", Port: "Ethernet1/12",
				},
			},
		}
	}

	require.NoError(t, e.as(operator).Create(e.ctx, iface("by-operator")))
	requireRefused(t, e.as(provider).Create(e.ctx, iface("by-provider")))
	requireRefused(t, e.as(realizer).Create(e.ctx, iface("by-realizer")))
}

func TestBootClaimsSurviveAProviderProjectChange(t *testing.T) {
	e := startEnv(t)
	e.configure()

	claim := physicalClaim(providerNamespace, "boot-then-unlisted")
	claim.Spec.Purpose = networkingv1alpha.NetworkInterfacePurposeBoot
	require.NoError(t, e.as(provider).Create(e.ctx, claim))
	key := client.ObjectKeyFromObject(claim)

	var stored networkingv1alpha.NetworkInterfaceClaim
	require.NoError(t, e.admin.Get(e.ctx, key, &stored))
	stored.Finalizers = []string{networkingv1alpha.NetworkInterfaceClaimDetachFinalizer}
	require.NoError(t, e.as(operator).Update(e.ctx, &stored))

	e.setLists(map[int]string{4: "[]"})
	require.Eventually(t, func() bool {
		probe := physicalClaim(providerNamespace, "boot-probe")
		probe.Spec.Purpose = networkingv1alpha.NetworkInterfacePurposeBoot
		err := e.as(provider).Create(e.ctx, probe)
		if err == nil {
			_ = e.admin.Delete(e.ctx, probe)
		}
		return refusedByPolicy(err)
	}, 30*time.Second, 200*time.Millisecond)

	require.NoError(t, e.admin.Get(e.ctx, key, &stored))
	stored.Finalizers = nil
	require.NoError(t, e.as(realizer).Update(e.ctx, &stored),
		"an existing boot claim must still release after its project leaves the list")
}

// The policy exempts the operator by username, and that username comes from the
// service account the cell bundle runs as.
func TestOperatorDefaultMatchesTheCellServiceAccount(t *testing.T) {
	opts := krusty.MakeDefaultOptions()
	opts.LoadRestrictions = kustypes.LoadRestrictionsNone
	resources, err := krusty.MakeKustomizer(opts).Run(filesys.MakeFsOnDisk(), filepath.Join("..", "..", "config", "cell"))
	require.NoError(t, err)

	var serviceAccount, namespace string
	var operators []string
	for _, resource := range resources.Resources() {
		switch resource.GetKind() {
		case "Deployment":
			name, err := resource.GetString("spec.template.spec.serviceAccountName")
			require.NoError(t, err)
			serviceAccount, namespace = name, resource.GetNamespace()
		case "ValidatingAdmissionPolicy":
			if resource.GetName() != "physical-attachment-writers" {
				continue
			}
			expression, err := resource.GetString("spec.variables[0].expression")
			require.NoError(t, err)
			operators = append(operators, expression)
		}
	}
	require.NotEmpty(t, serviceAccount)
	require.Equal(t, []string{"['system:serviceaccount:" + namespace + ":" + serviceAccount + "']"}, operators)
}
