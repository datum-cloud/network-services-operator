# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [Subnet](#subnet)




## Subnet
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






Subnet is the Schema for the subnets API

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
      <td>Subnet</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#subnetspec">spec</a></b></td>
        <td>object</td>
        <td>
          SubnetSpec defines the desired state of Subnet<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#subnetstatus">status</a></b></td>
        <td>object</td>
        <td>
          SubnetStatus defines the observed state of a Subnet<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Subnet.spec
<sup><sup>[↩ Parent](#subnet)</sup></sup>



SubnetSpec defines the desired state of Subnet

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
        <td><b>ipFamily</b></td>
        <td>enum</td>
        <td>
          The IP family of a subnet<br/>
          <br/>
            <i>Enum</i>: IPv4, IPv6<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#subnetspeclocation">location</a></b></td>
        <td>object</td>
        <td>
          The location which a subnet is associated with<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#subnetspecnetworkcontext">networkContext</a></b></td>
        <td>object</td>
        <td>
          A subnet's network context<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>prefixLength</b></td>
        <td>integer</td>
        <td>
          The prefix length of a subnet<br/>
          <br/>
            <i>Format</i>: int32<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>startAddress</b></td>
        <td>string</td>
        <td>
          The start address of a subnet<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>subnetClass</b></td>
        <td>string</td>
        <td>
          The class of subnet<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Subnet.spec.location
<sup><sup>[↩ Parent](#subnetspec)</sup></sup>



The location which a subnet is associated with

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


### Subnet.spec.networkContext
<sup><sup>[↩ Parent](#subnetspec)</sup></sup>



A subnet's network context

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
          The network context name<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Subnet.status
<sup><sup>[↩ Parent](#subnet)</sup></sup>



SubnetStatus defines the observed state of a Subnet

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
        <td><b><a href="#subnetstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Represents the observations of a subnet's current state.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>prefixLength</b></td>
        <td>integer</td>
        <td>
          The prefix length of a subnet<br/>
          <br/>
            <i>Format</i>: int32<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>startAddress</b></td>
        <td>string</td>
        <td>
          The start address of a subnet<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Subnet.status.conditions[index]
<sup><sup>[↩ Parent](#subnetstatus)</sup></sup>



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
