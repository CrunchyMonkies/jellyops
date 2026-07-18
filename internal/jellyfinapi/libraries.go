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
	"net/url"
	"sort"
	"strings"
)

// ListVirtualFolders returns the instance's libraries.
func (c *Client) ListVirtualFolders(ctx context.Context) ([]VirtualFolder, error) {
	var folders []VirtualFolder
	err := c.do(ctx, http.MethodGet, "/Library/VirtualFolders", nil, nil, &folders)
	return folders, err
}

// AddVirtualFolder creates a library with the given paths and options.
func (c *Client) AddVirtualFolder(ctx context.Context, lib DesiredLibrary, refresh bool) error {
	q := url.Values{}
	q.Set("name", lib.Name)
	if lib.CollectionType != "" {
		q.Set("collectionType", lib.CollectionType)
	}
	for _, p := range lib.Paths {
		q.Add("paths", p)
	}
	q.Set("refreshLibrary", boolStr(refresh))
	var body any
	if len(lib.Options) > 0 {
		body = map[string]any{"LibraryOptions": lib.Options}
	}
	return c.do(ctx, http.MethodPost, "/Library/VirtualFolders", q, body, nil)
}

// RemoveVirtualFolder deletes a library by name.
func (c *Client) RemoveVirtualFolder(ctx context.Context, name string, refresh bool) error {
	q := url.Values{"name": []string{name}, "refreshLibrary": []string{boolStr(refresh)}}
	return c.do(ctx, http.MethodDelete, "/Library/VirtualFolders", q, nil, nil)
}

// AddMediaPath adds a path to an existing library.
func (c *Client) AddMediaPath(ctx context.Context, name, path string, refresh bool) error {
	q := url.Values{"refreshLibrary": []string{boolStr(refresh)}}
	body := map[string]any{"Name": name, "PathInfo": map[string]string{"Path": path}}
	return c.do(ctx, http.MethodPost, "/Library/VirtualFolders/Paths", q, body, nil)
}

// RemoveMediaPath removes a path from an existing library.
func (c *Client) RemoveMediaPath(ctx context.Context, name, path string, refresh bool) error {
	q := url.Values{"name": []string{name}, "path": []string{path}, "refreshLibrary": []string{boolStr(refresh)}}
	return c.do(ctx, http.MethodDelete, "/Library/VirtualFolders/Paths", q, nil, nil)
}

// UpdateLibraryOptions replaces a library's options (POST
// /Library/VirtualFolders/LibraryOptions). id is the library's ItemId.
func (c *Client) UpdateLibraryOptions(ctx context.Context, id string, options json.RawMessage) error {
	body := map[string]any{"Id": id, "LibraryOptions": options}
	return c.do(ctx, http.MethodPost, "/Library/VirtualFolders/LibraryOptions", nil, body, nil)
}

// RefreshLibraries triggers a full library scan.
func (c *Client) RefreshLibraries(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/Library/Refresh", nil, nil, nil)
}

// readOnlyLibraryOptions are the LibraryOptions fields the operator forces off so
// Jellyfin never writes metadata/subtitles into a read-only media folder.
// MetadataSavers=[] (explicit empty) stops all savers, incl. the NFO saver that
// runs when the field is null (inheriting the server defaults).
var readOnlyLibraryOptions = map[string]json.RawMessage{
	"SaveLocalMetadata":      json.RawMessage(`false`),
	"MetadataSavers":         json.RawMessage(`[]`),
	"SaveSubtitlesWithMedia": json.RawMessage(`false`),
	"SaveLyricsWithMedia":    json.RawMessage(`false`),
	"SaveTrickplayWithMedia": json.RawMessage(`false`),
}

