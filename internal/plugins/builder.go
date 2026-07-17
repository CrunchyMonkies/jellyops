/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package plugins

import (
	"fmt"
	"path"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

// installScriptEnvVar carries the user install body so it never has to be
// shell-quoted into the wrapper command.
const installScriptEnvVar = "JELLYOPS_INSTALL_SCRIPT"

// BuildDeployment builds the desired Jellyfin Deployment for an instance and its
// bound (already ABI-validated, non-Failed) plugins. Output is deterministic:
// identical inputs produce byte-identical objects so reconciles do not churn
// rollouts.
func BuildDeployment(jf *jellyfinv1alpha1.Jellyfin, plugins []jellyfinv1alpha1.JellyfinPlugin) (*appsv1.Deployment, error) {
	pod, err := BuildPodTemplateSpec(jf, plugins)
	if err != nil {
		return nil, err
	}
	replicas := int32(1)
	if jf.Spec.Replicas != nil {
		replicas = *jf.Spec.Replicas
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jf.Name,
			Namespace: jf.Namespace,
			Labels:    InstanceLabels(jf),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: InstanceSelectorLabels(jf)},
			// Jellyfin is not active-active and its /config PVC is RWO, so two
			// pods cannot coexist during a rollout.
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: pod,
		},
	}, nil
}

// BuildPodTemplateSpec assembles the Jellyfin pod template including base
// storage, media folders, hardware acceleration, per-plugin image volumes, and
// the ordered staging/install init containers.
func BuildPodTemplateSpec(jf *jellyfinv1alpha1.Jellyfin, plugins []jellyfinv1alpha1.JellyfinPlugin) (corev1.PodTemplateSpec, error) {
	// Sort plugins by name for deterministic volume/init-container ordering.
	sorted := append([]jellyfinv1alpha1.JellyfinPlugin(nil), plugins...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	image := jf.Spec.Image
	if image == "" {
		image = DefaultJellyfinImage
	}

	jellyfin := corev1.Container{
		Name:  JellyfinContainerName,
		Image: image,
		Ports: []corev1.ContainerPort{{
			Name:          "http",
			ContainerPort: DefaultJellyfinPort,
			Protocol:      corev1.ProtocolTCP,
		}},
		Resources: jf.Spec.Resources,
		VolumeMounts: []corev1.VolumeMount{
			{Name: ConfigVolumeName, MountPath: ConfigMountPath},
		},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
		},
	}

	volumes := []corev1.Volume{configVolume(jf)}

	// Cache PVC (optional).
	if jf.Spec.Storage.Cache != nil {
		cachePath := CacheMountPath
		if jf.Spec.Storage.Cache.MountPath != "" {
			cachePath = jf.Spec.Storage.Cache.MountPath
		}
		volumes = append(volumes, corev1.Volume{
			Name: CacheVolumeName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: CacheClaimName(jf)},
			},
		})
		jellyfin.VolumeMounts = append(jellyfin.VolumeMounts, corev1.VolumeMount{Name: CacheVolumeName, MountPath: cachePath})
		jellyfin.Env = append(jellyfin.Env, corev1.EnvVar{Name: "JELLYFIN_CACHE_DIR", Value: cachePath})
	}

	// Media folders.
	for _, mf := range jf.Spec.Storage.Media {
		vol, mount := mediaVolumeAndMount(jf, mf)
		volumes = append(volumes, vol)
		jellyfin.VolumeMounts = append(jellyfin.VolumeMounts, mount)
	}

	// Web-as-volume mode: mount the web image read-only into the server pod and
	// host /web from it, instead of a separate nginx web tier. The server then
	// serves /web itself, so server-side static-file plugins (File Transformation)
	// can transform the web client. Overrides the fork image's baked --nowebclient.
	if web := jf.Spec.Web; web != nil && web.EffectiveMode() == jellyfinv1alpha1.WebModeVolume {
		volumes = append(volumes, webContentVolume(web))
		jellyfin.VolumeMounts = append(jellyfin.VolumeMounts, corev1.VolumeMount{
			Name:      WebContentVolumeName,
			MountPath: WebContentMountPath,
			SubPath:   web.SubPath,
			ReadOnly:  true,
		})
		jellyfin.Command = []string{DefaultJellyfinCommand}
		jellyfin.Env = append(jellyfin.Env, corev1.EnvVar{Name: "JELLYFIN_WEB_DIR", Value: WebContentMountPath})
	}

	var initContainers []corev1.Container

	// Per-plugin image volumes + injection + install.
	for i := range sorted {
		p := &sorted[i]
		volumes = append(volumes, imageVolume(p))

		switch p.Spec.Injection {
		case jellyfinv1alpha1.InjectionImageVolumeCopy:
			initContainers = append(initContainers, stagingContainer(image, p))
		default: // InjectionImageVolume
			jellyfin.VolumeMounts = append(jellyfin.VolumeMounts, corev1.VolumeMount{
				Name:      imageVolumeName(p),
				MountPath: path.Join(PluginsDirPath, PluginFolderName(EffectiveMeta(p))),
				SubPath:   PluginSubPath(p),
				ReadOnly:  true,
			})
		}

		// Legacy inline install (escape hatch): only built when a script/command is
		// actually set. spec.install may now exist solely to supply env/image to the
		// baked hooks below.
		if p.Spec.Install != nil && (p.Spec.Install.Script != "" || len(p.Spec.Install.Command) > 0) {
			c, err := installContainer(image, p)
			if err != nil {
				return corev1.PodTemplateSpec{}, fmt.Errorf("plugin %q: %w", p.Name, err)
			}
			initContainers = append(initContainers, c)
		}

		// Standard baked-hook runner: for imageVolumeCopy plugins the operator always
		// wires a runtime hook container that runs bootstrap.sh (every start) and
		// firstrun.sh (once) if the image baked them into its root. No-op if absent.
		if p.Spec.Injection == jellyfinv1alpha1.InjectionImageVolumeCopy {
			initContainers = append(initContainers, hookContainer(image, p))
		}
	}

	podSpec := corev1.PodSpec{
		Containers:       []corev1.Container{jellyfin},
		InitContainers:   initContainers,
		Volumes:          volumes,
		ImagePullSecrets: jf.Spec.ImagePullSecrets,
		SecurityContext: &corev1.PodSecurityContext{
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}

	applyHardwareAccel(&podSpec, jf.Spec.HardwareAcceleration)

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      InstanceSelectorLabels(jf),
			Annotations: jf.Spec.PodAnnotations,
		},
		Spec: podSpec,
	}, nil
}

