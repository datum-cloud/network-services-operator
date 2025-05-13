package validation

import (
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func ValidateHTTPRoute(route *gatewayv1.HTTPRoute) field.ErrorList {

	allErrs := field.ErrorList{}

	// TODO(jreese) go through and prohibit use of experimental fields, confirm we
	// want to support extended fields.

	allErrs = append(allErrs, validateParentRefs(route, field.NewPath("spec", "parentRefs"))...)
	allErrs = append(allErrs, validateHTTPRouteHostnames(route, field.NewPath("spec", "hostnames"))...)
	allErrs = append(allErrs, validateHTTPRouteRules(route, field.NewPath("spec", "rules"))...)
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

	// Only allow Group to be nil, or "gateway.networking.k8s.io"
	if parentRef.Group != nil && *parentRef.Group != "gateway.networking.k8s.io" {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("group"), parentRef.Group, `group must be unspecified or equal "gateway.networking.k8s.io"`))
	}

	// Only allow Kind to be "Gateway"
	if parentRef.Kind != nil && *parentRef.Kind != "Gateway" {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("kind"), parentRef.Kind, `kind must unspecified or equal "Gateway"`))
	}

	// Only allow Namespace to be nil, or the same as the HTTPRoute's namespace
	if parentRef.Namespace != nil && *parentRef.Namespace != gatewayv1.Namespace(route.Namespace) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("namespace"), parentRef.Namespace, "namespace must be unspecified or the same as the HTTPRoute's namespace"))
	}

	// Do not allow SectionName to be set
	if parentRef.SectionName != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("sectionName"), "sectionName is not permitted"))
	}

	// Do not allow Port to be set
	if parentRef.Port != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("port"), "port is not permitted"))
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

func validateHTTPRouteRules(route *gatewayv1.HTTPRoute, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, rule := range route.Spec.Rules {
		allErrs = append(allErrs, validateHTTPRouteRule(route, rule, fldPath.Index(i))...)
	}

	return allErrs
}

func validateHTTPRouteRule(route *gatewayv1.HTTPRoute, rule gatewayv1.HTTPRouteRule, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateHTTPRouteFilters(rule.Filters, fldPath.Child("filters"))...)
	allErrs = append(allErrs, validateHTTPRouteRuleBackendRefs(route, rule, fldPath.Child("backendRefs"))...)

	return allErrs
}

var supportedFilters = sets.New(
	gatewayv1.HTTPRouteFilterRequestHeaderModifier,
	gatewayv1.HTTPRouteFilterResponseHeaderModifier,
	gatewayv1.HTTPRouteFilterRequestRedirect,
	gatewayv1.HTTPRouteFilterURLRewrite,
)

func validateHTTPRouteFilters(filters []gatewayv1.HTTPRouteFilter, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, filter := range filters {
		allErrs = append(allErrs, validateHTTPRouteFilter(filter, fldPath.Index(i))...)
	}

	return allErrs
}

func validateHTTPRouteFilter(filter gatewayv1.HTTPRouteFilter, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if !supportedFilters.Has(filter.Type) {
		allErrs = append(allErrs, field.NotSupported(fldPath.Child("type"), filter.Type, sets.List(supportedFilters)))
	}

	if filter.ExtensionRef != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("extensionRef"), "extensionRef is not permitted"))
	}

	return allErrs
}

func validateHTTPRouteRuleBackendRefs(route *gatewayv1.HTTPRoute, rule gatewayv1.HTTPRouteRule, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, backendRef := range rule.BackendRefs {
		allErrs = append(allErrs, validateHTTPBackendRef(route, backendRef, fldPath.Index(i))...)
	}

	return allErrs
}

func validateHTTPBackendRef(route *gatewayv1.HTTPRoute, backendRef gatewayv1.HTTPBackendRef, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	// Do I need to validate the name?

	allErrs = append(allErrs, validateBackendObjectReference(route, backendRef.BackendObjectReference, fldPath)...)
	allErrs = append(allErrs, validateHTTPRouteFilters(backendRef.Filters, fldPath.Child("filters"))...)
	return allErrs
}

func validateBackendObjectReference(route *gatewayv1.HTTPRoute, backendRef gatewayv1.BackendObjectReference, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	// Group must be specified as "discovery.k8s.io"
	if backendRef.Group == nil || *backendRef.Group != "discovery.k8s.io" {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("group"), backendRef.Group, `group must be equal "discovery.k8s.io"`))
	}

	// Kind must be specified as "EndpointSlice"
	if backendRef.Kind == nil || *backendRef.Kind != "EndpointSlice" {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("kind"), backendRef.Kind, `kind must be equal "EndpointSlice"`))
	}

	// Only allow Namespace to be nil, or the same as the HTTPRoute's namespace
	if backendRef.Namespace != nil && *backendRef.Namespace != gatewayv1.Namespace(route.Namespace) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("namespace"), backendRef.Namespace, "namespace must be unspecified or the same as the Route's namespace"))
	}

	// Port must be set
	if backendRef.Port == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("port"), "port is required"))
	}

	return allErrs
}
