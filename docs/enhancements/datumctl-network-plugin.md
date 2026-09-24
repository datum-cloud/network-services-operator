# `datumctl network` plugin

| | |
|---|---|
| **Status** | Proposed |
| **Author** | Engineering |
| **Created** | 2026-09-24 |

> [!NOTE]
> Implementation plan, not shipped code. Paths prefixed `compute:` are in
> `datum-cloud/compute`; paths prefixed `datumctl:` are in
> `datum-cloud/datumctl` (v0.19.0).

## Context
Users have no CLI for VPCs. `datumctl compute` covers workloads, and the unmerged `origin/feat/datumctl-alb-plugin` covers HTTPProxy. This plugin covers network primitives, starting with two read-only commands:
- `datumctl network list`: the project's VPCs
- `datumctl network <vpc> list`: what is attached to one VPC, grouped by kind

What the API offers:

| CLI concept | Kind (`networking.datumapis.com/v1alpha`, ns `default`) | How it links to the VPC |
|---|---|---|
| VPC | `Network` | — |
| Per-location presence | `NetworkContext` | label `NetworkLabel` (`api/v1alpha/networkbinding_types.go`) |
| Subnets (/64 per location) | `Subnet` | label `NetworkLabel` |
| Devices (instance NICs) | `NetworkInterface` (project projection) | `spec.network.name`, filtered in the client because the objects carry no network label |
| Services | `NetworkService` | its `spec.networkInterfaces.selector` matches one of the VPC's interfaces |

Node and host details (VPCAttachment) are removed before interfaces reach the project (`internal/controller/networkinterface_projection.go`), so a device means a NIC plus its instance and workload labels. The UI in `ui/consumer/src/lib/api.ts` already makes these same queries and is the reference.

NetworkBindings (hub only) and NetworkInterfaceClaims (cell only) are not visible from the project control plane.

## Layout
The plugin gets its own package in this repo, modeled on `compute:internal/cmd/compute`. It shares no code with the ALB branch.

```
cmd/datumctl-network/main.go          # ServeManifest + ExecuteContext + exit code
internal/cmd/network/
  root.go        root_test.go         # root cmd, VPC-scoped dispatch, completion
  list.go        list_test.go         # `network list`
  vpc_list.go    vpc_list_test.go     # `network <vpc> list`
  util/
    client.go                         # NewClient(project): scheme = networkingv1alpha
    printer.go table.go time.go args.go completion.go conditions.go
.goreleaser-network.yaml
.github/workflows/release-network-plugin.yml
```

Copy these helpers from `compute:internal/cmd/compute/util/`, trimmed to what the plugin needs:
- `client.go`: `ProjectControlPlaneURL`, `NewClient`, `ProjectFromCmd`, `ResourceNamespace="default"`. The scheme is only `networkingv1alpha.AddToScheme`.
  - The control plane URL is `https://$DATUM_API_HOST/apis/resourcemanager.miloapis.com/v1alpha1/projects/<project>/control-plane`.
  - The bearer token comes from `plugin.Token()`.
- `printer.go`: `OutputFormat`, `PrintJSON`, `PrintYAML`
- `table.go`: `NewTabWriter`
- `time.go`: `RelativeAge`
- `args.go`: `NoPositionalArgs`
- `conditions.go`: `FindCondition`, plus a Ready-status string helper. `apimachinery/pkg/api/meta.FindStatusCondition` works too.

Also:
- The ALB branch's `internal/cmd/alb/util/client.go` `RestConfig` has a better error UX (CLIError with a fix hint). Adopt that pattern in simplified form.
- `main.go` follows `origin/feat/datumctl-alb-plugin:cmd/datumctl-alb/main.go`: `plugin.ServeManifest{Name:"network", APIVersion:1, MinAPIVersion:1}`, `var version = "dev"`, and a signal-aware context.

## Command routing (the `<vpc> list` shape)
Cobra has no native `<positional> <verb>` form, so:
- `root := plugin.NewRootCmd("network", "Manage VPCs and network primitives on Datum Cloud")`. It already provides persistent `--org`, `--project` and `-o`.
- `root.AddCommand(listCmd())`, so `network list` is a normal subcommand.
- `root.Args = cobra.ArbitraryArgs`. Without it, cobra rejects `network myvpc list` as an unknown command.
- `root.RunE` routing:
  - no args → help
  - `[vpc]` alone → error: `specify a verb: datumctl network <vpc> list`
  - `[vpc, verb, ...rest]` → look `verb` up in `vpcVerbs map[string]vpcVerb{"list": runVPCList}`; an unknown verb errors with the valid ones listed