func configVolume(jf *jellyfinv1alpha1.Jellyfin) corev1.Volume {
	return corev1.Volume{
		Name: ConfigVolumeName,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: ConfigClaimName(jf)},
		},
	}
}

// mediaReadOnly resolves the read-only flag for a media mount. Inline NFS
// defaults are honored via the explicit fields; provisioned RWX media stays
// writable so companion workers can write transcode output (spec §8.2).
func mediaReadOnly(mf jellyfinv1alpha1.MediaFolder) bool {
	if mf.ReadOnly {
		return true
	}
	if mf.NFS != nil && mf.NFS.ReadOnly {
		return true
	}
	return false
}

// mediaVolumeAndMount builds the volume + mount for a media folder. Inline NFS
// (provision=false, no mountOptions) uses an NFSVolumeSource; everything else is
// PVC-backed by a claim the reconciler provisions.
func mediaVolumeAndMount(jf *jellyfinv1alpha1.Jellyfin, mf jellyfinv1alpha1.MediaFolder) (corev1.Volume, corev1.VolumeMount) {
	ro := mediaReadOnly(mf)
	vol := corev1.Volume{Name: mediaVolumeName(mf)}

	if mf.NFS != nil && !mf.NFS.Provision && len(mf.NFS.MountOptions) == 0 {
		vol.NFS = &corev1.NFSVolumeSource{
			Server:   mf.NFS.Server,
			Path:     mf.NFS.Path,
			ReadOnly: ro,
		}
	} else {
		vol.PersistentVolumeClaim = &corev1.PersistentVolumeClaimVolumeSource{
			ClaimName: MediaClaimName(jf, mf),
			ReadOnly:  ro,
		}
	}

	return vol, corev1.VolumeMount{Name: vol.Name, MountPath: mf.MountPath, ReadOnly: ro}
}

