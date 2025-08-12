# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [HTTPProxy](#httpproxy)




## HTTPProxy
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






An HTTPProxy builds on top of Gateway API resources to provide a more convenient
method to manage simple reverse proxy use cases.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
      <td><b>apiVersion</b></td>
      <td>string</td>
      <td>networking.datumapis.com/v1alpha</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>HTTPProxy</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#httpproxyspec">spec</a></b></td>
        <td>object</td>
        <td>
          Spec defines the desired state of an HTTPProxy.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#httpproxystatus">status</a></b></td>
        <td>object</td>
        <td>
          Status defines the current state of an HTTPProxy.<br/>
          <br/>
            <i>Default</i>: map[conditions:[map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Accepted] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Programmed]]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec
<sup><sup>[↩ Parent](#httpproxy)</sup></sup>



Spec defines the desired state of an HTTPProxy.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#httpproxyspecrulesindex">rules</a></b></td>
        <td>[]object</td>
        <td>
          Rules are a list of HTTP matchers, filters and actions.<br/>
          <br/>
            <i>Validations</i>:<li>self.all(l1, !has(l1.name) || self.exists_one(l2, has(l2.name) && l1.name == l2.name)): Rule name must be unique within the route</li><li>(self.size() > 0 ? self[0].matches.size() : 0) + (self.size() > 1 ? self[1].matches.size() : 0) + (self.size() > 2 ? self[2].matches.size() : 0) + (self.size() > 3 ? self[3].matches.size() : 0) + (self.size() > 4 ? self[4].matches.size() : 0) + (self.size() > 5 ? self[5].matches.size() : 0) + (self.size() > 6 ? self[6].matches.size() : 0) + (self.size() > 7 ? self[7].matches.size() : 0) + (self.size() > 8 ? self[8].matches.size() : 0) + (self.size() > 9 ? self[9].matches.size() : 0) + (self.size() > 10 ? self[10].matches.size() : 0) + (self.size() > 11 ? self[11].matches.size() : 0) + (self.size() > 12 ? self[12].matches.size() : 0) + (self.size() > 13 ? self[13].matches.size() : 0) + (self.size() > 14 ? self[14].matches.size() : 0) + (self.size() > 15 ? self[15].matches.size() : 0) <= 128: While 16 rules and 64 matches per rule are allowed, the total number of matches across all rules in a route must be less than 128</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>hostnames</b></td>
        <td>[]string</td>
        <td>
          Hostnames defines a set of hostnames that should match against the HTTP
Host header to select a HTTPProxy used to process the request.

Valid values for Hostnames are determined by RFC 1123 definition of a
hostname with 1 notable exception:

1. IPs are not allowed.

Hostnames must be verified before being programmed. This is accomplished
via the use of `Domain` resources. A hostname is considered verified if any
verified `Domain` resource exists in the same namespace where the
`spec.domainName` of the resource either exactly matches the hostname, or
is a suffix match of the hostname. That means that a Domain with a
`spec.domainName` of `example.com` will match a hostname of
`test.example.com`, `foo.test.example.com`, and exactly `example.com`, but
not a hostname of `test-example.com`. If a `Domain` resource does not exist
that matches a hostname, one will automatically be created when the system
attempts to program the HTTPProxy.

In addition to verifying ownership, hostnames must be unique across the
platform. If a hostname is already programmed on another resource, a
conflict will be encountered and communicated in the `HostnamesVerified`
condition.

Hostnames which have been programmed will be listed in the
`status.hostnames` field. Any hostname which has not been programmed will
be listed in the `message` field of the `HostnamesVerified` condition with
an indication as to why it was not programmed.

The system may automatically generate and associate hostnames with the
HTTPProxy. In such cases, these will be listed in the `status.hostnames`
field and do not require additional configuration by the user.

Wildcard hostnames are not supported at this time.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index]
<sup><sup>[↩ Parent](#httpproxyspec)</sup></sup>



HTTPProxyRule defines semantics for matching an HTTP request based on
conditions (matches), processing it (filters), and forwarding the request to
backends.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindex">backends</a></b></td>
        <td>[]object</td>
        <td>
          Backends defines the backend(s) where matching requests should be
sent.

Note: While this field is a list, only a single element is permitted at
this time due to underlying Gateway limitations. Once addressed, MaxItems
will be increased to allow for multiple backends on any given route.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindex">filters</a></b></td>
        <td>[]object</td>
        <td>
          Filters define the filters that are applied to requests that match
this rule.

See documentation for the `filters` field in the `HTTPRouteRule` type at
https://gateway-api.sigs.k8s.io/reference/spec/#httprouterule<br/>
          <br/>
            <i>Validations</i>:<li>!(self.exists(f, f.type == 'RequestRedirect') && self.exists(f, f.type == 'URLRewrite')): May specify either requestRedirect or urlRewrite, but not both</li><li>self.filter(f, f.type == 'RequestHeaderModifier').size() <= 1: RequestHeaderModifier filter cannot be repeated</li><li>self.filter(f, f.type == 'ResponseHeaderModifier').size() <= 1: ResponseHeaderModifier filter cannot be repeated</li><li>self.filter(f, f.type == 'RequestRedirect').size() <= 1: RequestRedirect filter cannot be repeated</li><li>self.filter(f, f.type == 'URLRewrite').size() <= 1: URLRewrite filter cannot be repeated</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexmatchesindex">matches</a></b></td>
        <td>[]object</td>
        <td>
          Matches define conditions used for matching the rule against incoming
HTTP requests. Each match is independent, i.e. this rule will be matched
if **any** one of the matches is satisfied.

See documentation for the `matches` field in the `HTTPRouteRule` type at
https://gateway-api.sigs.k8s.io/reference/spec/#httprouterule<br/>
          <br/>
            <i>Default</i>: [map[path:map[type:PathPrefix value:/]]]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the route rule. This name MUST be unique within a Route
if it is set.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindex)</sup></sup>





<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>endpoint</b></td>
        <td>string</td>
        <td>
          Endpoint for the backend. Must be a valid URL.

Supports http and https protocols, IPs or DNS addresses in the host, custom
ports, and paths.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindex">filters</a></b></td>
        <td>[]object</td>
        <td>
          Filters defined at this level should be executed if and only if the
request is being forwarded to the backend defined here.<br/>
          <br/>
            <i>Validations</i>:<li>!(self.exists(f, f.type == 'RequestRedirect') && self.exists(f, f.type == 'URLRewrite')): May specify either requestRedirect or urlRewrite, but not both</li><li>self.filter(f, f.type == 'RequestHeaderModifier').size() <= 1: RequestHeaderModifier filter cannot be repeated</li><li>self.filter(f, f.type == 'ResponseHeaderModifier').size() <= 1: ResponseHeaderModifier filter cannot be repeated</li><li>self.filter(f, f.type == 'RequestRedirect').size() <= 1: RequestRedirect filter cannot be repeated</li><li>self.filter(f, f.type == 'URLRewrite').size() <= 1: URLRewrite filter cannot be repeated</li>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindex)</sup></sup>



HTTPRouteFilter defines processing steps that must be completed during the
request or response lifecycle. HTTPRouteFilters are meant as an extension
point to express processing that may be done in Gateway implementations. Some
examples include request or response modification, implementing
authentication strategies, rate-limiting, and traffic shaping. API
guarantee/conformance is defined based on the type of the filter.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          Type identifies the type of filter to apply. As with other API fields,
types are classified into three conformance levels:

- Core: Filter types and their corresponding configuration defined by
  "Support: Core" in this package, e.g. "RequestHeaderModifier". All
  implementations must support core filters.

