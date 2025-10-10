// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

// TrafficProtectionPolicyReconciler reconciles a TrafficProtectionPolicy object
type TrafficProtectionPolicyReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator

	DownstreamCluster cluster.Cluster
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=trafficprotectionpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=trafficprotectionpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=trafficprotectionpolicies/finalizers,verbs=update

func (r *TrafficProtectionPolicyReconciler) Reconcile(ctx context.Context, req NamespaceReconcileRequest) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)
	ctx = log.IntoContext(ctx, logger)

	// Ensure that the HTTP listener has the Coraza WAF filter configured.
	if err := r.ensureHTTPCorazaListenerFilter(ctx); err != nil {
		return ctrl.Result{}, err
	}

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("reconciling trafficprotectionpolicies")
	defer logger.Info("reconcile complete")

	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(req.ClusterName, cl.GetClient(), r.DownstreamCluster.GetClient())

	downstreamNamespaceName, err := downstreamStrategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, req.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Collect all traffic protection policies, gateways, and routes in the same namespace as the TPP

	var trafficProtectionPolicies networkingv1alpha.TrafficProtectionPolicyList
	if err := cl.GetClient().List(ctx, &trafficProtectionPolicies, client.InNamespace(req.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	var upstreamGateways gatewayv1.GatewayList
	if err := cl.GetClient().List(ctx, &upstreamGateways, client.InNamespace(req.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	var upstreamHTTPRoutes gatewayv1.HTTPRouteList
	if err := cl.GetClient().List(ctx, &upstreamHTTPRoutes, client.InNamespace(req.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	attachments := r.collectTrafficProtectionPolicyAttachments(ctx, trafficProtectionPolicies, upstreamGateways, upstreamHTTPRoutes)
	desiredPolicies, err := r.getDesiredEnvoyPatchPolicies(downstreamNamespaceName, attachments)
	if err != nil {
		return ctrl.Result{}, err
	}

	desiredPolicyNames := make(map[string]struct{}, len(desiredPolicies))
	for _, desiredPolicy := range desiredPolicies {
		desiredPolicyNames[desiredPolicy.Name] = struct{}{}

		policy := envoygatewayv1alpha1.EnvoyPatchPolicy{ObjectMeta: metav1.ObjectMeta{
			Namespace: desiredPolicy.Namespace,
			Name:      desiredPolicy.Name,
		}}

		result, err := controllerutil.CreateOrUpdate(ctx, downstreamStrategy.GetClient(), &policy, func() error {
			policy.Spec = desiredPolicy.Spec
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create or update envoypatchpolicy %s/%s: %w", policy.Namespace, policy.Name, err)
		}
		logger.Info("applied envoypatchpolicy to downstream cluster", "namespace", policy.Namespace, "name", policy.Name, "result", result)
	}

	var existingPolicies envoygatewayv1alpha1.EnvoyPatchPolicyList
	if err := downstreamStrategy.GetClient().List(
		ctx,
		&existingPolicies,
		client.InNamespace(downstreamNamespaceName),
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list managed envoypatchpolicies: %w", err)
	}

	for i := range existingPolicies.Items {
		existing := &existingPolicies.Items[i]
		if _, ok := desiredPolicyNames[existing.Name]; ok {
			continue
		}

		if err := downstreamStrategy.GetClient().Delete(ctx, existing); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete stale envoypatchpolicy %s/%s: %w", existing.Namespace, existing.Name, err)
		}
		logger.Info("deleted stale envoypatchpolicy from downstream cluster", "namespace", existing.Namespace, "name", existing.Name)
	}

	return ctrl.Result{}, nil
}

func (r *TrafficProtectionPolicyReconciler) ensureHTTPCorazaListenerFilter(ctx context.Context) error {
	envoyPatchPolicy := &envoygatewayv1alpha1.EnvoyPatchPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.Config.Gateway.DownstreamGatewayNamespace,
			Name:      "coraza-tcp-80",
		},
	}

	corazaConfigBytes, err := r.getCorazaListenerFilterConfig()
	if err != nil {
		return err
	}

	result, err := controllerutil.CreateOrUpdate(ctx, r.DownstreamCluster.GetClient(), envoyPatchPolicy, func() error {
		envoyPatchPolicy.Spec = envoygatewayv1alpha1.EnvoyPatchPolicySpec{
			TargetRef: gatewayv1alpha2.LocalPolicyTargetReference{
				Group: gatewayv1.GroupName,
				Kind:  "GatewayClass",
				Name:  gatewayv1.ObjectName(r.Config.Gateway.DownstreamGatewayClassName),
			},
			Type: envoygatewayv1alpha1.JSONPatchEnvoyPatchType,
			JSONPatches: []envoygatewayv1alpha1.EnvoyJSONPatchConfig{
				{
					Type: "type.googleapis.com/envoy.config.listener.v3.Listener",
					Name: fmt.Sprintf("tcp-%d", DefaultHTTPPort),
					Operation: envoygatewayv1alpha1.JSONPatchOperation{
						Op:    "add",
						Path:  ptr.To("/default_filter_chain/filters/0/typed_config/http_filters/0"),
						Value: &apiextensionsv1.JSON{Raw: corazaConfigBytes},
					},
				},
			},
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create or update envoypatchpolicy for http listener: %w", err)
	}

	logger := log.FromContext(ctx)
	logger.Info("ensured envoypatchpolicy for http listener", "namespace", envoyPatchPolicy.Namespace, "name", envoyPatchPolicy.Name, "result", result)

	return nil
}

func (r TrafficProtectionPolicyReconciler) getCorazaListenerFilterConfig() ([]byte, error) {
	directiveBytes, err := json.Marshal(r.Config.Gateway.Coraza.ListenerDirectives)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal coraza directives: %w", err)
	}

	corazaConfig := map[string]any{
		"name":     r.Config.Gateway.Coraza.FilterName,
		"disabled": true,
		"typed_config": map[string]any{
			"@type":        "type.googleapis.com/envoy.extensions.filters.http.golang.v3alpha.Config",
			"library_id":   r.Config.Gateway.Coraza.LibraryID,
			"library_path": r.Config.Gateway.Coraza.LibraryPath,
			"plugin_name":  r.Config.Gateway.Coraza.PluginName,
			"plugin_config": map[string]any{
				"@type": "type.googleapis.com/xds.type.v3.TypedStruct",
				"value": map[string]any{
					"directives": sanitizeJSONPath(fmt.Sprintf(`{
						"coraza": {
							"simple_directives": %s
						}
					}`, string(directiveBytes))),
					"default_directive": "coraza",
				},
			},
		},
	}

	corazaConfigBytes, err := json.Marshal(corazaConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal coraza config: %w", err)
	}
	return corazaConfigBytes, nil
}

func (r *TrafficProtectionPolicyReconciler) collectTrafficProtectionPolicyAttachments(
	ctx context.Context,
	trafficProtectionPolicies networkingv1alpha.TrafficProtectionPolicyList,
	upstreamGateways gatewayv1.GatewayList,
	upstreamHTTPRoutes gatewayv1.HTTPRouteList,
) []policyAttachment {
	logger := log.FromContext(ctx)

	logger.Info(
		"processing traffic protection policies",
		"totalPolicies", len(trafficProtectionPolicies.Items),
		"totalGateways", len(upstreamGateways.Items),
		"totalRoutes", len(upstreamHTTPRoutes.Items),
	)

	routeMapSize := len(upstreamHTTPRoutes.Items)
	gatewayMapSize := len(upstreamGateways.Items)

	routeMap := make(map[client.ObjectKey]*gatewayv1.HTTPRoute, routeMapSize)
	for i, route := range upstreamHTTPRoutes.Items {
		routeMap[client.ObjectKeyFromObject(&route)] = &upstreamHTTPRoutes.Items[i]
	}

	gatewayMap := make(map[client.ObjectKey]*gatewayv1.Gateway, gatewayMapSize)
	for i, gw := range upstreamGateways.Items {
		gatewayMap[client.ObjectKeyFromObject(&gw)] = &upstreamGateways.Items[i]
	}

	var policyAttachments []policyAttachment

	// Attach policies from least specific to most specific so that JSONPatch
	// operations can override properly.

	// Process the policies targeting Gateways
	for _, currPolicy := range trafficProtectionPolicies.Items {
		for _, currTarget := range currPolicy.Spec.TargetRefs {
			if currTarget.Kind == KindGateway && currTarget.SectionName == nil {
				policyAttachments = r.processTrafficProtectionPolicyForGateway(
					ctx,
					gatewayMap,
					policyAttachments,
					&currPolicy,
					currTarget,
				)
			}
		}
	}

	// Process the policies targeting Gateway Listeners
	for _, currPolicy := range trafficProtectionPolicies.Items {
		for _, currTarget := range currPolicy.Spec.TargetRefs {
			if currTarget.Kind == KindGateway && currTarget.SectionName != nil {
				policyAttachments = r.processTrafficProtectionPolicyForGateway(
					ctx,
					gatewayMap,
					policyAttachments,
					&currPolicy,
					currTarget,
				)
			}
		}
	}

	// Process the policies targeting xRoutes
	for _, currPolicy := range trafficProtectionPolicies.Items {
		for _, currTarget := range currPolicy.Spec.TargetRefs {
			if currTarget.Kind != KindGateway && currTarget.SectionName == nil {
				policyAttachments = r.processTrafficProtectionPolicyForHTTPRoute(
					ctx,
					routeMap,
					gatewayMap,
					policyAttachments,
					&currPolicy,
					currTarget,
				)
			}
		}
	}

	// Process the policies targeting RouteRules
	for _, currPolicy := range trafficProtectionPolicies.Items {
		for _, currTarget := range currPolicy.Spec.TargetRefs {
			if currTarget.Kind != KindGateway && currTarget.SectionName != nil {
				policyAttachments = r.processTrafficProtectionPolicyForHTTPRoute(
					ctx,
					routeMap,
					gatewayMap,
					policyAttachments,
					&currPolicy,
					currTarget,
				)
			}
		}
	}

	logger.Info("collected traffic protection policies", "totalAttachments", len(policyAttachments))

	return policyAttachments
}

type policyAttachment struct {
	Gateway          *gatewayv1.Gateway
	Listener         *gatewayv1.SectionName
	Route            *gatewayv1.HTTPRoute
	RuleSectionName  *gatewayv1.SectionName
	CorazaDirectives []string
}

func (r *TrafficProtectionPolicyReconciler) processTrafficProtectionPolicyForHTTPRoute(
	ctx context.Context,
	routeMap map[client.ObjectKey]*gatewayv1.HTTPRoute,
	gatewayMap map[client.ObjectKey]*gatewayv1.Gateway,
	policyAttachments []policyAttachment,
	policy *networkingv1alpha.TrafficProtectionPolicy,
	targetRef gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName,
) []policyAttachment {
	logger := log.FromContext(ctx)
	routeKey := client.ObjectKey{
		Name:      string(targetRef.Name),
		Namespace: policy.Namespace,
	}

	route, ok := routeMap[routeKey]
	if !ok {
		return policyAttachments
	}

	directives := r.getCorazaDirectivesForTrafficProtectionPolicy(policy)
	if len(directives) == 0 {
		return policyAttachments
	}

	for _, parentRef := range route.Spec.ParentRefs {
		if ptr.Deref(parentRef.Kind, KindGateway) != KindGateway {
			logger.Info("skipping parentRef that is not a gateway", "kind", parentRef.Kind)
			continue
		}

		gatewayKey := client.ObjectKey{
			Name:      string(parentRef.Name),
			Namespace: string(ptr.Deref(parentRef.Namespace, gatewayv1.Namespace(route.Namespace))),
		}

		gateway, ok := gatewayMap[gatewayKey]
		if !ok {
			logger.Info("could not find gateway for parentRef", "parentRef", gatewayKey)
			continue
		}

		// Attach policy to gateway with rule context.
		policyAttachments = append(policyAttachments, policyAttachment{
			Gateway:          gateway,
			Listener:         parentRef.SectionName,
			RuleSectionName:  targetRef.SectionName,
			Route:            route,
			CorazaDirectives: directives,
		})
	}
	return policyAttachments
}

func (r *TrafficProtectionPolicyReconciler) processTrafficProtectionPolicyForGateway(
	ctx context.Context,
	gatewayMap map[client.ObjectKey]*gatewayv1.Gateway,
	policyAttachments []policyAttachment,
	policy *networkingv1alpha.TrafficProtectionPolicy,
	targetRef gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName,
) []policyAttachment {
	logger := log.FromContext(ctx)

	gatewayKey := client.ObjectKey{
		Name:      string(targetRef.Name),
		Namespace: policy.Namespace,
	}

	gateway, ok := gatewayMap[gatewayKey]
	if !ok {
		logger.Info("could not find gateway for parentRef", "parentRef", gatewayKey)
		return policyAttachments
	}

	directives := r.getCorazaDirectivesForTrafficProtectionPolicy(policy)
	if len(directives) == 0 {
		return policyAttachments
	}

	// Attach policy to gateway with rule context.
	policyAttachments = append(policyAttachments, policyAttachment{
		Gateway:          gateway,
		Listener:         targetRef.SectionName,
		CorazaDirectives: directives,
	})
	return policyAttachments
}

func (r *TrafficProtectionPolicyReconciler) getDesiredEnvoyPatchPolicies(
	downstreamNamespaceName string,
	policyAttachments []policyAttachment,
) ([]*envoygatewayv1alpha1.EnvoyPatchPolicy, error) {
	attachmentsByGateway := make(map[string][]policyAttachment, len(policyAttachments))
	gatewayKeys := make([]string, 0)

	for _, attachment := range policyAttachments {
		key := client.ObjectKeyFromObject(attachment.Gateway).String()
		if _, ok := attachmentsByGateway[key]; !ok {
			gatewayKeys = append(gatewayKeys, key)
		}

		attachmentsByGateway[key] = append(attachmentsByGateway[key], attachment)
	}

	sort.Strings(gatewayKeys)

	desiredPolicies := make([]*envoygatewayv1alpha1.EnvoyPatchPolicy, 0, len(attachmentsByGateway))

	for _, key := range gatewayKeys {
		attachmentsForGateway := attachmentsByGateway[key]

		tlsFilterChainsWithAttachments := sets.New[string]()

		var jsonPatches []envoygatewayv1alpha1.EnvoyJSONPatchConfig
		for _, policyAttachment := range attachmentsForGateway {
			if len(policyAttachment.CorazaDirectives) == 0 {
				// Shouldn't happen until other types of rulesets are added
				continue
			}
			vhostConstraints := fmt.Sprintf(`@.kind=="%s" && @.namespace=="%s" && @.name=="%s"`,
				KindGateway,
				downstreamNamespaceName,
				policyAttachment.Gateway.Name,
			)

			if policyAttachment.Listener != nil {
				vhostConstraints += fmt.Sprintf(" && @.sectionName==\"%s\"", *policyAttachment.Listener)
			}

			var routeConstraints string
			if policyAttachment.Route != nil {
				var sectionNameConstraint string
				if policyAttachment.RuleSectionName != nil {
					sectionNameConstraint = fmt.Sprintf(` && @.sectionName=="%s"`, *policyAttachment.RuleSectionName)
				}

				routeConstraints = fmt.Sprintf(` && @.metadata.filter_metadata["envoy-gateway"].resources[?(@.kind=="%s" && @.namespace=="%s" && @.name=="%s"%s)]`,
					KindHTTPRoute,
					downstreamNamespaceName,
					policyAttachment.Route.Name,
					sectionNameConstraint,
				)
			}

			httpRoutesJSONPath := sanitizeJSONPath(
				fmt.Sprintf(`..virtual_hosts[?(
					@.metadata.filter_metadata["envoy-gateway"].resources[?(
						%s
					)]
				)]..routes[?(!@.bogus)%s]`,
					// @.bogus is here to ensure a list is collected by the JSONPath parser,
					// otherwise a single element is returned. Need to look into the
					// implementation to see why this happens.
					vhostConstraints,
					routeConstraints,
				),
			)

			directiveBytes, err := json.Marshal(policyAttachment.CorazaDirectives)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal coraza directives: %w", err)
			}

			corazaConfig := map[string]any{
				r.Config.Gateway.Coraza.FilterName: map[string]any{
					"@type": "type.googleapis.com/envoy.extensions.filters.http.golang.v3alpha.ConfigsPerRoute",
					"plugins_config": map[string]any{
						r.Config.Gateway.Coraza.PluginName: map[string]any{
							"config": map[string]any{
								"@type": "type.googleapis.com/xds.type.v3.TypedStruct",
								"value": map[string]any{
									"directives": sanitizeJSONPath(fmt.Sprintf(`{
										"coraza": {
											"simple_directives": %s
										}
									}`, string(directiveBytes))),
									"default_directive": "coraza",
								},
							},
						},
					},
				},
			}

			corazaConfigBytes, err := json.Marshal(corazaConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal coraza config: %w", err)
			}

			if policyAttachment.Listener == nil {
				// Attach to all HTTP listeners on the gateway, only requires a single patch
				// as there's a single RouteConfiguration for http-80
				jsonPatches = append(jsonPatches, envoygatewayv1alpha1.EnvoyJSONPatchConfig{
					Type: "type.googleapis.com/envoy.config.route.v3.RouteConfiguration",
					Name: fmt.Sprintf("http-%d", DefaultHTTPPort),
					Operation: envoygatewayv1alpha1.JSONPatchOperation{
						Op:       "add",
						JSONPath: ptr.To(httpRoutesJSONPath),
						Path:     ptr.To("/typed_per_filter_config"),
						Value:    &apiextensionsv1.JSON{Raw: corazaConfigBytes},
					},
				})

				// Attach to all TLS listeners on the gateway
				for _, listener := range policyAttachment.Gateway.Spec.Listeners {
					if listener.Protocol != gatewayv1.HTTPSProtocolType {
						continue
					}

					listenerRouteConfigName := fmt.Sprintf("%s/%s/%s", downstreamNamespaceName, policyAttachment.Gateway.Name, listener.Name)
					tlsFilterChainsWithAttachments.Insert(listenerRouteConfigName)

					jsonPatches = append(jsonPatches, envoygatewayv1alpha1.EnvoyJSONPatchConfig{
						Type: "type.googleapis.com/envoy.config.route.v3.RouteConfiguration",
						Name: listenerRouteConfigName,
						Operation: envoygatewayv1alpha1.JSONPatchOperation{
							Op:       "add",
							JSONPath: ptr.To(httpRoutesJSONPath),
							Path:     ptr.To("/typed_per_filter_config"),
							Value:    &apiextensionsv1.JSON{Raw: corazaConfigBytes},
						},
					})

				}
			} else {

				listenerRouteConfigName := fmt.Sprintf("%s/%s/%s", downstreamNamespaceName, policyAttachment.Gateway.Name, *policyAttachment.Listener)
				tlsFilterChainsWithAttachments.Insert(listenerRouteConfigName)

				jsonPatches = append(jsonPatches, envoygatewayv1alpha1.EnvoyJSONPatchConfig{
					Type: "type.googleapis.com/envoy.config.route.v3.RouteConfiguration",
					Name: listenerRouteConfigName,
					Operation: envoygatewayv1alpha1.JSONPatchOperation{
						Op:       "add",
						JSONPath: ptr.To(httpRoutesJSONPath),
						Path:     ptr.To("/typed_per_filter_config"),
						Value:    &apiextensionsv1.JSON{Raw: corazaConfigBytes},
					},
				})

			}
		}

		// Process TLS filter chains with attachments

		corazaConfigBytes, err := r.getCorazaListenerFilterConfig()
		if err != nil {
			return nil, err
		}

		for _, filterChainName := range sets.List(tlsFilterChainsWithAttachments) {
			jsonPatches = append(jsonPatches, envoygatewayv1alpha1.EnvoyJSONPatchConfig{
				Type: "type.googleapis.com/envoy.config.listener.v3.Listener",
				Name: fmt.Sprintf("tcp-%d", DefaultHTTPSPort),
				Operation: envoygatewayv1alpha1.JSONPatchOperation{
					Op:       "add",
					JSONPath: ptr.To(fmt.Sprintf(`..filter_chains[?(@.name=="%s")]`, filterChainName)),
					Path:     ptr.To("/filters/0/typed_config/http_filters/0"),
					Value:    &apiextensionsv1.JSON{Raw: corazaConfigBytes},
				},
			})
		}

		if len(jsonPatches) == 0 {
			continue
		}

		policyName := fmt.Sprintf("tpp-%s", attachmentsForGateway[0].Gateway.Name)
		desiredPolicies = append(desiredPolicies, &envoygatewayv1alpha1.EnvoyPatchPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: downstreamNamespaceName,
				Name:      policyName,
			},
			Spec: envoygatewayv1alpha1.EnvoyPatchPolicySpec{
				TargetRef: gatewayv1alpha2.LocalPolicyTargetReference{
					Group: gatewayv1.GroupName,
					Kind:  "GatewayClass",
					Name:  gatewayv1.ObjectName(r.Config.Gateway.DownstreamGatewayClassName),
				},
				Type:        envoygatewayv1alpha1.JSONPatchEnvoyPatchType,
				JSONPatches: jsonPatches,
			},
		})
	}

	return desiredPolicies, nil
}

func (r *TrafficProtectionPolicyReconciler) getCorazaDirectivesForTrafficProtectionPolicy(
	policy *networkingv1alpha.TrafficProtectionPolicy,
) []string {
	var owaspCRS *networkingv1alpha.OWASPCRS
	for _, ruleSet := range policy.Spec.RuleSets {
		if ruleSet.Type == networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet {
			owaspCRS = &ruleSet.OWASPCoreRuleSet
			break
		}
	}
	if owaspCRS == nil {
		return nil
	}

	secRuleEngine := "DetectionOnly"
	switch policy.Spec.Mode {
	case networkingv1alpha.TrafficProtectionPolicyEnforce:
		secRuleEngine = "On"
	case networkingv1alpha.TrafficProtectionPolicyDisabled:
		secRuleEngine = "Off"
	}

	directives := r.Config.Gateway.Coraza.RouteBaseDirectives

	directives = append(directives, fmt.Sprintf("SecRuleEngine %s", secRuleEngine))

	directives = append(directives,
		fmt.Sprintf(
			`SecAction "id:900110,phase:1,nolog,pass,t:none,setvar:tx.inbound_anomaly_score_threshold=%d,setvar:tx.outbound_anomaly_score_threshold=%d"`,
			owaspCRS.ScoreThresholds.Inbound,
			owaspCRS.ScoreThresholds.Outbound,
		),
	)

	directives = append(
		directives,
		fmt.Sprintf(
			`SecAction "id:900000,phase:1,pass,t:none,nolog,tag:'OWASP_CRS',setvar:tx.blocking_paranoia_level=%d"`,
			owaspCRS.ParanoiaLevel,
		),
	)

	if owaspCRS.SamplingPercentage < 100 {
		directives = append(
			directives,
			fmt.Sprintf(
				`SecAction "id:900400,phase:1,pass,nolog,setvar:tx.sampling_percentage=%d"`,
				owaspCRS.SamplingPercentage,
			),
		)
	}

	directives = append(directives, "Include @owasp_crs/*.conf")

	if ruleExclusions := owaspCRS.RuleExclusions; ruleExclusions != nil {
		for _, tag := range ruleExclusions.Tags {
			directives = append(directives, fmt.Sprintf("SecRuleRemoveByTag %q", tag))
		}

		for _, v := range ruleExclusions.IDs {
			directives = append(directives, fmt.Sprintf("SecRuleRemoveById %d", v))
		}

		for _, v := range ruleExclusions.IDRanges {
			directives = append(directives, fmt.Sprintf("SecRuleRemoveById %q", v))
		}
	}

	return directives
}

func sanitizeJSONPath(jsonPath string) string {
	jsonPath = strings.ReplaceAll(jsonPath, "\n", "")
	return strings.ReplaceAll(jsonPath, "\t", "")
}

// SetupWithManager sets up the controller with the Manager.
func (r *TrafficProtectionPolicyReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	return mcbuilder.TypedControllerManagedBy[NamespaceReconcileRequest](mgr).
		Watches(&networkingv1alpha.TrafficProtectionPolicy{}, EnqueueRequestForObjectNamespace).
		Watches(&gatewayv1.Gateway{}, EnqueueRequestForObjectNamespace).
		Watches(&gatewayv1.HTTPRoute{}, EnqueueRequestForObjectNamespace).
		Named("trafficprotectionpolicy").
		Complete(r)
}

var EnqueueRequestForObjectNamespace = mchandler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []NamespaceReconcileRequest {
	return []NamespaceReconcileRequest{{
		Namespace: obj.GetNamespace(),
	}}
})

type NamespaceReconcileRequest struct {
	Namespace string

	// ClusterName is the name of the cluster that the request belongs to.
	ClusterName string
}

// String returns the general purpose string representation.
func (r NamespaceReconcileRequest) String() string {
	if r.ClusterName == "" {
		return r.Namespace
	}
	return "cluster://" + r.ClusterName + string(types.Separator) + r.Namespace
}

// Cluster returns the name of the cluster that the request belongs to.
func (r NamespaceReconcileRequest) Cluster() string {
	return r.ClusterName
}

// WithCluster sets the name of the cluster that the request belongs to.
func (r NamespaceReconcileRequest) WithCluster(name string) NamespaceReconcileRequest {
	r.ClusterName = name
	return r
}
