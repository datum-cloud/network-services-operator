# Cell-local network load balancer health

The project controller cannot prove that a backend answers on its application
port. It sees a projected interface address, but the cell controller manager's
pod has no established route into each tenant VPC. Host-network connection
failures must not be interpreted as unhealthy applications.

`NetworkLoadBalancerBackendHealth` is an internal cell-local contract for a
runner on the gateway's VPC-aware socket path. The cell manager registers the
probe reconciler without a dialer today. Its `ProbesEvaluated` condition is
`False/NoDataPlaneDialer`, and no target is eligible from status alone.
Serving requires a dialer that selects the validated fabric VPC's VRF or
network namespace for each outgoing connection. It must never fall back to the
controller pod's ordinary network.

The cell intent controller must first verify the live source interface,
attachment UID, VPC identity, and project ownership described in the
[cell contract](network-load-balancer-cell-contract.md). It can then create one
health object for a route parent and location. The parent UID and generation
bind the health object to a particular load balancer instance. Each target
records the source interface UID, attachment UID, address, serving port,
and resolved probe. The probe runner also rechecks the live interface
UID, network, location, holder availability, attachment name, VPC ID, and
address before each check. The eventual traffic programmer must revalidate
the attachment UID and VPC association when it consumes healthy targets; probe success is
not an authorization to program or announce a VIP.

A VPC-aware runner evaluates up to 64 targets per object, with at most eight
concurrent probes per object and 32 across the process. Queueing for a probe
slot does not consume the probe deadline. Each TCP connection or HTTP/HTTPS GET
has a two-second deadline. A full 64-target sweep can take eight waves, or
16 seconds, and the next sweep starts five seconds after completion. Five
seconds is therefore a pause between sweeps, not a per-target interval.
Two consecutive successes are needed to mark a target healthy; a failure
removes it when that sweep is published. Results expire after 39 seconds:
two maximum sweeps, the five-second pause, and one probe timeout of scheduling
slack. A delayed sweep beyond this bound causes health to expire closed.
Consumers use `HealthyTargets`, which rejects a
result when the object generation, route UID or generation, target fingerprint,
probe timestamp, or expiry no longer matches. A removed or replaced source
cannot inherit an earlier success.

HTTP and HTTPS accept only 2xx responses and do not follow redirects. HTTPS
always validates the server certificate. With the current native route API,
which has no probe hostname, the certificate must have an IP SAN for the
backend IPv6 address. The cell-local contract can carry an explicit server
name once the public route API exposes one; until then a hostname is not
invented by the runner. UDP traffic uses a separate TCP, HTTP, or HTTPS probe
port and is never treated as healthy from a UDP packet alone.