func imageVolume(p *jellyfinv1alpha1.JellyfinPlugin) corev1.Volume {
	pull := p.Spec.PluginImage.PullPolicy
	if pull == "" {
		pull = corev1.PullIfNotPresent
	}
	return corev1.Volume{
		Name: imageVolumeName(p),
		VolumeSource: corev1.VolumeSource{
			Image: &corev1.ImageVolumeSource{
				Reference:  p.Spec.PluginImage.Reference,
				PullPolicy: pull,
			},
		},
	}
}

// webContentVolume builds the read-only image volume that holds the web client
// for web-as-volume mode (spec.web.mode: volume).
func webContentVolume(web *jellyfinv1alpha1.WebSpec) corev1.Volume {
	pull := web.PullPolicy
	if pull == "" {
		pull = corev1.PullIfNotPresent
	}
	return corev1.Volume{
		Name: WebContentVolumeName,
		VolumeSource: corev1.VolumeSource{
			Image: &corev1.ImageVolumeSource{
				Reference:  web.Image,
				PullPolicy: pull,
			},
		},
	}
}

// stagingContainer copies the read-only plugin image into the writable
// /config/plugins/<folder> dir (imageVolumeCopy mode) before Jellyfin starts.
func stagingContainer(defaultImage string, p *jellyfinv1alpha1.JellyfinPlugin) corev1.Container {
	folder := PluginFolderName(EffectiveMeta(p))
	src := path.Join(StagingSrcBase, p.Name)
	from := src
	if sub := PluginSubPath(p); sub != "" {
		from = path.Join(src, sub)
	}
	dest := path.Join(PluginsDirPath, folder)

	pull := p.Spec.PluginImage.PullPolicy
	if pull == "" {
		pull = corev1.PullIfNotPresent
	}

	cmd := fmt.Sprintf("set -e\nmkdir -p %s\ncp -a %s/. %s/",
		shellQuote(dest), shellQuote(from), shellQuote(dest))

	return corev1.Container{
		Name:            stagingContainerName(p),
		Image:           defaultImage,
		ImagePullPolicy: pull,
		Command:         []string{"sh", "-c", cmd},
		VolumeMounts: []corev1.VolumeMount{
			{Name: imageVolumeName(p), MountPath: src, ReadOnly: true},
			{Name: ConfigVolumeName, MountPath: ConfigMountPath},
		},
	}
}

// installContainer builds the pre-start install init container, applying
// runOnce, failurePolicy, and timeout wrapping around the user script/command.
func installContainer(defaultImage string, p *jellyfinv1alpha1.JellyfinPlugin) (corev1.Container, error) {
	inst := p.Spec.Install
	if inst.Script != "" && len(inst.Command) > 0 {
		return corev1.Container{}, fmt.Errorf("install: script and command are mutually exclusive")
	}
	if inst.Script == "" && len(inst.Command) == 0 {
		return corev1.Container{}, fmt.Errorf("install: one of script or command is required")
	}

	body := inst.Script
	if body == "" {
		tokens := append(append([]string{}, inst.Command...), inst.Args...)
		body = shellJoin(tokens)
	}

	image := defaultImage
	pull := corev1.PullPolicy("")
	if inst.Image != nil {
		image = inst.Image.Reference
		pull = inst.Image.PullPolicy
	}

	folder := PluginFolderName(EffectiveMeta(p))
	wrapper := buildInstallWrapper(folder, p.Name, inst)

	mounts := []corev1.VolumeMount{{Name: ConfigVolumeName, MountPath: ConfigMountPath}}
	mounts = append(mounts, inst.VolumeMounts...)

	env := []corev1.EnvVar{{Name: installScriptEnvVar, Value: body}}
	env = append(env, inst.Env...)

	return corev1.Container{
		Name:            installContainerName(p),
		Image:           image,
		ImagePullPolicy: pull,
		Command:         []string{"sh", "-c", wrapper},
		Env:             env,
		VolumeMounts:    mounts,
		Resources:       inst.Resources,
	}, nil
}

