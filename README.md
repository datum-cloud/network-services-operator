# Datum Network Services

The network services operator defines APIs and core controllers for interacting
with network related entities such as Networks, Network Contexts, and Subnets.

The operator itself is not responsible for provisioning of resources onto data
planes, but instead relies on infrastructure providers such as the [GCP
Infrastructure Provider][infra-provider-gcp] to interact with vendor or platform
specific APIs in order to satisfy the intents defined in custom resources

[infra-provider-gcp]: https://github.com/datum-cloud/infra-provider-gcp

## Documentation

Documentation will be available at [docs.datum.net](https://docs.datum.net/)
shortly.

## Getting Started

### Prerequisites

- Go v1.26.4+ (see `go.mod`).
- Docker v17.03+.
- kubectl v1.31.0+.
- Access to a Kubernetes v1.31.0+ cluster. For local development a
  [kind](https://kind.sigs.k8s.io/), [k3d](https://k3d.io/), or
  [minikube](https://minikube.sigs.k8s.io/) cluster works.

### Running locally for development

Run the operator from your host against a cluster in your current kubectl
context. This is the fast-iteration loop — no image build, no in-cluster
deploy.

The operator is multi-cluster: it watches an **upstream** control plane where
users declare intent and reconciles into a **downstream** cluster that hosts the
data plane. In the default `single` discovery mode `make run` uses your current
kubectl context as the upstream cluster, and `config/dev/config.yaml` points
downstream resource management at `./infra.kubeconfig`.

**1. Prepare the cluster** (installs CRDs, cert-manager, and webhook config):

```sh
make prepare-dev
```

`make prepare-dev` installs chainsaw, sets the controller image, installs
cert-manager and waits for its API, then runs `make install`. Run `make install`
on its own to (re)apply just the CRDs and webhook config
(`kustomize build config/dev | kubectl apply -f -`, into `kube-system`).

**2. Provide a downstream kubeconfig.** `make run` loads `./infra.kubeconfig`
for downstream resources at startup and exits if it is missing. To point
downstream at the same cluster you are already using:

```sh
kind get kubeconfig --name <cluster> > infra.kubeconfig
# or
cp "${KUBECONFIG:-$HOME/.kube/config}" infra.kubeconfig
```

**3. Run the operator:**

```sh
make run
```

This runs `go run ./cmd/main.go manager --server-config=./config/dev/config.yaml`
with metrics and health probes disabled. It regenerates manifests, deepcopy, and
runs `fmt`/`vet` first, then reconciles against your current context. Stop with
`Ctrl-C` and rerun to pick up changes.

To point at a different cluster, set `KUBECONFIG` or switch your kubectl context
before running.

### To Deploy on the cluster

**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/tmp:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/tmp:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall

**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

<!-- ## Contributing -->

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)
