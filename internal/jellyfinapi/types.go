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

// Package jellyfinapi is a small hand-rolled client for the subset of the
// Jellyfin HTTP API the operator needs: first-run bootstrap and virtual-folder
// (library) management.
package jellyfinapi

import "encoding/json"

// StartupConfiguration is returned by GET /Startup/Configuration.
type StartupConfiguration struct {
	UICulture         string `json:"UICulture,omitempty"`
	MetadataCountry   string `json:"MetadataCountryCode,omitempty"`
	PreferredLanguage string `json:"PreferredMetadataLanguage,omitempty"`
}

// AuthenticationResult is returned by POST /Users/AuthenticateByName.
type AuthenticationResult struct {
	AccessToken string `json:"AccessToken"`
	ServerID    string `json:"ServerId"`
	User        struct {
		ID   string `json:"Id"`
		Name string `json:"Name"`
	} `json:"User"`
}

// AuthKey is one entry from GET /Auth/Keys.
type AuthKey struct {
	AccessToken string `json:"AccessToken"`
	AppName     string `json:"AppName"`
}

// authKeysResult wraps GET /Auth/Keys.
type authKeysResult struct {
	Items []AuthKey `json:"Items"`
}

// VirtualFolder is one Jellyfin library from GET /Library/VirtualFolders.
type VirtualFolder struct {
	Name           string          `json:"Name"`
	CollectionType string          `json:"CollectionType,omitempty"`
	Locations      []string        `json:"Locations,omitempty"`
	ItemID         string          `json:"ItemId,omitempty"`
	LibraryOptions json.RawMessage `json:"LibraryOptions,omitempty"`
}

// DesiredLibrary is the operator's intent for one library.
type DesiredLibrary struct {
	Name           string
	CollectionType string
	Paths          []string
	Options        json.RawMessage
	// PreventWrites indicates the library's media is mounted read-only, so
	// Jellyfin must not be configured to write metadata/subtitles into it.
	PreventWrites bool
}

// LibraryDiff is the result of comparing desired vs existing libraries.
type LibraryDiff struct {
	// ToCreate are libraries that do not exist yet.
	ToCreate []DesiredLibrary
	// PathsToAdd maps an existing library name to paths missing from it.
	PathsToAdd map[string][]string
	// PathsToRemove maps an existing library name to stale paths to remove.
	PathsToRemove map[string][]string
	// ToRemove are managed libraries no longer desired (pruned only when enabled).
	ToRemove []string
}

// Changed reports whether the diff implies any mutation.
func (d LibraryDiff) Changed() bool {
	if len(d.ToCreate) > 0 || len(d.ToRemove) > 0 {
		return true
	}
	for _, v := range d.PathsToAdd {
		if len(v) > 0 {
			return true
		}
	}
	for _, v := range d.PathsToRemove {
		if len(v) > 0 {
			return true
		}
	}
	return false
}
