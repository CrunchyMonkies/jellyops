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

// TranscodingSpec declaratively bounds transcode cache growth. Jellyfin ships with
// throttling and segment deletion disabled, so a single stream can transcode the
// whole file ahead of playback and fill the cache PVC. These settings live in
// Jellyfin's encoding configuration (encoding.xml), which the operator reconciles
// over the HTTP API — so this block requires spec.api to be configured.
type TranscodingSpec struct {
	// Throttle pauses a transcode once it is far enough ahead of the player,
	// bounding per-stream scratch to a rolling window instead of the whole file.
	// +optional
	Throttle *ThrottleSpec `json:"throttle,omitempty"`

	// SegmentDeletion removes already-played transcode segments during playback so
	// a single long stream cannot exhaust the cache PVC.
	// +optional
	SegmentDeletion *SegmentDeletionSpec `json:"segmentDeletion,omitempty"`
}

// ThrottleSpec maps to Jellyfin's EnableThrottling / ThrottleDelaySeconds.
type ThrottleSpec struct {
	// Enabled toggles Jellyfin's EnableThrottling. Leave unset to let Jellyfin's
	// current value stand (the operator only writes fields you explicitly set).
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// DelaySeconds sets ThrottleDelaySeconds — how far ahead of the player a
	// transcode may run before it is throttled. Optional; Jellyfin defaults to 180.
	// +kubebuilder:validation:Minimum=0
	// +optional
	DelaySeconds *int32 `json:"delaySeconds,omitempty"`
}

// SegmentDeletionSpec maps to Jellyfin's EnableSegmentDeletion / SegmentKeepSeconds.
type SegmentDeletionSpec struct {
	// Enabled toggles Jellyfin's EnableSegmentDeletion. Leave unset to let
	// Jellyfin's current value stand.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// KeepSeconds sets SegmentKeepSeconds — how many seconds of already-played
	// segments to retain before deletion. Optional; Jellyfin defaults to 720.
	// +kubebuilder:validation:Minimum=0
	// +optional
	KeepSeconds *int32 `json:"keepSeconds,omitempty"`
}