- Flags such as `-o` / `--no-headers` are declared on root, so cobra parses them before `RunE` wherever they appear in the command line.
- Known limitation: a VPC named `list`, `help` or `completion` is shadowed. Add this to the help text.
- `root.ValidArgsFunction` completes network names at position 0 (plus cobra's built-in subcommand completion) and verbs from `vpcVerbs` at position 1. `util.CompleteNetworkNames` lists `NetworkList` in `default`.
- No activation gate for now. Networking has no service-catalog activation yet. Add one later the way compute does (`compute:internal/cmd/compute/util/activation.go`).

## `datumctl network list`
- Make one List call each for `NetworkList`, `NetworkContextList` and `NetworkInterfaceList` in `default`, and count in memory: contexts by the `NetworkLabel` label, interfaces by `spec.network.name`.
- Table: `NAME  IPV6 PREFIX  LOCATIONS  INTERFACES  READY  AGE`. `-o wide` adds `MTU  IPAM  REASON`.
  - `IPV6 PREFIX` = `status.ipam.ipv6Prefix`
  - `READY` / `REASON` come from the `Ready` condition
- With no VPCs: print `No VPCs found in project <p>.` to stderr and exit 0.
- `-o json|yaml` prints a `[]networkView{Name, IPv6Prefix, Locations, Interfaces, Ready, Network *v1alpha.Network}` array, the same view pattern as compute's `workloadView`.
- `--no-headers` is supported.
- Structure: `runList(cmd)` builds the client, then calls `listNetworks(ctx, out, errOut, c, opts)`. Passing the client in keeps the logic testable.

## `datumctl network <vpc> list`
1. Get the `Network`. On NotFound, fail with `VPC "<vpc>" not found in project <p>`.
2. Load the rest:
   - `SubnetList` with `client.MatchingLabels{NetworkLabel: vpc}`
   - `NetworkInterfaceList`, filtered on `spec.network.name == vpc`
   - `NetworkServiceList`, keeping a service if `metav1.LabelSelectorAsSelector(spec.networkInterfaces.selector)` matches the labels of one of the VPC's interfaces
3. Print a header line: `VPC <name>  IPv6 <prefix>  Ready <status>`.
4. Print each section with its own heading. An empty section prints `(none)`.
   - **Interfaces**: `NAME  WORKLOAD  LOCATION  INTERFACE  IPV6  PHASE  READY  AGE`
     - `WORKLOAD` comes from the `compute.datumapis.com/workload-name` label
     - `LOCATION` comes from `NetworkInterfaceLocationLabel`
     - `IPV6` is the primary address in `spec.addresses`
     - `READY` combines the Allocated/Prepared/Programmed conditions
     - wide adds `CLAIM` (the `held-by` label), `MODE` and `EXTERNAL`
   - **Subnets**: `NAME  LOCATION  CIDR  READY  AGE`. `CIDR` = `status.startAddress/prefixLength`.
   - **Services**: `NAME  PORTS  LOCATIONS  MEMBERS  HEALTHY  READY  AGE`, taken from `status.summary`.
5. `-o json|yaml` prints one object: `{network, interfaces[], subnets[], services[]}`.
6. The workload label key is a string constant in `util`, because NSO doesn't import the compute API.

## Build, deps
- `go get go.datum.net/datumctl@v0.19.0` (the same version compute uses), then `go mod tidy`. Check that k8s and controller-runtime stay at v0.36.1 / v0.24.1.
- Makefile: add `build-network-plugin` → `go build -o bin/datumctl-network ./cmd/datumctl-network`. `make test` already covers `./internal/...`.

## Release: making `datumctl plugin install network` work
How datumctl installs a plugin (from `datumctl:internal/cmd/plugin/install.go` and `helpers.go`):
- **`install owner/repo[@v]`** takes the plugin name from the repo name. `datum-cloud/network-services-operator` would give plugin `network-services-operator` and look for `datumctl-network-services-operator_*` archives, so this path can't install `network` from this repo.
- **`install network[@v]`** looks the name up in the `datum-cloud/datumctl-plugins` catalog. Compute ships this way, and so will we.
- In both paths the release needs an archive named `datumctl-network_<Os>_<x86_64|arm64>.tar.gz` (`.zip` on Windows), a `checksums.txt` covering it, and a `datumctl-network` binary inside that answers `--plugin-manifest`.

**`.goreleaser-network.yaml`** is `compute:.goreleaser-plugin.yaml` with these changes:
- `project_name`, build `id`/`binary` and archive `id` are `datumctl-network`; `main: ./cmd/datumctl-network`
- the same OS/arch matrix: no windows/arm64 build, because datumctl has no asset name for it
- `ldflags: -X main.version=v{{.Version}}`, so a `v0.29.0-dev.1` tag gives a binary whose manifest says `v0.29.0-dev.1`
- the same archive `name_template` (`title .Os`, amd64 → `x86_64`)
- `checksum.name_template: checksums.txt`. The NSO release doesn't publish its own checksums today; `publish.yaml` only builds images and bundles, so there is nothing to collide with.
- `release.mode: append`, so the plugin archives are added without replacing the hand-written operator release notes
- `release.prerelease: auto`, so dev tags are published as prereleases and don't become "latest"
- `snapshot.version_template: "{{ incpatch .Version }}-dev.{{ .ShortCommit }}"` for local `goreleaser release --snapshot` builds
- A separate file name, so it doesn't collide with the ALB branch's `.goreleaser-plugin.yaml`

**Dev version numbering.** Plugin previews use NSO prerelease tags, `vX.Y.Z-dev.N` (e.g. `v0.29.0-dev.1`), the same scheme compute uses (`v0.8.0-dev.9`). Publishing a GitHub prerelease for that tag triggers the workflow below. Users install it with `datumctl plugin install network@v0.29.0-dev.1`. Once a GA tag is released, a bare `datumctl plugin install network` resolves to it.

**`.github/workflows/release-network-plugin.yml`** is `compute:.github/workflows/release-plugin.yml` with these changes:
- It triggers on `release: published`, which includes prereleases.
- Job 1 runs `goreleaser release --config .goreleaser-network.yaml --clean`, checking out `github.event.release.tag_name` with `fetch-depth: 0`.
- Job 2 mints a `datumctl-plugins` token from the release bot app, then runs `datum-cloud/actions/update-plugin-index@v1.21.0` with `plugin-name: network` and `version: <tag>`.
- Before merging, confirm that the NSO repo has the `RELEASE_BOT_APP_ID` and `RELEASE_BOT_APP_PRIVATE_KEY` secrets, and that the app can reach `datumctl-plugins`.
- The workflow file has to be pushed over SSH, because `gh`'s token lacks the `workflow` scope.
- The first catalog PR adds a new `network` entry, so someone on the `datumctl-plugins` side has to review it.

## Tests (fake client, as in compute)
- `list_test.go`: table for empty, one Ready VPC, and a not-ready VPC with a reason; wide columns; JSON round-trip; counts correct across two VPCs.
- `vpc_list_test.go`: interfaces from another VPC are left out; subnets are selected by label; a service is matched by selector and one on another VPC is left out; NotFound error; JSON shape.
- `root_test.go` (real cobra root with `SetArgs`):
  - `list` routes to the list command
  - `myvpc list` routes to the VPC list
  - `myvpc` alone and `myvpc bogus` give the right errors
  - `list extra` is rejected by `NoPositionalArgs`

## Verification
1. `make test` and `make lint`.
2. `go build -o bin/datumctl-network ./cmd/datumctl-network`. Check that `bin/datumctl-network --plugin-manifest` prints valid JSON.
3. Put `bin/` on `PATH`, run `datumctl plugin trust network`, then against a real project:
   - `datumctl network list` and `datumctl network list -o wide`
   - `datumctl network <vpc> list` and `datumctl network <vpc> list -o yaml`
   - tab completion: `datumctl network <TAB>` and `datumctl network <vpc> <TAB>`
4. Compare the output with the portal's VPC view (`ui/consumer`) for the same project.
5. Release config, checked locally (goreleaser comes from `$PATH`, then `nix run nixpkgs#goreleaser`):
   - `goreleaser check --config .goreleaser-network.yaml`
   - `goreleaser release --snapshot --clean --config .goreleaser-network.yaml`. Check that `dist/` has `datumctl-network_Darwin_arm64.tar.gz` etc. plus `checksums.txt`, and that the extracted binary's `--plugin-manifest` reports the `-dev.<sha>` version.
6. End to end: tag and publish the prerelease `v0.29.0-dev.1`, confirm the assets are attached and the catalog PR opened, merge it, then run `datumctl plugin install network@v0.29.0-dev.1 && datumctl network list`.