// buildInstallWrapper composes the shell wrapper that runs the install body from
// the JELLYOPS_INSTALL_SCRIPT env var, applying runOnce/timeout/failurePolicy.
func buildInstallWrapper(folder, pluginName string, inst *jellyfinv1alpha1.PluginInstall) string {
	marker := path.Join(InstalledMarkerDir, folder)

	run := fmt.Sprintf(`sh -c "$%s"`, installScriptEnvVar)
	if inst.TimeoutSeconds != nil {
		run = fmt.Sprintf(`timeout %d %s`, *inst.TimeoutSeconds, run)
	}

	var b strings.Builder
	if inst.RunOnce {
		fmt.Fprintf(&b, "MARKER=%s\n", shellQuote(marker))
		b.WriteString("if [ -f \"$MARKER\" ]; then exit 0; fi\n")
	}

	touch := ""
	if inst.RunOnce {
		touch = "mkdir -p \"$(dirname \"$MARKER\")\"\ntouch \"$MARKER\"\n"
	}

	if inst.FailurePolicy == jellyfinv1alpha1.FailurePolicyFail {
		// Fail-closed: abort (and skip the marker) on any error.
		b.WriteString("set -e\n")
		b.WriteString(run + "\n")
		b.WriteString(touch)
	} else {
		// Fail-open (default Ignore): swallow errors and never block startup.
		b.WriteString("if " + run + "; then\n")
		b.WriteString(indent(touch))
		fmt.Fprintf(&b, "else\n  echo \"jellyops: install for %s failed (failurePolicy=Ignore)\"\nfi\n", pluginName)
		b.WriteString("exit 0\n")
	}
	return b.String()
}

