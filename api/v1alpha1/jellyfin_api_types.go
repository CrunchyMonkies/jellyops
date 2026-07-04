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

// JellyfinAPISpec configures how the operator drives the running Jellyfin
// instance over its HTTP API on :8096 (spec §7.6).
type JellyfinAPISpec struct {
	// Mode selects how the operator obtains an admin API key.
	//   provided  - read an existing key (or username/password) from
	//               credentialsSecret.
	//   bootstrap - drive the first-run wizard, mint a key, and persist it into
	//               generatedSecretName.
	// +kubebuilder:validation:Enum=provided;bootstrap
	// +kubebuilder:default=bootstrap
	// +optional
	Mode string `json:"mode,omitempty"`

	// CredentialsSecret holds admin credentials. Keys: apiKey, or
	// username + password.
	// +optional
	CredentialsSecret *corev1.LocalObjectReference `json:"credentialsSecret,omitempty"`

	// GeneratedSecretName is the operator-owned Secret where a bootstrapped API
	// key is stored so bootstrap is idempotent and survives restarts.
	// +optional
	GeneratedSecretName string `json:"generatedSecretName,omitempty"`

	// ManageLibraries enables reconciliation of Jellyfin virtual folders against
	// storage.media[].library.
	// +optional
	ManageLibraries bool `json:"manageLibraries,omitempty"`

	// Prune removes managed libraries that are no longer declared. Only libraries
	// previously created by the operator are ever removed.
	// +optional
	Prune bool `json:"prune,omitempty"`

	// RefreshLibraryOnChange triggers a Jellyfin library scan after any change.
	// +optional
	RefreshLibraryOnChange bool `json:"refreshLibraryOnChange,omitempty"`
}
