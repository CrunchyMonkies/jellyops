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

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Injection modes for delivering plugin files into the Jellyfin pod.
const (
	// InjectionImageVolume mounts the plugin image read-only directly at
	// /config/plugins/<Name_Version>.
	InjectionImageVolume = "imageVolume"
	// InjectionImageVolumeCopy mounts the image read-only and copies it into the
	// writable plugins dir via a staging init container (so Jellyfin can mutate
	// meta.json at runtime).
	InjectionImageVolumeCopy = "imageVolumeCopy"
)

// Install failure policies.
const (
	// FailurePolicyIgnore lets Jellyfin boot even if the install script fails.
	FailurePolicyIgnore = "Ignore"
	// FailurePolicyFail blocks pod startup on a non-zero install exit.
	FailurePolicyFail = "Fail"
)

// JellyfinPluginStatus phases.
const (
	PluginPhasePending        = "Pending"
	PluginPhaseInjecting      = "Injecting"
	PluginPhaseInstalling     = "Installing"
	PluginPhaseWorkloadsReady = "WorkloadsReady"
	PluginPhaseLoaded         = "Loaded"
	PluginPhaseFailed         = "Failed"
	PluginPhaseDisabled       = "Disabled"
)

// JellyfinPluginSpec defines the desired state of a JellyfinPlugin.
type JellyfinPluginSpec struct {
	// JellyfinRef binds this plugin to a Jellyfin instance by name in the same
	// namespace. When empty, the instance's pluginSelector labels bind it.
	// +optional
	JellyfinRef *corev1.LocalObjectReference `json:"jellyfinRef,omitempty"`

	// PluginImage is the OCI image whose filesystem contains the plugin directory
	// (DLLs + meta.json).
	// +required
	PluginImage ImageSource `json:"pluginImage"`

	// Meta describes the plugin identity. When absent, the operator falls back to
	// image labels / meta.json. CR-provided meta always takes precedence.
	// +optional
	Meta PluginMeta `json:"meta,omitempty"`

	// Injection selects how plugin files reach the Jellyfin pod.
	// +kubebuilder:validation:Enum=imageVolume;imageVolumeCopy
	// +kubebuilder:default=imageVolume
	// +optional
	Injection string `json:"injection,omitempty"`

	// Install is a pre-start setup script run as an init container before the
	// Jellyfin container starts (and after imageVolumeCopy staging).
	// +optional
	Install *PluginInstall `json:"install,omitempty"`

	// Workloads are companion Deployments the operator manages for this plugin
	// (e.g. jellycode transcoding workers).
	// +listType=map
	// +listMapKey=name
	// +optional
	Workloads []PluginWorkload `json:"workloads,omitempty"`

	// Services are companion Services fronting the plugin's workloads or the
	// Jellyfin pod (e.g. a gRPC endpoint).
	// +listType=map
	// +listMapKey=name
	// +optional
	Services []PluginService `json:"services,omitempty"`
}

// JellyfinPluginStatus defines the observed state of a JellyfinPlugin.
type JellyfinPluginStatus struct {
	// Phase is a coarse lifecycle summary:
	// Pending|Injecting|Installing|WorkloadsReady|Loaded|Failed|Disabled.
	// +optional
	Phase string `json:"phase,omitempty"`

	// ObservedGeneration is the .metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Injected is true when plugin files are present in the running pod template.
	// +optional
	Injected bool `json:"injected,omitempty"`

	// Installed is true when the install script completed successfully (or was
	// not declared).
	// +optional
	Installed bool `json:"installed,omitempty"`

	// ABICompatible reports whether meta.targetAbi matches the instance server
	// version.
	// +optional
	ABICompatible bool `json:"abiCompatible,omitempty"`

	// LoadedVersion is the plugin version currently staged into the instance.
	// +optional
	LoadedVersion string `json:"loadedVersion,omitempty"`

	// WorkloadReadyReplicas is the total ready replicas across companion
	// workloads.
	// +optional
	WorkloadReadyReplicas int32 `json:"workloadReadyReplicas,omitempty"`

	// Conditions represent the current state of the JellyfinPlugin resource
	// (ABICompatible, Injected, Installed, WorkersAvailable).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=jfplugin
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.loadedVersion`
// +kubebuilder:printcolumn:name="ABI",type=string,JSONPath=`.status.conditions[?(@.type=="ABICompatible")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// JellyfinPlugin is the Schema for the jellyfinplugins API.
type JellyfinPlugin struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of JellyfinPlugin
	// +required
	Spec JellyfinPluginSpec `json:"spec"`

	// status defines the observed state of JellyfinPlugin
	// +optional
	Status JellyfinPluginStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// JellyfinPluginList contains a list of JellyfinPlugin.
type JellyfinPluginList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []JellyfinPlugin `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &JellyfinPlugin{}, &JellyfinPluginList{})
		return nil
	})
}
