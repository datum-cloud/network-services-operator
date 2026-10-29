# Manage Network Load Balancers with `datumctl nlb`

The NLB plugin publishes existing network services on public TCP or UDP ports.
It also supports sharing a public IP between listeners and publishing UDP port
ranges where those features are available.

This plugin is under development. Catalog installation becomes available after
its first release. The examples below describe the implemented commands.

## Publish a TCP service

Create a service for an existing workload, then make its named port public:

```sh
datumctl network service create api --workload api --port tls:8443
datumctl nlb create api --network default --listener tls --protocol tcp --port 443 --network-service api --backend-port tls
datumctl nlb endpoints api
```

The application manages its own TLS certificate. The NLB forwards encrypted
traffic unchanged. Each serving location gets its own hostname and public IP.

The current provisional defaults are IPv6, locations that follow the backend
service, and a two-minute wait for readiness. Use `--families ipv4,ipv6` to
request both versions where supported. Use `--location <name>` to select a
location, repeating the flag for more. `--location-source all` selects all
eligible locations.

## Share an IP for control and media

Create a service with a control port, a UDP range, and a separate TCP health port:

```sh
datumctl network service create media --workload media --port control:443 --port name=media,port-range=10000-10127,protocol=UDP --port health:8080
datumctl nlb create media --network default --listener control --protocol tcp --port 443 --network-service media --backend-port control
datumctl nlb listener add media media --address public --protocol udp --port-range 10000-10127 --network-service media --backend-port media --health-protocol tcp --health-port health
datumctl nlb describe media
```

A UDP range includes both ends and forwards each public port to the same port
on the service. The backend range must cover the entire public range. A health
check covers the whole range; it does not test each UDP port separately.

If the platform does not support shared addresses or ranges in the selected
locations, the command reports the problem. It does not substitute other settings.

## Inspect and change settings

```sh
datumctl nlb list
datumctl nlb describe media
datumctl nlb endpoints media --listener media
datumctl nlb listener list media
datumctl nlb address list media
datumctl nlb route list media
```

`describe` shows each listener separately. A reachable shared IP does not mean
every listener is ready. Individual backend probe results are currently shown
as unknown; inspect the network service for membership and port information.

Use the route name from `route list` to change a plugin-created route:

```sh
datumctl nlb route update <route-name> --network-service media-v2 --backend-port media --health-protocol tcp --health-port health
datumctl nlb listener update media media --port-range 10000-10063
datumctl nlb update media --display-name "Media service"
```

Add another address request with `address add`, then use `listener add --address`
or `listener update --address` to attach a listener. Changing a listener's
protocol or range requires a compatible backend. The CLI refuses to rewrite
routes managed outside this plugin.

Every change supports `--dry-run`. Separate previews do not guarantee that a
change involving several resources will succeed as a whole. Create, listener
add/update, and route update wait for every selected location and requested IP
version; use `--no-wait` to return after saving or `--timeout` to change the limit.
Address edits and listener removal report saved settings; use `describe` to
follow the platform applying those changes.

If setup partly succeeds, the error names what was saved. Inspect it, fix the
problem, and repeat the same create or listener-add command to finish. A timeout
keeps the resources so the platform can finish preparing them.

Use `-o json` or `-o yaml` for scripts, and `-o name` for names. Progress and
recovery suggestions go to stderr. `version` and help work offline.

## Remove public access

```sh
datumctl nlb listener remove media media
datumctl nlb delete media
```

These commands explain their effects before asking for confirmation. Use `--yes`
in scripts. Deleting the NLB removes its public access and routes created by the
plugin, while preserving backend services and separately managed routes. By
default, deletion waits for the load balancer to disappear; `--no-wait` reports
that deletion was requested while cleanup continues.
