package validation

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

const (
	httpProxyCRDPath         = "../../config/crd/bases/networking.datumapis.com_httpproxies.yaml"
	gatewayCRDKustomization  = "../../config/crd/gateway/kustomization.yaml"
	gatewayAPIModulePath     = "sigs.k8s.io/gateway-api"
	installedHTTPRouteCRDKey = "gateway.networking.k8s.io_httproutes.yaml"
)

var gatewayAPIResourceURL = regexp.MustCompile(`^https://raw\.githubusercontent\.com/kubernetes-sigs/gateway-api/refs/tags/([^/]+)/(config/crd/[^/]+/[^/]+\.yaml)$`)

func TestGatewayAPICRDsMatchGoModule(t *testing.T) {
	_, moduleVersion := gatewayAPIModule(t)
	for file, ref := range installedGatewayAPICRDs(t) {
		assert.Equal(t, moduleVersion, ref.version, "%s is installed from gateway-api %s, but go.mod requires %s", file, ref.version, moduleVersion)
	}
}

func TestHTTPProxySchemaFitsInstalledHTTPRoute(t *testing.T) {
	moduleDir, _ := gatewayAPIModule(t)
	installed, ok := installedGatewayAPICRDs(t)[installedHTTPRouteCRDKey]
	require.True(t, ok, "%s does not install %s", gatewayCRDKustomization, installedHTTPRouteCRDKey)

	proxyRule := crdRuleSchema(t, httpProxyCRDPath, "v1alpha")
	routeRule := crdRuleSchema(t, filepath.Join(moduleDir, installed.path), "v1")

	problems := httpProxyRuleFitsHTTPRouteRule(proxyRule, routeRule)
	for _, p := range problems {
		t.Error(p)
	}
}

func TestSchemaFitsDetectsLooserProxySchema(t *testing.T) {
	route := apiextensionsv1.JSONSchemaProps{
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"statusCode": {Enum: jsonValues(t, 301, 302)},
			"name":       {MaxLength: ptrInt64(253), Pattern: "^[a-z]+$"},
			"items":      {MaxItems: ptrInt64(16), Items: &apiextensionsv1.JSONSchemaPropsOrArray{Schema: &apiextensionsv1.JSONSchemaProps{}}},
		},
		XValidations: apiextensionsv1.ValidationRules{{Rule: "self.size() <= 1"}},
	}
	proxy := apiextensionsv1.JSONSchemaProps{
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"statusCode": {Enum: jsonValues(t, 301, 302, 308)},
			"name":       {MaxLength: ptrInt64(512)},
			"items":      {MaxItems: ptrInt64(16), Items: &apiextensionsv1.JSONSchemaPropsOrArray{Schema: &apiextensionsv1.JSONSchemaProps{}}},
			"extra":      {},
		},
	}

	problems := schemaFits("filter", proxy, route)
	assert.ElementsMatch(t, []string{
		"filter: CEL rule \"self.size() <= 1\" is missing",
		"filter.extra: field does not exist in the HTTPRoute schema and would be dropped",
		"filter.name: maxLength 512 exceeds 253",
		"filter.name: pattern \"\" does not match \"^[a-z]+$\"",
		"filter.statusCode: enum value 308 is not accepted",
	}, problems)
	assert.Empty(t, schemaFits("filter", route, route))
}

func httpProxyRuleFitsHTTPRouteRule(proxyRule, routeRule apiextensionsv1.JSONSchemaProps) []string {
	var problems []string
	for name := range proxyRule.Properties {
		if name == "backends" {
			continue
		}
		if _, ok := routeRule.Properties[name]; !ok {
			problems = append(problems, fmt.Sprintf("rules[].%s: field does not exist in the HTTPRoute schema and would be dropped", name))
		}
	}
	problems = append(problems, schemaFits("rules[].matches", proxyRule.Properties["matches"], routeRule.Properties["matches"])...)
	problems = append(problems, filtersFit("rules[].filters", proxyRule.Properties["filters"], routeRule.Properties["filters"])...)
	problems = append(problems, filtersFit(
		"rules[].backends[].filters",
		proxyRule.Properties["backends"].Items.Schema.Properties["filters"],
		routeRule.Properties["backendRefs"].Items.Schema.Properties["filters"],
	)...)
	return problems
}

func filtersFit(path string, proxy, route apiextensionsv1.JSONSchemaProps) []string {
	filter := proxy.Items.Schema
	var problems []string
	for _, p := range schemaFits(path, proxy, route) {
		unusable := false
		for name := range filter.Properties {
			if p != fmt.Sprintf("%s[].%s: field does not exist in the HTTPRoute schema and would be dropped", path, name) {
				continue
			}
			unusable = !slices.ContainsFunc(filter.Properties["type"].Enum, func(v apiextensionsv1.JSON) bool {
				return strings.EqualFold(strings.Trim(string(v.Raw), `"`), name)
			})
		}
		if !unusable {
			problems = append(problems, p)
		}
	}
	return problems
}

