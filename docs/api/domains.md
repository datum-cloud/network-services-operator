# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [Domain](#domain)




## Domain
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






Domain represents a domain name in the Datum system

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
      <td>Domain</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#domainspec">spec</a></b></td>
        <td>object</td>
        <td>
          DomainSpec defines the desired state of Domain<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#domainstatus">status</a></b></td>
        <td>object</td>
        <td>
          DomainStatus defines the observed state of Domain<br/>
          <br/>
            <i>Default</i>: map[conditions:[map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Verified] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:VerifiedDNS] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:VerifiedHTTP]]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Domain.spec
<sup><sup>[↩ Parent](#domain)</sup></sup>



DomainSpec defines the desired state of Domain

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
        <td><b>domainName</b></td>
        <td>string</td>
        <td>
          DomainName is the fully qualified domain name (FQDN) to be managed<br/>
          <br/>
            <i>Validations</i>:<li>oldSelf == '' || self == oldSelf: A domain name is immutable and cannot be changed after creation</li><li>self.indexOf('.') != -1: Must have at least two segments separated by dots</li>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Domain.status
<sup><sup>[↩ Parent](#domain)</sup></sup>



DomainStatus defines the observed state of Domain

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
        <td><b><a href="#domainstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#domainstatusregistrar">registrar</a></b></td>
        <td>object</td>
        <td>
          DomainRegistrarStatus represents the registrar information for a domain<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#domainstatusverification">verification</a></b></td>
        <td>object</td>
        <td>
          DomainVerificationStatus represents the verification status of a domain<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Domain.status.conditions[index]
<sup><sup>[↩ Parent](#domainstatus)</sup></sup>



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


### Domain.status.registrar
<sup><sup>[↩ Parent](#domainstatus)</sup></sup>



DomainRegistrarStatus represents the registrar information for a domain

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
        <td><b>clientStatusCodes</b></td>
        <td>[]string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>createdDate</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#domainstatusregistrardnssec">dnssec</a></b></td>
        <td>object</td>
        <td>
          DNSSECStatus represents the DNSSEC status of a domain<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>expirationDate</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>ianaID</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>ianaName</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>modifiedDate</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>nameservers</b></td>
        <td>[]string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>serverStatusCodes</b></td>
        <td>[]string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Domain.status.registrar.dnssec
<sup><sup>[↩ Parent](#domainstatusregistrar)</sup></sup>



DNSSECStatus represents the DNSSEC status of a domain

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
        <td><b>signed</b></td>
        <td>boolean</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Domain.status.verification
<sup><sup>[↩ Parent](#domainstatus)</sup></sup>



DomainVerificationStatus represents the verification status of a domain

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
        <td><b><a href="#domainstatusverificationdnsrecord">dnsRecord</a></b></td>
        <td>object</td>
        <td>
          DNSVerificationRecord represents a DNS record required for verification<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#domainstatusverificationhttptoken">httpToken</a></b></td>
        <td>object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>nextVerificationAttempt</b></td>
        <td>string</td>
        <td>
          <br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### Domain.status.verification.dnsRecord
<sup><sup>[↩ Parent](#domainstatusverification)</sup></sup>



DNSVerificationRecord represents a DNS record required for verification

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
        <td><b>content</b></td>
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
        <td><b>type</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### Domain.status.verification.httpToken
<sup><sup>[↩ Parent](#domainstatusverification)</sup></sup>





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
        <td><b>body</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>url</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>
