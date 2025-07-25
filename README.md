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

- go version v1.24.0+
- docker version 17.03+.
- kubectl version v1.31.0+.
- Access to a Kubernetes v1.31.0+ cluster.

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
