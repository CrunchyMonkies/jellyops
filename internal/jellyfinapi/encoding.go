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
)

// encodingConfigPath is Jellyfin's named encoding configuration store. GET returns
// the current EncodingOptions object; POST replaces it wholesale (full-object
// replace), so callers must round-trip the whole object rather than PATCH a field.
const encodingConfigPath = "/System/Configuration/encoding"

// GetEncodingConfig returns the instance's current encoding configuration as raw
// JSON so unmanaged fields (QSV/VAAPI device paths, tone-mapping, etc.) can be
// round-tripped untouched.
func (c *Client) GetEncodingConfig(ctx context.Context) (json.RawMessage, error) {
	var cfg json.RawMessage
	if err := c.do(ctx, http.MethodGet, encodingConfigPath, nil, nil, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// UpdateEncodingConfig replaces the instance's encoding configuration. Because the
// endpoint is a full-object replace, cfg must be a complete object — produced by
// overlaying managed fields onto GetEncodingConfig via EnforceEncodingOptions.
func (c *Client) UpdateEncodingConfig(ctx context.Context, cfg json.RawMessage) error {
	return c.do(ctx, http.MethodPost, encodingConfigPath, nil, cfg, nil)
}

// DesiredEncoding is the set of transcode-cache fields the operator manages. A nil
// pointer means "leave Jellyfin's current value untouched"; the operator never
// writes a field the user did not explicitly declare.
type DesiredEncoding struct {
	EnableThrottling      *bool
	ThrottleDelaySeconds  *int32
	EnableSegmentDeletion *bool
	SegmentKeepSeconds    *int32
}

// Empty reports whether no managed field is set (nothing to reconcile).
func (d DesiredEncoding) Empty() bool {
	return d.EnableThrottling == nil && d.ThrottleDelaySeconds == nil &&
		d.EnableSegmentDeletion == nil && d.SegmentKeepSeconds == nil
}

// EnforceEncodingOptions overlays the managed transcode fields onto the instance's
// current encoding config, preserving every other field, and reports whether
// anything changed. A nil/empty/`null` current is treated as an empty object. This
// mirrors EnforceReadOnlyOptions so the full-object POST never clobbers unmanaged
// encoding settings.
func EnforceEncodingOptions(current json.RawMessage, desired DesiredEncoding) (json.RawMessage, bool, error) {
	opts := map[string]json.RawMessage{}
	if len(bytesTrim(current)) > 0 {
		if err := json.Unmarshal(current, &opts); err != nil {
			return nil, false, err
		}
	}

	changed := false
	set := func(key string, val any) error {
		raw, err := json.Marshal(val)
		if err != nil {
			return err
		}
		if cur, ok := opts[key]; ok && jsonEqual(cur, raw) {
			return nil
		}
		opts[key] = raw
		changed = true
		return nil
	}

	if desired.EnableThrottling != nil {
		if err := set("EnableThrottling", *desired.EnableThrottling); err != nil {
			return nil, false, err
		}
	}
	if desired.ThrottleDelaySeconds != nil {
		if err := set("ThrottleDelaySeconds", *desired.ThrottleDelaySeconds); err != nil {
			return nil, false, err
		}
	}
	if desired.EnableSegmentDeletion != nil {
		if err := set("EnableSegmentDeletion", *desired.EnableSegmentDeletion); err != nil {
			return nil, false, err
		}
	}
	if desired.SegmentKeepSeconds != nil {
		if err := set("SegmentKeepSeconds", *desired.SegmentKeepSeconds); err != nil {
			return nil, false, err
		}
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