- Extended: Filter types and their corresponding configuration defined by
  "Support: Extended" in this package, e.g. "RequestMirror". Implementers
  are encouraged to support extended filters.

- Implementation-specific: Filters that are defined and supported by
  specific vendors.
  In the future, filters showing convergence in behavior across multiple
  implementations will be considered for inclusion in extended or core
  conformance levels. Filter-specific configuration for such filters
  is specified using the ExtensionRef field. `Type` should be set to
  "ExtensionRef" for custom filters.

Implementers are encouraged to define custom implementation types to
extend the core API with implementation-specific behavior.

If a reference to a custom filter type cannot be resolved, the filter
MUST NOT be skipped. Instead, requests that would have been processed by
that filter MUST receive a HTTP error response.

Note that values may be added to this enum, implementations
must ensure that unknown values will not cause a crash.

Unknown values here must result in the implementation setting the
Accepted Condition for the Route to `status: False`, with a
Reason of `UnsupportedValue`.<br/>
          <br/>
            <i>Enum</i>: RequestHeaderModifier, ResponseHeaderModifier, RequestMirror, RequestRedirect, URLRewrite, ExtensionRef<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexextensionref">extensionRef</a></b></td>
        <td>object</td>
        <td>
          ExtensionRef is an optional, implementation-specific extension to the
"filter" behavior.  For example, resource "myroutefilter" in group
"networking.example.net"). ExtensionRef MUST NOT be used for core and
extended filters.

This filter can be used multiple times within the same rule.

Support: Implementation-specific<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexrequestheadermodifier">requestHeaderModifier</a></b></td>
        <td>object</td>
        <td>
          RequestHeaderModifier defines a schema for a filter that modifies request
headers.

Support: Core<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexrequestmirror">requestMirror</a></b></td>
        <td>object</td>
        <td>
          RequestMirror defines a schema for a filter that mirrors requests.
Requests are sent to the specified destination, but responses from
that destination are ignored.

This filter can be used multiple times within the same rule. Note that
not all implementations will be able to support mirroring to multiple
backends.

Support: Extended

<gateway:experimental:validation:XValidation:message="Only one of percent or fraction may be specified in HTTPRequestMirrorFilter",rule="!(has(self.percent) && has(self.fraction))"><br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexrequestredirect">requestRedirect</a></b></td>
        <td>object</td>
        <td>
          RequestRedirect defines a schema for a filter that responds to the
request with an HTTP redirection.

Support: Core<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexresponseheadermodifier">responseHeaderModifier</a></b></td>
        <td>object</td>
        <td>
          ResponseHeaderModifier defines a schema for a filter that modifies response
headers.

Support: Extended<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexurlrewrite">urlRewrite</a></b></td>
        <td>object</td>
        <td>
          URLRewrite defines a schema for a filter that modifies a request during forwarding.

Support: Extended<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].extensionRef
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindex)</sup></sup>



ExtensionRef is an optional, implementation-specific extension to the
"filter" behavior.  For example, resource "myroutefilter" in group
"networking.example.net"). ExtensionRef MUST NOT be used for core and
extended filters.

This filter can be used multiple times within the same rule.

Support: Implementation-specific

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>group</b></td>
        <td>string</td>
        <td>
          Group is the group of the referent. For example, "gateway.networking.k8s.io".
When unspecified or empty string, core API group is inferred.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind is kind of the referent. For example "HTTPRoute" or "Service".<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the referent.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].requestHeaderModifier
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindex)</sup></sup>



RequestHeaderModifier defines a schema for a filter that modifies request
headers.

Support: Core

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexrequestheadermodifieraddindex">add</a></b></td>
        <td>[]object</td>
        <td>
          Add adds the given header(s) (name, value) to the request
before the action. It appends to any existing values associated
with the header name.

Input:
  GET /foo HTTP/1.1
  my-header: foo

Config:
  add:
  - name: "my-header"
    value: "bar,baz"

Output:
  GET /foo HTTP/1.1
  my-header: foo,bar,baz<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>remove</b></td>
        <td>[]string</td>
        <td>
          Remove the given header(s) from the HTTP request before the action. The
value of Remove is a list of HTTP header names. Note that the header
names are case-insensitive (see
https://datatracker.ietf.org/doc/html/rfc2616#section-4.2).

Input:
  GET /foo HTTP/1.1
  my-header1: foo
  my-header2: bar
  my-header3: baz

Config:
  remove: ["my-header1", "my-header3"]

Output:
  GET /foo HTTP/1.1
  my-header2: bar<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexrequestheadermodifiersetindex">set</a></b></td>
        <td>[]object</td>
        <td>
          Set overwrites the request with the given header (name, value)
before the action.

Input:
  GET /foo HTTP/1.1
  my-header: foo

Config:
  set:
  - name: "my-header"
    value: "bar"

Output:
  GET /foo HTTP/1.1
  my-header: bar<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].requestHeaderModifier.add[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindexrequestheadermodifier)</sup></sup>



HTTPHeader represents an HTTP Header name and value as defined by RFC 7230.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the HTTP Header to be matched. Name matching MUST be
case insensitive. (See https://tools.ietf.org/html/rfc7230#section-3.2).

If multiple entries specify equivalent header names, the first entry with
an equivalent name MUST be considered for a match. Subsequent entries
with an equivalent header name MUST be ignored. Due to the
case-insensitivity of header names, "foo" and "Foo" are considered
equivalent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value is the value of HTTP Header to be matched.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].requestHeaderModifier.set[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindexrequestheadermodifier)</sup></sup>



HTTPHeader represents an HTTP Header name and value as defined by RFC 7230.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the HTTP Header to be matched. Name matching MUST be
case insensitive. (See https://tools.ietf.org/html/rfc7230#section-3.2).

If multiple entries specify equivalent header names, the first entry with
an equivalent name MUST be considered for a match. Subsequent entries
with an equivalent header name MUST be ignored. Due to the
case-insensitivity of header names, "foo" and "Foo" are considered
equivalent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value is the value of HTTP Header to be matched.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].requestMirror
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindex)</sup></sup>



RequestMirror defines a schema for a filter that mirrors requests.
Requests are sent to the specified destination, but responses from
that destination are ignored.

This filter can be used multiple times within the same rule. Note that
not all implementations will be able to support mirroring to multiple
backends.

Support: Extended

<gateway:experimental:validation:XValidation:message="Only one of percent or fraction may be specified in HTTPRequestMirrorFilter",rule="!(has(self.percent) && has(self.fraction))">

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexrequestmirrorbackendref">backendRef</a></b></td>
        <td>object</td>
        <td>
          BackendRef references a resource where mirrored requests are sent.

Mirrored requests must be sent only to a single destination endpoint
within this BackendRef, irrespective of how many endpoints are present
within this BackendRef.

If the referent cannot be found, this BackendRef is invalid and must be
dropped from the Gateway. The controller must ensure the "ResolvedRefs"
condition on the Route status is set to `status: False` and not configure
this backend in the underlying implementation.

If there is a cross-namespace reference to an *existing* object
that is not allowed by a ReferenceGrant, the controller must ensure the
"ResolvedRefs"  condition on the Route is set to `status: False`,
with the "RefNotPermitted" reason and not configure this backend in the
underlying implementation.

In either error case, the Message of the `ResolvedRefs` Condition
should be used to provide more detail about the problem.

Support: Extended for Kubernetes Service

