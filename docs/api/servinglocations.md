# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [ServingLocation](#servinglocation)




## ServingLocation
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






ServingLocation names the location a cell serves. Its name is the name of the
Location it was published from.

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
      <td>ServingLocation</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#servinglocationspec">spec</a></b></td>
        <td>object</td>
        <td>
          ServingLocationSpec carries the identity and locality of the location a cell
serves. It deliberately omits the provider block, coordinates, location class
and display name: a field earns its place here by having a reader at a cell.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ServingLocation.spec
<sup><sup>[↩ Parent](#servinglocation)</sup></sup>



ServingLocationSpec carries the identity and locality of the location a cell
serves. It deliberately omits the provider block, coordinates, location class
and display name: a field earns its place here by having a reader at a cell.

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
        <td><b>topology</b></td>
        <td>map[string]string</td>
        <td>
          Topology carries the locality of the location, keyed as it is on Location
and LocationBinding. An empty city code makes every location-scoped
placement fail with nothing catching it, so it is required here.<br/>
          <br/>
            <i>Validations</i>:<li>'topology.datum.net/city-code' in self && self['topology.datum.net/city-code'] != '': topology must carry a non-empty topology.datum.net/city-code</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#servinglocationspecsource">source</a></b></td>
        <td>object</td>
        <td>
          Source describes the record this copy was published from. It is declared
spec-side because the propagation layer strips status, so a reader that
must reason about staleness has nowhere else to read it.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ServingLocation.spec.source
<sup><sup>[↩ Parent](#servinglocationspec)</sup></sup>



Source describes the record this copy was published from. It is declared
spec-side because the propagation layer strips status, so a reader that
must reason about staleness has nowhere else to read it.

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
        <td><b>generation</b></td>
        <td>integer</td>
        <td>
          Generation is the metadata.generation of the Location this copy was
published from.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>publishedAt</b></td>
        <td>string</td>
        <td>
          PublishedAt is when the published content last changed, not when the
publisher last ran.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>
