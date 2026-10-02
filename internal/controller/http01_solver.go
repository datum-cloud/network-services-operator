// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"maps"
	"net/http"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const http01SolverLabel = "meta.datumapis.com/http01-solver"

// http01SolverRoute names one ACME HTTP-01 answer to publish on a downstream
// gateway: GET /.well-known/acme-challenge/<token> returns <key>.
type http01SolverRoute struct {
	name   string
	token  string
	key    string
	labels map[string]string
}

func (s http01SolverRoute) objectLabels() map[string]string {
	labels := map[string]string{http01SolverLabel: labelValueTrue}
	maps.Copy(labels, s.labels)
	return labels
}

func (s http01SolverRoute) path() string {
	return fmt.Sprintf("/.well-known/acme-challenge/%s", s.token)
}

// ensureHTTP01SolverRoutes creates or updates the HTTPRouteFilter and HTTPRoute
// that answer a challenge on the gateway, both controlled by owner so they
// leave with it.
func ensureHTTP01SolverRoutes(
	ctx context.Context,
	cl client.Client,
	scheme *runtime.Scheme,
	owner client.Object,
	gateway *gatewayv1.Gateway,
	solver http01SolverRoute,
) error {
	logger := log.FromContext(ctx)

	httpRouteFilter := &envoygatewayv1alpha1.HTTPRouteFilter{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: gateway.Namespace,
			Name:      solver.name,
		},
	}

	result, err := controllerutil.CreateOrUpdate(ctx, cl, httpRouteFilter, func() error {
		if err := controllerutil.SetControllerReference(owner, httpRouteFilter, scheme); err != nil {
			return fmt.Errorf("failed to set controller reference on HTTPRouteFilter: %w", err)
		}
		httpRouteFilter.Labels = solver.objectLabels()
		httpRouteFilter.Spec = envoygatewayv1alpha1.HTTPRouteFilterSpec{
			DirectResponse: &envoygatewayv1alpha1.HTTPDirectResponseFilter{
				ContentType: ptr.To("text/plain"),
				StatusCode:  ptr.To(http.StatusOK),
				Body: &envoygatewayv1alpha1.CustomResponseBody{
					Type:   ptr.To(envoygatewayv1alpha1.ResponseValueTypeInline),
					Inline: ptr.To(solver.key),
				},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update HTTPRouteFilter: %w", err)
	}
	logger.Info("HTTPRouteFilter reconciled", "name", solver.name, "result", result)

	httpRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: gateway.Namespace,
			Name:      solver.name,
		},
	}

	result, err = controllerutil.CreateOrUpdate(ctx, cl, httpRoute, func() error {
		if err := controllerutil.SetControllerReference(owner, httpRoute, scheme); err != nil {
			return fmt.Errorf("failed to set controller reference on HTTPRoute: %w", err)
		}
		httpRoute.Labels = solver.objectLabels()
		httpRoute.Spec = gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name: gatewayv1.ObjectName(gateway.GetName()),
					},
				},
			},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{
							Path: &gatewayv1.HTTPPathMatch{
								Type:  ptr.To(gatewayv1.PathMatchExact),
								Value: ptr.To(solver.path()),
							},
						},
					},
					Filters: []gatewayv1.HTTPRouteFilter{
						{
							Type: gatewayv1.HTTPRouteFilterExtensionRef,
							ExtensionRef: &gatewayv1.LocalObjectReference{
								Group: envoygatewayv1alpha1.GroupName,
								Kind:  envoygatewayv1alpha1.KindHTTPRouteFilter,
								Name:  gatewayv1.ObjectName(httpRouteFilter.GetName()),
							},
						},
					},
				},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update HTTPRoute: %w", err)
	}
	logger.Info("HTTPRoute reconciled", "name", solver.name, "result", result)

	return nil
}

// deleteHTTP01SolverRoutes removes the solver objects published under name in
// the namespace, tolerating ones that are already gone.
func deleteHTTP01SolverRoutes(ctx context.Context, cl client.Client, namespace, name string) error {
	meta := metav1.ObjectMeta{Namespace: namespace, Name: name}
	for _, obj := range []client.Object{
		&gatewayv1.HTTPRoute{ObjectMeta: meta},
		&envoygatewayv1alpha1.HTTPRouteFilter{ObjectMeta: meta},
	} {
		if err := cl.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete solver %T %s/%s: %w", obj, namespace, name, err)
		}
	}
	return nil
}
