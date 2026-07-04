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
	"net/http"
	"net/url"
	"sort"
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

// RefreshLibraries triggers a full library scan.
func (c *Client) RefreshLibraries(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/Library/Refresh", nil, nil, nil)
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
