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

// PlaybackSpec declaratively manages Jellyfin's Dashboard → Playback settings that
// live in the root ServerConfiguration (resume behaviour + streaming bitrate cap),
// reconciled over the HTTP API (GET/POST /System/Configuration), so this block
// requires spec.api. Transcoding (encoding config) is managed separately via
// spec.transcoding. Every field is an optional pointer: nil means "leave Jellyfin's
// current value untouched".
type PlaybackSpec struct {
	// MinResumePct is the minimum played percentage before a resume point is stored.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	MinResumePct *int32 `json:"minResumePct,omitempty"`

	// MaxResumePct is the maximum played percentage still treated as resumable
	// (beyond it the item is considered watched).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	MaxResumePct *int32 `json:"maxResumePct,omitempty"`

	// MinResumeDurationSeconds is the minimum item length (seconds) to store a resume point.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinResumeDurationSeconds *int32 `json:"minResumeDurationSeconds,omitempty"`

	// MinAudiobookResumeMinutes maps ServerConfiguration.MinAudiobookResume.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinAudiobookResumeMinutes *int32 `json:"minAudiobookResumeMinutes,omitempty"`

	// MaxAudiobookResumeMinutes maps ServerConfiguration.MaxAudiobookResume.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxAudiobookResumeMinutes *int32 `json:"maxAudiobookResumeMinutes,omitempty"`

	// RemoteClientBitrateLimit caps remote (internet) client bitrate in bps (0 = unlimited).
	// +kubebuilder:validation:Minimum=0
	// +optional
	RemoteClientBitrateLimit *int32 `json:"remoteClientBitrateLimit,omitempty"`
}
