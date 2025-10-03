package validation

import (
	"fmt"
	"sort"
	"strings"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
)

func ValidateHTTPRoute(route *gatewayv1.HTTPRoute, opts config.HTTPRouteValidationOptions) field.ErrorList {

	allErrs := field.ErrorList{}

	// TODO(jreese) go through and prohibit use of experimental fields, confirm we
	// want to support extended fields.

	allErrs = append(allErrs, validateParentRefs(route, field.NewPath("spec", "parentRefs"))...)
	allErrs = append(allErrs, validateHTTPRouteHostnames(route, field.NewPath("spec", "hostnames"))...)
	allErrs = append(allErrs, validateHTTPRouteRules(route, field.NewPath("spec", "rules"), opts)...)
	return allErrs
}

func validateParentRefs(route *gatewayv1.HTTPRoute, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, parentRef := range route.Spec.ParentRefs {
		allErrs = append(allErrs, validateParentRef(route, parentRef, fldPath.Index(i))...)
	}

	return allErrs
}

func validateParentRef(route *gatewayv1.HTTPRoute, parentRef gatewayv1.ParentReference, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	// Only allow Group to be "gateway.networking.k8s.io"
	if ptr.Deref(parentRef.Group, gatewayv1.GroupName) != gatewayv1.GroupName {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("group"), parentRef.Group, `group must be unspecified or equal "gateway.networking.k8s.io"`))
	}

	// Only allow Kind to be "Gateway"
	if ptr.Deref(parentRef.Kind, "Gateway") != "Gateway" {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("kind"), parentRef.Kind, `kind must unspecified or equal "Gateway"`))
	}

	// Only allow Namespace to be nil, or the same as the HTTPRoute's namespace
	if ptr.Deref(parentRef.Namespace, gatewayv1.Namespace(route.Namespace)) != gatewayv1.Namespace(route.Namespace) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("namespace"), parentRef.Namespace, "namespace must be unspecified or the same as the HTTPRoute's namespace"))
	}

	return allErrs
}

func validateHTTPRouteHostnames(route *gatewayv1.HTTPRoute, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	// Do not allow hostnames to be set for now. We will allow them in the future.
	if len(route.Spec.Hostnames) > 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath, "hostnames are not permitted"))
	}

	return allErrs
}

func validateHTTPRouteRules(route *gatewayv1.HTTPRoute, fldPath *field.Path, opts config.HTTPRouteValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, rule := range route.Spec.Rules {
		allErrs = append(allErrs, validateHTTPRouteRule(route, rule, fldPath.Index(i), opts)...)
	}

	return allErrs
}

func validateHTTPRouteRule(route *gatewayv1.HTTPRoute, rule gatewayv1.HTTPRouteRule, fldPath *field.Path, opts config.HTTPRouteValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateFilters(rule.Filters, supportedHTTPRouteRuleFilters, fldPath.Child("filters"))...)
	allErrs = append(allErrs, validateHTTPRouteRuleBackendRefs(route, rule, fldPath.Child("backendRefs"), opts)...)

	return allErrs
}

var supportedHTTPRouteRuleFilters = sets.New(
	gatewayv1.HTTPRouteFilterRequestHeaderModifier,
	gatewayv1.HTTPRouteFilterResponseHeaderModifier,
	gatewayv1.HTTPRouteFilterRequestRedirect,
	gatewayv1.HTTPRouteFilterURLRewrite,
	gatewayv1.HTTPRouteFilterCORS,
	gatewayv1.HTTPRouteFilterExtensionRef,
)

var supportedHTTPBackendRefFilters = sets.New(
	gatewayv1.HTTPRouteFilterRequestHeaderModifier,
	gatewayv1.HTTPRouteFilterResponseHeaderModifier,
	gatewayv1.HTTPRouteFilterExtensionRef,
)

func validateFilters(filters []gatewayv1.HTTPRouteFilter, supportedFilters sets.Set[gatewayv1.HTTPRouteFilterType], fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, filter := range filters {
		allErrs = append(allErrs, validateHTTPRouteFilter(filter, supportedFilters, fldPath.Index(i))...)
	}

	return allErrs
}

