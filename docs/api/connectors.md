# API Reference

Packages:

- [networking.datumapis.com/v1alpha1](#networkingdatumapiscomv1alpha1)

# networking.datumapis.com/v1alpha1

Resource Types:

- [Connector](#connector)




## Connector
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha1 )</sup></sup>






Connector is the Schema for the connectors API.

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
      <td>networking.datumapis.com/v1alpha1</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>Connector</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#connectorspec">spec</a></b></td>
        <td>object</td>
        <td>
          Spec defines the desired state of a Connector<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#connectorstatus">status</a></b></td>
        <td>object</td>
        <td>
          Status defines the observed state of a Connector<br/>
          <br/>
            <i>Default</i>: map[conditions:[map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Accepted]]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Connector.spec
<sup><sup>[↩ Parent](#connector)</sup></sup>



Spec defines the desired state of a Connector

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
        <td><b>connectorClassName</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#connectorspeccapabilitiesindex">capabilities</a></b></td>
        <td>[]object</td>
        <td>
          Capabilities desired to be supported by the connector.

A connector may choose to not support all requested capabilities, and may
also choose to support additional capabilities not requested here. The
condition of each capability will reflect whether the capability is supported
or not in the ConnectorStatus.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Connector.spec.capabilities[index]
<sup><sup>[↩ Parent](#connectorspec)</sup></sup>





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
        <td>string</td>
        <td>
          Type of capability<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#connectorspeccapabilitiesindexconnecttcp">connectTCP</a></b></td>
        <td>object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Connector.spec.capabilities[index].connectTCP
<sup><sup>[↩ Parent](#connectorspeccapabilitiesindex)</sup></sup>





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
        <td><b>disabled</b></td>
        <td>boolean</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Connector.status
<sup><sup>[↩ Parent](#connector)</sup></sup>



Status defines the observed state of a Connector

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
        <td><b><a href="#connectorstatuscapabilitiesindex">capabilities</a></b></td>
        <td>[]object</td>
        <td>
          Capabilities describe the status of each capability of the connector.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#connectorstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions describe the current conditions of the HTTPProxy.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#connectorstatusconnectiondetails">connectionDetails</a></b></td>
        <td>object</td>
        <td>
          ConnectionDetails provide details on how to connect to the connector.<br/>
          <br/>
            <i>Validations</i>:<li>!(self.type != 'PublicKey' && has(self.publicKey)): publicKey field must be nil if the type is not PublicKey</li><li>self.type == 'PublicKey' && has(self.publicKey): publicKey field must be specified if the type is PublicKey</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#connectorstatusleaseref">leaseRef</a></b></td>
        <td>object</td>
        <td>
          LeaseRef references the Lease used to report connector liveness.

The connector controller creates the Lease when a Connector is created
and records it here. Connector implementations (agents) are expected to
periodically renew the Lease to indicate liveness.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Connector.status.capabilities[index]
<sup><sup>[↩ Parent](#connectorstatus)</sup></sup>





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
        <td>string</td>
        <td>
          Type of capability<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#connectorstatuscapabilitiesindexconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions describe the current conditions of the capability.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Connector.status.capabilities[index].conditions[index]
<sup><sup>[↩ Parent](#connectorstatuscapabilitiesindex)</sup></sup>



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


### Connector.status.conditions[index]
<sup><sup>[↩ Parent](#connectorstatus)</sup></sup>



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


### Connector.status.connectionDetails
<sup><sup>[↩ Parent](#connectorstatus)</sup></sup>



ConnectionDetails provide details on how to connect to the connector.

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
          Type of connection details provided.<br/>
          <br/>
            <i>Enum</i>: PublicKey<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#connectorstatusconnectiondetailspublickey">publicKey</a></b></td>
        <td>object</td>
        <td>
          PublicKey connection details<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Connector.status.connectionDetails.publicKey
<sup><sup>[↩ Parent](#connectorstatusconnectiondetails)</sup></sup>



PublicKey connection details

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
        <td><b><a href="#connectorstatusconnectiondetailspublickeyaddressesindex">addresses</a></b></td>
        <td>[]object</td>
        <td>
          Addresses where the connector can be reached<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>homeRelay</b></td>
        <td>string</td>
        <td>
          Home Relay server of the connector

Must be a valid URL<br/>
          <br/>
            <i>Validations</i>:<li>isURL(self): Must be a URL.</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>discoveryMode</b></td>
        <td>enum</td>
        <td>
          The mode used to discover the public key<br/>
          <br/>
            <i>Enum</i>: DNS<br/>
            <i>Default</i>: DNS<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>id</b></td>
        <td>string</td>
        <td>
          The public key to dial and connect to<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Connector.status.connectionDetails.publicKey.addresses[index]
<sup><sup>[↩ Parent](#connectorstatusconnectiondetailspublickey)</sup></sup>



PublicKeyConnectorAddress defines an address and port for a connector.

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
        <td><b>address</b></td>
        <td>string</td>
        <td>
          IPv4 or IPv6 address.<br/>
          <br/>
            <i>Validations</i>:<li>isIP(self): Must be an IP address.</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>port</b></td>
        <td>integer</td>
        <td>
          Port where the connector can be reached.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 65535<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Connector.status.leaseRef
<sup><sup>[↩ Parent](#connectorstatus)</sup></sup>



LeaseRef references the Lease used to report connector liveness.

The connector controller creates the Lease when a Connector is created
and records it here. Connector implementations (agents) are expected to
periodically renew the Lease to indicate liveness.

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
