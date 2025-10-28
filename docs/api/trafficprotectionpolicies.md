# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [TrafficProtectionPolicy](#trafficprotectionpolicy)




## TrafficProtectionPolicy
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






TrafficProtectionPolicy is the Schema for the trafficprotectionpolicies API.

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
      <td>TrafficProtectionPolicy</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#trafficprotectionpolicyspec">spec</a></b></td>
        <td>object</td>
        <td>
          TrafficProtectionPolicySpec defines the desired state of TrafficProtectionPolicy.<br/>
          <br/>
            <i>Validations</i>:<li>has(self.targetRefs) ? self.targetRefs.all(ref, ref.group == 'gateway.networking.k8s.io') : true : this policy can only have a targetRefs[*].group of gateway.networking.k8s.io</li><li>has(self.targetRefs) ? self.targetRefs.all(ref, ref.kind in ['Gateway', 'HTTPRoute']) : true : this policy can only have a targetRefs[*].kind of Gateway/HTTPRoute</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#trafficprotectionpolicystatus">status</a></b></td>
        <td>object</td>
        <td>
          TrafficProtectionPolicyStatus defines the observed state of TrafficProtectionPolicy.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### TrafficProtectionPolicy.spec
<sup><sup>[↩ Parent](#trafficprotectionpolicy)</sup></sup>



TrafficProtectionPolicySpec defines the desired state of TrafficProtectionPolicy.

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
        <td><b><a href="#trafficprotectionpolicyspecrulesetsindex">ruleSets</a></b></td>
        <td>[]object</td>
        <td>
          RuleSets specifies the TrafficProtectionPolicy rulesets to apply.<br/>
          <br/>
            <i>Validations</i>:<li>self.filter(f, f.type == 'OWASPCoreRuleSet').size() <= 1: OWASPCoreRuleSet filter cannot be repeated</li>
            <i>Default</i>: [map[owaspCoreRuleSet:map[] type:OWASPCoreRuleSet]]<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#trafficprotectionpolicyspectargetrefsindex">targetRefs</a></b></td>
        <td>[]object</td>
        <td>
          TargetRefs are the names of the Gateway resources this policy
is being attached to.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>mode</b></td>
        <td>enum</td>
        <td>
          Mode specifies the mode of traffic protection to apply.
If not specified, defaults to "Observe".<br/>
          <br/>
            <i>Enum</i>: Observe, Enforce, Disabled<br/>
            <i>Default</i>: Observe<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>samplingPercentage</b></td>
        <td>integer</td>
        <td>
          SamplingPercentage controls the percentage of traffic that will be processed
by the TrafficProtectionPolicy.<br/>
          <br/>
            <i>Default</i>: 100<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 100<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### TrafficProtectionPolicy.spec.ruleSets[index]
<sup><sup>[↩ Parent](#trafficprotectionpolicyspec)</sup></sup>





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
          Type specifies the type of TrafficProtectionPolicy ruleset.<br/>
          <br/>
            <i>Enum</i>: OWASPCoreRuleSet<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#trafficprotectionpolicyspecrulesetsindexowaspcoreruleset">owaspCoreRuleSet</a></b></td>
        <td>object</td>
        <td>
          OWASPCoreRuleSet defines configuration options for the OWASP ModSecurity
Core Rule Set (CRS).<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### TrafficProtectionPolicy.spec.ruleSets[index].owaspCoreRuleSet
<sup><sup>[↩ Parent](#trafficprotectionpolicyspecrulesetsindex)</sup></sup>



OWASPCoreRuleSet defines configuration options for the OWASP ModSecurity
Core Rule Set (CRS).

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
        <td><b>paranoiaLevel</b></td>
        <td>integer</td>
        <td>
          ParanoiaLevel specifies the OWASP ModSecurity Core Rule Set (CRS)
paranoia level to apply.<br/>
          <br/>
            <i>Default</i>: 1<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 4<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#trafficprotectionpolicyspecrulesetsindexowaspcorerulesetruleexclusions">ruleExclusions</a></b></td>
        <td>object</td>
        <td>
          RuleExclusions can be used to disable specific OWASP ModSecurity Rules.
This allows operators to disable specific rules that may be causing false
positives.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#trafficprotectionpolicyspecrulesetsindexowaspcorerulesetscorethresholds">scoreThresholds</a></b></td>
        <td>object</td>
        <td>
          ScoreThresholds specifies the OWASP ModSecurity Core Rule Set (CRS)
score thresholds to block a request or response.

See: https://coreruleset.org/docs/2-how-crs-works/2-1-anomaly_scoring/<br/>
          <br/>
            <i>Default</i>: map[]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### TrafficProtectionPolicy.spec.ruleSets[index].owaspCoreRuleSet.ruleExclusions
<sup><sup>[↩ Parent](#trafficprotectionpolicyspecrulesetsindexowaspcoreruleset)</sup></sup>



RuleExclusions can be used to disable specific OWASP ModSecurity Rules.
This allows operators to disable specific rules that may be causing false
positives.

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
        <td><b>idRanges</b></td>
        <td>[]string</td>
        <td>
          IDRanges is a list of specific rule ID ranges to disable.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>ids</b></td>
        <td>[]integer</td>
        <td>
          IDs is a list of specific rule IDs to disable<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>tags</b></td>
        <td>[]string</td>
        <td>
          Tags is a list of rule tags to disable.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### TrafficProtectionPolicy.spec.ruleSets[index].owaspCoreRuleSet.scoreThresholds
<sup><sup>[↩ Parent](#trafficprotectionpolicyspecrulesetsindexowaspcoreruleset)</sup></sup>



ScoreThresholds specifies the OWASP ModSecurity Core Rule Set (CRS)
score thresholds to block a request or response.

See: https://coreruleset.org/docs/2-how-crs-works/2-1-anomaly_scoring/

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
        <td><b>inbound</b></td>
        <td>integer</td>
        <td>
          Inbound is the score threshold for blocking inbound (request) traffic.<br/>
          <br/>
            <i>Default</i>: 5<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 10000<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>outbound</b></td>
        <td>integer</td>
        <td>
          Outbound is the score threshold for blocking outbound (response) traffic.<br/>
          <br/>
            <i>Default</i>: 4<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 10000<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### TrafficProtectionPolicy.spec.targetRefs[index]
<sup><sup>[↩ Parent](#trafficprotectionpolicyspec)</sup></sup>



LocalPolicyTargetReferenceWithSectionName identifies an API object to apply a
direct policy to. This should be used as part of Policy resources that can
target single resources. For more information on how this policy attachment
mode works, and a sample Policy resource, refer to the policy attachment
documentation for Gateway API.

Note: This should only be used for direct policy attachment when references
to SectionName are actually needed. In all other cases,
LocalPolicyTargetReference should be used.

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
          Group is the group of the target resource.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind is kind of the target resource.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name is the name of the target resource.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>sectionName</b></td>
        <td>string</td>
        <td>
          SectionName is the name of a section within the target resource. When
unspecified, this targetRef targets the entire resource. In the following
resources, SectionName is interpreted as the following:

* Gateway: Listener name
* HTTPRoute: HTTPRouteRule name
* Service: Port name

If a SectionName is specified, but does not exist on the targeted object,
the Policy must fail to attach, and the policy implementation should record
a `ResolvedRefs` or similar Condition in the Policy's status.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### TrafficProtectionPolicy.status
<sup><sup>[↩ Parent](#trafficprotectionpolicy)</sup></sup>



TrafficProtectionPolicyStatus defines the observed state of TrafficProtectionPolicy.

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
        <td><b><a href="#trafficprotectionpolicystatusancestorsindex">ancestors</a></b></td>
        <td>[]object</td>
        <td>
          Ancestors is a list of ancestor resources (usually Gateways) that are
associated with the policy, and the status of the policy with respect to
each ancestor. When this policy attaches to a parent, the controller that
manages the parent and the ancestors MUST add an entry to this list when
the controller first sees the policy and SHOULD update the entry as
appropriate when the relevant ancestor is modified.

Note that choosing the relevant ancestor is left to the Policy designers;
an important part of Policy design is designing the right object level at
which to namespace this status.

Note also that implementations MUST ONLY populate ancestor status for
the Ancestor resources they are responsible for. Implementations MUST
use the ControllerName field to uniquely identify the entries in this list
that they are responsible for.

Note that to achieve this, the list of PolicyAncestorStatus structs
MUST be treated as a map with a composite key, made up of the AncestorRef
and ControllerName fields combined.

A maximum of 16 ancestors will be represented in this list. An empty list
means the Policy is not relevant for any ancestors.

If this slice is full, implementations MUST NOT add further entries.
Instead they MUST consider the policy unimplementable and signal that
on any related resources such as the ancestor that would be referenced
here. For example, if this list was full on BackendTLSPolicy, no
additional Gateways would be able to reference the Service targeted by
the BackendTLSPolicy.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### TrafficProtectionPolicy.status.ancestors[index]
<sup><sup>[↩ Parent](#trafficprotectionpolicystatus)</sup></sup>



PolicyAncestorStatus describes the status of a route with respect to an
associated Ancestor.

Ancestors refer to objects that are either the Target of a policy or above it
in terms of object hierarchy. For example, if a policy targets a Service, the
Policy's Ancestors are, in order, the Service, the HTTPRoute, the Gateway, and
the GatewayClass. Almost always, in this hierarchy, the Gateway will be the most
useful object to place Policy status on, so we recommend that implementations
SHOULD use Gateway as the PolicyAncestorStatus object unless the designers
have a _very_ good reason otherwise.

In the context of policy attachment, the Ancestor is used to distinguish which
resource results in a distinct application of this policy. For example, if a policy
targets a Service, it may have a distinct result per attached Gateway.

Policies targeting the same resource may have different effects depending on the
ancestors of those resources. For example, different Gateways targeting the same
Service may have different capabilities, especially if they have different underlying
implementations.

For example, in BackendTLSPolicy, the Policy attaches to a Service that is
used as a backend in a HTTPRoute that is itself attached to a Gateway.
In this case, the relevant object for status is the Gateway, and that is the
ancestor object referred to in this status.

Note that a parent is also an ancestor, so for objects where the parent is the
relevant object for status, this struct SHOULD still be used.

This struct is intended to be used in a slice that's effectively a map,
with a composite key made up of the AncestorRef and the ControllerName.

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
        <td><b><a href="#trafficprotectionpolicystatusancestorsindexancestorref">ancestorRef</a></b></td>
        <td>object</td>
        <td>
          AncestorRef corresponds with a ParentRef in the spec that this
PolicyAncestorStatus struct describes the status of.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>controllerName</b></td>
        <td>string</td>
        <td>
          ControllerName is a domain/path string that indicates the name of the
controller that wrote this status. This corresponds with the
controllerName field on GatewayClass.

Example: "example.net/gateway-controller".

The format of this field is DOMAIN "/" PATH, where DOMAIN and PATH are
valid Kubernetes names
(https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names).

Controllers MUST populate this field when writing status. Controllers should ensure that
entries to status populated with their ControllerName are cleaned up when they are no
longer necessary.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#trafficprotectionpolicystatusancestorsindexconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions describes the status of the Policy with respect to the given Ancestor.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### TrafficProtectionPolicy.status.ancestors[index].ancestorRef
<sup><sup>[↩ Parent](#trafficprotectionpolicystatusancestorsindex)</sup></sup>



AncestorRef corresponds with a ParentRef in the spec that this
PolicyAncestorStatus struct describes the status of.

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
          Name is the name of the referent.

Support: Core<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>group</b></td>
        <td>string</td>
        <td>
          Group is the group of the referent.
When unspecified, "gateway.networking.k8s.io" is inferred.
To set the core API group (such as for a "Service" kind referent),
Group must be explicitly set to "" (empty string).

Support: Core<br/>
          <br/>
            <i>Default</i>: gateway.networking.k8s.io<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind is kind of the referent.

There are two kinds of parent resources with "Core" support:

* Gateway (Gateway conformance profile)
* Service (Mesh conformance profile, ClusterIP Services only)

Support for other resources is Implementation-Specific.<br/>
          <br/>
            <i>Default</i>: Gateway<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the namespace of the referent. When unspecified, this refers
to the local namespace of the Route.

Note that there are specific rules for ParentRefs which cross namespace
boundaries. Cross-namespace references are only valid if they are explicitly
allowed by something in the namespace they are referring to. For example:
Gateway has the AllowedRoutes field, and ReferenceGrant provides a
generic way to enable any other kind of cross-namespace reference.

<gateway:experimental:description>
ParentRefs from a Route to a Service in the same namespace are "producer"
routes, which apply default routing rules to inbound connections from
any namespace to the Service.

ParentRefs from a Route to a Service in a different namespace are
"consumer" routes, and these routing rules are only applied to outbound
connections originating from the same namespace as the Route, for which
the intended destination of the connections are a Service targeted as a
ParentRef of the Route.
</gateway:experimental:description>

Support: Core<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>port</b></td>
        <td>integer</td>
        <td>
          Port is the network port this Route targets. It can be interpreted
differently based on the type of parent resource.

When the parent resource is a Gateway, this targets all listeners
listening on the specified port that also support this kind of Route(and
select this Route). It's not recommended to set `Port` unless the
networking behaviors specified in a Route must apply to a specific port
as opposed to a listener(s) whose port(s) may be changed. When both Port
and SectionName are specified, the name and port of the selected listener
must match both specified values.

<gateway:experimental:description>
When the parent resource is a Service, this targets a specific port in the
Service spec. When both Port (experimental) and SectionName are specified,
the name and port of the selected port must match both specified values.
</gateway:experimental:description>

Implementations MAY choose to support other parent resources.
Implementations supporting other types of parent resources MUST clearly
document how/if Port is interpreted.

For the purpose of status, an attachment is considered successful as
long as the parent resource accepts it partially. For example, Gateway
listeners can restrict which Routes can attach to them by Route kind,
namespace, or hostname. If 1 of 2 Gateway listeners accept attachment
from the referencing Route, the Route MUST be considered successfully
attached. If no Gateway listeners accept attachment from this Route,
the Route MUST be considered detached from the Gateway.

Support: Extended<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 65535<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>sectionName</b></td>
        <td>string</td>
        <td>
          SectionName is the name of a section within the target resource. In the
following resources, SectionName is interpreted as the following:

* Gateway: Listener name. When both Port (experimental) and SectionName
are specified, the name and port of the selected listener must match
both specified values.
* Service: Port name. When both Port (experimental) and SectionName
are specified, the name and port of the selected listener must match
both specified values.

Implementations MAY choose to support attaching Routes to other resources.
If that is the case, they MUST clearly document how SectionName is
interpreted.

When unspecified (empty string), this will reference the entire resource.
For the purpose of status, an attachment is considered successful if at
least one section in the parent resource accepts it. For example, Gateway
listeners can restrict which Routes can attach to them by Route kind,
namespace, or hostname. If 1 of 2 Gateway listeners accept attachment from
the referencing Route, the Route MUST be considered successfully
attached. If no Gateway listeners accept attachment from this Route, the
Route MUST be considered detached from the Gateway.

Support: Core<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### TrafficProtectionPolicy.status.ancestors[index].conditions[index]
<sup><sup>[↩ Parent](#trafficprotectionpolicystatusancestorsindex)</sup></sup>



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
