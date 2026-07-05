# JellyOps — Jellyfin Kubernetes Operator

JellyOps is a Go/kubebuilder operator that manages the full lifecycle of
[Jellyfin](https://jellyfin.org) media-server instances and their plugins.
Plugins are delivered **as OCI container images** and mounted on demand using
Kubernetes [image volumes](https://kubernetes.io/docs/tasks/configure-pod-container/image-volumes/) —
everything is declarative through Custom Resources; no imperative steps.

See [`docs/specification.md`](docs/specification.md) for the full design.

## What it does

- **`Jellyfin`** (namespaced) — a running Jellyfin instance: Deployment, config/cache
  PVCs, media library folders (inline NFS, provisioned RWX NFS, or PVCs), Service,
  optional Ingress, and hardware-accelerated transcoding.
- **`JellyfinPlugin`** (namespaced) — a plugin delivered as an image volume, plus any
  companion workloads/Services (e.g. jellycode's remote transcoding workers). Enabling
  or removing a plugin is just creating or deleting this CR.

The operator is **plugin-agnostic**: it ships no plugin-specific logic.

## Controllers

| Controller | Responsibility |
|---|---|
| `JellyfinReconciler` | Owns the instance Deployment, PVCs, Service, and Ingress. Composes the pod template — image volumes, `imageVolumeCopy` staging, and pre-start install init containers — from bound plugins. Watches `JellyfinPlugin` and re-rolls the instance when the plugin set changes. |
| `JellyfinPluginReconciler` | Validates ABI compatibility, owns companion workloads/Services, and drives graceful teardown (scale-to-zero drain honoring `terminationGracePeriodSeconds`) via a finalizer. |
| `JellyfinAPIReconciler` | Day-2 loop: once an instance is `Ready`, drives Jellyfin's first-run wizard, mints and persists an API key, and reconciles libraries (`storage.media[].library`) over the in-cluster HTTP API on `:8096`. Never addresses the public Ingress. |

## Plugin delivery: image volumes

Two injection modes (`JellyfinPlugin.spec.injection`):

- **`imageVolume`** (default) — mounts the plugin image read-only directly at
  `/config/plugins/<Name_Version>`.
- **`imageVolumeCopy`** — mounts the image read-only and copies it into the writable
  plugins dir via a staging init container, so Jellyfin can mutate `meta.json` at runtime.

An optional `spec.install` runs a pre-start setup script as an init container, with
`runOnce` markers, `failurePolicy` (Ignore/Fail), and `timeoutSeconds` support.

> **Runtime prerequisite:** image volumes are beta in Kubernetes **1.33** (GA in 1.36)
> and require a CRI runtime with image-volume support (containerd/CRI-O). The `ImageVolume`
> feature gate must be enabled on clusters older than where it defaults on.

## Getting started

### Prerequisites
- Go 1.24+
- Docker 17.03+, kubectl 1.11+
- A Kubernetes 1.33+ cluster with image-volume support (for the plugin-mount path)

### Develop & test

```sh
make manifests generate   # regenerate CRDs, RBAC, and deepcopy after any api/ edit
make test                 # unit tests + envtest (apiserver+etcd; ImageVolume gate enabled)
make build                # build the manager binary
```

Tests are layered:
- **Unit** (`internal/plugins`, `internal/jellyfinapi`) — the pod-template builder and the
  HTTP client/library-diff, fully offline.
- **envtest** (`internal/controller`) — real apiserver+etcd; verifies owned objects,
  cross-watch, finalizer drain, Secret/status wiring. No kubelet, so pod-run behavior
  (image-volume mount, install execution, live bootstrap) is proven only on a real cluster.

### Install

Prebuilt multi-arch images (`linux/amd64`, `linux/arm64`) are published to GHCR by CI
(`.github/workflows/publish.yml`) — on every push to the `production` branch and on
version tags:

| Ref pushed | Image tags |
|---|---|
| `production` branch | `ghcr.io/crunchymonkies/jellyops:production`, `:latest`, `:sha-<short>` |
| tag `vX.Y.Z` | `ghcr.io/crunchymonkies/jellyops:X.Y.Z`, `:X.Y`, `:latest`, `:sha-<short>` |

Install the CRDs and deploy the operator (pin a version tag for production):

```sh
make install                                              # install CRDs
make deploy IMG=ghcr.io/crunchymonkies/jellyops:latest    # or :X.Y.Z
kubectl apply -k config/samples                           # example Jellyfin + jellycode JellyfinPlugin
```

Cutting a release: push to `production` for a rolling `:latest`, or push a `vX.Y.Z` tag
for a pinned, immutable version.

> If the GHCR package is private, add an `imagePullSecret` for `ghcr.io` to the operator's
> namespace (or make the package public in the repo's package settings).

### Build locally

```sh
make docker-build docker-push IMG=<registry>/jellyops:tag
```

## Project layout

```
api/v1alpha1/        # Jellyfin & JellyfinPlugin types (+ generated deepcopy)
internal/plugins/    # pure pod-template / image-volume / workload builder
internal/jellyfinapi/# hand-rolled Jellyfin HTTP client (bootstrap + libraries)
internal/controller/ # the three reconcilers
config/              # CRDs, RBAC, manager, samples (Kustomize)
cmd/main.go          # manager entrypoint, leader election
```

## License

Apache 2.0.
