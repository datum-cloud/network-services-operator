# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [Location](#location)




## Location
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






Location is the Schema for the locations API.

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
      <td>Location</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#locationspec">spec</a></b></td>
        <td>object</td>
        <td>
          LocationSpec defines the desired state of Location.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#locationstatus">status</a></b></td>
        <td>object</td>
        <td>
          LocationStatus defines the observed state of Location.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Location.spec
<sup><sup>[↩ Parent](#location)</sup></sup>



LocationSpec defines the desired state of Location.

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
        <td><b>locationClassName</b></td>
        <td>string</td>
        <td>
          The location class that indicates control plane behavior of entities
associated with the location.

Valid values are:
	- datum-managed
	- self-managed<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#locationspecprovider">provider</a></b></td>
        <td>object</td>
        <td>
          The location provider<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>topology</b></td>
        <td>map[string]string</td>
        <td>
          The topology of the location

This may contain arbitrary topology keys. Some keys may be well known, such
as:
	- topology.datum.net/city-code<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Location.spec.provider
<sup><sup>[↩ Parent](#locationspec)</sup></sup>



The location provider

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
        <td><b><a href="#locationspecprovidergcp">gcp</a></b></td>
        <td>object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Location.spec.provider.gcp
<sup><sup>[↩ Parent](#locationspecprovider)</sup></sup>





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
        <td><b>projectId</b></td>
        <td>string</td>
        <td>
          The GCP project servicing the location

For locations with the class of `datum-managed`, a service account will be
required for each unique GCP project ID across all locations registered in a
namespace.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>region</b></td>
        <td>string</td>
        <td>
          The GCP region servicing the location<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>zone</b></td>
        <td>string</td>
        <td>
          The GCP zone servicing the location<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Location.status
<sup><sup>[↩ Parent](#location)</sup></sup>



LocationStatus defines the observed state of Location.

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
        <td><b><a href="#locationstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Represents the observations of a location's current state.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Location.status.conditions[index]
<sup><sup>[↩ Parent](#locationstatus)</sup></sup>



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
