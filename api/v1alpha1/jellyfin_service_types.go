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
	// Type selects the acceleration backend.
	// +kubebuilder:validation:Enum=vaapi;qsv;nvidia
	Type string `json:"type"`

	// DevicePath is the render device to mount for vaapi/qsv, e.g.
	// /dev/dri/renderD128. Ignored for nvidia (handled via device plugin).
	// +optional
	DevicePath string `json:"devicePath,omitempty"`

	// RenderGroupGID is added as a supplemental group so the container can access
	// the render device.
	// +optional
	RenderGroupGID *int64 `json:"renderGroupGID,omitempty"`

	// RuntimeClassName selects a runtime class for nvidia acceleration, e.g.
	// "nvidia".
	// +optional
	RuntimeClassName string `json:"runtimeClassName,omitempty"`
}
