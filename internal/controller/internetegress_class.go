// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// internetEgressUnavailable says no egress intent can be written for a
// network, and carries the message a consumer reads on the network context.
//
// It is the only failure this resolution has: a class that cannot be resolved
// is not a temporary error to retry quietly, it is an answer a consumer has to
// be told.
type internetEgressUnavailable struct {
	message string
}

func (e *internetEgressUnavailable) Error() string {
	return e.message
}

func unresolvedInternetEgress(format string, args ...any) *internetEgressUnavailable {
	return &internetEgressUnavailable{message: fmt.Sprintf(format, args...)}
}

// resolveInternetEgress builds the egress intent one location is instructed
// with, from what the network declares and what the serving class provides.
//
// Nothing downstream of this selects a class, picks a default, or reads the
// class at all, so every ambiguity has to be settled here or refused here.
//
// A network declaring nothing resolves to nothing, which is not the same as a
// network that reaches nothing: the first carries no instruction and the second
// carries Disabled.
func resolveInternetEgress(
	ctx context.Context,
	cl client.Reader,
	network *networkingv1alpha.Network,
) (*networkingv1alpha.NetworkContextInternetEgress, error) {
	if network.Spec.Egress == nil || network.Spec.Egress.Internet == nil {
		return nil, nil
	}
	declared := network.Spec.Egress.Internet

	switch declared.Mode {
	case networkingv1alpha.NetworkInternetEgressDisabled:
		// A network that reaches nothing needs no class, so disabling egress
		// never depends on an operator having defined one.
		return &networkingv1alpha.NetworkContextInternetEgress{
			Mode: networkingv1alpha.NetworkInternetEgressDisabled,
		}, nil
	case networkingv1alpha.NetworkInternetEgressEnabled:
	default:
		return nil, nil
	}

	class, err := resolveInternetEgressClass(ctx, cl, declared.Class)
	if err != nil {
		return nil, err
	}

	reach := reachServedBy(declared.Reach, class.Spec.Reach)
	if len(reach) == 0 {
		return nil, unresolvedInternetEgress(
			"InternetEgressClass %q reaches none of the address families network %q declares.",
			class.Name, network.Name)
	}

	return &networkingv1alpha.NetworkContextInternetEgress{
		Mode:          networkingv1alpha.NetworkInternetEgressEnabled,
		Reach:         reach,
		ClassName:     class.Name,
		Sharing:       class.Spec.Sharing,
		ParametersRef: class.Spec.ParametersRef.DeepCopy(),
	}, nil
}

// resolveInternetEgressClass finds the class serving a network.
//
// A network naming a class is served by that class or by nothing. Falling back
// to the default would put traffic on a path the consumer did not ask for, and
// the name they did ask for would have gone unreported.
func resolveInternetEgressClass(
	ctx context.Context,
	cl client.Reader,
	named string,
) (*networkingv1alpha.InternetEgressClass, error) {
	if named != "" {
		var class networkingv1alpha.InternetEgressClass
		if err := cl.Get(ctx, client.ObjectKey{Name: named}, &class); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, unresolvedInternetEgress(
					"InternetEgressClass %q does not exist, and a network naming a class is served by no other.",
					named)
			}
			return nil, fmt.Errorf("failed reading internet egress class %q: %w", named, err)
		}
		return &class, nil
	}

	var classes networkingv1alpha.InternetEgressClassList
	if err := cl.List(ctx, &classes); err != nil {
		return nil, fmt.Errorf("failed listing internet egress classes: %w", err)
	}

	marked := make([]int, 0, len(classes.Items))
	for i := range classes.Items {
		if classes.Items[i].Annotations[networkingv1alpha.InternetEgressClassDefaultAnnotation] == "true" {
			marked = append(marked, i)
		}
	}

	switch len(marked) {
	case 1:
		return &classes.Items[marked[0]], nil
	case 0:
		return nil, unresolvedInternetEgress(
			"No InternetEgressClass is marked as the default, so a network naming no class has none to be served by.")
	default:
		// Two defaults are refused rather than broken by a rule invented here.
		// Any tie-break — the oldest, the first by name — would put a
		// consumer's traffic on a path chosen by an accident of ordering, and
		// silently move it the moment the other class is edited.
		names := make([]string, 0, len(marked))
		for _, i := range marked {
			names = append(names, classes.Items[i].Name)
		}
		slices.Sort(names)
		return nil, unresolvedInternetEgress(
			"InternetEgressClasses %s are each marked as the default, so which one serves a network naming no class is ambiguous.",
			strings.Join(quoted(names), ", "))
	}
}

// reachServedBy is what the network asked for and the class provides. A
// network asking for nothing in particular takes what the class reaches.
func reachServedBy(declared, served []networkingv1alpha.IPFamily) []networkingv1alpha.IPFamily {
	if len(declared) == 0 {
		return slices.Clone(served)
	}

	reach := make([]networkingv1alpha.IPFamily, 0, len(declared))
	for _, family := range declared {
		if slices.Contains(served, family) {
			reach = append(reach, family)
		}
	}
	return reach
}

func quoted(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, fmt.Sprintf("%q", value))
	}
	return out
}