Support: Implementation-specific for any other resource<br/>
          <br/>
            <i>Validations</i>:<li>(size(self.group) == 0 && self.kind == 'Service') ? has(self.port) : true: Must have port for Service reference</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexrequestmirrorfraction">fraction</a></b></td>
        <td>object</td>
        <td>
          Fraction represents the fraction of requests that should be
mirrored to BackendRef.

Only one of Fraction or Percent may be specified. If neither field
is specified, 100% of requests will be mirrored.

<gateway:experimental><br/>
          <br/>
            <i>Validations</i>:<li>self.numerator <= self.denominator: numerator must be less than or equal to denominator</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>percent</b></td>
        <td>integer</td>
        <td>
          Percent represents the percentage of requests that should be
mirrored to BackendRef. Its minimum value is 0 (indicating 0% of
requests) and its maximum value is 100 (indicating 100% of requests).

Only one of Fraction or Percent may be specified. If neither field
is specified, 100% of requests will be mirrored.

<gateway:experimental><br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 0<br/>
            <i>Maximum</i>: 100<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].requestMirror.backendRef
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindexrequestmirror)</sup></sup>



BackendRef references a resource where mirrored requests are sent.

Mirrored requests must be sent only to a single destination endpoint
within this BackendRef, irrespective of how many endpoints are present
within this BackendRef.

If the referent cannot be found, this BackendRef is invalid and must be
dropped from the Gateway. The controller must ensure the "ResolvedRefs"
condition on the Route status is set to `status: False` and not configure
this backend in the underlying implementation.

If there is a cross-namespace reference to an *existing* object
that is not allowed by a ReferenceGrant, the controller must ensure the
"ResolvedRefs"  condition on the Route is set to `status: False`,
with the "RefNotPermitted" reason and not configure this backend in the
underlying implementation.

In either error case, the Message of the `ResolvedRefs` Condition
should be used to provide more detail about the problem.

Support: Extended for Kubernetes Service

Support: Implementation-specific for any other resource

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the referent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>group</b></td>
        <td>string</td>
        <td>
          Group is the group of the referent. For example, "gateway.networking.k8s.io".
When unspecified or empty string, core API group is inferred.<br/>
          <br/>
            <i>Default</i>: <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind is the Kubernetes resource kind of the referent. For example
"Service".

Defaults to "Service" when not specified.

ExternalName services can refer to CNAME DNS records that may live
outside of the cluster and as such are difficult to reason about in
terms of conformance. They also may not be safe to forward to (see
CVE-2021-25740 for more information). Implementations SHOULD NOT
support ExternalName Services.

Support: Core (Services with a type other than ExternalName)

Support: Implementation-specific (Services with type ExternalName)<br/>
          <br/>
            <i>Default</i>: Service<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of the backend. When unspecified, the local
namespace is inferred.

Note that when a namespace different than the local namespace is specified,
a ReferenceGrant object is required in the referent namespace to allow that
namespace's owner to accept the reference. See the ReferenceGrant
documentation for details.

Support: Core<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>port</b></td>
        <td>integer</td>
        <td>
          Port specifies the destination port number to use for this resource.
Port is required when the referent is a Kubernetes Service. In this
case, the port number is the service port number, not the target port.
For other resources, destination port might be derived from the referent
resource or this field.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 65535<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].requestMirror.fraction
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindexrequestmirror)</sup></sup>



Fraction represents the fraction of requests that should be
mirrored to BackendRef.

Only one of Fraction or Percent may be specified. If neither field
is specified, 100% of requests will be mirrored.

<gateway:experimental>

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>numerator</b></td>
        <td>integer</td>
        <td>
          <br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>denominator</b></td>
        <td>integer</td>
        <td>
          <br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Default</i>: 100<br/>
            <i>Minimum</i>: 1<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].requestRedirect
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindex)</sup></sup>



RequestRedirect defines a schema for a filter that responds to the
request with an HTTP redirection.

Support: Core

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>hostname</b></td>
        <td>string</td>
        <td>
          Hostname is the hostname to be used in the value of the `Location`
header in the response.
When empty, the hostname in the `Host` header of the request is used.

Support: Core<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexrequestredirectpath">path</a></b></td>
        <td>object</td>
        <td>
          Path defines parameters used to modify the path of the incoming request.
The modified path is then used to construct the `Location` header. When
empty, the request path is used as-is.

Support: Extended<br/>
          <br/>
            <i>Validations</i>:<li>self.type == 'ReplaceFullPath' ? has(self.replaceFullPath) : true: replaceFullPath must be specified when type is set to 'ReplaceFullPath'</li><li>has(self.replaceFullPath) ? self.type == 'ReplaceFullPath' : true: type must be 'ReplaceFullPath' when replaceFullPath is set</li><li>self.type == 'ReplacePrefixMatch' ? has(self.replacePrefixMatch) : true: replacePrefixMatch must be specified when type is set to 'ReplacePrefixMatch'</li><li>has(self.replacePrefixMatch) ? self.type == 'ReplacePrefixMatch' : true: type must be 'ReplacePrefixMatch' when replacePrefixMatch is set</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>port</b></td>
        <td>integer</td>
        <td>
          Port is the port to be used in the value of the `Location`
header in the response.

If no port is specified, the redirect port MUST be derived using the
following rules:

* If redirect scheme is not-empty, the redirect port MUST be the well-known
  port associated with the redirect scheme. Specifically "http" to port 80
  and "https" to port 443. If the redirect scheme does not have a
  well-known port, the listener port of the Gateway SHOULD be used.
* If redirect scheme is empty, the redirect port MUST be the Gateway
  Listener port.

Implementations SHOULD NOT add the port number in the 'Location'
header in the following cases:

* A Location header that will use HTTP (whether that is determined via
  the Listener protocol or the Scheme field) _and_ use port 80.
* A Location header that will use HTTPS (whether that is determined via
  the Listener protocol or the Scheme field) _and_ use port 443.

Support: Extended<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 65535<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>scheme</b></td>
        <td>enum</td>
        <td>
          Scheme is the scheme to be used in the value of the `Location` header in
the response. When empty, the scheme of the request is used.

Scheme redirects can affect the port of the redirect, for more information,
refer to the documentation for the port field of this filter.

Note that values may be added to this enum, implementations
must ensure that unknown values will not cause a crash.

Unknown values here must result in the implementation setting the
Accepted Condition for the Route to `status: False`, with a
Reason of `UnsupportedValue`.

Support: Extended<br/>
          <br/>
            <i>Enum</i>: http, https<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>statusCode</b></td>
        <td>integer</td>
        <td>
          StatusCode is the HTTP status code to be used in response.

Note that values may be added to this enum, implementations
must ensure that unknown values will not cause a crash.

Unknown values here must result in the implementation setting the
Accepted Condition for the Route to `status: False`, with a
Reason of `UnsupportedValue`.

Support: Core<br/>
          <br/>
            <i>Enum</i>: 301, 302<br/>
            <i>Default</i>: 302<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].requestRedirect.path
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindexrequestredirect)</sup></sup>



Path defines parameters used to modify the path of the incoming request.
The modified path is then used to construct the `Location` header. When
empty, the request path is used as-is.

Support: Extended

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          Type defines the type of path modifier. Additional types may be
added in a future release of the API.

Note that values may be added to this enum, implementations
must ensure that unknown values will not cause a crash.

Unknown values here must result in the implementation setting the
Accepted Condition for the Route to `status: False`, with a
Reason of `UnsupportedValue`.<br/>
          <br/>
            <i>Enum</i>: ReplaceFullPath, ReplacePrefixMatch<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>replaceFullPath</b></td>
        <td>string</td>
        <td>
          ReplaceFullPath specifies the value with which to replace the full path