func validateHTTPRouteFilter(filter gatewayv1.HTTPRouteFilter, supportedFilters sets.Set[gatewayv1.HTTPRouteFilterType], fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if !supportedFilters.Has(filter.Type) {
		allErrs = append(allErrs, field.NotSupported(fldPath.Child("type"), filter.Type, sets.List(supportedFilters)))
	}

	if filter.ExtensionRef != nil {
		extentionRefFieldPath := fldPath.Child("extensionRef")
		if filter.ExtensionRef.Group != envoygatewayv1alpha1.GroupName {
			allErrs = append(allErrs, field.NotSupported(extentionRefFieldPath.Child("group"), filter.ExtensionRef.Group, []string{envoygatewayv1alpha1.GroupName}))
		}
		if filter.ExtensionRef.Kind != envoygatewayv1alpha1.KindHTTPRouteFilter {
			allErrs = append(allErrs, field.NotSupported(extentionRefFieldPath.Child("kind"), filter.ExtensionRef.Kind, []string{envoygatewayv1alpha1.KindHTTPRouteFilter}))
		}
	}

	return allErrs
}

func validateHTTPRouteRuleBackendRefs(route *gatewayv1.HTTPRoute, rule gatewayv1.HTTPRouteRule, fldPath *field.Path, opts config.HTTPRouteValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, backendRef := range rule.BackendRefs {
		allErrs = append(allErrs, validateHTTPBackendRef(route, backendRef, fldPath.Index(i), opts)...)
	}

	return allErrs
}

func validateHTTPBackendRef(route *gatewayv1.HTTPRoute, backendRef gatewayv1.HTTPBackendRef, fldPath *field.Path, opts config.HTTPRouteValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	// Do I need to validate the name?

	allErrs = append(allErrs, validateBackendObjectReference(route, backendRef.BackendObjectReference, fldPath, opts)...)
	allErrs = append(allErrs, validateFilters(backendRef.Filters, supportedHTTPBackendRefFilters, fldPath.Child("filters"))...)
	return allErrs
}

func validateBackendObjectReference(route *gatewayv1.HTTPRoute, backendRef gatewayv1.BackendObjectReference, fldPath *field.Path, opts config.HTTPRouteValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	allowedKindsByGroup := map[string]sets.Set[string]{
		"discovery.k8s.io":             sets.New("EndpointSlice"),
		envoygatewayv1alpha1.GroupName: sets.New(envoygatewayv1alpha1.KindBackend),
	}

	if opts.AllowServiceBackends {
		allowedKindsByGroup[""] = sets.New("Service")
	}

	allowedGroups := make([]string, 0, len(allowedKindsByGroup))
	for group := range allowedKindsByGroup {
		allowedGroups = append(allowedGroups, group)
	}
	sort.Strings(allowedGroups)

	groupPtr := backendRef.Group
	group := string(ptr.Deref(groupPtr, gatewayv1.Group("")))
	kindPtr := backendRef.Kind
	kind := string(ptr.Deref(kindPtr, gatewayv1.Kind("")))

	allowedKinds, groupKnown := allowedKindsByGroup[group]
	var groupAllowed bool
	if groupPtr == nil {
		if !groupKnown {
			allErrs = append(allErrs, field.Required(fldPath.Child("group"), "group is required"))
		} else {
			groupAllowed = true
		}
	} else if !groupKnown {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("group"),
			backendRef.Group,
			fmt.Sprintf("group must be one of [%s]", strings.Join(allowedGroups, ", ")),
		))
	} else {
		groupAllowed = true
	}

	var kindAllowed bool
	if kindPtr == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("kind"), "kind is required"))
	} else if groupAllowed {
		if !allowedKinds.Has(kind) {
			allowedKindList := allowedKinds.UnsortedList()
			sort.Strings(allowedKindList)
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("kind"),
				backendRef.Kind,
				fmt.Sprintf("kind must be one of [%s] for group %q", strings.Join(allowedKindList, ", "), group),
			))
		} else {
			kindAllowed = true
		}
	}

	if backendRef.Namespace != nil && *backendRef.Namespace != gatewayv1.Namespace(route.Namespace) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("namespace"), backendRef.Namespace, "namespace must be unspecified or the same as the Route's namespace"))
	}

	if groupAllowed && kindAllowed {
		if group == "discovery.k8s.io" && kind == "EndpointSlice" {
			if backendRef.Port == nil {
				allErrs = append(allErrs, field.Required(fldPath.Child("port"), "port is required"))
			}
		}
	}

	return allErrs
}
