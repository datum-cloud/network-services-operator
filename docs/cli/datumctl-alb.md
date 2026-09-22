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
datumctl alb create --display-name "Customer API" --endpoint https://origin.example.com
datumctl alb create my-app --network-service storefront --port http
datumctl alb create my-app --endpoint https://origin.example.com --hostname app.example.com
datumctl alb create my-app --endpoint https://origin.example.com --no-wait
datumctl alb create my-app --endpoint https://203.0.113.10 --tls-hostname origin.example.com
```

Origins given at create time form the default `/` route. `--endpoint` is a URL and may repeat. `--network-service` names an existing NetworkService in the project and must be paired with the `--port` **name** that service declares (`http`, not `8080`). The plugin never creates, edits, or deletes a NetworkService; if the one you name does not exist, create fails with not-found.

Omit the object name and pass `--display-name` to derive a DNS-safe name the same way the cloud portal does (kebab-case plus a short random suffix). The printed create message shows the name that was used.

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

Removing the last origin on a route is refused; remove the route instead. The default `/` route cannot be removed while other routes exist unless you pass `--force`, and the last remaining route cannot be removed at all. Force HTTPS shows in `route list` as a `system` route and is controlled by `alb update`, not `route remove`.

Rules written outside this plugin with exact or regex path matches, header or method conditions, or several matches show as `advanced` in `route list`. The plugin leaves them alone; edit those with `datumctl apply -f`.

Every mutation re-reads the load balancer, patches with its `resourceVersion`, and retries once if something else changed it in between.

A route takes up to 16 origins, and traffic is split across them. One constraint decides whether a pool works today:

**Origins in the same route must agree on the Host header sent upstream.** A NetworkService origin needs no Host rewrite, so pools of those work. A URL origin takes its Host from its own hostname, so two URL origins on different hostnames conflict, and the load balancer refuses to publish the change — it keeps serving what it published last and says why in the status message rather than failing the write.

There are two ways round it, and one of them is a trap:

- **Give each origin its own route.** Safe.
- **Set a Host override on the route** with `alb header set`. This makes the origins agree and publishes — but it sends the same Host to all of them, so any origin that routes by hostname (Vercel, Netlify, Fly.io, Cloudflare Pages) answers the wrong site or a 404. It looks like it worked.

A connector origin must be the only origin in its route.

## Hostnames

```sh
datumctl alb hostname add my-app app.example.com
datumctl alb hostname list my-app
datumctl alb hostname remove my-app app.example.com
```

The default hostname is assigned by the platform and shown by `describe`. Custom hostnames must be unique on the platform and are verified through `Domain` resources. `list` shows the generated hostname and a `CUSTOM` summary (first attached name, `+N` when there are more); `describe` prints each custom hostname with available / DNS / cert status.

## Access logs

```sh
datumctl alb logs my-app
datumctl alb logs my-app --since 1h --limit 50
datumctl alb logs my-app --method GET --code 500 --code 502
datumctl alb logs my-app --host app.example.com --follow
```

Rows come from the same project logs API the cloud portal uses, pinned to this load balancer. `--method` and `--code` filter in the query; `--host` filters after fetch. `--follow` polls for new lines (there is no live tail). `-o wide` adds request id and upstream.

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

`--host-header` on `create` is the same Host override the portal offers. Additional headers are allowed here.

## What the portal does with what this writes

The portal edits one route with one origin. It has no concept of a second route, a second origin, a path match, or a per-origin filter — it cannot show them, and it does not warn you that they are there.

**It does not lock the form.** Editing the origin, Force HTTPS, HSTS, the TLS hostname or the Host header rebuilds the whole rule list from the three fields the portal models, and sends it as a merge patch. Anything this plugin wrote that the portal does not represent is dropped: extra routes, extra origins and their weights, path matches, per-origin filters. The save succeeds and reports success.

So on a load balancer with more than the portal's shape:

- **Safe in the portal:** custom hostnames, traffic protection, and basic auth. Those edits do not touch the rules.
- **Destructive in the portal:** anything on the origin, TLS or redirect cards.

Use `datumctl alb` for a load balancer that has routes or pools, and keep portal edits to hostnames, protection and auth until the portal's own routes editor ships.

Two smaller differences worth knowing:

- Traffic protection here takes paranoia 1 to 4; the portal offers only 1 and 2. Setting 3 or 4 is fine, the portal just cannot change it.
- The portal shows a load balancer's display name from its own annotation, which this plugin does not write. A load balancer created here shows its object name in the portal until you rename it there.

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

Every command accepts `-o table|wide|json|yaml|name`. `json` and `yaml` emit the underlying API objects. `list --status active|pending|error` narrows the table; `Active` means the platform has programmed the load balancer.
