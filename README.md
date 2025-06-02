# Datum Network Services

The network services operator defines APIs and core controllers for interacting
with network related entities such as Networks, Network Contexts, and Subnets.

The operator itself is not responsible for provisioning of resources onto data
planes, but instead relies on infrastructure providers such as the [GCP
Infrastructure Provider][infra-provider-gcp] to interact with vendor or platform
specific APIs in order to satisfy the intents defined in custom resources

[infra-provider-gcp]: https://github.com/datum-cloud/infra-provider-gcp

## Architecture

The Network Services Operator uses a multi-cluster architecture that implements a control plane/data plane separation pattern:

### Control Plane (nso-standard cluster)
- Runs the Network Services Operator and manages high-level network configurations
- Defines APIs and core controllers for network-related entities
- Responsible for defining the desired state of network resources
- Focuses on what network resources should exist

### Data Plane (nso-infra cluster)
- Contains the actual network infrastructure and resources
- Implements the infrastructure provider (e.g., GCP Infrastructure Provider)
- Responsible for implementing the actual network resources based on control plane configurations
- Focuses on how to implement the resources using specific infrastructure providers

This separation provides several benefits:
- Clear separation of concerns between configuration and implementation
- Infrastructure independence (control plane doesn't need to know implementation details)
- Enhanced security through isolation of control plane from infrastructure
- Flexible resource management across clusters

## Location Configuration

Locations define where network resources should be provisioned in the data plane. Example configurations can be found in the `config/samples` directory. For instance, to create a GCP-based location:

1. Make sure you're connected to the data plane cluster:
```sh
KUBECONFIG="${TMPDIR}/.kind-nso-infra.yaml" kubectl config use-context kind-nso-infra
```

2. Apply the location from the samples:
```sh
kubectl apply -f config/samples/location.yaml
```

3. Verify the location was created:
```sh
kubectl get locations
```

## Documentation

Documentation will be available at [docs.datum.net](https://docs.datum.net/)
shortly.

## Getting Started

### Prerequisites

- go version v1.23.0+
- docker version 17.03+.
- kubectl version v1.31.0+.
- kustomize (required for deploying CRs)
- helm (required for deploying CRs with helm charts)
- Access to a Kubernetes v1.31.0+ cluster.

#### Development Setup

When running the operator locally with `make run`, you need to provide a kubeconfig file for the downstream cluster. The operator expects this file at `./infra.kubeconfig` by default. You can either:

1. Create a symbolic link to your kubeconfig:
```sh
ln -s ~/.kube/config ./infra.kubeconfig
```

2. Or modify the configuration in `config/dev/config.yaml` to point to your kubeconfig file:
```yaml
downstreamResourceManagement:
  kubeconfigPath: /path/to/your/kubeconfig
```

#### Using k9s with Kind Clusters

The project uses two Kind clusters: `nso-standard` and `nso-infra`. If the clusters don't exist, create them first:

1. Create the standard cluster:
```sh
make kind-standard-cluster
```

2. Create the infrastructure cluster:
```sh
make kind-infra-cluster
```

If the clusters already exist but you need to get their kubeconfigs:
```sh
# For standard cluster
kind get kubeconfig --name nso-standard > "${TMPDIR}/.kind-nso-standard.yaml"

# For infrastructure cluster
kind get kubeconfig --name nso-infra > "${TMPDIR}/.kind-nso-infra.yaml"
```

After getting the kubeconfigs, you can connect to them using k9s:

To connect to the standard cluster:
```sh
KUBECONFIG="${TMPDIR}/.kind-nso-standard.yaml" k9s
```

To connect to the infrastructure cluster:
```sh
KUBECONFIG="${TMPDIR}/.kind-nso-infra.yaml" k9s
```

You can also create an alias in your shell configuration for easier access:
```sh
# Add to your ~/.zshrc or ~/.bashrc
alias k9s-standard='KUBECONFIG="${TMPDIR}/.kind-nso-standard.yaml" k9s'
alias k9s-infra='KUBECONFIG="${TMPDIR}/.kind-nso-infra.yaml" k9s'
```

To delete the clusters when you're done:
```sh
kind delete cluster --name nso-standard
kind delete cluster --name nso-infra
```

#### Installing Kustomize

The project uses a local version of Kustomize to ensure version consistency. You can install it using the project's Makefile:

```sh
make kustomize
```

This will install Kustomize v5.5.0 in the project's `bin` directory.

Alternatively, you can install Kustomize globally using one of the following methods:

**Using Homebrew (macOS):**
```sh
brew install kustomize
```

**Using curl (Unix-like systems):**
```sh
curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash
```

**Using go install (if you have Go installed):**
```sh
go install sigs.k8s.io/kustomize/kustomize/v5@latest
```

Verify the installation:
```sh
kustomize version
```

#### Installing Helm

You can install Helm using one of the following methods:

**Using Homebrew (macOS):**
```sh
brew install helm
```

**Using curl (Unix-like systems):**
```sh
curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
```

Verify the installation:
```sh
helm version
```

### To Deploy on the cluster

**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/tmp:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don't work.

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
