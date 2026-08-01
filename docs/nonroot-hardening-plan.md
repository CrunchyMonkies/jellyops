# Non-root hardening plan for Jellyfin pods (operator change)

## Goal
Run every Jellyfin-managed container as a non-root user with a locked-down securityContext:
`runAsNonRoot: true`, an explicit `runAsUser`/`runAsGroup`, `capabilities.drop: ["ALL"]`,
`allowPrivilegeEscalation: false` (already set), `seccompProfile: RuntimeDefault` (already set),
granting back only what a container demonstrably needs.

Scope: the `Jellyfin` server pod, its plugin **init-containers** (staging / hook / install), the
web tier, and all `JellyfinPlugin` **workloads** (the 3 jellycode worker pools). This is an
**operator (jellyops) code change** — the securityContexts are hardcoded in Go
(`internal/plugins/builder.go`, `workloads.go`, `web.go`); the CRD does not expose them today.

## Current state (verified on bne1-cluster1)
- **All images run as root** — no `USER` in the Jellyfin fork, worker, server, or plugin
  Dockerfiles; the live server and workers report `uid=0`. So `runAsNonRoot: true` **alone** makes
  every pod fail admission — we must set an explicit non-zero `runAsUser`.
- Volume perms today: `/config` is `0777` (world-writable), `/media` is `0755` (world-readable).
  So a non-root UID can write config and read media **provided PVC ownership cooperates** →
  needs `fsGroup` so the operator-provisioned PVCs (config, cache) and worker scratch (emptyDir)
  are group-writable.
- **GPU access is the hard part.** The intel worker gets `/dev/dri/card2` (`crw-rw---- root root`,
  mode 0660) injected by the **Intel GPU device plugin**, not a CR hostPath. As root it just
  works; as non-root the process must be a member of the **group that owns the render node**
  (render/video GID), added via `SecurityContext.SupplementalGroups`. That GID is
  **node/environment-specific** and must be supplied, not guessed. NVENC (nvidia runtime) generally
  works non-root without extra caps/groups, but must be validated.
- **Staging init-container breaks under non-root:** `builder.go:stagingContainer` runs
  `set -e; mkdir -p <dest>; cp -a <src>/. <dest>/`. `cp -a` tries to preserve ownership; as
  non-root that fails and, under `set -e`, aborts the pod. Must become `cp -r` (or `cp` without
  `-a`), keeping perms but not ownership.

## Target securityContext

Container-level (all app + init containers):
```go
SecurityContext: &corev1.SecurityContext{
    RunAsNonRoot:             ptr.To(true),
    RunAsUser:                ptr.To(int64(UID)),
    RunAsGroup:               ptr.To(int64(GID)),
    AllowPrivilegeEscalation: ptr.To(false),        // already set
    Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
    // ReadOnlyRootFilesystem: deliberately NOT set — Jellyfin/ffmpeg write to /config, /cache,
    // /tmp scratch; revisit later with explicit writable mounts if we want it.
}
```
Pod-level:
```go
SecurityContext: &corev1.PodSecurityContext{
    RunAsNonRoot:       ptr.To(true),
    RunAsUser:          ptr.To(int64(UID)),
    RunAsGroup:         ptr.To(int64(GID)),
    FSGroup:            ptr.To(int64(GID)),                 // PVC + emptyDir group-write
    SeccompProfile:     &corev1.SeccompProfile{Type: RuntimeDefault},   // already set
    SupplementalGroups: []int64{ /* render/video GID for GPU pools only */ },
}
```

Proposed defaults: **UID=GID=1000**, `fsGroup=1000` (subject to confirmation — see decisions).

## Per-container application & exceptions
- **Jellyfin server** (`builder.go` container `jellyfin` + podSpec): apply both blocks. `fsGroup`
  fixes `/config`+`/cache` PVC writes. For a hw-accel **server** (vaapi), keep the existing
  `applyHardwareAccel` render-group `SupplementalGroups` path.
- **Init-containers** staging/hook/install (`builder.go`): same container securityContext (drop
  ALL, non-root). Fix staging `cp -a` → `cp -r`. They write into `/config` (0777 + fsGroup) so
  non-root is fine.
- **Worker pools** (`workloads.go` container + podSpec): apply both blocks. **cpu**: no GPU, clean.
  **intel**: add the render/video GID to `SupplementalGroups` (env-specific). **nvidia**: validate
  NVENC works non-root under the nvidia runtime with caps dropped; add group only if required.
  Worker scratch is an `emptyDir` at `/tmp/worker` → `fsGroup` makes it writable.
- **Web tier** (`web.go`): apply both blocks; nginx-style web serving needs no added caps when
  listening on a high port (confirm the port is >1024, else it needs `NET_BIND_SERVICE` back or a
  port change).

## Code changes
- `internal/plugins/builder.go`: server container SC, pod SC (add non-root/runAsUser/fsGroup),
  init-container SC helper, `cp -a`→`cp -r` in `stagingContainer`.
- `internal/plugins/workloads.go`: worker container SC, pod SC, supplemental-group plumbing.
- `internal/plugins/web.go`: web container + pod SC.
- **CRD (recommended)** `api/v1alpha1`: expose an optional `podSecurity` block
  (`runAsUser`/`runAsGroup`/`fsGroup`/`supplementalGroups`) on `Jellyfin` and on `PluginWorkload`,
  defaulting to the hardcoded values. This is needed because the **GPU render GID is
  environment-specific** and shouldn't be hardcoded; it also lets media-NFS UID/GID be matched
  per instance. Regenerate CRDs (`make manifests generate`).

## Rollout & verification (staged — do NOT roll everything at once)
1. Build + push the operator image; redeploy the operator (it re-renders pods on reconcile).
2. **Server first**: confirm the Jellyfin pod starts non-root (`kubectl exec ... id` → uid=1000),
   can write `/config` (log in, change a setting), read media (library scan), and — if hw server —
   transcode. Watch for `CreateContainerConfigError`/permission-denied.
3. **One worker pool next (cpu)**: confirm it registers and transcodes as non-root.
4. **intel/nvidia**: confirm GPU probe still reports `vaapi`/`nvenc` and a hardware transcode
   actually runs (the render-group GID is the likely failure point).
5. Confirm `/metrics` still served (HttpListener binds a high port, non-root OK).

## Rollback
Revert the operator image tag (or the CR `podSecurity` block) and reconcile; pods roll back to the
current root securityContext. Keep the change behind the CRD field so it can be disabled per
instance without redeploying the operator.

## Open decisions / risks (need input before executing)
1. **UID/GID + fsGroup value** — default 1000:1000? Must match media-NFS ownership if media is not
   world-readable at deeper levels than the `0755` top dir we sampled.
2. **GPU render/video GID** for the intel (and possibly nvidia) worker — must be supplied per node;
   discover with `stat -c '%g' /dev/dri/renderD128` on the GPU node. This is the single most
   likely thing to break hardware transcoding under non-root.
3. **Media NFS perms** deeper than the top level — if any media dirs/files aren't group/other
   readable for the chosen UID/GID, library scans/transcodes fail. Sample before rolling.
4. Whether to also set `readOnlyRootFilesystem` (deferred — needs explicit writable tmp mounts).