of a request during a rewrite or redirect.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>replacePrefixMatch</b></td>
        <td>string</td>
        <td>
          ReplacePrefixMatch specifies the value with which to replace the prefix
match of a request during a rewrite or redirect. For example, a request
to "/foo/bar" with a prefix match of "/foo" and a ReplacePrefixMatch
of "/xyz" would be modified to "/xyz/bar".

Note that this matches the behavior of the PathPrefix match type. This
matches full path elements. A path element refers to the list of labels
in the path split by the `/` separator. When specified, a trailing `/` is
ignored. For example, the paths `/abc`, `/abc/`, and `/abc/def` would all
match the prefix `/abc`, but the path `/abcd` would not.

ReplacePrefixMatch is only compatible with a `PathPrefix` HTTPRouteMatch.
Using any other HTTPRouteMatch type on the same HTTPRouteRule will result in
the implementation setting the Accepted Condition for the Route to `status: False`.

Request Path | Prefix Match | Replace Prefix | Modified Path<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].responseHeaderModifier
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindex)</sup></sup>



ResponseHeaderModifier defines a schema for a filter that modifies response
headers.

Support: Extended

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexresponseheadermodifieraddindex">add</a></b></td>
        <td>[]object</td>
        <td>
          Add adds the given header(s) (name, value) to the request
before the action. It appends to any existing values associated
with the header name.

Input:
  GET /foo HTTP/1.1
  my-header: foo

Config:
  add:
  - name: "my-header"
    value: "bar,baz"

Output:
  GET /foo HTTP/1.1
  my-header: foo,bar,baz<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>remove</b></td>
        <td>[]string</td>
        <td>
          Remove the given header(s) from the HTTP request before the action. The
value of Remove is a list of HTTP header names. Note that the header
names are case-insensitive (see
https://datatracker.ietf.org/doc/html/rfc2616#section-4.2).

Input:
  GET /foo HTTP/1.1
  my-header1: foo
  my-header2: bar
  my-header3: baz

Config:
  remove: ["my-header1", "my-header3"]

Output:
  GET /foo HTTP/1.1
  my-header2: bar<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexresponseheadermodifiersetindex">set</a></b></td>
        <td>[]object</td>
        <td>
          Set overwrites the request with the given header (name, value)
before the action.

Input:
  GET /foo HTTP/1.1
  my-header: foo

Config:
  set:
  - name: "my-header"
    value: "bar"

Output:
  GET /foo HTTP/1.1
  my-header: bar<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].responseHeaderModifier.add[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindexresponseheadermodifier)</sup></sup>



HTTPHeader represents an HTTP Header name and value as defined by RFC 7230.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the HTTP Header to be matched. Name matching MUST be
case insensitive. (See https://tools.ietf.org/html/rfc7230#section-3.2).

If multiple entries specify equivalent header names, the first entry with
an equivalent name MUST be considered for a match. Subsequent entries
with an equivalent header name MUST be ignored. Due to the
case-insensitivity of header names, "foo" and "Foo" are considered
equivalent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value is the value of HTTP Header to be matched.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].responseHeaderModifier.set[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindexresponseheadermodifier)</sup></sup>



HTTPHeader represents an HTTP Header name and value as defined by RFC 7230.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the HTTP Header to be matched. Name matching MUST be
case insensitive. (See https://tools.ietf.org/html/rfc7230#section-3.2).

If multiple entries specify equivalent header names, the first entry with
an equivalent name MUST be considered for a match. Subsequent entries
with an equivalent header name MUST be ignored. Due to the
case-insensitivity of header names, "foo" and "Foo" are considered
equivalent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value is the value of HTTP Header to be matched.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].urlRewrite
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindex)</sup></sup>



URLRewrite defines a schema for a filter that modifies a request during forwarding.

Support: Extended

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>hostname</b></td>
        <td>string</td>
        <td>
          Hostname is the value to be used to replace the Host header value during
forwarding.

Support: Extended<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexbackendsindexfiltersindexurlrewritepath">path</a></b></td>
        <td>object</td>
        <td>
          Path defines a path rewrite.

Support: Extended<br/>
          <br/>
            <i>Validations</i>:<li>self.type == 'ReplaceFullPath' ? has(self.replaceFullPath) : true: replaceFullPath must be specified when type is set to 'ReplaceFullPath'</li><li>has(self.replaceFullPath) ? self.type == 'ReplaceFullPath' : true: type must be 'ReplaceFullPath' when replaceFullPath is set</li><li>self.type == 'ReplacePrefixMatch' ? has(self.replacePrefixMatch) : true: replacePrefixMatch must be specified when type is set to 'ReplacePrefixMatch'</li><li>has(self.replacePrefixMatch) ? self.type == 'ReplacePrefixMatch' : true: type must be 'ReplacePrefixMatch' when replacePrefixMatch is set</li>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].backends[index].filters[index].urlRewrite.path
<sup><sup>[↩ Parent](#httpproxyspecrulesindexbackendsindexfiltersindexurlrewrite)</sup></sup>



Path defines a path rewrite.

Support: Extended

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          Type defines the type of path modifier. Additional types may be
added in a future release of the API.

Note that values may be added to this enum, implementations
must ensure that unknown values will not cause a crash.

Unknown values here must result in the implementation setting the
Accepted Condition for the Route to `status: False`, with a
Reason of `UnsupportedValue`.<br/>
          <br/>
            <i>Enum</i>: ReplaceFullPath, ReplacePrefixMatch<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>replaceFullPath</b></td>
        <td>string</td>
        <td>
          ReplaceFullPath specifies the value with which to replace the full path
of a request during a rewrite or redirect.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>replacePrefixMatch</b></td>
        <td>string</td>
        <td>
          ReplacePrefixMatch specifies the value with which to replace the prefix
match of a request during a rewrite or redirect. For example, a request
to "/foo/bar" with a prefix match of "/foo" and a ReplacePrefixMatch
of "/xyz" would be modified to "/xyz/bar".

Note that this matches the behavior of the PathPrefix match type. This
matches full path elements. A path element refers to the list of labels
in the path split by the `/` separator. When specified, a trailing `/` is
ignored. For example, the paths `/abc`, `/abc/`, and `/abc/def` would all
match the prefix `/abc`, but the path `/abcd` would not.

ReplacePrefixMatch is only compatible with a `PathPrefix` HTTPRouteMatch.
Using any other HTTPRouteMatch type on the same HTTPRouteRule will result in
the implementation setting the Accepted Condition for the Route to `status: False`.

Request Path | Prefix Match | Replace Prefix | Modified Path<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindex)</sup></sup>



HTTPRouteFilter defines processing steps that must be completed during the
request or response lifecycle. HTTPRouteFilters are meant as an extension
point to express processing that may be done in Gateway implementations. Some
examples include request or response modification, implementing
authentication strategies, rate-limiting, and traffic shaping. API
guarantee/conformance is defined based on the type of the filter.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          Type identifies the type of filter to apply. As with other API fields,
types are classified into three conformance levels:

- Core: Filter types and their corresponding configuration defined by
  "Support: Core" in this package, e.g. "RequestHeaderModifier". All
  implementations must support core filters.

- Extended: Filter types and their corresponding configuration defined by
  "Support: Extended" in this package, e.g. "RequestMirror". Implementers
  are encouraged to support extended filters.

- Implementation-specific: Filters that are defined and supported by
  specific vendors.
  In the future, filters showing convergence in behavior across multiple
  implementations will be considered for inclusion in extended or core
  conformance levels. Filter-specific configuration for such filters
  is specified using the ExtensionRef field. `Type` should be set to
  "ExtensionRef" for custom filters.

