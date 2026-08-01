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

// ServiceSpec configures the Service fronting the Jellyfin :8096 port.
type ServiceSpec struct {
	// Type is the Service type. Defaults to ClusterIP.
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`

	// Port is the Service port exposing Jellyfin. Defaults to 8096.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`

	// Annotations are applied to the Service (e.g. load-balancer hints).
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// IngressSpec exposes the instance over HTTP(S) via an Ingress.
type IngressSpec struct {
	// ClassName selects the IngressClass, e.g. "nginx".
	// +optional
	ClassName string `json:"className,omitempty"`

	// Host is the external hostname, e.g. jellyfin.example.com.
	// +kubebuilder:validation:MinLength=1
	Host string `json:"host"`

	// TLS optionally enables TLS termination using the referenced Secret.
	// +optional
	TLS *IngressTLS `json:"tls,omitempty"`

	// Annotations are applied to the Ingress.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// IngressTLS configures TLS for the Ingress.
type IngressTLS struct {
	// SecretName references a kubernetes.io/tls Secret for the host.
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName"`
}

// HardwareAccel attaches a transcoding accelerator device to the Jellyfin pod.
type HardwareAccel struct {
	// Type selects the acceleration backend. At pod startup the operator points
	// Jellyfin's encoding config at it (HardwareAccelerationType nvenc for
	// nvidia, plus the render device for vaapi/qsv). Omit hardwareAcceleration
	// entirely for CPU transcoding; the encoding config is then left untouched.
	// +kubebuilder:validation:Enum=vaapi;qsv;nvidia
	Type string `json:"type"`

	// DevicePath pins the render device for vaapi/qsv, e.g. /dev/dri/renderD129.
	// When unset, /dev/dri is mounted and the device is auto-detected at pod
	// startup (the render node is not always renderD128). Ignored for nvidia and
	// when DeviceResource is set.
	// +optional
	DevicePath string `json:"devicePath,omitempty"`

	// DeviceResource requests a device-plugin resource for vaapi/qsv, e.g.
	// "gpu.intel.com/xe" or "gpu.intel.com/i915", instead of a hostPath mount.
	// The plugin injects the render device (auto-detected at startup) and the
	// request schedules the pod onto a GPU node. Prefer this over the hostPath
	// behaviour, which the "baseline"/"restricted" Pod Security Standards forbid.
	// +optional
	DeviceResource string `json:"deviceResource,omitempty"`

	// RenderGroupGID is added as a supplemental group so the container can access
	// the render device.
	// +optional
	RenderGroupGID *int64 `json:"renderGroupGID,omitempty"`

	// RuntimeClassName selects a runtime class for nvidia acceleration, e.g.
	// "nvidia".
	// +optional
	RuntimeClassName string `json:"runtimeClassName,omitempty"`
}

// PodSecuritySpec overrides the operator's hardened pod-security defaults.
// When omitted, every managed pod runs non-root (UID/GID 1000) with all
// capabilities dropped.
type PodSecuritySpec struct {
	// RunAsNonRoot controls the pod/container non-root enforcement. Defaults to true.
	// +optional
	RunAsNonRoot *bool `json:"runAsNonRoot,omitempty"`

	// RunAsUser is the UID for the pod/container. Defaults to 1000.
	// +optional
	RunAsUser *int64 `json:"runAsUser,omitempty"`

	// RunAsGroup is the primary GID for the pod/container. Defaults to 1000.
	// +optional
	RunAsGroup *int64 `json:"runAsGroup,omitempty"`

	// FsGroup is the filesystem group applied to all volumes. Defaults to 1000.
	// +optional
	FsGroup *int64 `json:"fsGroup,omitempty"`

	// SupplementalGroups are additional GIDs applied to the pod (merged with
	// any groups added by hardware acceleration).
	// +optional
	SupplementalGroups []int64 `json:"supplementalGroups,omitempty"`
}
