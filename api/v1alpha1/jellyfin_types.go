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

// JellyfinSpec defines the desired state of a Jellyfin instance.
// +kubebuilder:validation:XValidation:rule="!(has(self.ingress) && has(self.gateway))",message="ingress and gateway are mutually exclusive"
// +kubebuilder:validation:XValidation:rule="!has(self.transcoding) || has(self.api)",message="transcoding requires api to be configured (encoding settings are applied over the HTTP API)"
type JellyfinSpec struct {
	// Image is the Jellyfin server container image. Defaults to a stock/official
	// Jellyfin image when empty. Override only when a plugin requires an
	// ABI-incompatible server build (see spec §4.4).
	// +optional
	Image string `json:"image,omitempty"`

	// Replicas is the number of Jellyfin pods. Jellyfin is not active-active, so
	// this is typically 1.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Storage configures the config/cache PVCs and media library folders.
	// +optional
	Storage JellyfinStorage `json:"storage,omitempty"`

	// Service configures the ClusterIP/LoadBalancer Service fronting :8096.
	// +optional
	Service ServiceSpec `json:"service,omitempty"`

	// Ingress optionally exposes the instance over HTTP(S). Mutually exclusive
	// with Gateway.
	// +optional
	Ingress *IngressSpec `json:"ingress,omitempty"`

	// Web deploys a separate web-tier (jellyfin-web/nginx) Deployment and Service.
	// +optional
	Web *WebSpec `json:"web,omitempty"`

	// Gateway configures a Gateway API HTTPRoute. Mutually exclusive with Ingress.
	// +optional
	Gateway *GatewaySpec `json:"gateway,omitempty"`

	// Resources sets the Jellyfin container resource requests/limits.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// HardwareAcceleration optionally attaches a GPU/VAAPI device for transcoding.
	// +optional
	HardwareAcceleration *HardwareAccel `json:"hardwareAcceleration,omitempty"`

	// API enables day-2 reconciliation of in-app state (libraries) via Jellyfin's
	// HTTP API on :8096 (see spec §7.6).
	// +optional
	API *JellyfinAPISpec `json:"api,omitempty"`

	// Transcoding bounds transcode cache growth (throttling + segment deletion) by
	// reconciling Jellyfin's encoding configuration over the HTTP API. Requires the
	// api block (see spec §7.6); an XValidation rule enforces this.
	// +optional
	Transcoding *TranscodingSpec `json:"transcoding,omitempty"`

	// PluginSelector selects JellyfinPlugins bound to this instance by label, in
	// addition to plugins that reference the instance directly via jellyfinRef.
	// +optional
	PluginSelector *metav1.LabelSelector `json:"pluginSelector,omitempty"`

	// PodAnnotations are merged onto the Jellyfin pod template metadata.
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// ImagePullSecrets are attached to the Jellyfin pod for pulling the server
	// and plugin images.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
}

// JellyfinStatus defines the observed state of a Jellyfin instance.
type JellyfinStatus struct {
	// Phase is a coarse, human-oriented lifecycle summary.
	// +optional
	Phase string `json:"phase,omitempty"`

	// ObservedGeneration is the .metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Endpoints reports the in-cluster and external addresses of the instance.
	// +optional
	Endpoints InstanceEndpoints `json:"endpoints,omitempty"`

	// LoadedPlugins lists the plugins currently mounted into the running pod
	// template.
	// +listType=map
	// +listMapKey=name
	// +optional
	LoadedPlugins []LoadedPlugin `json:"loadedPlugins,omitempty"`

	// APICredentialsSecret is the name of the operator-owned Secret holding the
	// admin API key minted during bootstrap. The key itself is never surfaced.
	// +optional
	APICredentialsSecret string `json:"apiCredentialsSecret,omitempty"`

	// ManagedLibraries is the set of Jellyfin virtual-folder names the operator
	// currently manages. Prune only ever removes names from this set.
	// +optional
	ManagedLibraries []string `json:"managedLibraries,omitempty"`

	// Conditions represent the current state of the Jellyfin resource
	// (Ready, PluginsLoaded, APIReady, LibrariesReady).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// InstanceEndpoints reports how the instance can be reached.
type InstanceEndpoints struct {
	// Service is the in-cluster URL (http://<svc>.<ns>.svc:8096) the operator uses.
	// +optional
	Service string `json:"service,omitempty"`

	// Ingress is the external URL when an Ingress is configured.
	// +optional
	Ingress string `json:"ingress,omitempty"`
}

// LoadedPlugin records a plugin mounted into the instance pod template.
type LoadedPlugin struct {
	// Name is the JellyfinPlugin resource name.
	Name string `json:"name"`

	// PluginName is the display name from meta.json / spec.meta.name.
	// +optional
	PluginName string `json:"pluginName,omitempty"`

	// Version is the plugin version currently staged.
	// +optional
	Version string `json:"version,omitempty"`

	// GUID is the plugin GUID.
	// +optional
	GUID string `json:"guid,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=jf
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Jellyfin is the Schema for the jellyfins API.
type Jellyfin struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Jellyfin
	// +required
	Spec JellyfinSpec `json:"spec"`

	// status defines the observed state of Jellyfin
	// +optional
	Status JellyfinStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// JellyfinList contains a list of Jellyfin.
type JellyfinList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Jellyfin `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Jellyfin{}, &JellyfinList{})
		return nil
	})
}