Implementers are encouraged to define custom implementation types to
extend the core API with implementation-specific behavior.

If a reference to a custom filter type cannot be resolved, the filter
MUST NOT be skipped. Instead, requests that would have been processed by
that filter MUST receive a HTTP error response.

Note that values may be added to this enum, implementations
must ensure that unknown values will not cause a crash.

Unknown values here must result in the implementation setting the
Accepted Condition for the Route to `status: False`, with a
Reason of `UnsupportedValue`.<br/>
          <br/>
            <i>Enum</i>: RequestHeaderModifier, ResponseHeaderModifier, RequestMirror, RequestRedirect, URLRewrite, ExtensionRef<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexextensionref">extensionRef</a></b></td>
        <td>object</td>
        <td>
          ExtensionRef is an optional, implementation-specific extension to the
"filter" behavior.  For example, resource "myroutefilter" in group
"networking.example.net"). ExtensionRef MUST NOT be used for core and
extended filters.

This filter can be used multiple times within the same rule.

Support: Implementation-specific<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexrequestheadermodifier">requestHeaderModifier</a></b></td>
        <td>object</td>
        <td>
          RequestHeaderModifier defines a schema for a filter that modifies request
headers.

Support: Core<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexrequestmirror">requestMirror</a></b></td>
        <td>object</td>
        <td>
          RequestMirror defines a schema for a filter that mirrors requests.
Requests are sent to the specified destination, but responses from
that destination are ignored.

This filter can be used multiple times within the same rule. Note that
not all implementations will be able to support mirroring to multiple
backends.

Support: Extended

<gateway:experimental:validation:XValidation:message="Only one of percent or fraction may be specified in HTTPRequestMirrorFilter",rule="!(has(self.percent) && has(self.fraction))"><br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexrequestredirect">requestRedirect</a></b></td>
        <td>object</td>
        <td>
          RequestRedirect defines a schema for a filter that responds to the
request with an HTTP redirection.

Support: Core<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexresponseheadermodifier">responseHeaderModifier</a></b></td>
        <td>object</td>
        <td>
          ResponseHeaderModifier defines a schema for a filter that modifies response
headers.

Support: Extended<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexurlrewrite">urlRewrite</a></b></td>
        <td>object</td>
        <td>
          URLRewrite defines a schema for a filter that modifies a request during forwarding.

Support: Extended<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].extensionRef
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindex)</sup></sup>



ExtensionRef is an optional, implementation-specific extension to the
"filter" behavior.  For example, resource "myroutefilter" in group
"networking.example.net"). ExtensionRef MUST NOT be used for core and
extended filters.

This filter can be used multiple times within the same rule.

Support: Implementation-specific

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>group</b></td>
        <td>string</td>
        <td>
          Group is the group of the referent. For example, "gateway.networking.k8s.io".
When unspecified or empty string, core API group is inferred.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind is kind of the referent. For example "HTTPRoute" or "Service".<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the referent.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].requestHeaderModifier
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindex)</sup></sup>



RequestHeaderModifier defines a schema for a filter that modifies request
headers.

Support: Core

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexrequestheadermodifieraddindex">add</a></b></td>
        <td>[]object</td>
        <td>
          Add adds the given header(s) (name, value) to the request
before the action. It appends to any existing values associated
with the header name.

Input:
  GET /foo HTTP/1.1
  my-header: foo

Config:
  add:
  - name: "my-header"
    value: "bar,baz"

Output:
  GET /foo HTTP/1.1
  my-header: foo,bar,baz<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>remove</b></td>
        <td>[]string</td>
        <td>
          Remove the given header(s) from the HTTP request before the action. The
value of Remove is a list of HTTP header names. Note that the header
names are case-insensitive (see
https://datatracker.ietf.org/doc/html/rfc2616#section-4.2).

Input:
  GET /foo HTTP/1.1
  my-header1: foo
  my-header2: bar
  my-header3: baz

Config:
  remove: ["my-header1", "my-header3"]

Output:
  GET /foo HTTP/1.1
  my-header2: bar<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexrequestheadermodifiersetindex">set</a></b></td>
        <td>[]object</td>
        <td>
          Set overwrites the request with the given header (name, value)
before the action.

Input:
  GET /foo HTTP/1.1
  my-header: foo

Config:
  set:
  - name: "my-header"
    value: "bar"

Output:
  GET /foo HTTP/1.1
  my-header: bar<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].requestHeaderModifier.add[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindexrequestheadermodifier)</sup></sup>



HTTPHeader represents an HTTP Header name and value as defined by RFC 7230.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the HTTP Header to be matched. Name matching MUST be
case insensitive. (See https://tools.ietf.org/html/rfc7230#section-3.2).

If multiple entries specify equivalent header names, the first entry with
an equivalent name MUST be considered for a match. Subsequent entries
with an equivalent header name MUST be ignored. Due to the
case-insensitivity of header names, "foo" and "Foo" are considered
equivalent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value is the value of HTTP Header to be matched.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].requestHeaderModifier.set[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindexrequestheadermodifier)</sup></sup>



HTTPHeader represents an HTTP Header name and value as defined by RFC 7230.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the HTTP Header to be matched. Name matching MUST be
case insensitive. (See https://tools.ietf.org/html/rfc7230#section-3.2).

If multiple entries specify equivalent header names, the first entry with
an equivalent name MUST be considered for a match. Subsequent entries
with an equivalent header name MUST be ignored. Due to the
case-insensitivity of header names, "foo" and "Foo" are considered
equivalent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value is the value of HTTP Header to be matched.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].requestMirror
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindex)</sup></sup>



RequestMirror defines a schema for a filter that mirrors requests.
Requests are sent to the specified destination, but responses from
that destination are ignored.

This filter can be used multiple times within the same rule. Note that
not all implementations will be able to support mirroring to multiple
backends.

Support: Extended

<gateway:experimental:validation:XValidation:message="Only one of percent or fraction may be specified in HTTPRequestMirrorFilter",rule="!(has(self.percent) && has(self.fraction))">

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexrequestmirrorbackendref">backendRef</a></b></td>
        <td>object</td>
        <td>
          BackendRef references a resource where mirrored requests are sent.

Mirrored requests must be sent only to a single destination endpoint
within this BackendRef, irrespective of how many endpoints are present
within this BackendRef.

If the referent cannot be found, this BackendRef is invalid and must be
dropped from the Gateway. The controller must ensure the "ResolvedRefs"
condition on the Route status is set to `status: False` and not configure
this backend in the underlying implementation.

If there is a cross-namespace reference to an *existing* object
that is not allowed by a ReferenceGrant, the controller must ensure the
"ResolvedRefs"  condition on the Route is set to `status: False`,
with the "RefNotPermitted" reason and not configure this backend in the
underlying implementation.

In either error case, the Message of the `ResolvedRefs` Condition
should be used to provide more detail about the problem.

Support: Extended for Kubernetes Service

Support: Implementation-specific for any other resource<br/>
          <br/>
            <i>Validations</i>:<li>(size(self.group) == 0 && self.kind == 'Service') ? has(self.port) : true: Must have port for Service reference</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexrequestmirrorfraction">fraction</a></b></td>
        <td>object</td>
        <td>
          Fraction represents the fraction of requests that should be
mirrored to BackendRef.

Only one of Fraction or Percent may be specified. If neither field
is specified, 100% of requests will be mirrored.

<gateway:experimental><br/>
          <br/>
            <i>Validations</i>:<li>self.numerator <= self.denominator: numerator must be less than or equal to denominator</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>percent</b></td>
        <td>integer</td>
        <td>
          Percent represents the percentage of requests that should be
