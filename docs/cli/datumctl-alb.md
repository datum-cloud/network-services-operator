# Manage Application Load Balancers with `datumctl alb`

The `alb` plugin for [`datumctl`](https://github.com/datum-cloud/datumctl) lets you create and manage Application Load Balancers on Datum Cloud from your terminal. It writes the same `HTTPProxy`, traffic protection, and basic auth resources as the cloud portal.

## Install the plugin

Install it from the official Datum plugin catalog, then confirm that `datumctl` found it:

```console
$ datumctl plugin install alb
Installed alb v0.1.0 from datum  [official]

$ datumctl alb version
datumctl-alb v0.1.0 (Networking API networking.datumapis.com/v1alpha)
```

Until the catalog lists the plugin, install a release archive from this repository:

```sh
datumctl plugin install datum-cloud/network-services-operator@<tag>
```

`datumctl alb version` needs no login, no project, and no network, so run it first whenever something else fails.

## Get started

```sh
datumctl alb create my-app --endpoint https://origin.example.com
datumctl alb describe my-app
datumctl alb hostname add my-app app.example.com
```

Create waits for Datum to assign a default hostname (for example `<uid>.datumproxy.net`). Point a CNAME at that hostname, or attach a custom hostname you already own.

## Create a load balancer

```sh
datumctl alb create my-app --endpoint https://origin.example.com
datumctl alb create my-app --network-service storefront --port http
datumctl alb create my-app --endpoint https://origin.example.com --hostname app.example.com
datumctl alb create my-app --endpoint https://origin.example.com --no-wait
datumctl alb create my-app --endpoint https://203.0.113.10 --tls-hostname origin.example.com
```

Origins given at create time form the default `/` route. `--endpoint` is a URL and may repeat. `--network-service` names an existing NetworkService in the project and must be paired with the `--port` **name** that service declares (`http`, not `8080`). The plugin never creates, edits, or deletes a NetworkService; if the one you name does not exist, create fails with not-found.

Defaults match the cloud portal:

- Force HTTPS is on
- Traffic protection is `Enforce` at paranoia 1
- No custom hostnames are required

Pass `--no-waf` to skip traffic protection, or `--waf-mode Observe` to log without blocking.

## Routes and origins

A route is a path prefix plus the pool of origins that serve it.

```sh
datumctl alb route list my-app
datumctl alb route add my-app --path /api --endpoint https://api.example.com
datumctl alb route add my-app --path /checkout \
  --endpoint https://a.example.com --endpoint https://b.example.com
datumctl alb route update my-app --path / --network-service storefront --port http
datumctl alb route remove my-app --path /api
```

`route update` replaces every origin on that path and leaves other routes alone. To change a single origin:

```sh
datumctl alb route backend list my-app
datumctl alb route backend add my-app --path /api --endpoint https://api-2.example.com
datumctl alb route backend remove my-app --path /api --endpoint https://api.example.com
```

Removing the last origin on a route is refused; remove the route instead. The default `/` route cannot be removed while other routes exist unless you pass `--force`. Force HTTPS shows in `route list` as a `system` route and is controlled by `alb update`, not `route remove`.

The API currently accepts one origin per route. The commands already take a pool so nothing changes when that cap lifts; until then the server rejects a second origin on the same path.

## Hostnames

```sh
datumctl alb hostname add my-app app.example.com
datumctl alb hostname list my-app
datumctl alb hostname remove my-app app.example.com
```

The default hostname is assigned by the platform and shown by `describe`. Custom hostnames must be unique on the platform and are verified through `Domain` resources.

## Traffic protection

```sh
datumctl alb waf set my-app --mode Enforce --paranoia 1
datumctl alb waf set my-app --mode Observe --paranoia 2
datumctl alb waf describe my-app
datumctl alb waf disable my-app
```

## Request headers

```sh
datumctl alb header set my-app Host=origin.example.com
datumctl alb header set my-app X-Debug=1
datumctl alb header list my-app
datumctl alb header unset my-app X-Debug
```

`--host-header` on `create` is the same Host override the portal offers. Additional headers are allowed here; the portal treats those load balancers as advanced and shows them read-only.

## Basic authentication

```sh
echo 'secret' | datumctl alb auth set my-app --user admin --password-stdin
datumctl alb auth list my-app
datumctl alb auth unset my-app
```

Passwords are never printed. Usernames are stored in an htpasswd secret using SHA hashes, matching Envoy Gateway.

## Update and delete

```sh
datumctl alb update my-app --display-name "Production API"
datumctl alb update my-app --no-force-https
datumctl alb delete my-app --yes
```

`update` covers settings that apply to the whole load balancer: the display name (stored as `kubernetes.io/display-name`, 50 characters max) and Force HTTPS. Origins live on routes; passing `--endpoint` here points you at `route update`.

Delete also removes the attached traffic protection policy and basic auth configuration. NetworkServices a route referenced are left in place.

## Output

Every command accepts `-o table|wide|json|yaml|name`. `json` and `yaml` emit the underlying API objects.
