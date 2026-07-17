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

// GeneralSpec declaratively manages Jellyfin's Dashboard → General server settings.
// These live in the root ServerConfiguration (system.xml), reconciled over the HTTP
// API (GET/POST /System/Configuration), so this block requires spec.api. Every field
// is an optional pointer: nil means "leave Jellyfin's current value untouched" — the
// operator only writes fields you explicitly set.
type GeneralSpec struct {
	// ServerName is the friendly server name shown to clients.
	// +optional
	ServerName *string `json:"serverName,omitempty"`

	// UICulture is the display language, e.g. "en-US".
	// +optional
	UICulture *string `json:"uiCulture,omitempty"`

	// QuickConnectAvailable toggles Quick Connect.
	// +optional
	QuickConnectAvailable *bool `json:"quickConnectAvailable,omitempty"`

	// EnableMetrics exposes the Prometheus metrics endpoint.
	// +optional
	EnableMetrics *bool `json:"enableMetrics,omitempty"`

	// EnableNormalizedItemByNameIds normalizes ItemByName ids.
	// +optional
	EnableNormalizedItemByNameIds *bool `json:"enableNormalizedItemByNameIds,omitempty"`

	// AllowClientLogUpload lets clients upload logs to the server.
	// +optional
	AllowClientLogUpload *bool `json:"allowClientLogUpload,omitempty"`

	// EnableSlowResponseWarning logs a warning for slow API responses.
	// +optional
	EnableSlowResponseWarning *bool `json:"enableSlowResponseWarning,omitempty"`

	// SlowResponseThresholdMs is the slow-response warning threshold in ms.
	// +kubebuilder:validation:Minimum=0
	// +optional
	SlowResponseThresholdMs *int64 `json:"slowResponseThresholdMs,omitempty"`

	// LibraryScanFanoutConcurrency bounds parallel library scan fanout (0 = auto).
	// +kubebuilder:validation:Minimum=0
	// +optional
	LibraryScanFanoutConcurrency *int32 `json:"libraryScanFanoutConcurrency,omitempty"`

	// LibraryMetadataRefreshConcurrency bounds parallel metadata refresh (0 = auto).
	// +kubebuilder:validation:Minimum=0
	// +optional
	LibraryMetadataRefreshConcurrency *int32 `json:"libraryMetadataRefreshConcurrency,omitempty"`

	// ParallelImageEncodingLimit bounds concurrent image encodes (0 = auto).
	// +kubebuilder:validation:Minimum=0
	// +optional
	ParallelImageEncodingLimit *int32 `json:"parallelImageEncodingLimit,omitempty"`

	// ActivityLogRetentionDays is how long to keep the activity log.
	// +kubebuilder:validation:Minimum=0
	// +optional
	ActivityLogRetentionDays *int32 `json:"activityLogRetentionDays,omitempty"`

	// LibraryMonitorDelay is the real-time monitor debounce in seconds.
	// +kubebuilder:validation:Minimum=0
	// +optional
	LibraryMonitorDelay *int32 `json:"libraryMonitorDelay,omitempty"`

	// LibraryUpdateDuration is the library update debounce in seconds.
	// +kubebuilder:validation:Minimum=0
	// +optional
	LibraryUpdateDuration *int32 `json:"libraryUpdateDuration,omitempty"`

	// InactiveSessionThreshold auto-closes idle sessions after N minutes (0 = disabled).
	// +kubebuilder:validation:Minimum=0
	// +optional
	InactiveSessionThreshold *int32 `json:"inactiveSessionThreshold,omitempty"`

	// LogFileRetentionDays is how long to keep log files.
	// +kubebuilder:validation:Minimum=0
	// +optional
	LogFileRetentionDays *int32 `json:"logFileRetentionDays,omitempty"`

	// CachePath overrides the server cache directory.
	// +optional
	CachePath *string `json:"cachePath,omitempty"`

	// MetadataPath overrides the server metadata directory.
	// +optional
	MetadataPath *string `json:"metadataPath,omitempty"`

	// CorsHosts is the allowed CORS origins list. Setting this (including an empty
	// list) replaces Jellyfin's list; omit the field to leave it untouched.
	// +optional
	CorsHosts []string `json:"corsHosts,omitempty"`
}
