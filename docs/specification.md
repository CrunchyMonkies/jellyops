# JellyOps — Jellyfin Kubernetes Operator Specification

> Status: Draft `v0.1` · API version: `jellyfin.jellyops.io/v1alpha1` · Target audience: platform/operations engineers and contributors.

---

## 1. Overview & Goals

**JellyOps** is a Go-based Kubernetes operator that declaratively manages the full lifecycle of [Jellyfin](https://jellyfin.org) media-server instances and their plugins. Plugins are delivered **as OCI container images** and mounted on demand using Kubernetes [image volumes](https://kubernetes.io/docs/tasks/configure-pod-container/image-volumes/), so a plugin is version-pinned, immutable, and enabled/disabled by editing a custom resource rather than copying files into a pod.

The operator ships **no plugin-specific logic**: it knows how to deliver *any* plugin image and reconcile whatever workloads and services that plugin declares. A running instance with **no plugins installed** is fully supported; a plugin is added or removed purely by creating or deleting a `JellyfinPlugin` custom resource.

**jellycode** (`../jellycode/`) is a **separately developed, fully optional reference plugin** — a distributed-transcoding plugin that offloads `ffmpeg` work from the Jellyfin server to a pool of remote worker pods over gRPC. The operator treats it exactly like any third-party plugin. It serves as the proving ground for the operator's most important design requirement — that a plugin is frequently **more than files**, also shipping companion workloads (the transcode workers) and networking (a gRPC Service) — but nothing about jellycode is baked into the operator.

### 1.1 Goals

- **Declarative Jellyfin instances** — one `Jellyfin` custom resource describes an entire instance: workload, storage (config/cache/media), Service, optional Ingress/TLS, resources, and hardware acceleration.
- **On-demand, image-based plugins** — a `JellyfinPlugin` custom resource references an OCI image; the operator mounts it into the target instance as an image volume. No registry curation in the cluster, no manual file copies.
- **Plugins that carry workloads** — a plugin may declare companion Deployments and Services (e.g. jellycode's worker pool), which the operator reconciles alongside the file injection.
- **Safe rollout semantics** — enabling, disabling, or upgrading a plugin is a controlled pod-template change with status conditions and events.
- **GitOps-friendly** — all state lives in CRs; no imperative steps; resources carry owner references and finalizers for clean teardown.

### 1.2 Non-goals

- Not a fork of Jellyfin, and not a build system for it. The operator consumes prebuilt Jellyfin and plugin images.
- **No true runtime hot-reload.** Jellyfin loads plugins at startup; enabling/disabling a plugin requires a pod restart. The operator makes this declarative and controlled, not instantaneous.
- Not a general media-server PaaS, multi-tenant billing system, or content manager.
- **Not coupled to any specific plugin.** The operator ships no plugin-specific logic; plugins (including jellycode) are developed, versioned, and distributed independently, and are enabled or removed purely by creating or deleting a `JellyfinPlugin` CR.

### 1.3 Glossary

| Term | Meaning |
|------|---------|
| **Instance** | A running Jellyfin deployment, described by a `Jellyfin` CR. |
| **Plugin image** | An OCI image whose filesystem contains a Jellyfin plugin directory (DLLs + `meta.json`). |
| **Image volume** | A Kubernetes volume sourced from an OCI image (`volumes[].image`), mounted read-only. |
| **Companion workload** | A Deployment/Service a plugin needs in addition to its files (e.g. jellycode workers). |
| **ABI** | The Jellyfin plugin Application Binary Interface (`targetAbi` in `meta.json`). A plugin only loads against a compatible server. |
| **`meta.json`** | The Jellyfin plugin manifest (guid, name, version, `targetAbi`, `framework`, `status`). |

---

## 2. Personas & User Stories

- **As a platform engineer**, I create one `Jellyfin` CR and get a fully wired instance (workload + PVCs + Service + Ingress) without writing raw manifests.
- **As a platform engineer**, I enable distributed transcoding by installing the optional jellycode plugin — one `JellyfinPlugin` CR pointing at its image; the operator injects the plugin files **and** spins up a pool of worker pods that dial the plugin's gRPC endpoint. (jellycode is one third-party plugin among many; the operator has no special knowledge of it.)
- **As a platform engineer**, I scale transcode throughput by bumping `spec.workloads[].replicas` on the `JellyfinPlugin`.
- **As a platform engineer**, I upgrade a plugin by changing its image tag/digest; the operator performs a controlled rollout and reports the new loaded version in status.
- **As a platform engineer**, I disable a plugin by deleting its CR; the operator removes the image volume on the next rollout and tears down the companion workers (draining them first).

---

## 3. Architecture

### 3.1 Component diagram

```
                         ┌──────────────────────────────────────────┐
                         │        jellyops controller-manager        │
                         │  ┌────────────────┐  ┌─────────────────┐  │
   kubectl apply ─────▶  │  │ JellyfinRecon. │  │ JellyfinPlugin  │  │
   (CRs)                 │  │                │  │ Reconciler      │  │
                         │  └───────┬────────┘  └────────┬────────┘  │
                         └──────────┼────────────────────┼──────────┘
                                    │ owns/reconciles     │
            ┌───────────────────────┼─────────────────────┼───────────────────────┐
            ▼                       ▼                     ▼                         ▼
   ┌─────────────────┐   ┌────────────────────┐   ┌──────────────┐        ┌─────────────────┐
   │ Jellyfin Deploy │   │ Service (8096 HTTP) │   │ PVCs:        │        │ plugin workload │
   │  ┌───────────┐  │   │ Service :9090 (plug)│   │ config/cache │        │ Deployment      │
   │  │ init: copy│  │   └────────────────────┘   │ media (RWX)  │        │  (N replicas)   │
   │  │ plugins   │  │            ▲                └──────────────┘        │  e.g. jellycode │
   │  └─────┬─────┘  │            │                                        └────────┬────────┘
   │  image volumes  │            │  gRPC bidi stream (h2c :9090)                   │
   │  (1 per plugin) │            └─────────────────────────────────────────────────┘
   │  jellyfin :8096 │              workers DIAL the server's plugin listener
   │  plugin listener│
   └─────────────────┘
```

Components on the right — the `:9090` gRPC Service, the companion-workload Deployment, and the plugin listener/stream — are **plugin-provided and optional**. They exist only when an installed plugin declares them (`spec.services` / `spec.workloads`); the jellycode worker is shown as the example. An instance with no plugins has only the Jellyfin Deployment, its `:8096` Service, and PVCs.

### 3.2 Reconciliation & ownership

- The operator runs as a single `controller-manager` Deployment with leader election.
- Every resource the operator creates (Deployments, Services, PVCs, Ingress) carries an **owner reference** to the originating CR, so garbage collection is automatic on CR deletion.
- **Finalizers** guard ordered teardown where needed — a plugin's companion workloads are gracefully terminated (scaled down, honoring their `preStop` hooks and `terminationGracePeriodSeconds`) before their Deployment and Service are deleted. The drain *behavior* lives in the workload, not the operator: the operator never speaks a plugin's protocol. jellycode's worker, for example, drains on `SIGTERM` via its own gRPC `Drain`/`JobControl STOP` contract.
- Reconcilers are **level-triggered and idempotent**: they compute the desired state from the CR(s) and converge actual state to it; partial failures requeue with backoff.

### 3.3 Plugin lifecycle state machine

```
   (CR created)
        │
        ▼
   ┌─────────┐  ABI ok &      ┌────────────┐  files mounted   ┌────────────────┐  workloads ready  ┌────────┐
   │ Pending │ ─ image valid ▶│ Injecting  │ ───────────────▶ │ WorkloadsReady │ ────────────────▶ │ Loaded │
   └────┬────┘                └─────┬──────┘                  └───────┬────────┘                   └───┬────┘
        │ ABI mismatch /            │ rollout / pull error            │ workers crashloop               │ CR deleted
        ▼ bad image                 ▼                                 ▼                                 ▼
   ┌────────┐                  ┌────────┐                        ┌────────┐                        ┌──────────┐
   │ Failed │                  │ Failed │                        │ Failed │                        │ Disabled │
   └────────┘                  └────────┘                        └────────┘                        └──────────┘
```

When a plugin declares `spec.install` (§6.4), an **`Installing`** phase sits between `Injecting` and `WorkloadsReady`: the pre-start install script runs (as an init container) before the Jellyfin container boots. A script failure sets the `Installed` condition and, under the default `failurePolicy: Ignore`, does not block the transition; under `Fail` it moves the plugin to `Failed`.

Each transition is surfaced as a `status.condition` and a Kubernetes `Event` on the `JellyfinPlugin`.

### 3.4 Networking

- **Jellyfin HTTP** — port `8096`, exposed via a Service and optional Ingress. This is the only port the operator itself requires.
- **Plugin-provided services** — a plugin may declare `spec.services`; the operator creates the corresponding `ClusterIP` Service(s). Ports and protocols belong to the plugin, not the operator. *Example:* the jellycode plugin hosts an embedded h2c gRPC listener on `:9090` and declares a Service so its worker pods can resolve and dial it.
- **Worker → server direction** *(jellycode example)* — jellycode's workers are clients; each opens one long-lived bidirectional gRPC stream to the server's `:9090` Service. No inbound port is needed on workers. Other plugins may use entirely different topologies.
- **Operator → instance API** — the `JellyfinAPIReconciler` (§7.6) reaches Jellyfin on `:8096` through the in-cluster Service (never the Ingress) to reconcile libraries.
- **NetworkPolicy** — the spec recommends restricting plugin service ports (e.g. jellycode's `:9090`) to their consumer pods (label selector) and `:8096` to the ingress controller **and the operator** (so API reconciliation still works). See §9.

---

## 4. Custom Resource Definitions

CRD group: `jellyfin.jellyops.io`, version `v1alpha1`. Go types are sketched in kubebuilder style; non-essential fields are elided with `// ...`.

### 4.1 `Jellyfin` (namespaced)

Describes a full instance.

```go
type JellyfinSpec struct {
    // Container image for the Jellyfin server. Defaults to the official/stable Jellyfin image.
    // Override only when a plugin requires an ABI-incompatible server build (see §4.4).
    Image     string `json:"image,omitempty"`
    Replicas  *int32 `json:"replicas,omitempty"` // typically 1 (Jellyfin is not active-active)

    Storage   JellyfinStorage `json:"storage,omitempty"`
    Service   ServiceSpec     `json:"service,omitempty"`
    Ingress   *IngressSpec    `json:"ingress,omitempty"`
    Resources corev1.ResourceRequirements `json:"resources,omitempty"`

    HardwareAcceleration *HardwareAccel `json:"hardwareAcceleration,omitempty"`

    // PluginSelector binds JellyfinPlugin resources to this instance by label.
    // A plugin may also target an instance directly via its jellyfinRef.
    PluginSelector *metav1.LabelSelector `json:"pluginSelector,omitempty"`

    Env          []corev1.EnvVar       `json:"env,omitempty"`
    PodTemplate  *PodTemplateOverrides `json:"podTemplate,omitempty"` // node selector, tolerations, securityContext...
    UpdateStrategy appsv1.DeploymentStrategy `json:"updateStrategy,omitempty"`

    // API enables day-2 reconciliation of in-app state (libraries) via Jellyfin's HTTP API (§7.6).
    API *JellyfinAPISpec `json:"api,omitempty"`
}

// JellyfinAPISpec configures how the operator authenticates to, and reconciles state inside,
// the running Jellyfin instance over its HTTP API on :8096.
type JellyfinAPISpec struct {
    // Mode selects how the operator obtains an admin API key:
    //   "provided"  -> read an existing key (or username/password) from CredentialsSecret
    //   "bootstrap" -> operator completes Jellyfin's first-run wizard, creates an admin user,
    //                  mints an API key, and stores it in a Secret it owns (GeneratedSecretName)
    Mode string `json:"mode,omitempty"`

    CredentialsSecret  *corev1.LocalObjectReference `json:"credentialsSecret,omitempty"` // keys: apiKey | username+password
    GeneratedSecretName string                      `json:"generatedSecretName,omitempty"` // bootstrap output (operator-owned)

    // Library reconciliation policy.
    ManageLibraries        bool `json:"manageLibraries,omitempty"`        // create/update libraries from storage.media[].library
    Prune                  bool `json:"prune,omitempty"`                  // remove operator-managed libraries no longer in spec
    RefreshLibraryOnChange bool `json:"refreshLibraryOnChange,omitempty"` // trigger a library scan after changes
}

type JellyfinStorage struct {
    Config PVCSpec  `json:"config,omitempty"`           // RWO; Jellyfin config + plugin staging dir
    Cache  *PVCSpec `json:"cache,omitempty"`            // RWO; transcode scratch / cache
    Media  []MediaFolder `json:"media,omitempty"`       // library folders; NFS or PVC backed (see §4.5)
}

type PVCSpec struct {
    ExistingClaim string                 `json:"existingClaim,omitempty"`
    Size          resource.Quantity      `json:"size,omitempty"`
    StorageClass  *string                `json:"storageClassName,omitempty"`
    AccessModes   []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`
    MountPath     string                 `json:"mountPath,omitempty"`
}

// MediaFolder is one library folder mounted into the Jellyfin pod (and, for shared-media
// plugins like jellycode, into the worker pods at the same path — see §8.2). Exactly one
// source (nfs | pvc | existingClaim) must be set.
type MediaFolder struct {
    Name      string `json:"name"`                 // library/folder identifier, e.g. "movies"
    MountPath string `json:"mountPath"`            // path inside the pod, e.g. /media/movies
    ReadOnly  bool   `json:"readOnly,omitempty"`   // mount the library read-only (recommended for media)

    NFS           *NFSSource `json:"nfs,omitempty"`           // mount an NFS export
    PVC           *PVCSpec   `json:"pvc,omitempty"`           // provision/mount a PVC
    ExistingClaim string     `json:"existingClaim,omitempty"` // reuse an existing PVC by name

    // Library, if set (and spec.api.manageLibraries is true), registers this folder as a
    // Jellyfin library (virtual folder) pointing at MountPath via the API reconciler (§7.6).
    Library *LibrarySpec `json:"library,omitempty"`
}

// LibrarySpec maps a media folder to a Jellyfin "virtual folder" (library).
type LibrarySpec struct {
    // CollectionType: movies | tvshows | music | musicvideos | homevideos | books | mixed.
    CollectionType string `json:"collectionType"`
    DisplayName    string `json:"displayName,omitempty"` // shown in Jellyfin; defaults to MediaFolder.Name
    // Options passes through Jellyfin LibraryOptions (metadata providers, scanners, real-time
    // monitoring, etc.). Opaque to the operator; applied verbatim on create/update.
    Options *runtime.RawExtension `json:"options,omitempty"`
}

// NFSSource targets an NFS export to be added as a Jellyfin library folder.
type NFSSource struct {
    Server   string `json:"server"`               // NFS server host or IP
    Path     string `json:"path"`                 // exported path on the server, e.g. /export/movies
    ReadOnly bool   `json:"readOnly,omitempty"`    // export-level read-only (NFS hard read-only)

    // Provision controls how the NFS export is wired in:
    //   false (default) -> inline corev1.NFSVolumeSource on the pod (simplest)
    //   true            -> operator creates a PV + PVC (ReadWriteMany) backed by this export,
    //                      so the SAME claim can be shared with companion workloads (§8.2)
    Provision     bool   `json:"provision,omitempty"`
    StorageClass  string `json:"storageClassName,omitempty"` // used when Provision=true
    MountOptions  []string `json:"mountOptions,omitempty"`   // e.g. ["nfsvers=4.1","hard","timeo=600"]
}

type HardwareAccel struct {
    Type        string `json:"type"`                  // "vaapi" | "nvidia" | "qsv" | "none"
    DevicePath  string `json:"devicePath,omitempty"`  // e.g. /dev/dri/renderD128 for VAAPI
    RenderGroup *int64 `json:"renderGroupGID,omitempty"`
    // For NVIDIA: requests nvidia.com/gpu via Resources + runtimeClassName.
}

type JellyfinStatus struct {
    ObservedGeneration int64              `json:"observedGeneration,omitempty"`
    Phase              string             `json:"phase,omitempty"` // Pending|Ready|Degraded
    Conditions         []metav1.Condition `json:"conditions,omitempty"`
    LoadedPlugins      []LoadedPlugin     `json:"loadedPlugins,omitempty"`
    Endpoints          InstanceEndpoints  `json:"endpoints,omitempty"`

    // API reconciliation (§7.6).
    ManagedLibraries     []string `json:"managedLibraries,omitempty"`     // libraries the operator owns
    APICredentialsSecret string   `json:"apiCredentialsSecret,omitempty"` // resolved/generated key Secret
}
```

Example:

```yaml
apiVersion: jellyfin.jellyops.io/v1alpha1
kind: Jellyfin
metadata:
  name: home-media
  namespace: media
spec:
  image: ghcr.io/example/jellyfin:12.0.0-net10   # jellycode requires a v12/.NET 10 server; stock Jellyfin is the operator default — override only for ABI-incompatible plugins (see §4.4)
  replicas: 1
  storage:
    config: { size: 5Gi, accessModes: [ReadWriteOnce] }
    cache:  { size: 50Gi, accessModes: [ReadWriteOnce] }
    media:
      - name: movies
        mountPath: /media/movies
        readOnly: true
        nfs: { server: 10.0.0.10, path: /export/movies, mountOptions: ["nfsvers=4.1", "hard"] }
        library: { collectionType: movies, displayName: "Movies" }
      - name: tv
        mountPath: /media/tv
        readOnly: true
        nfs:
          server: 10.0.0.10
          path: /export/tv
          provision: true          # operator creates a shared RWX PV+PVC so workers reuse it (§8.2)
          storageClassName: nfs-media
        library: { collectionType: tvshows, displayName: "TV Shows" }
      - name: home-videos
        mountPath: /media/home-videos
        existingClaim: home-videos-pvc
        # no library: block -> mounted on disk but NOT registered as a Jellyfin library
  api:
    mode: bootstrap                 # operator runs first-run wizard, mints an API key
    generatedSecretName: home-media-api
    manageLibraries: true
    prune: true                     # operator-managed libraries removed from spec are deleted
    refreshLibraryOnChange: true
  service:
    type: ClusterIP
  ingress:
    className: nginx
    host: jellyfin.example.com
    tls: { secretName: jellyfin-tls }
  hardwareAcceleration:
    type: vaapi
    devicePath: /dev/dri/renderD128
    renderGroupGID: 44
  pluginSelector:
    matchLabels: { jellyfin.jellyops.io/instance: home-media }
```

### 4.2 `JellyfinPlugin` (namespaced)

Describes a plugin: its image (files) **and** any companion workloads/services.

```go
type JellyfinPluginSpec struct {
    // Binding: target a specific instance by name, or rely on the instance's pluginSelector
    // matching this object's labels.
    JellyfinRef *corev1.LocalObjectReference `json:"jellyfinRef,omitempty"`

    // The OCI image whose filesystem contains the plugin directory.
    PluginImage ImageSource `json:"pluginImage"`

    // Plugin manifest facts. Used to derive the on-disk folder name and to validate ABI.
    // If omitted, the operator reads them from image labels / the embedded meta.json.
    Meta PluginMeta `json:"meta,omitempty"`

    // How plugin files reach the Jellyfin plugins dir.
    //   "imageVolume"     -> mount the image read-only at /config/plugins/<Name_version> (default)
    //   "imageVolumeCopy" -> mount the image read-only AND copy it into the writable plugins dir
    //                        via an init container (mitigates Jellyfin writing meta.json status; §6.2)
    Injection string `json:"injection,omitempty"`

    // Companion workloads (e.g. jellycode transcode workers) the operator creates and owns.
    Workloads []PluginWorkload `json:"workloads,omitempty"`

    // Services the operator creates for the instance/workloads (e.g. expose plugin gRPC :9090).
    Services []PluginService `json:"services,omitempty"`

    // Optional seed for the plugin's Jellyfin XML configuration (declarative).
    Config *runtime.RawExtension `json:"config,omitempty"`

    // Optional setup step run as an init container BEFORE the Jellyfin container
    // starts (after any imageVolumeCopy staging). The imperative complement to
    // Config: use it to run migrations, fetch assets, generate files, or otherwise
    // prepare the writable /config dir before the app boots. See §6.4.
    Install *PluginInstall `json:"install,omitempty"`
}

type ImageSource struct {
    Reference  string            `json:"reference"`            // e.g. ghcr.io/example/jellycode-plugin@sha256:...
    PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"` // Always | IfNotPresent | Never
    PullSecret *corev1.LocalObjectReference `json:"pullSecret,omitempty"`
    SubPath    string            `json:"subPath,omitempty"`    // dir inside the image holding plugin files
}

type PluginMeta struct {
    GUID      string `json:"guid,omitempty"`
    Name      string `json:"name,omitempty"`
    Version   string `json:"version,omitempty"`
    TargetAbi string `json:"targetAbi,omitempty"`
}

type PluginWorkload struct {
    Name        string                       `json:"name"`
    Image       ImageSource                  `json:"image"`
    Replicas    *int32                       `json:"replicas,omitempty"`
    Args        []string                     `json:"args,omitempty"`
    Env         []corev1.EnvVar              `json:"env,omitempty"`
    Ports       []corev1.ContainerPort       `json:"ports,omitempty"`
    Resources   corev1.ResourceRequirements  `json:"resources,omitempty"`
    VolumeMounts []corev1.VolumeMount        `json:"volumeMounts,omitempty"` // e.g. shared media/scratch
    Autoscaling *WorkloadAutoscaling         `json:"autoscaling,omitempty"`  // optional HPA (Phase 2)
}

// PluginInstall describes a setup script the operator runs as an init container,
// ordered before the Jellyfin container starts (and after imageVolumeCopy staging).
// See §6.4 for the full mechanics.
type PluginInstall struct {
    // Inline script run with `sh -c`. Mutually exclusive with Command.
    Script  string   `json:"script,omitempty"`
    // Explicit entrypoint/args (e.g. a script baked into an image). Mutually
    // exclusive with Script.
    Command []string `json:"command,omitempty"`
    Args    []string `json:"args,omitempty"`

    // Image that runs the script. Defaults to the Jellyfin instance image so the
    // script sees the exact runtime env/paths/filesystem layout Jellyfin will use;
    // override to run in the plugin image (spec.pluginImage) or a minimal runner.
    Image *ImageSource `json:"image,omitempty"`

    Env       []corev1.EnvVar             `json:"env,omitempty"`
    // Extra mounts. The writable /config PVC and the staged plugin dir are mounted
    // by default (a read-only image volume cannot be written to; see §6.2).
    VolumeMounts []corev1.VolumeMount     `json:"volumeMounts,omitempty"`
    Resources corev1.ResourceRequirements `json:"resources,omitempty"`

    // "Ignore" (default) logs a Warning event and lets Jellyfin start anyway,
    // preserving the instance's fail-open bias (§7.5). "Fail" blocks the pod on a
    // non-zero exit (fail-closed for the plugin) when a broken install must halt
    // startup.
    FailurePolicy string `json:"failurePolicy,omitempty"` // Ignore | Fail

    // Bounded runtime before the operator treats the script as failed.
    TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`

    // Run once per plugin version: record a marker under /config keyed on
    // <Name>_<version> and skip the script on later pod restarts, so repeated
    // restarts don't re-run a non-idempotent script.
    RunOnce bool `json:"runOnce,omitempty"`
}

type JellyfinPluginStatus struct {
    Phase               string             `json:"phase,omitempty"` // Pending|Injecting|Installing|WorkloadsReady|Loaded|Failed|Disabled
    Injected            bool               `json:"injected,omitempty"`
    Installed           *bool              `json:"installed,omitempty"` // nil if no install step declared
    ABICompatible       *bool              `json:"abiCompatible,omitempty"`
    WorkloadReadyReplicas map[string]int32 `json:"workloadReadyReplicas,omitempty"`
    Conditions          []metav1.Condition `json:"conditions,omitempty"`
}
```

Example — enabling jellycode end-to-end:

```yaml
apiVersion: jellyfin.jellyops.io/v1alpha1
kind: JellyfinPlugin
metadata:
  name: jellycode-distributed-transcoding
  namespace: media
  labels:
    jellyfin.jellyops.io/instance: home-media
spec:
  jellyfinRef: { name: home-media }
  pluginImage:
    reference: ghcr.io/example/jellycode-plugin@sha256:<digest>
    pullPolicy: IfNotPresent
  meta:
    guid: b9f8c1a2-3d4e-4f5a-9b6c-7d8e9f0a1b2c
    name: "Distributed Transcoding"
    version: "0.0.1.0"
    targetAbi: "12.0.0.0"
  injection: imageVolumeCopy        # Jellyfin writes meta.json status; see §6.2
  install:                          # pre-start setup script; see §6.4
    # runs in the Jellyfin image by default; /config is writable
    script: |
      mkdir -p /config/plugins/configurations
      cp -n /config/plugins/"Distributed Transcoding_0.0.1.0"/defaults/*.xml \
        /config/plugins/configurations/
    runOnce: true                   # skip on later restarts via a /config marker
    # failurePolicy: Ignore is the default (Jellyfin still boots on error)
  services:
    - name: jellycode-grpc
      selector: instance            # points at the Jellyfin pod
      ports: [{ name: grpc, port: 9090, targetPort: 9090 }]
  workloads:
    - name: worker
      image:
        reference: ghcr.io/example/jellycode-worker@sha256:<digest>
      replicas: 3
      args:
        - "--server=http://jellycode-grpc.media.svc:9090"
        - "--ffmpeg=/usr/bin/ffmpeg"
        - "--max-concurrent=2"
        - "--scratch=/scratch"
        # --worker-id defaults to <hostname>-<pid> if omitted
      resources:
        requests: { cpu: "2", memory: 2Gi }
      volumeMounts:
        - { name: media, mountPath: /media }   # identity path mapping (Phase 0); see §8
```

### 4.3 `ClusterJellyfinPluginType` (cluster-scoped, optional / Phase 2)

A reusable, admin-curated catalog entry (default image, ABI range, default workloads) that a namespaced `JellyfinPlugin` can reference by name to avoid repeating boilerplate. Out of scope for Phase 1; reserved here so the API can grow into it.

### 4.4 ABI compatibility & plugin base-image prerequisites

The operator enforces ABI compatibility generically, for **every** plugin, and never privileges any particular server build:

- **Default server image is stock/official Jellyfin.** `Jellyfin.spec.image` is optional and defaults to the stable upstream image. The operator has no plugin-specific default.
- **Generic ABI gate.** For each bound plugin, the operator validates `JellyfinPlugin.meta.targetAbi` against the instance's running server version and sets an `ABICompatible` condition. On mismatch it transitions **the plugin** to `Failed` and emits an event — it never silently mounts an unloadable plugin and never blocks or fails the instance itself.
- **Plugins carry their own base-image prerequisites.** A plugin that needs a non-default server ABI documents that requirement; the user opts in by overriding `spec.image`.

*Example (jellycode prerequisite):* jellycode's `meta.json` declares `targetAbi: 12.0.0.0` and `framework: net10.0`, which stock Jellyfin 10.x is ABI-incompatible with and will refuse to load. A user enabling jellycode must therefore set `spec.image` to a compatible **custom Jellyfin v12 / .NET 10 build**. That is jellycode's prerequisite — documented and shipped with jellycode — not an operator default. Sourcing and maintaining such an image is out of scope for the operator.

### 4.5 NFS media folders

`storage.media[]` accepts NFS exports directly, so library folders can be added without pre-creating PVCs. Each `MediaFolder` resolves to a volume mounted at its `mountPath` inside the Jellyfin pod. Mounting is always performed; **registering the folder as a Jellyfin library is opt-in** via the folder's `library:` block plus `spec.api.manageLibraries: true`, at which point the API reconciler (§7.6) creates/updates the corresponding virtual folder. A folder with no `library:` block is mounted but left for the user to configure in-app.

Two NFS wiring modes, selected by `nfs.provision`:

- **Inline (default, `provision: false`)** — the operator adds a `corev1.NFSVolumeSource` (`server` + `path`) volume to the pod and mounts it read-only by default. Simplest path; no PV/PVC objects to manage. Best when only the Jellyfin pod needs the folder.
- **Provisioned (`provision: true`)** — the operator creates a **`ReadWriteMany` PV + PVC** backed by the export. This is the recommended mode when a companion plugin needs the **same media at the same path** — notably jellycode's workers (§8.2), which assume identity path mapping. The generated claim is shared into the worker pods so server and workers see byte-identical paths.

Guidance:

- **Read-only by default** — media libraries are mounted `readOnly: true` unless the user opts out; this protects source media from accidental writes and is the safe default for shared NFS.
- **Mount options** — `nfs.mountOptions` (e.g. `nfsvers=4.1`, `hard`, `timeo`) are passed through (inline via the pod, or as PV `mountOptions` when provisioned).
- **Validation** — the operator requires exactly one source per `MediaFolder` (`nfs` | `pvc` | `existingClaim`) and rejects ambiguous entries with a `Warning` event and a `StorageInvalid` condition.

---

## 5. Plugin Packaging Contract

A plugin image is an ordinary OCI image (or OCI artifact) whose filesystem contains a single Jellyfin plugin directory.

### 5.1 Image layout

- Plugin files (`*.dll`, `meta.json`, and third-party dependencies) live at the **image root** or at a documented `subPath` (recommended: `/plugin`).
- For jellycode this is exactly the output already produced by its build: only the plugin + gRPC/Protobuf assemblies, with host assemblies stripped (the project's `StripHostAssemblies` MSBuild target). The same files the existing `docker/Dockerfile` copies into `"/config/plugins/Distributed Transcoding_0.0.1.0/"` go into the plugin image instead.
- The on-disk plugin folder name is `"<Name>_<version>"` (e.g. `Distributed Transcoding_0.0.1.0`), derived from `meta` or read from the embedded `meta.json`.

### 5.2 Self-description via image labels

To let the operator validate without unpacking, the plugin image SHOULD carry labels mirroring `meta.json`:

| Label | Example |
|-------|---------|
| `io.jellyops.plugin.guid` | `b9f8c1a2-3d4e-4f5a-9b6c-7d8e9f0a1b2c` |
| `io.jellyops.plugin.name` | `Distributed Transcoding` |
| `io.jellyops.plugin.version` | `0.0.1.0` |
| `io.jellyops.plugin.targetAbi` | `12.0.0.0` |
| `io.jellyops.plugin.subPath` | `/plugin` |

If labels are absent, the operator falls back to reading `meta.json` from the mounted image volume during the `Injecting` phase. CR-provided `spec.meta` always takes precedence.

### 5.3 Companion images

A plugin that needs workloads ships additional images referenced from `spec.workloads[].image` — for jellycode, the **worker** image (the published `Worker` console app + `ffmpeg`). These are plain runnable images; only the *plugin* image is consumed as a read-only image volume.

---

## 6. Image Volume Mechanics (core delivery mechanism)

Kubernetes [image volumes](https://kubernetes.io/docs/tasks/configure-pod-container/image-volumes/) let a pod mount an OCI image's filesystem as a **read-only** volume. Stability:

- **Alpha:** Kubernetes 1.31 · **Beta:** 1.33 (adds `subPath`/`subPathExpr`, on by default) · **GA:** 1.36.
- The **container runtime must support image volumes** (recent containerd / CRI-O). This is a hard cluster prerequisite (see §14).

### 6.1 Pod-spec shape

For each enabled plugin the operator adds one image volume and a corresponding mount under the Jellyfin plugins directory:

```yaml
spec:
  containers:
    - name: jellyfin
      volumeMounts:
        - name: plugin-jellycode
          mountPath: "/config/plugins/Distributed Transcoding_0.0.1.0"
          subPath: plugin           # if files live under /plugin in the image
          readOnly: true
  volumes:
    - name: plugin-jellycode
      image:
        reference: ghcr.io/example/jellycode-plugin@sha256:<digest>
        pullPolicy: IfNotPresent
```

- **One image per volume.** Multiple plugins ⇒ multiple image volumes, each mounted at its own `/config/plugins/<Name_version>` path. The parent `/config` (config PVC) remains writable; read-only plugin mounts sit beneath it.
- **Digest-pinning recommended** — reference plugins by `@sha256:...` for immutability and reproducible rollouts.

### 6.2 Read-only caveat and mitigation

Image volumes are **read-only**, but Jellyfin updates plugin **status back into `meta.json`** when it loads/enables a plugin. A pure read-only mount can therefore produce load warnings or prevent status persistence.

The operator offers two injection modes via `JellyfinPlugin.spec.injection`:

- **`imageVolume`** (default, simplest) — mount the image directly read-only at the plugin path. Best when the target Jellyfin tolerates a read-only plugin dir. Lowest overhead; fully immutable.
- **`imageVolumeCopy`** (default recommendation for jellycode) — mount the image read-only at a staging path (e.g. `/plugins-src/<name>`) and run an **init container** that copies it into the writable config plugins dir before Jellyfin starts:

  ```yaml
  initContainers:
    - name: stage-jellycode
      image: busybox:stable
      command: ["sh", "-c",
        "mkdir -p \"/config/plugins/Distributed Transcoding_0.0.1.0\" &&
         cp -a /plugins-src/jellycode/. \"/config/plugins/Distributed Transcoding_0.0.1.0/\""]
      volumeMounts:
        - { name: plugin-jellycode, mountPath: /plugins-src/jellycode, readOnly: true }
        - { name: config, mountPath: /config }
  ```

  The image volume remains the **immutable, version-pinned source of truth**; the writable copy exists only so Jellyfin can mutate `meta.json` status at runtime. On plugin upgrade, the new digest changes the init container's source and the rollout re-stages.

### 6.3 Rollout & reload semantics

- Adding, removing, or upgrading a plugin changes the Jellyfin pod template (volumes/mounts/init containers), triggering a **controlled Deployment rollout**.
- Jellyfin only discovers plugins at startup, so a restart is inherent and expected — the operator does not attempt unsupported hot-reload. Status reflects `Injecting → Installing → Loaded` as the new pod becomes ready.

### 6.4 Install / pre-start hook

A plugin may declare `spec.install` (§4.2) to run a **setup script before the Jellyfin container starts**. This is the imperative complement to `spec.config` (a declarative XML seed): use it for work config-seeding can't express — running a migration, generating a key or file, fetching an extra asset, or preparing the writable `/config` tree.

- **Mechanism** — the operator renders the install step as a Kubernetes **init container**. Init containers run sequentially in declared order, so the ordering is:

  ```
  imageVolumeCopy staging  →  install script  →  jellyfin (main container)
  ```

  For an `imageVolume` (no-copy) plugin there is no staging container, so the install container simply runs before the main container. The install container mounts the writable `/config` PVC and the staged plugin dir by default (a read-only image volume cannot be written to; see §6.2); add more via `install.volumeMounts`.

- **Runner image** — defaults to the **Jellyfin instance image** so the script sees the exact runtime env, paths, and filesystem layout Jellyfin will use. Override with `install.image` to run in the plugin image (`spec.pluginImage`) or a minimal runner; pull policy / digest-pinning carries over from the chosen `ImageSource`.

- **Script vs. command** — supply either an inline `script` (run via `sh -c`) or an explicit `command`/`args` (e.g. a script baked into the image). The two are mutually exclusive; declaring both is rejected with a `Warning` event.

- **Failure semantics** — a raw init-container failure blocks the **whole pod**, which conflicts with the instance's fail-open bias (§7.5). `install.failurePolicy` governs this:
  - **`Ignore`** (default) — the operator wraps the command so a non-zero exit is swallowed, emits a `Warning` event, and Jellyfin still starts. The instance stays fail-open, but may load a half-installed plugin; the `Installed` condition (§7.4) reflects the failure.
  - **`Fail`** — the init container fails hard and the pod is blocked (plugin **and** instance down until fixed). Choose this when a broken install must halt startup.

- **Idempotency (`runOnce`)** — set `runOnce: true` to run the script only once per plugin version: the operator records a marker under `/config` keyed on `<Name>_<version>` and skips the script on later pod restarts, so a non-idempotent script isn't re-run every time the pod cycles. A plugin upgrade (new version) uses a new marker and runs again.

- **Timeout** — `install.timeoutSeconds` bounds the init container; on expiry it is treated as a failure and subject to `failurePolicy`.

Rendered pod spec (install after staging, before Jellyfin):

```yaml
initContainers:
  - name: stage-jellycode          # imageVolumeCopy (§6.2)
    image: busybox:stable
    # ... copies /plugins-src/jellycode → /config/plugins/...
  - name: install-jellycode        # spec.install
    image: <jellyfin-instance-image>
    command: ["sh", "-c",
      "test -f \"/config/.jellyops/installed/Distributed Transcoding_0.0.1.0\" && exit 0;
       mkdir -p /config/plugins/configurations &&
       cp -n \"/config/plugins/Distributed Transcoding_0.0.1.0/defaults/\"*.xml /config/plugins/configurations/;
       mkdir -p /config/.jellyops/installed &&
       touch \"/config/.jellyops/installed/Distributed Transcoding_0.0.1.0\""]
    volumeMounts:
      - { name: config, mountPath: /config }
containers:
  - name: jellyfin
    # ... starts only after both init containers succeed (or install is Ignored)
```

---

## 7. Reconciliation & Controller Behavior

### 7.1 Controllers

- **`JellyfinReconciler`** — owns the instance Deployment, PVCs, Service(s), and optional Ingress. It computes the pod template, including all image volumes/mounts and init containers contributed by bound `JellyfinPlugin`s.
- **`JellyfinPluginReconciler`** — validates the plugin (image reachable, ABI compatible), owns companion workloads/Services, and reports plugin status. It triggers a re-reconcile of the bound `Jellyfin` when the plugin set or its images change (watch + enqueue).
- **`JellyfinAPIReconciler`** — a day-2 loop that, once an instance is `Ready`, authenticates to Jellyfin's HTTP API and reconciles in-app state (libraries) against `storage.media[].library` (§7.6). Unlike the others it converges state held **inside Jellyfin**, not Kubernetes objects.

### 7.2 Desired-state derivation

1. Resolve the set of `JellyfinPlugin`s bound to an instance (via `jellyfinRef` or the instance's `pluginSelector`).
2. For each plugin, validate ABI and image; on failure mark the plugin `Failed` and **exclude** it from the pod template (a broken plugin never blocks the instance).
3. Build the Jellyfin pod template: base container + media/config/cache volumes + one image volume per healthy plugin + init containers for `imageVolumeCopy` plugins, followed by an install init container for each plugin declaring `spec.install` — ordered staging → install → main container (§6.4).
4. Create/update companion workloads and Services declared by each plugin.
5. Apply via server-side apply / controller-runtime; converge and requeue on drift.

### 7.3 Ownership, finalizers, teardown

- All generated objects carry owner references for GC.
- A finalizer on `JellyfinPlugin` ensures companion workloads are **gracefully terminated before deletion**: the operator scales them down and waits (bounded) for Kubernetes-native graceful shutdown — the workload's `preStop` hook and `terminationGracePeriodSeconds` — before deleting the Deployment and Service. The drain logic itself lives in the workload; the operator does not speak any plugin's protocol. *Example:* jellycode's worker handles `SIGTERM` by running its own gRPC `Drain` then `JobControl{STOP}` per active job, so in-flight transcodes aren't hard-killed.

### 7.4 Status conditions (Kubernetes conventions)

| Resource | Condition | Meaning |
|----------|-----------|---------|
| `Jellyfin` | `Ready` | Instance Deployment available and Service/Ingress provisioned. |
| `Jellyfin` | `PluginsLoaded` | All bound, healthy plugins are mounted in the running pod. |
| `JellyfinPlugin` | `ABICompatible` | `meta.targetAbi` matches the instance server version. |
| `JellyfinPlugin` | `Injected` | Plugin files present in the running pod. |
| `JellyfinPlugin` | `Installed` | `spec.install` script completed successfully (or was skipped via `runOnce`, or failed under `failurePolicy: Ignore` — reason distinguishes). Absent when no install step is declared. |
| `JellyfinPlugin` | `WorkersAvailable` | Declared companion workloads meet desired ready replicas. |

### 7.5 Errors, requeue, events

- Image pull failures, ABI mismatches, and missing PVCs surface as conditions + `Warning` events and requeue with exponential backoff.
- `spec.install` script failures (§6.4) surface a `Warning` event and set the `Installed` condition. Under the default `failurePolicy: Ignore` the pod still starts (instance stays serving); under `Fail` the init container blocks the pod and the reconcile requeues with backoff until the script succeeds or the spec is corrected.
- The operator never leaves the instance in a non-serving state because of a single bad plugin (fail-open for the instance, fail-closed for the plugin). The default `install.failurePolicy: Ignore` upholds this; `Fail` is an explicit opt-out for installs that must gate startup.

### 7.6 Jellyfin API reconciliation (libraries)

The reconcilers above converge **Kubernetes** objects. Libraries, however, are state held **inside Jellyfin's database**, reachable only through its HTTP API on `:8096`. The `JellyfinAPIReconciler` closes that gap: it makes `storage.media[].library` the desired state and converges Jellyfin's virtual folders to match. It is **gated on `spec.api`** — absent that block, the operator never touches the running app.

#### 7.6.1 Authentication & credential bootstrap

The reconciler needs an admin API key. `spec.api.mode` selects how it gets one:

- **`provided`** — the operator reads an existing key (or `username`/`password`) from `credentialsSecret`. Simplest when an instance already exists or keys are managed externally.
- **`bootstrap`** — for a fresh instance the operator drives Jellyfin's **first-run wizard** end to end:
  1. Wait for `:8096` readiness, then `GET /Startup/Configuration`.
  2. `POST /Startup/User` to create the initial admin (credentials generated or taken from `credentialsSecret`).
  3. `POST /Startup/Complete` to finish setup.
  4. Authenticate (`POST /Users/AuthenticateByName`) and mint a durable API key (`POST /Auth/Keys`).
  5. Persist `apiKey` (and the admin credentials) into an operator-owned Secret named `generatedSecretName`, so the bootstrap is **idempotent** and survives operator restarts (re-bootstrap is skipped if the Secret already holds a working key).

All API calls use the token header (`Authorization: MediaBrowser Token="<key>"`). The operator addresses the instance through its in-cluster Service, never the public Ingress.

#### 7.6.2 Library reconciliation loop

When `manageLibraries: true`, for each `MediaFolder` that has a `library:` block:

1. **List** existing virtual folders: `GET /Library/VirtualFolders`.
2. **Diff** desired vs. actual, keyed by library **name** (`displayName` ∨ `MediaFolder.Name`):
   - *Missing* → `POST /Library/VirtualFolders?name=…&collectionType=…&paths=<mountPath>` (+ `LibraryOptions` from `library.options`).
   - *Present, path/options drifted* → add/remove paths (`/Library/VirtualFolders/Paths`) and `POST /Library/VirtualFolders/LibraryOptions`.
   - *Present and matching* → no-op.
3. **Prune** (only when `spec.api.prune: true`) → delete operator-managed libraries that are no longer in spec. Ownership is tracked so hand-created libraries are never touched (see §7.6.3).
4. **Refresh** (when `refreshLibraryOnChange: true`) → trigger a scan (`POST /Library/Refresh`) after changes so new media is picked up.

The loop is **level-triggered and idempotent**: it re-lists and re-diffs every reconcile, so out-of-band edits in the Jellyfin UI are detected and (for managed libraries) corrected on the next pass.

#### 7.6.3 Ownership, drift & safety

- **Managed-set tracking** — the operator records which libraries it owns (by name) in `Jellyfin.status.managedLibraries`. Only those are eligible for update/prune; everything else in Jellyfin is left alone. This prevents the operator from deleting libraries a user created by hand.
- **Mount/library ordering** — a library is only registered after its `mountPath` exists in the running pod, so Jellyfin never points at a missing path. Changing a folder's storage source triggers a pod rollout (Kubernetes reconcile) first; the API reconcile runs once the new pod is `Ready`.
- **Failure isolation** — API failures (auth, transient 5xx) set an `APIReady=false` condition and requeue with backoff; they do **not** affect the instance's serving state or the plugin reconcilers.

#### 7.6.4 Status & conditions

| Resource | Condition | Meaning |
|----------|-----------|---------|
| `Jellyfin` | `APIReady` | Operator is authenticated to the instance API. |
| `Jellyfin` | `LibrariesReady` | All desired libraries exist with the correct paths/options. |

`Jellyfin.status` also gains `managedLibraries []string` (the owned set) and `apiCredentialsSecret` (the resolved/generated Secret name).

---

## 8. Companion Workloads

The operator reconciles arbitrary plugin-declared companion workloads and services **generically**, with no knowledge of what they do. A plugin lists `spec.workloads[]` (each becomes a Deployment) and `spec.services[]` (each becomes a Service); the operator owns them, wires owner references and finalizers, and surfaces their readiness via `WorkersAvailable`. Everything below uses the **jellycode plugin as the worked example** of that generic surface — none of it is operator built-in.

jellycode's worker is a standalone .NET console app that **dials** the plugin's gRPC server and runs `ffmpeg`; the operator models it as one `PluginWorkload` like any other.

### 8.1 Worker Deployment shape (jellycode example)

- **Command-line args** (verified against `src/Worker/Program.cs`):
  - `--server` — gRPC endpoint (default `http://127.0.0.1:9090`); set to the in-cluster Service, e.g. `http://jellycode-grpc.media.svc:9090`.
  - `--worker-id` — defaults to `<hostname>-<pid>`; leave unset to get a unique id per pod, or template from the pod name.
  - `--ffmpeg` — path to the `ffmpeg` binary in the worker image (default `ffmpeg`).
  - `--max-concurrent` — streams per pod (default `2`); a primary throughput/replica tuning knob.
  - `--scratch` — local scratch dir (default temp); back with an `emptyDir` or fast local volume.
- **Replicas / scaling** — `spec.workloads[].replicas`; optional HPA hooks in Phase 2 (CPU or a custom queue-depth metric derived from worker `Heartbeat.free_slots`).

### 8.2 Shared storage / path mapping (jellycode example)

jellycode Phase 0 uses **identity path mapping** (`PathMap` in `transcode.proto` is documented as identity for now): the server and workers must see media and the canonical transcode output paths at the **same absolute paths**. Implications the operator must surface:

- **Media** must be reachable at the **same absolute path** in both the Jellyfin pod and the worker pods. The cleanest way to guarantee this is an NFS media folder with `nfs.provision: true` (§4.5): the operator's generated **ReadWriteMany** PV/PVC is mounted at the identical `mountPath` on both sides. An existing RWX PVC works equally well.
- The canonical transcode output dir (`AssignJob.output_dir`) must likewise be reachable identically by both sides, or future phases must populate `PathMap` to translate. The spec recommends shared RWX media (NFS-provisioned or an RWX PVC) and documents identity path mapping as a Phase 0 constraint.

### 8.3 gRPC contract touchpoints (jellycode example)

The operator does **not** depend on this proto — it drives only Kubernetes-native graceful termination (§7.3). These are the internal touchpoints jellycode's own worker and plugin use to map that lifecycle onto their protocol, from `src/Contracts/Protos/transcode.proto`:

- **Lifecycle/scale-down** — jellycode's worker maps `SIGTERM` (sent by the operator's finalizer-driven graceful drain, §7.3) onto `ServerFrame.Drain` (stop accepting new jobs) and `JobControl{action: STOP}` per job.
- **Capacity signals** — worker `Register{max_concurrent, hwaccels, encoders}` and `Heartbeat{active_jobs, free_slots, cpu}` are the natural inputs for autoscaling and observability (§10).

### 8.4 Security posture (current vs. recommended)

- **Today:** the worker negotiates **cleartext h2c** (`Http2UnencryptedSupport`), and the plugin exposes `:9090` without TLS — a Phase 0 limitation.
- **Recommended:** confine `:9090` with a NetworkPolicy (workers-only), and treat in-cluster mTLS (service mesh or app-level) as the hardening path before exposing transcoding across trust boundaries.

---

## 9. Security & Hardening

- **Operator RBAC** — least privilege: full control only over its own CRDs and the specific built-in kinds it manages (Deployments, Services, PVCs, Ingresses, Events) in watched namespaces; no cluster-admin.
- **Pod security** — run Jellyfin and workers as non-root where the images allow, drop capabilities, set `readOnlyRootFilesystem` where practical (writable paths via mounts), apply a `RuntimeDefault` seccomp profile. HW acceleration (device mounts / `nvidia.com/gpu`) is the documented exception.
- **Image pull & supply chain** — support `imagePullSecrets`; **recommend digest-pinned** plugin/worker references; reserve cosign/signature verification of plugin images as a Phase 2 admission-time gate.
- **Plugin trust model** — only cluster admins create `JellyfinPlugin`/catalog resources; plugins are namespace-scoped; ABI gating prevents loading incompatible code. Image volumes are read-only, reducing tamper surface for the file payload.
- **API credentials (§7.6)** — admin keys live only in Kubernetes Secrets (user-supplied for `provided` mode, operator-generated for `bootstrap`). The operator reaches Jellyfin via the in-cluster Service, never the public Ingress; the token is never logged or written to status (status holds only the Secret name). The operator's RBAC for these Secrets is scoped to the watched namespaces. Bootstrap admin credentials should be rotated per the user's policy; the operator reads, never hard-codes, them.
- **Network** — restrict any plugin-provided service ports (e.g. jellycode's `:9090`) to their consumer pods and `:8096` to the ingress controller via NetworkPolicy.

---

## 10. Observability

- **Operator metrics** — controller-runtime's Prometheus metrics (reconcile counts, latency, errors) plus custom gauges: plugins per phase, ABI-incompatible count, worker ready replicas.
- **Conditions & events** — every state transition (§3.3) emits an Event and updates `status.conditions`, making `kubectl describe` the primary debugging surface.
- **Plugin/worker health** — surface worker `Heartbeat` aggregates (active jobs, free slots, cpu) where available; recommend a Grafana dashboard for transcode capacity and a `WorkersAvailable` alert.

---

## 11. Tech Stack & Project Layout

- **Language/framework** — Go with **kubebuilder** / `controller-runtime`.
- **Suggested repo layout:**

  ```
  jellyops/
  ├── api/v1alpha1/                 # Jellyfin & JellyfinPlugin types + zz_generated deepcopy
  ├── internal/controller/          # JellyfinReconciler, JellyfinPluginReconciler
  ├── internal/plugins/             # image-volume + meta.json validation, pod-template builder
  ├── config/crd/                   # generated CRD manifests
  ├── config/rbac/                  # least-privilege role/bindings
  ├── config/samples/               # example CRs (incl. jellycode)
  ├── cmd/main.go                   # manager entrypoint, leader election
  ├── Dockerfile                    # operator image
  └── Makefile                      # generate, manifests, test, docker-build
  ```

- **Build/test** — `make manifests generate`; unit tests with `envtest` (a real apiserver+etcd) for reconcile logic; an e2e suite (kind/minikube) gated on a runtime that supports image volumes.
- **Install** — ship CRDs + operator via Kustomize (`config/default`) and/or a Helm chart.

---

## 12. Versioning, Compatibility & Upgrades

- **API** — start at `v1alpha1`; graduate to `v1beta1`/`v1` with conversion webhooks once the schema stabilizes.
- **ABI matrix** — document a Jellyfin-server-version ↔ plugin-`targetAbi` compatibility table; the operator enforces it via the `ABICompatible` condition.
- **Plugin upgrades** — bump `pluginImage.reference` (prefer a new digest); the operator re-stages and rolls out, reporting the new version in `Jellyfin.status.loadedPlugins`.
- **Operator upgrades** — CRD changes are additive within a version; conversion webhooks handle cross-version moves.

---

## 13. Roadmap / Phasing

- **Phase 1 (MVP)** — `Jellyfin` CRD (full lifecycle) + `JellyfinPlugin` CRD; image-volume injection (`imageVolume` + `imageVolumeCopy`); generic companion-workload + Service reconciliation with finalizer-driven graceful drain (validated end-to-end using the jellycode plugin); ABI validation; NFS/PVC media folders; **API reconciliation: credential bootstrap + library create/update/prune (§7.6)**; status/conditions/events.
- **Phase 2** — `ClusterJellyfinPluginType` catalog; HPA for workers (heartbeat-driven); in-cluster mTLS for the transcode mesh; cosign verification of plugin images; investigate restart-minimizing reload; multi-instance plugin sharing; richer `PathMap` (non-identity) support; broader API reconciliation (users, system settings, scheduled tasks).

---

## 14. Open Questions / Risks

- **Read-only `meta.json`** — confirm whether the target Jellyfin v12 build tolerates a read-only plugin dir; if so, `imageVolume` can be the default and the copy mitigation becomes optional.
- **Runtime support for image volumes** — image volumes require a recent CRI runtime; clusters on older containerd/CRI-O cannot use this mechanism. This is a hard prerequisite that must be checked at install time (the operator should surface a clear precondition error).
- **jellycode identity path mapping** — Phase 0 assumes server and workers share absolute paths, forcing RWX media and shared output paths. Non-identity mapping is a future `PathMap` feature; until then the operator must enforce/validate matching mount paths.
- **Plugin base-image prerequisites** — the operator defaults to stock Jellyfin, so no operator default depends on an external build. Plugins that need a non-default ABI (e.g. jellycode's `targetAbi 12.0.0.0` / `net10.0` server) must source and maintain that image themselves; the operator only needs to validate ABI and surface a clear error on mismatch.
- **gRPC security** — cleartext h2c is acceptable only inside a trusted cluster boundary; production cross-zone/transcoding-at-the-edge needs the Phase 2 mTLS work.

---

### Appendix A — jellycode source references

These map the **reference plugin's** example values back to their source of truth in the sibling `../jellycode/` repo. They describe jellycode, not operator internals — the operator derives none of these values by name.

| Claim in this spec | Source of truth |
|--------------------|-----------------|
| guid / version / `targetAbi 12.0.0.0` / `framework net10.0` | `jellycode/src/Plugin/meta.json` |
| Plugin dir name `Distributed Transcoding_0.0.1.0`, ports 8096/9090, host-assembly stripping | `jellycode/docker/Dockerfile` |
| Worker CLI args (`--server/--worker-id/--ffmpeg/--max-concurrent/--scratch`), h2c switch | `jellycode/src/Worker/Program.cs` |
| `Drain`, `JobControl{STOP}`, `Register`, `Heartbeat`, identity `PathMap` | `jellycode/src/Contracts/Protos/transcode.proto` |
