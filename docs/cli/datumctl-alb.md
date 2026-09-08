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
datumctl alb create my-app --endpoint https://origin.example.com --hostname app.example.com
datumctl alb create my-app --endpoint https://origin.example.com --no-wait
datumctl alb create my-app --endpoint https://203.0.113.10 --tls-hostname origin.example.com
```

Defaults match the cloud portal:

- Force HTTPS is on
- Traffic protection is `Enforce` at paranoia 1
- No custom hostnames are required

Pass `--no-waf` to skip traffic protection, or `--waf-mode Observe` to log without blocking.

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
datumctl alb update my-app --endpoint https://new-origin.example.com
datumctl alb update my-app --no-force-https
datumctl alb delete my-app --yes
```

Delete also removes the attached traffic protection policy and basic auth configuration.

## Output

Every command accepts `-o table|wide|json|yaml|name`. `json` and `yaml` emit the underlying API objects.
