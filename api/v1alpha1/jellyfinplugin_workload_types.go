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

package v1alpha1

import corev1 "k8s.io/api/core/v1"

// ImageSource references an OCI image and how to pull it.
type ImageSource struct {
	// Reference is the image reference. Digest-pinning is recommended for
	// reproducibility and supply-chain safety.
	// +kubebuilder:validation:MinLength=1
	Reference string `json:"reference"`

	// PullPolicy is the image pull policy. Defaults to IfNotPresent.
	// +kubebuilder:validation:Enum=Always;Never;IfNotPresent
	// +optional
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`

	// SubPath is the directory within the image filesystem that holds the plugin
	// files (mounted/copied instead of the whole image root).
	// +optional
	SubPath string `json:"subPath,omitempty"`
}

// PluginMeta describes a Jellyfin plugin's identity (mirrors meta.json).
type PluginMeta struct {
	// GUID is the plugin GUID.
	// +optional
	GUID string `json:"guid,omitempty"`

	// Name is the plugin display name; also the plugins-dir folder name prefix.
	// +optional
	Name string `json:"name,omitempty"`

	// Version is the plugin version, e.g. "0.0.1.0".
	// +optional
	Version string `json:"version,omitempty"`

	// TargetABI is the Jellyfin ABI the plugin targets, e.g. "12.0.0.0". Validated
	// against the instance server version.
	// +optional
	TargetABI string `json:"targetAbi,omitempty"`

	// SubPath is the directory within the plugin image holding the plugin files.
	// Overrides pluginImage.subPath when both are set.
	// +optional
	SubPath string `json:"subPath,omitempty"`
}

// PluginWorkload is a companion Deployment the operator manages for a plugin.
type PluginWorkload struct {
	// Name identifies the workload; used to name the Deployment.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Image is the runnable workload image (e.g. the jellycode worker).
	// +required
	Image ImageSource `json:"image"`

	// Replicas is the desired replica count. Defaults to 1.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Command overrides the image entrypoint.
	// +optional
	Command []string `json:"command,omitempty"`

	// Args are passed to the workload container.
	// +optional
	Args []string `json:"args,omitempty"`

	// Env are environment variables for the workload container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Ports the workload container exposes.
	// +optional
	Ports []corev1.ContainerPort `json:"ports,omitempty"`

	// Resources for the workload container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// VolumeMounts for the workload container. Volumes are declared below.
	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Volumes available to the workload pod (e.g. an emptyDir scratch, or a
	// shared media PVC for identity path mapping).
	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// InstanceMedia controls how the bound Jellyfin instance's media folders are
	// auto-mounted into this workload. When nil, every instance media folder is
	// mounted read-only at the instance's paths (identity mapping) — the default.
	// Use it to scope a worker to a subset of libraries and/or grant read-write to
	// specific ones (e.g. a Shoko Server that organizes only the anime library).
	// A folder the workload hand-declares in Volumes/VolumeMounts still wins.
	// +optional
	InstanceMedia *InstanceMediaSelection `json:"instanceMedia,omitempty"`

	// TerminationGracePeriodSeconds gives the workload time to drain in-flight
	// work on SIGTERM before deletion.
	// +optional
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`

	// NodeSelector constrains the workload to nodes with matching labels (e.g. a GPU node).
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations let the workload schedule onto tainted nodes (e.g. a node dedicated to
	// GPU workloads via a NoExecute taint).
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// PriorityClassName assigns a PriorityClass to the workload pod, controlling
	// scheduling priority and preemption behavior.
	// +optional
	PriorityClassName string `json:"priorityClassName,omitempty"`

	// RuntimeClassName selects a RuntimeClass for the workload pod (e.g. "nvidia" so
	// an NVENC worker gets the NVIDIA container runtime and GPU access).
	// +optional
	RuntimeClassName string `json:"runtimeClassName,omitempty"`

	// PodSecurity overrides the instance-level pod-security defaults for this
	// workload. When nil, the bound Jellyfin instance's podSecurity (or the
	// operator hardened default) applies.
	// +optional
	PodSecurity *PodSecuritySpec `json:"podSecurity,omitempty"`

	// ReadinessProbe gates whether the workload pod is Ready (and therefore counted
	// toward the WorkloadsReady condition and reachable via a plugin Service). Use it
	// for workloads that take time to become serviceable (e.g. a Shoko Server that
	// must open its database before answering :8111).
	// +optional
	ReadinessProbe *corev1.Probe `json:"readinessProbe,omitempty"`

	// LivenessProbe restarts the workload container when it stops responding.
	// +optional
	LivenessProbe *corev1.Probe `json:"livenessProbe,omitempty"`

	// StartupProbe holds off the liveness/readiness probes until the workload has
	// finished a slow start.
	// +optional
	StartupProbe *corev1.Probe `json:"startupProbe,omitempty"`

	// Autoscaling is a Phase-2 placeholder for HPA hooks.
	// +optional
	Autoscaling *WorkloadAutoscaling `json:"autoscaling,omitempty"`
}

// Instance media auto-mount modes for a companion workload.
const (
	// InstanceMediaAll mounts every instance media folder (the default).
	InstanceMediaAll = "All"
	// InstanceMediaSelected mounts only the folders named in Include.
	InstanceMediaSelected = "Selected"
	// InstanceMediaNone mounts no instance media.
	InstanceMediaNone = "None"
)

// InstanceMediaSelection scopes which of the bound instance's media folders are
// auto-mounted into a companion workload, and at what access mode.
type InstanceMediaSelection struct {
	// Mode selects the auto-mount policy:
	//   "All"      - mount every instance media folder (the default when empty).
	//   "Selected" - mount only the folders named in Include.
	//   "None"     - do not auto-mount any instance media.
	// +kubebuilder:validation:Enum=All;Selected;None
	// +optional
	Mode string `json:"mode,omitempty"`

	// Include names instance media folders (by MediaFolder.Name) to mount when
	// Mode is "Selected". Ignored for other modes.
	// +optional
	Include []string `json:"include,omitempty"`

	// ReadWrite names instance media folders to mount read-write instead of the
	// default read-only. A name not among the mounted folders is ignored.
	// +optional
	ReadWrite []string `json:"readWrite,omitempty"`
}

// PluginServiceTarget selects what a plugin Service points at.
const (
	// ServiceTargetInstance points the Service at the Jellyfin pod.
	ServiceTargetInstance = "instance"
)

// PluginService is a companion Service the operator manages for a plugin.
type PluginService struct {
	// Name identifies the Service.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Selector chooses the Service target:
	//   "instance"        - the Jellyfin pod
	//   "workload:<name>" - the named companion workload
	// +kubebuilder:validation:MinLength=1
	Selector string `json:"selector"`

	// Ports the Service exposes.
	// +optional
	Ports []corev1.ServicePort `json:"ports,omitempty"`

	// Type is the Service type. Defaults to ClusterIP.
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`
}

// PluginInstall configures plugin setup that runs as init containers before the
// Jellyfin container starts (and after imageVolumeCopy staging).
//
// Script/Command are optional. When both are omitted, this block simply supplies
// env/image/volumeMounts/failurePolicy/timeout to the standard baked hooks that
// jellyops auto-runs for imageVolumeCopy plugins — bootstrap.sh (every start) and
// firstrun.sh (once per instance), if the plugin image baked them at its root.
// When Script or Command is set, that inline script also runs as an init container.
type PluginInstall struct {
	// Image runs the install container. Defaults to the Jellyfin server image so
	// the script sees Jellyfin's filesystem layout. Set to the plugin image or a
	// minimal runner to override.
	// +optional
	Image *ImageSource `json:"image,omitempty"`

	// Script is an inline script run with `sh -c`. Mutually exclusive with
	// Command/Args.
	// +optional
	Script string `json:"script,omitempty"`

	// Command overrides the entrypoint. Mutually exclusive with Script.
	// +optional
	Command []string `json:"command,omitempty"`

	// Args for Command.
	// +optional
	Args []string `json:"args,omitempty"`

	// Env are environment variables for the install container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// VolumeMounts added to the install container in addition to the writable
	// /config PVC and the staged plugin dir.
	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Resources for the install container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// FailurePolicy controls whether a non-zero exit blocks pod startup.
	// Ignore (default) lets Jellyfin boot; Fail blocks (fail-closed).
	// +kubebuilder:validation:Enum=Ignore;Fail
	// +kubebuilder:default=Ignore
	// +optional
	FailurePolicy string `json:"failurePolicy,omitempty"`

	// TimeoutSeconds bounds the install runtime; on timeout the script is treated
	// as failed (subject to FailurePolicy).
	// +kubebuilder:validation:Minimum=1
	// +optional
	TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`

	// RunOnce records a marker under /config keyed on plugin version so the script
	// runs only once per version.
	// +optional
	RunOnce bool `json:"runOnce,omitempty"`
}

// WorkloadAutoscaling is a Phase-2 placeholder for HPA configuration.
type WorkloadAutoscaling struct {
	// MinReplicas is the lower bound.
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the upper bound.
	// +optional
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

	// TargetCPUUtilizationPercentage is the CPU target for the HPA.
	// +optional
	TargetCPUUtilizationPercentage *int32 `json:"targetCPUUtilizationPercentage,omitempty"`
}
