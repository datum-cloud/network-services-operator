# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [InternetEgressClass](#internetegressclass)




## InternetEgressClass
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






InternetEgressClass is the Schema for the internetegressclasses API.

An operator defines a class and a consumer names it on a network. The class
decides how a network reaches the internet, not which destinations it may
reach.

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
      <td>InternetEgressClass</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#internetegressclassspec">spec</a></b></td>
        <td>object</td>
        <td>
          Spec defines the desired state of an InternetEgressClass<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>object</td>
        <td>
          Status defines the observed state of an InternetEgressClass<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### InternetEgressClass.spec
<sup><sup>[↩ Parent](#internetegressclass)</sup></sup>



Spec defines the desired state of an InternetEgressClass

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
          ControllerName is the name of the controller responsible for this
InternetEgressClass.<br/>
          <br/>
            <i>Default</i>: networking.datumapis.com/cell-egress<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reach</b></td>
        <td>[]enum</td>
        <td>
          Reach are the destination address families a network on this class
reaches. The class names destinations, never the translation that
delivers them.

Only IPv6 is accepted. A class advertising IPv4 would promise what no
component in the platform can deliver, so the value is withheld until a
resolver and a translator sharing a prefix are paired.<br/>
          <br/>
            <i>Validations</i>:<li>self.all(f, f == 'IPv6'): Only IPv6 is accepted; reaching IPv4 destinations needs a resolver and a translator sharing a prefix, and the platform pairs neither</li><li>self.all(f, self.exists_one(g, g == f)): Each address family may be listed at most once</li>
            <i>Enum</i>: IPv4, IPv6<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>sharing</b></td>
        <td>enum</td>
        <td>
          Sharing is the operator-side decision a consumer reads back as
stability on a network interface: Shared reports None, and Dedicated
reports Network.<br/>
          <br/>
            <i>Enum</i>: Shared, Dedicated<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#internetegressclassspecparametersref">parametersRef</a></b></td>
        <td>object</td>
        <td>
          ParametersRef names the configuration the controller serving this class
reads, such as the address class an egress address is drawn from. Its
type is defined by that controller.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### InternetEgressClass.spec.parametersRef
<sup><sup>[↩ Parent](#internetegressclassspec)</sup></sup>



ParametersRef names the configuration the controller serving this class
reads, such as the address class an egress address is drawn from. Its
type is defined by that controller.

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
        <td><b>group</b></td>
        <td>string</td>
        <td>
          Group of the referent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind of the referent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name of the referent.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>