mirrored to BackendRef. Its minimum value is 0 (indicating 0% of
requests) and its maximum value is 100 (indicating 100% of requests).

Only one of Fraction or Percent may be specified. If neither field
is specified, 100% of requests will be mirrored.

<gateway:experimental><br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 0<br/>
            <i>Maximum</i>: 100<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].requestMirror.backendRef
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindexrequestmirror)</sup></sup>



BackendRef references a resource where mirrored requests are sent.

Mirrored requests must be sent only to a single destination endpoint
within this BackendRef, irrespective of how many endpoints are present
within this BackendRef.

If the referent cannot be found, this BackendRef is invalid and must be
dropped from the Gateway. The controller must ensure the "ResolvedRefs"
condition on the Route status is set to `status: False` and not configure
this backend in the underlying implementation.

If there is a cross-namespace reference to an *existing* object
that is not allowed by a ReferenceGrant, the controller must ensure the
"ResolvedRefs"  condition on the Route is set to `status: False`,
with the "RefNotPermitted" reason and not configure this backend in the
underlying implementation.

In either error case, the Message of the `ResolvedRefs` Condition
should be used to provide more detail about the problem.

Support: Extended for Kubernetes Service

Support: Implementation-specific for any other resource

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the referent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>group</b></td>
        <td>string</td>
        <td>
          Group is the group of the referent. For example, "gateway.networking.k8s.io".
When unspecified or empty string, core API group is inferred.<br/>
          <br/>
            <i>Default</i>: <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind is the Kubernetes resource kind of the referent. For example
"Service".

Defaults to "Service" when not specified.

ExternalName services can refer to CNAME DNS records that may live
outside of the cluster and as such are difficult to reason about in
terms of conformance. They also may not be safe to forward to (see
CVE-2021-25740 for more information). Implementations SHOULD NOT
support ExternalName Services.

Support: Core (Services with a type other than ExternalName)

Support: Implementation-specific (Services with type ExternalName)<br/>
          <br/>
            <i>Default</i>: Service<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of the backend. When unspecified, the local
namespace is inferred.

Note that when a namespace different than the local namespace is specified,
a ReferenceGrant object is required in the referent namespace to allow that
namespace's owner to accept the reference. See the ReferenceGrant
documentation for details.

Support: Core<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>port</b></td>
        <td>integer</td>
        <td>
          Port specifies the destination port number to use for this resource.
Port is required when the referent is a Kubernetes Service. In this
case, the port number is the service port number, not the target port.
For other resources, destination port might be derived from the referent
resource or this field.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 65535<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].requestMirror.fraction
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindexrequestmirror)</sup></sup>



Fraction represents the fraction of requests that should be
mirrored to BackendRef.

Only one of Fraction or Percent may be specified. If neither field
is specified, 100% of requests will be mirrored.

<gateway:experimental>

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>numerator</b></td>
        <td>integer</td>
        <td>
          <br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>denominator</b></td>
        <td>integer</td>
        <td>
          <br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Default</i>: 100<br/>
            <i>Minimum</i>: 1<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].requestRedirect
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindex)</sup></sup>



RequestRedirect defines a schema for a filter that responds to the
request with an HTTP redirection.

Support: Core

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>hostname</b></td>
        <td>string</td>
        <td>
          Hostname is the hostname to be used in the value of the `Location`
header in the response.
When empty, the hostname in the `Host` header of the request is used.

Support: Core<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexrequestredirectpath">path</a></b></td>
        <td>object</td>
        <td>
          Path defines parameters used to modify the path of the incoming request.
The modified path is then used to construct the `Location` header. When
empty, the request path is used as-is.

Support: Extended<br/>
          <br/>
            <i>Validations</i>:<li>self.type == 'ReplaceFullPath' ? has(self.replaceFullPath) : true: replaceFullPath must be specified when type is set to 'ReplaceFullPath'</li><li>has(self.replaceFullPath) ? self.type == 'ReplaceFullPath' : true: type must be 'ReplaceFullPath' when replaceFullPath is set</li><li>self.type == 'ReplacePrefixMatch' ? has(self.replacePrefixMatch) : true: replacePrefixMatch must be specified when type is set to 'ReplacePrefixMatch'</li><li>has(self.replacePrefixMatch) ? self.type == 'ReplacePrefixMatch' : true: type must be 'ReplacePrefixMatch' when replacePrefixMatch is set</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>port</b></td>
        <td>integer</td>
        <td>
          Port is the port to be used in the value of the `Location`
header in the response.

If no port is specified, the redirect port MUST be derived using the
following rules:

* If redirect scheme is not-empty, the redirect port MUST be the well-known
  port associated with the redirect scheme. Specifically "http" to port 80
  and "https" to port 443. If the redirect scheme does not have a
  well-known port, the listener port of the Gateway SHOULD be used.
* If redirect scheme is empty, the redirect port MUST be the Gateway
  Listener port.

Implementations SHOULD NOT add the port number in the 'Location'
header in the following cases:

* A Location header that will use HTTP (whether that is determined via
  the Listener protocol or the Scheme field) _and_ use port 80.
* A Location header that will use HTTPS (whether that is determined via
  the Listener protocol or the Scheme field) _and_ use port 443.

Support: Extended<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 65535<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>scheme</b></td>
        <td>enum</td>
        <td>
          Scheme is the scheme to be used in the value of the `Location` header in
the response. When empty, the scheme of the request is used.

Scheme redirects can affect the port of the redirect, for more information,
refer to the documentation for the port field of this filter.

Note that values may be added to this enum, implementations
must ensure that unknown values will not cause a crash.

Unknown values here must result in the implementation setting the
Accepted Condition for the Route to `status: False`, with a
Reason of `UnsupportedValue`.

Support: Extended<br/>
          <br/>
            <i>Enum</i>: http, https<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>statusCode</b></td>
        <td>integer</td>
        <td>
          StatusCode is the HTTP status code to be used in response.

Note that values may be added to this enum, implementations
must ensure that unknown values will not cause a crash.

Unknown values here must result in the implementation setting the
Accepted Condition for the Route to `status: False`, with a
Reason of `UnsupportedValue`.

Support: Core<br/>
          <br/>
            <i>Enum</i>: 301, 302<br/>
            <i>Default</i>: 302<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].requestRedirect.path
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindexrequestredirect)</sup></sup>



Path defines parameters used to modify the path of the incoming request.
The modified path is then used to construct the `Location` header. When
empty, the request path is used as-is.

Support: Extended

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          Type defines the type of path modifier. Additional types may be
added in a future release of the API.

Note that values may be added to this enum, implementations
must ensure that unknown values will not cause a crash.

Unknown values here must result in the implementation setting the
Accepted Condition for the Route to `status: False`, with a
Reason of `UnsupportedValue`.<br/>
          <br/>
            <i>Enum</i>: ReplaceFullPath, ReplacePrefixMatch<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>replaceFullPath</b></td>
        <td>string</td>
        <td>
          ReplaceFullPath specifies the value with which to replace the full path
of a request during a rewrite or redirect.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>replacePrefixMatch</b></td>
        <td>string</td>
        <td>
          ReplacePrefixMatch specifies the value with which to replace the prefix
match of a request during a rewrite or redirect. For example, a request
to "/foo/bar" with a prefix match of "/foo" and a ReplacePrefixMatch
of "/xyz" would be modified to "/xyz/bar".

