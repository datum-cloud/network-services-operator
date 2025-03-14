# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [SubnetClaim](#subnetclaim)




## SubnetClaim
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






SubnetClaim is the Schema for the subnetclaims API

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
      <td>SubnetClaim</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#subnetclaimspec">spec</a></b></td>
        <td>object</td>
        <td>
          SubnetClaimSpec defines the desired state of SubnetClaim<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#subnetclaimstatus">status</a></b></td>
        <td>object</td>
        <td>
          SubnetClaimStatus defines the observed state of SubnetClaim<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### SubnetClaim.spec
<sup><sup>[↩ Parent](#subnetclaim)</sup></sup>



SubnetClaimSpec defines the desired state of SubnetClaim

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
          The IP family of a subnet claim<br/>
          <br/>
            <i>Enum</i>: IPv4, IPv6<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#subnetclaimspeclocation">location</a></b></td>
        <td>object</td>
        <td>
          The location which a subnet claim is associated with<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#subnetclaimspecnetworkcontext">networkContext</a></b></td>
        <td>object</td>
        <td>
          The network context to claim a subnet in<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>subnetClass</b></td>
        <td>string</td>
        <td>
          The class of subnet required<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>prefixLength</b></td>
        <td>integer</td>
        <td>
          The prefix length of a subnet claim<br/>
          <br/>
            <i>Format</i>: int32<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>startAddress</b></td>
        <td>string</td>
        <td>
          The start address of a subnet claim<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### SubnetClaim.spec.location
<sup><sup>[↩ Parent](#subnetclaimspec)</sup></sup>



The location which a subnet claim is associated with

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


### SubnetClaim.spec.networkContext
<sup><sup>[↩ Parent](#subnetclaimspec)</sup></sup>



The network context to claim a subnet in

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


### SubnetClaim.status
<sup><sup>[↩ Parent](#subnetclaim)</sup></sup>



SubnetClaimStatus defines the observed state of SubnetClaim

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
        <td><b><a href="#subnetclaimstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Represents the observations of a subnet claim's current state.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>prefixLength</b></td>
        <td>integer</td>
        <td>
          The prefix length of a subnet claim<br/>
          <br/>
            <i>Format</i>: int32<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>startAddress</b></td>
        <td>string</td>
        <td>
          The start address of a subnet claim<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#subnetclaimstatussubnetref">subnetRef</a></b></td>
        <td>object</td>
        <td>
          The subnet which has been claimed from<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### SubnetClaim.status.conditions[index]
<sup><sup>[↩ Parent](#subnetclaimstatus)</sup></sup>



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


### SubnetClaim.status.subnetRef
<sup><sup>[↩ Parent](#subnetclaimstatus)</sup></sup>



The subnet which has been claimed from

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
      </tr></tbody>
</table>