func schemaFits(path string, proxy, route apiextensionsv1.JSONSchemaProps) []string {
	var problems []string
	report := func(format string, args ...any) {
		problems = append(problems, path+": "+fmt.Sprintf(format, args...))
	}

	if len(route.Enum) > 0 {
		allowed := make([]string, 0, len(route.Enum))
		for _, v := range route.Enum {
			allowed = append(allowed, string(v.Raw))
		}
		if len(proxy.Enum) == 0 {
			report("accepts any value, HTTPRoute accepts only %v", allowed)
		}
		for _, v := range proxy.Enum {
			if !slices.Contains(allowed, string(v.Raw)) {
				report("enum value %s is not accepted", v.Raw)
			}
		}
	}

	upper := func(name string, p, r *int64) {
		if r != nil && (p == nil || *p > *r) {
			report("%s %s exceeds %d", name, describeBound(p), *r)
		}
	}
	lower := func(name string, p, r *int64) {
		if r != nil && (p == nil || *p < *r) {
			report("%s %s is below %d", name, describeBound(p), *r)
		}
	}
	upper("maxItems", proxy.MaxItems, route.MaxItems)
	upper("maxLength", proxy.MaxLength, route.MaxLength)
	lower("minItems", proxy.MinItems, route.MinItems)
	lower("minLength", proxy.MinLength, route.MinLength)
	if route.Maximum != nil && (proxy.Maximum == nil || *proxy.Maximum > *route.Maximum) {
		report("maximum exceeds %v", *route.Maximum)
	}
	if route.Minimum != nil && (proxy.Minimum == nil || *proxy.Minimum < *route.Minimum) {
		report("minimum is below %v", *route.Minimum)
	}
	if route.Pattern != "" && proxy.Pattern != route.Pattern {
		report("pattern %q does not match %q", proxy.Pattern, route.Pattern)
	}
	for _, r := range route.Required {
		if !slices.Contains(proxy.Required, r) {
			report("field %q is not required", r)
		}
	}
	for _, rule := range route.XValidations {
		if !slices.ContainsFunc(proxy.XValidations, func(p apiextensionsv1.ValidationRule) bool { return p.Rule == rule.Rule }) {
			report("CEL rule %q is missing", rule.Rule)
		}
	}

	for name, proxyProp := range proxy.Properties {
		routeProp, ok := route.Properties[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s.%s: field does not exist in the HTTPRoute schema and would be dropped", path, name))
			continue
		}
		problems = append(problems, schemaFits(path+"."+name, proxyProp, routeProp)...)
	}
	if proxy.Items != nil && proxy.Items.Schema != nil && route.Items != nil && route.Items.Schema != nil {
		problems = append(problems, schemaFits(path+"[]", *proxy.Items.Schema, *route.Items.Schema)...)
	}

	return problems
}

func describeBound(v *int64) string {
	if v == nil {
		return "unset"
	}
	return fmt.Sprint(*v)
}

type gatewayAPIResource struct {
	version string
	path    string
}

// installedGatewayAPICRDs returns the Gateway API CRDs that
// config/crd/gateway installs, keyed by file name.
func installedGatewayAPICRDs(t *testing.T) map[string]gatewayAPIResource {
	t.Helper()
	var kustomization struct {
		Resources []string `json:"resources"`
	}
	data, err := os.ReadFile(gatewayCRDKustomization)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(data, &kustomization))

	resources := map[string]gatewayAPIResource{}
	for _, r := range kustomization.Resources {
		if !strings.Contains(r, "kubernetes-sigs/gateway-api") {
			continue
		}
		m := gatewayAPIResourceURL.FindStringSubmatch(r)
		require.NotNil(t, m, "%s: gateway-api resource %q is not a pinned raw CRD URL", gatewayCRDKustomization, r)
		resources[filepath.Base(m[2])] = gatewayAPIResource{version: m[1], path: m[2]}
	}
	require.NotEmpty(t, resources, "%s installs no gateway-api CRDs", gatewayCRDKustomization)
	return resources
}

func gatewayAPIModule(t *testing.T) (dir, version string) {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}\t{{.Version}}", gatewayAPIModulePath).Output()
	require.NoError(t, err, "resolving %s", gatewayAPIModulePath)
	dir, version, ok := strings.Cut(strings.TrimSpace(string(out)), "\t")
	require.True(t, ok && dir != "" && version != "", "unexpected go list output %q", out)
	return dir, version
}

func crdRuleSchema(t *testing.T, path, version string) apiextensionsv1.JSONSchemaProps {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var crd apiextensionsv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(data, &crd))
	for _, v := range crd.Spec.Versions {
		if v.Name == version {
			rules := v.Schema.OpenAPIV3Schema.Properties["spec"].Properties["rules"]
			require.NotNil(t, rules.Items, "%s %s has no spec.rules items schema", crd.Name, version)
			require.NotEmpty(t, rules.Items.Schema.Properties, "%s %s has an empty rule schema", crd.Name, version)
			return *rules.Items.Schema
		}
	}
	t.Fatalf("%s does not define version %s", crd.Name, version)
	return apiextensionsv1.JSONSchemaProps{}
}

func jsonValues(t *testing.T, values ...any) []apiextensionsv1.JSON {
	out := make([]apiextensionsv1.JSON, 0, len(values))
	for _, v := range values {
		raw, err := yaml.Marshal(v)
		require.NoError(t, err)
		j, err := yaml.YAMLToJSON(raw)
		require.NoError(t, err)
		out = append(out, apiextensionsv1.JSON{Raw: j})
	}
	return out
}

func ptrInt64(v int64) *int64 {
	return &v
}
