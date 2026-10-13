# Manage networks with `datumctl network`

The `network` plugin for [`datumctl`](https://github.com/datum-cloud/datumctl) manages networks, network services, and the network interfaces instances hold on them. It writes the same `Network` and `NetworkService` resources as the cloud portal, and its `describe` and `diagnose` output comes from the same code the Patch assistant uses, so the two never describe a network differently.

## Install the plugin

Once the catalog lists it:

```sh
datumctl plugin install network
datumctl network version
```

From a checkout:

```sh
make install-network-plugin
datumctl network version
```

The catalog entry is added by hand for the first release, the same way as `alb`'s: publish a release, then open the output of `hack/plugin-catalog-entry.sh network <tag>` as `plugins/network.yaml` against [datum-cloud/datumctl-plugins](https://github.com/datum-cloud/datumctl-plugins). After that, every release updates it on its own.

`datumctl network version` needs no login, no project, and no network.

## Networks

A network is a private, IPv6-only address space your workloads attach to. Datum assigns it an IPv6 /48 when it is created, so the name is the only required choice.

```sh
datumctl network create backend
datumctl network create backend --mtu 1400 --wait
datumctl network list
datumctl network describe backend
```

A new network is in no location. It reaches a location when a workload placed there attaches to it:

```sh
datumctl compute deploy api --image ghcr.io/acme/api:1.4.2 --location us-east-1 --network backend
```

The MTU is the only setting that can change after create, and the change applies to every instance on the network:

```sh
datumctl network update backend --mtu 1400
```

Deleting a network lists the workloads that lose it, the network services that stop serving, and any kept interfaces whose addresses are given up, before asking you to type its name. Removal finishes once every instance has let go of its address.

```sh
datumctl network delete backend
```

## Network services

A network service gives a workload's instances on one network a stable name and named ports. A load balancer reaches it by the service's name and a port name.

```sh
datumctl network service create api --workload api-backend --port http:8080
datumctl alb create shop --network-service api --port http
```

Select by label instead of by workload with `--selector app=web`. The labels have to be on the instances' network interfaces, not only on the workload.

`update` replaces what you name: `--workload` or `--selector` replaces the selection, and `--port` replaces the whole port list, so name every port the service should keep. Deleting a service lists the load balancers that stop reaching it.

## Interfaces

Each instance holds a network interface on its workload's network. A workload can ask to keep an instance's addresses when the instance goes away; the kept interface holds them until an instance of the same name returns.

```sh
datumctl network interface list --network backend
datumctl network interface list --kept
datumctl network interface release api-0-eth0
```

Releasing a kept interface gives its addresses up for good. An interface a running instance holds cannot be released.

## When something is not working

```sh
datumctl network diagnose backend
datumctl network service diagnose api
```

Both walk every object behind the network or service and report the cause that names something: what it is on, the location and workload it affects, who has to act — you, Datum, or nobody yet — and what to do. A service whose instances are not ready is a workload problem, not a network one, and the output says which instances and what they report.

## Output

Every read takes `-o table|wide|json|yaml|name`. Errors print a fix and exit with a stable code named `NETWORK_*`, the same codes as `alb`. `--yes` skips confirmation; without it, a destructive command run non-interactively refuses rather than guessing.
