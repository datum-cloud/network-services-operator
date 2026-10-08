# API Reference

Packages:

- [networking.datumapis.com/v1alpha1](#networkingdatumapiscomv1alpha1)

# networking.datumapis.com/v1alpha1

Resource Types:

- [ConnectorAdvertisement](#connectoradvertisement)




## ConnectorAdvertisement
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha1 )</sup></sup>






ConnectorAdvertisement is the Schema for the connectoradvertisements API.

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
      <td>ConnectorAdvertisement</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#connectoradvertisementspec">spec</a></b></td>
        <td>object</td>
        <td>
          Spec defines the desired state of a ConnectorAdvertisement<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#connectoradvertisementstatus">status</a></b></td>
        <td>object</td>
        <td>
          Status defines the observed state of a ConnectorAdvertisement<br/>
          <br/>
            <i>Default</i>: map[conditions:[map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Accepted]]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ConnectorAdvertisement.spec
<sup><sup>[↩ Parent](#connectoradvertisement)</sup></sup>



Spec defines the desired state of a ConnectorAdvertisement

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
        <td><b><a href="#connectoradvertisementspecconnectorref">connectorRef</a></b></td>
        <td>object</td>
        <td>
          ConnectorRef references the Connector being advertised.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#connectoradvertisementspeclayer4index">layer4</a></b></td>
        <td>[]object</td>
        <td>
          Layer 4 services being advertised.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ConnectorAdvertisement.spec.connectorRef
<sup><sup>[↩ Parent](#connectoradvertisementspec)</sup></sup>



ConnectorRef references the Connector being advertised.

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
          Name of the referenced Connector.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ConnectorAdvertisement.spec.layer4[index]
<sup><sup>[↩ Parent](#connectoradvertisementspec)</sup></sup>





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
          Name of the advertisement.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#connectoradvertisementspeclayer4indexservicesindex">services</a></b></td>
        <td>[]object</td>
        <td>
          Layer 4 services being advertised.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ConnectorAdvertisement.spec.layer4[index].services[index]
<sup><sup>[↩ Parent](#connectoradvertisementspeclayer4index)</sup></sup>





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
          Address of the service.

Can be an IPv4, IPv6, or a DNS address. A DNS address may contain
wildcards. A DNS address acts as an allow list for what addresses the
connector will allow to be requested through it.

DNS resolution is the responsibility of the connector.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#connectoradvertisementspeclayer4indexservicesindexportsindex">ports</a></b></td>
        <td>[]object</td>
        <td>
          Ports of the service.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ConnectorAdvertisement.spec.layer4[index].services[index].ports[index]
<sup><sup>[↩ Parent](#connectoradvertisementspeclayer4indexservicesindex)</sup></sup>



Layer4ServicePort represents a port for a Layer 4 service.

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
          Named port for the service.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>port</b></td>
        <td>integer</td>
        <td>
          Port number for the service.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 65535<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>protocol</b></td>
        <td>string</td>
        <td>
          Protocol for port. Must be TCP or UDP, defaults to "TCP".<br/>
          <br/>
            <i>Default</i>: TCP<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### ConnectorAdvertisement.status
<sup><sup>[↩ Parent](#connectoradvertisement)</sup></sup>



Status defines the observed state of a ConnectorAdvertisement

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
        <td><b><a href="#connectoradvertisementstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions describe the current conditions of the ConnectorAdvertisement.

Known conditions:
- Accepted: indicates whether the referenced Connector has been resolved.
  When Accepted is False, the reason will explain why the reference
  could not be resolved (for example, ConnectorNotFound).<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ConnectorAdvertisement.status.conditions[index]
<sup><sup>[↩ Parent](#connectoradvertisementstatus)</sup></sup>



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