Note that this matches the behavior of the PathPrefix match type. This
matches full path elements. A path element refers to the list of labels
in the path split by the `/` separator. When specified, a trailing `/` is
ignored. For example, the paths `/abc`, `/abc/`, and `/abc/def` would all
match the prefix `/abc`, but the path `/abcd` would not.

ReplacePrefixMatch is only compatible with a `PathPrefix` HTTPRouteMatch.
Using any other HTTPRouteMatch type on the same HTTPRouteRule will result in
the implementation setting the Accepted Condition for the Route to `status: False`.

Request Path | Prefix Match | Replace Prefix | Modified Path<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].responseHeaderModifier
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindex)</sup></sup>



ResponseHeaderModifier defines a schema for a filter that modifies response
headers.

Support: Extended

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexresponseheadermodifieraddindex">add</a></b></td>
        <td>[]object</td>
        <td>
          Add adds the given header(s) (name, value) to the request
before the action. It appends to any existing values associated
with the header name.

Input:
  GET /foo HTTP/1.1
  my-header: foo

Config:
  add:
  - name: "my-header"
    value: "bar,baz"

Output:
  GET /foo HTTP/1.1
  my-header: foo,bar,baz<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>remove</b></td>
        <td>[]string</td>
        <td>
          Remove the given header(s) from the HTTP request before the action. The
value of Remove is a list of HTTP header names. Note that the header
names are case-insensitive (see
https://datatracker.ietf.org/doc/html/rfc2616#section-4.2).

Input:
  GET /foo HTTP/1.1
  my-header1: foo
  my-header2: bar
  my-header3: baz

Config:
  remove: ["my-header1", "my-header3"]

Output:
  GET /foo HTTP/1.1
  my-header2: bar<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexresponseheadermodifiersetindex">set</a></b></td>
        <td>[]object</td>
        <td>
          Set overwrites the request with the given header (name, value)
before the action.

Input:
  GET /foo HTTP/1.1
  my-header: foo

Config:
  set:
  - name: "my-header"
    value: "bar"

Output:
  GET /foo HTTP/1.1
  my-header: bar<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].responseHeaderModifier.add[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindexresponseheadermodifier)</sup></sup>



HTTPHeader represents an HTTP Header name and value as defined by RFC 7230.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the HTTP Header to be matched. Name matching MUST be
case insensitive. (See https://tools.ietf.org/html/rfc7230#section-3.2).

If multiple entries specify equivalent header names, the first entry with
an equivalent name MUST be considered for a match. Subsequent entries
with an equivalent header name MUST be ignored. Due to the
case-insensitivity of header names, "foo" and "Foo" are considered
equivalent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value is the value of HTTP Header to be matched.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].responseHeaderModifier.set[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindexresponseheadermodifier)</sup></sup>



HTTPHeader represents an HTTP Header name and value as defined by RFC 7230.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the HTTP Header to be matched. Name matching MUST be
case insensitive. (See https://tools.ietf.org/html/rfc7230#section-3.2).

If multiple entries specify equivalent header names, the first entry with
an equivalent name MUST be considered for a match. Subsequent entries
with an equivalent header name MUST be ignored. Due to the
case-insensitivity of header names, "foo" and "Foo" are considered
equivalent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value is the value of HTTP Header to be matched.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].urlRewrite
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindex)</sup></sup>



URLRewrite defines a schema for a filter that modifies a request during forwarding.

Support: Extended

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>hostname</b></td>
        <td>string</td>
        <td>
          Hostname is the value to be used to replace the Host header value during
forwarding.

Support: Extended<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexfiltersindexurlrewritepath">path</a></b></td>
        <td>object</td>
        <td>
          Path defines a path rewrite.

Support: Extended<br/>
          <br/>
            <i>Validations</i>:<li>self.type == 'ReplaceFullPath' ? has(self.replaceFullPath) : true: replaceFullPath must be specified when type is set to 'ReplaceFullPath'</li><li>has(self.replaceFullPath) ? self.type == 'ReplaceFullPath' : true: type must be 'ReplaceFullPath' when replaceFullPath is set</li><li>self.type == 'ReplacePrefixMatch' ? has(self.replacePrefixMatch) : true: replacePrefixMatch must be specified when type is set to 'ReplacePrefixMatch'</li><li>has(self.replacePrefixMatch) ? self.type == 'ReplacePrefixMatch' : true: type must be 'ReplacePrefixMatch' when replacePrefixMatch is set</li>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].filters[index].urlRewrite.path
<sup><sup>[↩ Parent](#httpproxyspecrulesindexfiltersindexurlrewrite)</sup></sup>



Path defines a path rewrite.

Support: Extended

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          Type defines the type of path modifier. Additional types may be
added in a future release of the API.

Note that values may be added to this enum, implementations
must ensure that unknown values will not cause a crash.

Unknown values here must result in the implementation setting the
Accepted Condition for the Route to `status: False`, with a
Reason of `UnsupportedValue`.<br/>
          <br/>
            <i>Enum</i>: ReplaceFullPath, ReplacePrefixMatch<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>replaceFullPath</b></td>
        <td>string</td>
        <td>
          ReplaceFullPath specifies the value with which to replace the full path
of a request during a rewrite or redirect.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>replacePrefixMatch</b></td>
        <td>string</td>
        <td>
          ReplacePrefixMatch specifies the value with which to replace the prefix
match of a request during a rewrite or redirect. For example, a request
to "/foo/bar" with a prefix match of "/foo" and a ReplacePrefixMatch
of "/xyz" would be modified to "/xyz/bar".

Note that this matches the behavior of the PathPrefix match type. This
matches full path elements. A path element refers to the list of labels
in the path split by the `/` separator. When specified, a trailing `/` is
ignored. For example, the paths `/abc`, `/abc/`, and `/abc/def` would all
match the prefix `/abc`, but the path `/abcd` would not.

ReplacePrefixMatch is only compatible with a `PathPrefix` HTTPRouteMatch.
Using any other HTTPRouteMatch type on the same HTTPRouteRule will result in
the implementation setting the Accepted Condition for the Route to `status: False`.

Request Path | Prefix Match | Replace Prefix | Modified Path<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].matches[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindex)</sup></sup>



HTTPRouteMatch defines the predicate used to match requests to a given
action. Multiple match types are ANDed together, i.e. the match will
evaluate to true only if all conditions are satisfied.

For example, the match below will match a HTTP request only if its path
starts with `/foo` AND it contains the `version: v1` header:

```
match:

	path:
	  value: "/foo"
	headers:
	- name: "version"
	  value "v1"

```

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#httpproxyspecrulesindexmatchesindexheadersindex">headers</a></b></td>
        <td>[]object</td>
        <td>
          Headers specifies HTTP request header matchers. Multiple match values are
ANDed together, meaning, a request must match all the specified headers
to select the route.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>method</b></td>
        <td>enum</td>
        <td>
          Method specifies HTTP method matcher.
When specified, this route will be matched only if the request has the
specified method.

Support: Extended<br/>
          <br/>
            <i>Enum</i>: GET, HEAD, POST, PUT, DELETE, CONNECT, OPTIONS, TRACE, PATCH<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexmatchesindexpath">path</a></b></td>
        <td>object</td>
        <td>
          Path specifies a HTTP request path matcher. If this field is not