// EnforceReadOnlyOptions overlays the write-disabling fields onto a library's
// current LibraryOptions, preserving every other field. It returns the updated
// options and whether anything actually changed. A nil/empty current is treated
// as an empty object.
func EnforceReadOnlyOptions(current json.RawMessage) (json.RawMessage, bool, error) {
	opts := map[string]json.RawMessage{}
	if len(bytesTrim(current)) > 0 {
		if err := json.Unmarshal(current, &opts); err != nil {
			return nil, false, err
		}
	}
	changed := false
	for k, v := range readOnlyLibraryOptions {
		if cur, ok := opts[k]; !ok || !jsonEqual(cur, v) {
			opts[k] = v
			changed = true
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

// MergeLibraryOptions deep-merges the desired options onto the current library
// options. For nested JSON objects both sides share, it recurses so sub-keys
// (e.g. TypeOptions) are merged rather than clobbered. Arrays and scalars in
// desired overwrite current. Returns the merged JSON and whether anything
// changed. An empty/nil desired returns current unchanged.
func MergeLibraryOptions(current, desired json.RawMessage) (json.RawMessage, bool, error) {
	if len(bytesTrim(desired)) == 0 {
		return current, false, nil
	}
	var curMap map[string]any
	if len(bytesTrim(current)) > 0 {
		if err := json.Unmarshal(current, &curMap); err != nil {
			return nil, false, err
		}
	}
	if curMap == nil {
		curMap = map[string]any{}
	}
	var desMap map[string]any
	if err := json.Unmarshal(desired, &desMap); err != nil {
		return nil, false, err
	}

	deepMerge(curMap, desMap)

	merged, err := json.Marshal(curMap)
	if err != nil {
		return nil, false, err
	}
	// Compare by re-marshalling original current so whitespace differences
	// don't trigger a spurious update.
	origNorm, _ := json.Marshal(jsonToMap(current))
	changed := string(merged) != string(origNorm)
	if !changed {
		return current, false, nil
	}
	return merged, true, nil
}

// deepMerge recursively sets every key from src onto dst. When both dst and src
// hold a JSON object (map[string]any) for the same key, the function recurses;
// otherwise the src value wins outright.
func deepMerge(dst, src map[string]any) {
	for k, sv := range src {
		dv, exists := dst[k]
		if exists {
			if dm, dok := dv.(map[string]any); dok {
				if sm, sok := sv.(map[string]any); sok {
					deepMerge(dm, sm)
					continue
				}
			}
		}
		dst[k] = sv
	}
}

// jsonToMap unmarshals a JSON blob into a map for normalised comparison.
// Returns an empty map on nil/empty/error.
func jsonToMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if len(bytesTrim(raw)) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func bytesTrim(b json.RawMessage) []byte {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		return nil
	}
	return []byte(s)
}

// jsonEqual compares two raw JSON values by their decoded form so that, e.g.,
// `false` and ` false ` (or key-ordered objects) compare equal.
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	am, err1 := json.Marshal(av)
	bm, err2 := json.Marshal(bv)
	return err1 == nil && err2 == nil && string(am) == string(bm)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// DiffLibraries compares the desired libraries against what exists on the
// instance. managed is the set of library names the operator previously managed;
// prune enables removal of managed libraries that are no longer desired.
// Libraries not in the managed set are never touched (protects hand-created ones).
func DiffLibraries(desired []DesiredLibrary, existing []VirtualFolder, managed []string, prune bool) LibraryDiff {
	existingByName := make(map[string]VirtualFolder, len(existing))
	for _, f := range existing {
		existingByName[f.Name] = f
	}
	desiredByName := make(map[string]DesiredLibrary, len(desired))

	diff := LibraryDiff{
		PathsToAdd:    map[string][]string{},
		PathsToRemove: map[string][]string{},
	}

	for _, d := range desired {
		desiredByName[d.Name] = d
		cur, ok := existingByName[d.Name]
		if !ok {
			diff.ToCreate = append(diff.ToCreate, d)
			continue
		}
		have := toSet(cur.Locations)
		want := toSet(d.Paths)
		for _, p := range d.Paths {
			if !have[p] {
				diff.PathsToAdd[d.Name] = append(diff.PathsToAdd[d.Name], p)
			}
		}
		for _, p := range cur.Locations {
			if !want[p] {
				diff.PathsToRemove[d.Name] = append(diff.PathsToRemove[d.Name], p)
			}
		}
	}

	if prune {
		managedSet := toSet(managed)
		for _, f := range existing {
			if _, want := desiredByName[f.Name]; !want && managedSet[f.Name] {
				diff.ToRemove = append(diff.ToRemove, f.Name)
			}
		}
		sort.Strings(diff.ToRemove)
	}

	// Sort create list for determinism.
	sort.Slice(diff.ToCreate, func(i, j int) bool { return diff.ToCreate[i].Name < diff.ToCreate[j].Name })
	return diff
}

func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
