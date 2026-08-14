# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [ServingLocation](#servinglocation)




## ServingLocation
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






ServingLocation tells a cell which location it serves.

A cell is a cluster that runs workloads at one physical location. It cannot
tell where it is on its own, so the platform delivers it a ServingLocation:
a read-only copy of a Location, carrying the name and topology of the place
the cell sits in. Everything the cell does that depends on where it is,
such as claiming network addresses, resolves through this object.

A ServingLocation takes the name of the Location it was copied from. Expect
exactly one on a cell. Two or more means more than one location has been
delivered to the same cell, and the cell refuses to guess between them.

This object is managed for you. Create and edit Locations on the platform
control plane; the copies follow.

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
          ServingLocationSpec describes the location a cell serves.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ServingLocation.spec
<sup><sup>[↩ Parent](#servinglocation)</sup></sup>



ServingLocationSpec describes the location a cell serves.

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
          Topology describes where in the world this location is. Workloads placed
at this location inherit it, and placement rules that ask for a city or a
region are answered from these keys.

The map holds arbitrary keys. Some keys are well known:

	topology.datum.net/city-code: IAD
	topology.datum.net/region: us-east-1

You must supply topology.datum.net/city-code, and it must not be empty.
A location with no city code cannot serve placement requests that name a
city, so the API rejects it. Any other key you set is carried through
unchanged and is available to workloads at this location.

This field copies the topology of the Location it was published from.
Edit the Location, not this copy.<br/>
          <br/>
            <i>Validations</i>:<li>'topology.datum.net/city-code' in self && self['topology.datum.net/city-code'] != '': topology must carry a non-empty topology.datum.net/city-code</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#servinglocationspecsource">source</a></b></td>
        <td>object</td>
        <td>
          Source identifies the Location this copy came from. Use it to tell how
current the copy is: compare it against the Location of the same name to
see whether an edit has reached this cell yet.

The publisher sets this field. Leave it alone.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### ServingLocation.spec.source
<sup><sup>[↩ Parent](#servinglocationspec)</sup></sup>



Source identifies the Location this copy came from. Use it to tell how
current the copy is: compare it against the Location of the same name to
see whether an edit has reached this cell yet.

The publisher sets this field. Leave it alone.

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
          Generation is the metadata.generation of the Location this copy came
from. When it is lower than the Location's current generation, an edit
has not reached this cell yet.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>publishedAt</b></td>
        <td>string</td>
        <td>
          PublishedAt is when the content of this copy last changed. A copy that is
re-checked but not changed keeps its original timestamp, so an old
timestamp means the location has been stable, not that publishing has
stalled.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>
