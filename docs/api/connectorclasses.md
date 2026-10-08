# API Reference

Packages:

- [networking.datumapis.com/v1alpha1](#networkingdatumapiscomv1alpha1)

# networking.datumapis.com/v1alpha1

Resource Types:

- [ConnectorClass](#connectorclass)




## ConnectorClass
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha1 )</sup></sup>






ConnectorClass is the Schema for the connectorclasses API.

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
      <td>ConnectorClass</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#connectorclassspec">spec</a></b></td>
        <td>object</td>
        <td>
          Spec defines the desired state of a ConnectorClass<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>object</td>
        <td>
          Status defines the observed state of a ConnectorClass<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ConnectorClass.spec
<sup><sup>[↩ Parent](#connectorclass)</sup></sup>



Spec defines the desired state of a ConnectorClass

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
        <td><b>controllerName</b></td>
        <td>string</td>
        <td>
          ControllerName is the name of the controller responsible for this ConnectorClass.<br/>
          <br/>
            <i>Default</i>: networking.datumapis.com/datum-connect<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>