specified, a default prefix match on the "/" path is provided.<br/>
          <br/>
            <i>Validations</i>:<li>(self.type in ['Exact','PathPrefix']) ? self.value.startsWith('/') : true: value must be an absolute path and start with '/' when type one of ['Exact', 'PathPrefix']</li><li>(self.type in ['Exact','PathPrefix']) ? !self.value.contains('//') : true: must not contain '//' when type one of ['Exact', 'PathPrefix']</li><li>(self.type in ['Exact','PathPrefix']) ? !self.value.contains('/./') : true: must not contain '/./' when type one of ['Exact', 'PathPrefix']</li><li>(self.type in ['Exact','PathPrefix']) ? !self.value.contains('/../') : true: must not contain '/../' when type one of ['Exact', 'PathPrefix']</li><li>(self.type in ['Exact','PathPrefix']) ? !self.value.contains('%2f') : true: must not contain '%2f' when type one of ['Exact', 'PathPrefix']</li><li>(self.type in ['Exact','PathPrefix']) ? !self.value.contains('%2F') : true: must not contain '%2F' when type one of ['Exact', 'PathPrefix']</li><li>(self.type in ['Exact','PathPrefix']) ? !self.value.contains('#') : true: must not contain '#' when type one of ['Exact', 'PathPrefix']</li><li>(self.type in ['Exact','PathPrefix']) ? !self.value.endsWith('/..') : true: must not end with '/..' when type one of ['Exact', 'PathPrefix']</li><li>(self.type in ['Exact','PathPrefix']) ? !self.value.endsWith('/.') : true: must not end with '/.' when type one of ['Exact', 'PathPrefix']</li><li>self.type in ['Exact','PathPrefix'] || self.type == 'RegularExpression': type must be one of ['Exact', 'PathPrefix', 'RegularExpression']</li><li>(self.type in ['Exact','PathPrefix']) ? self.value.matches(r"""^(?:[-A-Za-z0-9/._~!$&'()*+,;=:@]|[%][0-9a-fA-F]{2})+$""") : true: must only contain valid characters (matching ^(?:[-A-Za-z0-9/._~!$&'()*+,;=:@]|[%][0-9a-fA-F]{2})+$) for types ['Exact', 'PathPrefix']</li>
            <i>Default</i>: map[type:PathPrefix value:/]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxyspecrulesindexmatchesindexqueryparamsindex">queryParams</a></b></td>
        <td>[]object</td>
        <td>
          QueryParams specifies HTTP query parameter matchers. Multiple match
values are ANDed together, meaning, a request must match all the
specified query parameters to select the route.

Support: Extended<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].matches[index].headers[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindexmatchesindex)</sup></sup>



HTTPHeaderMatch describes how to select a HTTP route by matching HTTP request
headers.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the HTTP Header to be matched. Name matching MUST be
case insensitive. (See https://tools.ietf.org/html/rfc7230#section-3.2).

If multiple entries specify equivalent header names, only the first
entry with an equivalent name MUST be considered for a match. Subsequent
entries with an equivalent header name MUST be ignored. Due to the
case-insensitivity of header names, "foo" and "Foo" are considered
equivalent.

When a header is repeated in an HTTP request, it is
implementation-specific behavior as to how this is represented.
Generally, proxies should follow the guidance from the RFC:
https://www.rfc-editor.org/rfc/rfc7230.html#section-3.2.2 regarding
processing a repeated header, with special handling for "Set-Cookie".<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value is the value of HTTP Header to be matched.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          Type specifies how to match against the value of the header.

Support: Core (Exact)

Support: Implementation-specific (RegularExpression)

Since RegularExpression HeaderMatchType has implementation-specific
conformance, implementations can support POSIX, PCRE or any other dialects
of regular expressions. Please read the implementation's documentation to
determine the supported dialect.<br/>
          <br/>
            <i>Enum</i>: Exact, RegularExpression<br/>
            <i>Default</i>: Exact<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].matches[index].path
<sup><sup>[↩ Parent](#httpproxyspecrulesindexmatchesindex)</sup></sup>



Path specifies a HTTP request path matcher. If this field is not
specified, a default prefix match on the "/" path is provided.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          Type specifies how to match against the path Value.

Support: Core (Exact, PathPrefix)

Support: Implementation-specific (RegularExpression)<br/>
          <br/>
            <i>Enum</i>: Exact, PathPrefix, RegularExpression<br/>
            <i>Default</i>: PathPrefix<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value of the HTTP path to match against.<br/>
          <br/>
            <i>Default</i>: /<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.spec.rules[index].matches[index].queryParams[index]
<sup><sup>[↩ Parent](#httpproxyspecrulesindexmatchesindex)</sup></sup>



HTTPQueryParamMatch describes how to select a HTTP route by matching HTTP
query parameters.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the HTTP query param to be matched. This must be an
exact string match. (See
https://tools.ietf.org/html/rfc7230#section-2.7.3).

If multiple entries specify equivalent query param names, only the first
entry with an equivalent name MUST be considered for a match. Subsequent
entries with an equivalent query param name MUST be ignored.

If a query param is repeated in an HTTP request, the behavior is
purposely left undefined, since different data planes have different
capabilities. However, it is *recommended* that implementations should
match against the first value of the param if the data plane supports it,
as this behavior is expected in other load balancing contexts outside of
the Gateway API.

Users SHOULD NOT route traffic based on repeated query params to guard
themselves against potential differences in the implementations.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value is the value of HTTP query param to be matched.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>enum</td>
        <td>
          Type specifies how to match against the value of the query parameter.

Support: Extended (Exact)

Support: Implementation-specific (RegularExpression)

Since RegularExpression QueryParamMatchType has Implementation-specific
conformance, implementations can support POSIX, PCRE or any other
dialects of regular expressions. Please read the implementation's
documentation to determine the supported dialect.<br/>
          <br/>
            <i>Enum</i>: Exact, RegularExpression<br/>
            <i>Default</i>: Exact<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.status
<sup><sup>[↩ Parent](#httpproxy)</sup></sup>



Status defines the current state of an HTTPProxy.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#httpproxystatusaddressesindex">addresses</a></b></td>
        <td>[]object</td>
        <td>
          Addresses lists the network addresses that have been bound to the
HTTPProxy.

This field will not contain custom hostnames defined in the HTTPProxy. See
the `hostnames` field<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#httpproxystatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions describe the current conditions of the HTTPProxy.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>hostnames</b></td>
        <td>[]string</td>
        <td>
          Hostnames lists the hostnames that have been bound to the HTTPProxy.

If this list does not match that defined in the HTTPProxy, see the
`HostnamesVerified` condition message for details.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.status.addresses[index]
<sup><sup>[↩ Parent](#httpproxystatus)</sup></sup>



GatewayStatusAddress describes a network address that is bound to a Gateway.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          Value of the address. The validity of the values will depend
on the type and support by the controller.

Examples: `1.2.3.4`, `128::1`, `my-ip-address`.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          Type of the address.<br/>
          <br/>
            <i>Default</i>: IPAddress<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### HTTPProxy.status.conditions[index]
<sup><sup>[↩ Parent](#httpproxystatus)</sup></sup>



Condition contains details for one aspect of the current state of this API Resource.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          lastTransitionTime is the last time the condition transitioned from one status to another.
This should be when the underlying condition changed.  If that is not known, then using the time when the API field changed is acceptable.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          message is a human readable message indicating details about the transition.
This may be an empty string.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          reason contains a programmatic identifier indicating the reason for the condition's last transition.
Producers of specific condition types may define expected values and meanings for this field,
and whether the values are considered a guaranteed API.
The value should be a CamelCase string.
This field may not be empty.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          status of the condition, one of True, False, Unknown.<br/>
          <br/>
            <i>Enum</i>: True, False, Unknown<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          type of condition in CamelCase or in foo.example.com/CamelCase.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          observedGeneration represents the .metadata.generation that the condition was set based upon.
For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
with respect to the current state of the instance.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>
