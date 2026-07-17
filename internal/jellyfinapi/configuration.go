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

package jellyfinapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
)

// Jellyfin configuration stores. Both are full-object replace: GET returns the whole
// object, POST overwrites it, so callers must overlay managed fields onto the current
// object (see overlayConfig) rather than PATCH individual keys.
const (
	// serverConfigPath is the root ServerConfiguration (system.xml) — General and
	// Playback (resume/streaming) settings live here.
	serverConfigPath = "/System/Configuration"
	// brandingConfigPath is the "branding" named configuration.
	brandingConfigPath = "/System/Configuration/branding"
)

// GetServerConfig returns the root ServerConfiguration as raw JSON so unmanaged
// fields round-trip untouched.
func (c *Client) GetServerConfig(ctx context.Context) (json.RawMessage, error) {
	var cfg json.RawMessage
	if err := c.do(ctx, http.MethodGet, serverConfigPath, nil, nil, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// UpdateServerConfig replaces the root ServerConfiguration. cfg must be a complete
// object (overlay managed fields onto GetServerConfig via EnforceServerConfig).
func (c *Client) UpdateServerConfig(ctx context.Context, cfg json.RawMessage) error {
	return c.do(ctx, http.MethodPost, serverConfigPath, nil, cfg, nil)
}

// GetBrandingConfig returns the branding named configuration as raw JSON.
func (c *Client) GetBrandingConfig(ctx context.Context) (json.RawMessage, error) {
	var cfg json.RawMessage
	if err := c.do(ctx, http.MethodGet, brandingConfigPath, nil, nil, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// UpdateBrandingConfig replaces the branding named configuration. cfg must be a
// complete object (overlay onto GetBrandingConfig via EnforceBranding) so the
// server-managed SplashscreenLocation is preserved.
func (c *Client) UpdateBrandingConfig(ctx context.Context, cfg json.RawMessage) error {
	return c.do(ctx, http.MethodPost, brandingConfigPath, nil, cfg, nil)
}

// overlayConfig merges the managed key/value pairs onto the instance's current config,
// preserving every unmanaged key, and reports whether anything changed. A nil/empty/
// `null` current is treated as an empty object. Keys are applied in sorted order for
// determinism. This is the shared engine behind EnforceServerConfig/EnforceBranding
// (mirrors EnforceEncodingOptions) so the full-object POST never clobbers unmanaged
// settings.
func overlayConfig(current json.RawMessage, managed map[string]any) (json.RawMessage, bool, error) {
	opts := map[string]json.RawMessage{}
	if len(bytesTrim(current)) > 0 {
		if err := json.Unmarshal(current, &opts); err != nil {
			return nil, false, err
		}
	}

	keys := make([]string, 0, len(managed))
	for k := range managed {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	changed := false
	for _, key := range keys {
		raw, err := json.Marshal(managed[key])
		if err != nil {
			return nil, false, err
		}
		if cur, ok := opts[key]; ok && jsonEqual(cur, raw) {
			continue
		}
		opts[key] = raw
		changed = true
	}

	if !changed {
		return current, false, nil
	}
	out, err := json.Marshal(opts)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// DesiredServerConfig is the set of ServerConfiguration fields the operator manages
// (Dashboard General + Playback). A nil pointer means "leave Jellyfin's current value
// untouched". Keys match Jellyfin's ServerConfiguration JSON property names.
type DesiredServerConfig struct {
	// General
	ServerName                        *string
	UICulture                         *string
	QuickConnectAvailable             *bool
	EnableMetrics                     *bool
	EnableNormalizedItemByNameIds     *bool
	AllowClientLogUpload              *bool
	EnableSlowResponseWarning         *bool
	SlowResponseThresholdMs           *int64
	LibraryScanFanoutConcurrency      *int32
	LibraryMetadataRefreshConcurrency *int32
	ParallelImageEncodingLimit        *int32
	ActivityLogRetentionDays          *int32
	LibraryMonitorDelay               *int32
	LibraryUpdateDuration             *int32
	InactiveSessionThreshold          *int32
	LogFileRetentionDays              *int32
	CachePath                         *string
	MetadataPath                      *string
	CorsHosts                         []string
	// Playback
	MinResumePct             *int32
	MaxResumePct             *int32
	MinResumeDurationSeconds *int32
	MinAudiobookResume       *int32
	MaxAudiobookResume       *int32
	RemoteClientBitrateLimit *int32
}

func (d DesiredServerConfig) managed() map[string]any {
	m := map[string]any{}
	put := func(key string, v any) {
		if v != nil {
			m[key] = v
		}
	}
	// Dereference pointers so json.Marshal emits scalars, not pointers.
	if d.ServerName != nil {
		put("ServerName", *d.ServerName)
	}
	if d.UICulture != nil {
		put("UICulture", *d.UICulture)
	}
	if d.QuickConnectAvailable != nil {
		put("QuickConnectAvailable", *d.QuickConnectAvailable)
	}
	if d.EnableMetrics != nil {
		put("EnableMetrics", *d.EnableMetrics)
	}
	if d.EnableNormalizedItemByNameIds != nil {
		put("EnableNormalizedItemByNameIds", *d.EnableNormalizedItemByNameIds)
	}
	if d.AllowClientLogUpload != nil {
		put("AllowClientLogUpload", *d.AllowClientLogUpload)
	}
	if d.EnableSlowResponseWarning != nil {
		put("EnableSlowResponseWarning", *d.EnableSlowResponseWarning)
	}
	if d.SlowResponseThresholdMs != nil {
		put("SlowResponseThresholdMs", *d.SlowResponseThresholdMs)
	}
	if d.LibraryScanFanoutConcurrency != nil {
		put("LibraryScanFanoutConcurrency", *d.LibraryScanFanoutConcurrency)
	}
	if d.LibraryMetadataRefreshConcurrency != nil {
		put("LibraryMetadataRefreshConcurrency", *d.LibraryMetadataRefreshConcurrency)
	}
	if d.ParallelImageEncodingLimit != nil {
		put("ParallelImageEncodingLimit", *d.ParallelImageEncodingLimit)
	}
	if d.ActivityLogRetentionDays != nil {
		put("ActivityLogRetentionDays", *d.ActivityLogRetentionDays)
	}
	if d.LibraryMonitorDelay != nil {
		put("LibraryMonitorDelay", *d.LibraryMonitorDelay)
	}
	if d.LibraryUpdateDuration != nil {
		put("LibraryUpdateDuration", *d.LibraryUpdateDuration)
	}
	if d.InactiveSessionThreshold != nil {
		put("InactiveSessionThreshold", *d.InactiveSessionThreshold)
	}
	if d.LogFileRetentionDays != nil {
		put("LogFileRetentionDays", *d.LogFileRetentionDays)
	}
	if d.CachePath != nil {
		put("CachePath", *d.CachePath)
	}
	if d.MetadataPath != nil {
		put("MetadataPath", *d.MetadataPath)
	}
	if d.CorsHosts != nil {
		put("CorsHosts", d.CorsHosts)
	}
	if d.MinResumePct != nil {
		put("MinResumePct", *d.MinResumePct)
	}
	if d.MaxResumePct != nil {
		put("MaxResumePct", *d.MaxResumePct)
	}
	if d.MinResumeDurationSeconds != nil {
		put("MinResumeDurationSeconds", *d.MinResumeDurationSeconds)
	}
	if d.MinAudiobookResume != nil {
		put("MinAudiobookResume", *d.MinAudiobookResume)
	}
	if d.MaxAudiobookResume != nil {
		put("MaxAudiobookResume", *d.MaxAudiobookResume)
	}
	if d.RemoteClientBitrateLimit != nil {
		put("RemoteClientBitrateLimit", *d.RemoteClientBitrateLimit)
	}
	return m
}

// Empty reports whether no managed field is set (nothing to reconcile).
func (d DesiredServerConfig) Empty() bool { return len(d.managed()) == 0 }

// EnforceServerConfig overlays the managed ServerConfiguration fields onto the
// instance's current config, preserving every other field.
func EnforceServerConfig(current json.RawMessage, desired DesiredServerConfig) (json.RawMessage, bool, error) {
	return overlayConfig(current, desired.managed())
}

// DesiredBranding is the set of branding fields the operator manages. A nil pointer
// means "leave Jellyfin's current value untouched". SplashscreenLocation is never
// managed (round-tripped untouched by the overlay).
type DesiredBranding struct {
	LoginDisclaimer     *string
	CustomCss           *string
	SplashscreenEnabled *bool
}

func (d DesiredBranding) managed() map[string]any {
	m := map[string]any{}
	if d.LoginDisclaimer != nil {
		m["LoginDisclaimer"] = *d.LoginDisclaimer
	}
	if d.CustomCss != nil {
		m["CustomCss"] = *d.CustomCss
	}
	if d.SplashscreenEnabled != nil {
		m["SplashscreenEnabled"] = *d.SplashscreenEnabled
	}
	return m
}

// Empty reports whether no managed field is set (nothing to reconcile).
func (d DesiredBranding) Empty() bool { return len(d.managed()) == 0 }

// EnforceBranding overlays the managed branding fields onto the instance's current
// branding config, preserving SplashscreenLocation and any other field.
func EnforceBranding(current json.RawMessage, desired DesiredBranding) (json.RawMessage, bool, error) {
	return overlayConfig(current, desired.managed())
}
