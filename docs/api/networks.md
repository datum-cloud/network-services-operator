# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [Network](#network)




## Network
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






Network is the Schema for the networks API

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
      <td>Network</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#networkspec">spec</a></b></td>
        <td>object</td>
        <td>
          NetworkSpec defines the desired state of a Network<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkstatus">status</a></b></td>
        <td>object</td>
        <td>
          NetworkStatus defines the observed state of Network<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Network.spec
<sup><sup>[↩ Parent](#network)</sup></sup>



NetworkSpec defines the desired state of a Network

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
        <td><b><a href="#networkspecipam">ipam</a></b></td>
        <td>object</td>
        <td>
          IPAM settings for the network.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>ipFamilies</b></td>
        <td>[]enum</td>
        <td>
          IP Families to permit on a network. Defaults to IPv4.<br/>
          <br/>
            <i>Enum</i>: IPv4, IPv6<br/>
            <i>Default</i>: [IPv4]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>mtu</b></td>
        <td>integer</td>
        <td>
          Network MTU. May be between 1300 and 8856.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Default</i>: 1460<br/>
            <i>Minimum</i>: 1300<br/>
            <i>Maximum</i>: 8856<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Network.spec.ipam
<sup><sup>[↩ Parent](#networkspec)</sup></sup>



IPAM settings for the network.

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
        <td><b>mode</b></td>
        <td>enum</td>
        <td>
          IPAM mode<br/>
          <br/>
            <i>Enum</i>: Auto, Policy<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>ipv4Range</b></td>
        <td>string</td>
        <td>
          IPv4 range to use in auto mode networks. Defaults to 10.128.0.0/9.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>ipv6Range</b></td>
        <td>string</td>
        <td>
          IPv6 range to use in auto mode networks. Defaults to a /48 allocated from `fd20::/20`.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Network.status
<sup><sup>[↩ Parent](#network)</sup></sup>



NetworkStatus defines the observed state of Network

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
        <td><b><a href="#networkstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Represents the observations of a network's current state.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Network.status.conditions[index]
<sup><sup>[↩ Parent](#networkstatus)</sup></sup>



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
