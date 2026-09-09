# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [LocationBinding](#locationbinding)




## LocationBinding
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






LocationBinding is the Schema for the locationbindings API. It is a
cluster-scoped projection of a cluster-scoped Location into a project's
virtual control plane, created once the location's class is supported, the
Location is Ready, and the corresponding ServiceAvailability is Available.

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
      <td>LocationBinding</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#locationbindingspec">spec</a></b></td>
        <td>object</td>
        <td>
          LocationBindingSpec defines the desired state of LocationBinding.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#locationbindingstatus">status</a></b></td>
        <td>object</td>
        <td>
          LocationBindingStatus defines the observed state of LocationBinding.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### LocationBinding.spec
<sup><sup>[↩ Parent](#locationbinding)</sup></sup>



LocationBindingSpec defines the desired state of LocationBinding.

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
        <td><b><a href="#locationbindingspeclocationref">locationRef</a></b></td>
        <td>object</td>
        <td>
          LocationRef references the canonical cluster-scoped Location object.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>displayName</b></td>
        <td>string</td>
        <td>
          DisplayName is a human-readable label for the location.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>locationClassName</b></td>
        <td>string</td>
        <td>
          LocationClassName mirrors spec.locationClassName from the referenced Location.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>topology</b></td>
        <td>map[string]string</td>
        <td>
          Topology mirrors spec.topology from the referenced Location, containing
well-known keys like topology.datum.net/city-code and topology.datum.net/region.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### LocationBinding.spec.locationRef
<sup><sup>[↩ Parent](#locationbindingspec)</sup></sup>



LocationRef references the canonical cluster-scoped Location object.

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
          Name of the referent.
This field is effectively required, but due to backwards compatibility is
allowed to be empty. Instances of this type with an empty value here are
almost certainly wrong.
More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names<br/>
          <br/>
            <i>Default</i>: <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### LocationBinding.status
<sup><sup>[↩ Parent](#locationbinding)</sup></sup>



LocationBindingStatus defines the observed state of LocationBinding.

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
        <td><b><a href="#locationbindingstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### LocationBinding.status.conditions[index]
<sup><sup>[↩ Parent](#locationbindingstatus)</sup></sup>



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