func indent(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// hookContainer builds the standard baked-hook runner init container. For an
// imageVolumeCopy plugin the operator can't inspect the image at reconcile time,
// so this container detects the hooks at runtime in the staged plugin dir:
//   - firstrun.sh runs once per instance (marker-gated under FirstRunMarkerDir),
//   - bootstrap.sh runs on every pod start.
//
// Both are optional (no-op if absent). Config (image/env/volumeMounts/failurePolicy/
// timeout) is taken from spec.install when present, so a plugin can supply env/secrets
// without an inline script.
func hookContainer(defaultImage string, p *jellyfinv1alpha1.JellyfinPlugin) corev1.Container {
	inst := p.Spec.Install

	image := defaultImage
	pull := corev1.PullPolicy("")
	mounts := []corev1.VolumeMount{{Name: ConfigVolumeName, MountPath: ConfigMountPath}}
	var env []corev1.EnvVar
	var resources corev1.ResourceRequirements
	if inst != nil {
		if inst.Image != nil {
			image = inst.Image.Reference
			pull = inst.Image.PullPolicy
		}
		mounts = append(mounts, inst.VolumeMounts...)
		env = append(env, inst.Env...)
		resources = inst.Resources
	}

	folder := PluginFolderName(EffectiveMeta(p))
	wrapper := buildHookWrapper(folder, p.Name, inst)

	return corev1.Container{
		Name:            hookContainerName(p),
		Image:           image,
		ImagePullPolicy: pull,
		Command:         []string{"sh", "-c", wrapper},
		Env:             env,
		VolumeMounts:    mounts,
		Resources:       resources,
	}
}

// buildHookWrapper composes the shell that runs the baked firstrun.sh (once) and
// bootstrap.sh (every start) from the staged plugin dir, honoring failurePolicy and
// timeout from spec.install (defaults: fail-open, no timeout).
func buildHookWrapper(folder, pluginName string, inst *jellyfinv1alpha1.PluginInstall) string {
	stage := path.Join(PluginsDirPath, folder)
	marker := path.Join(FirstRunMarkerDir, folder)

	failClosed := inst != nil && inst.FailurePolicy == jellyfinv1alpha1.FailurePolicyFail
	timeoutPrefix := ""
	if inst != nil && inst.TimeoutSeconds != nil {
		timeoutPrefix = fmt.Sprintf("timeout %d ", *inst.TimeoutSeconds)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "STAGE=%s\n", shellQuote(stage))
	fmt.Fprintf(&b, "MARKER=%s\n", shellQuote(marker))

	// firstrun.sh — once per instance.
	b.WriteString(`if [ -f "$STAGE/firstrun.sh" ] && [ ! -f "$MARKER" ]; then` + "\n")
	if failClosed {
		fmt.Fprintf(&b, "  %ssh \"$STAGE/firstrun.sh\"\n", timeoutPrefix)
		b.WriteString("  mkdir -p \"$(dirname \"$MARKER\")\"\n  touch \"$MARKER\"\n")
	} else {
		fmt.Fprintf(&b, "  if %ssh \"$STAGE/firstrun.sh\"; then\n", timeoutPrefix)
		b.WriteString("    mkdir -p \"$(dirname \"$MARKER\")\"\n    touch \"$MARKER\"\n")
		fmt.Fprintf(&b, "  else\n    echo \"jellyops: firstrun for %s failed (failurePolicy=Ignore)\"\n  fi\n", pluginName)
	}
	b.WriteString("fi\n")

	// bootstrap.sh — every start.
	b.WriteString(`if [ -f "$STAGE/bootstrap.sh" ]; then` + "\n")
	if failClosed {
		fmt.Fprintf(&b, "  %ssh \"$STAGE/bootstrap.sh\"\n", timeoutPrefix)
	} else {
		fmt.Fprintf(&b, "  %ssh \"$STAGE/bootstrap.sh\" || echo \"jellyops: bootstrap for %s failed (failurePolicy=Ignore)\"\n", timeoutPrefix, pluginName)
	}
	b.WriteString("fi\n")

	if failClosed {
		// Fail-closed: any hook error already aborts via the shell's exit status
		// because we run without swallowing; make it explicit with set -e semantics.
		return "set -e\n" + b.String()
	}
	b.WriteString("exit 0\n")
	return b.String()
}

// applyHardwareAccel attaches a transcoding device to the pod/container.
func applyHardwareAccel(pod *corev1.PodSpec, hw *jellyfinv1alpha1.HardwareAccel) {
	if hw == nil {
		return
	}
	jellyfin := &pod.Containers[0]
	switch hw.Type {
	case "vaapi", "qsv":
		dev := hw.DevicePath
		if dev == "" {
			dev = "/dev/dri/renderD128"
		}
		pod.Volumes = append(pod.Volumes, corev1.Volume{
			Name: "dri-device",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: dev, Type: ptr.To(corev1.HostPathCharDev)},
			},
		})
		jellyfin.VolumeMounts = append(jellyfin.VolumeMounts, corev1.VolumeMount{Name: "dri-device", MountPath: dev})
		if hw.RenderGroupGID != nil {
			if pod.SecurityContext == nil {
				pod.SecurityContext = &corev1.PodSecurityContext{}
			}
			pod.SecurityContext.SupplementalGroups = append(pod.SecurityContext.SupplementalGroups, *hw.RenderGroupGID)
		}
	case "nvidia":
		if jellyfin.Resources.Limits == nil {
			jellyfin.Resources.Limits = corev1.ResourceList{}
		}
		jellyfin.Resources.Limits["nvidia.com/gpu"] = resourceQuantityOne()
		if hw.RuntimeClassName != "" {
			pod.RuntimeClassName = ptr.To(hw.RuntimeClassName)
		}
	}
}
