# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [NetworkLoadBalancerRoute](#networkloadbalancerroute)




## NetworkLoadBalancerRoute
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






NetworkLoadBalancerRoute connects load balancer listeners to NetworkService
backends. References are restricted to the route's namespace.

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
      <td>NetworkLoadBalancerRoute</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerroutespec">spec</a></b></td>
        <td>object</td>
        <td>
          NetworkLoadBalancerRouteSpec attaches weighted backends to named listeners.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerroutestatus">status</a></b></td>
        <td>object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkLoadBalancerRoute.spec
<sup><sup>[↩ Parent](#networkloadbalancerroute)</sup></sup>



NetworkLoadBalancerRouteSpec attaches weighted backends to named listeners.

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
        <td><b><a href="#networkloadbalancerroutespecbackendsindex">backends</a></b></td>
        <td>[]object</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerroutespecparentrefsindex">parentRefs</a></b></td>
        <td>[]object</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkLoadBalancerRoute.spec.backends[index]
<sup><sup>[↩ Parent](#networkloadbalancerroutespec)</sup></sup>



NetworkLoadBalancerRouteBackend forwards new connections or flows to a
named NetworkService port. Weight zero disables selection.

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
        <td><b><a href="#networkloadbalancerroutespecbackendsindexnetworkservice">networkService</a></b></td>
        <td>object</td>
        <td>
          NetworkLoadBalancerNetworkServiceRef names a backend service and one of its
named ports in the route's namespace.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerroutespecbackendsindexhealthcheck">healthCheck</a></b></td>
        <td>object</td>
        <td>
          NetworkLoadBalancerHealthCheck checks an individual backend endpoint. UDP
backends require a separate TCP, HTTP, or HTTPS health endpoint.<br/>
          <br/>
            <i>Validations</i>:<li>has(self.path) ? self.protocol in ['HTTP', 'HTTPS'] : true: path is only valid for HTTP and HTTPS probes</li><li>self.protocol == 'TCP' || has(self.port): HTTP and HTTPS probes require a port</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>weight</b></td>
        <td>integer</td>
        <td>
          <br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Default</i>: 1<br/>
            <i>Minimum</i>: 0<br/>
            <i>Maximum</i>: 256<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkLoadBalancerRoute.spec.backends[index].networkService
<sup><sup>[↩ Parent](#networkloadbalancerroutespecbackendsindex)</sup></sup>



NetworkLoadBalancerNetworkServiceRef names a backend service and one of its
named ports in the route's namespace.

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
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>port</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkLoadBalancerRoute.spec.backends[index].healthCheck
<sup><sup>[↩ Parent](#networkloadbalancerroutespecbackendsindex)</sup></sup>



NetworkLoadBalancerHealthCheck checks an individual backend endpoint. UDP
backends require a separate TCP, HTTP, or HTTPS health endpoint.

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
        <td><b>protocol</b></td>
        <td>enum</td>
        <td>
          <br/>
          <br/>
            <i>Enum</i>: TCP, HTTP, HTTPS<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>path</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>port</b></td>
        <td>string</td>
        <td>
          Name of the probe port on the referenced NetworkService. When omitted,
TCP probes use the backend's serving port.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkLoadBalancerRoute.spec.parentRefs[index]
<sup><sup>[↩ Parent](#networkloadbalancerroutespec)</sup></sup>



NetworkLoadBalancerRouteParentRef attaches a route to one named listener on
a load balancer in the same namespace.

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
        <td><b>listener</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkLoadBalancerRoute.status
<sup><sup>[↩ Parent](#networkloadbalancerroute)</sup></sup>





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
        <td><b><a href="#networkloadbalancerroutestatusparentsindex">parents</a></b></td>
        <td>[]object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkLoadBalancerRoute.status.parents[index]
<sup><sup>[↩ Parent](#networkloadbalancerroutestatus)</sup></sup>



NetworkLoadBalancerRouteParentStatus reports attachment and effectiveness
for one parent listener. The oldest accepted catch-all route is effective.

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
        <td><b>effective</b></td>
        <td>boolean</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>listener</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerroutestatusparentsindexconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions include Accepted, ResolvedRefs, and Programmed with
observedGeneration.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkLoadBalancerRoute.status.parents[index].conditions[index]
<sup><sup>[↩ Parent](#networkloadbalancerroutestatusparentsindex)</sup></sup>



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
