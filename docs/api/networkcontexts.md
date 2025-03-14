# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [NetworkContext](#networkcontext)




## NetworkContext
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






NetworkContext is the Schema for the networkcontexts API

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
      <td>NetworkContext</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#networkcontextspec">spec</a></b></td>
        <td>object</td>
        <td>
          NetworkContextSpec defines the desired state of NetworkContext<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkcontextstatus">status</a></b></td>
        <td>object</td>
        <td>
          NetworkContextStatus defines the observed state of NetworkContext<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.spec
<sup><sup>[↩ Parent](#networkcontext)</sup></sup>



NetworkContextSpec defines the desired state of NetworkContext

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
        <td><b><a href="#networkcontextspeclocation">location</a></b></td>
        <td>object</td>
        <td>
          The location of where a network context exists.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkcontextspecnetwork">network</a></b></td>
        <td>object</td>
        <td>
          The attached network<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkContext.spec.location
<sup><sup>[↩ Parent](#networkcontextspec)</sup></sup>



The location of where a network context exists.

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
          Name of a datum location<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace for the datum location<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkContext.spec.network
<sup><sup>[↩ Parent](#networkcontextspec)</sup></sup>



The attached network

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
          The network name<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkContext.status
<sup><sup>[↩ Parent](#networkcontext)</sup></sup>



NetworkContextStatus defines the observed state of NetworkContext

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
        <td><b><a href="#networkcontextstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Represents the observations of a network context's current state.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.status.conditions[index]
<sup><sup>[↩ Parent](#networkcontextstatus)</sup></sup>



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
