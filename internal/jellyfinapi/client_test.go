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
	"net/http/httptest"
	"strings"
	"testing"
)

func newClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "test-device", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAuthHeader(t *testing.T) {
	c, _ := New("http://x", "dev-1", nil)
	if got := c.authHeader(); !strings.Contains(got, `Client="JellyOps"`) || !strings.Contains(got, `DeviceId="dev-1"`) || strings.Contains(got, "Token=") {
		t.Errorf("pre-token header wrong: %q", got)
	}
	c.SetToken("abc")
	if got := c.authHeader(); !strings.Contains(got, `Token="abc"`) {
		t.Errorf("post-token header missing token: %q", got)
	}
}

func TestBootstrapHappyPath(t *testing.T) {
	var createdUser, completed, createdKey, configPosted bool
	mux := http.NewServeMux()
	mux.HandleFunc("/Startup/Configuration", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			configPosted = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(StartupConfiguration{UICulture: "en-US"})
	})
	mux.HandleFunc("/Startup/User", func(w http.ResponseWriter, r *http.Request) { createdUser = true })
	mux.HandleFunc("/Startup/RemoteAccess", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/Startup/Complete", func(w http.ResponseWriter, r *http.Request) { completed = true })
	mux.HandleFunc("/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AuthenticationResult{AccessToken: "session-tok"})
	})
	mux.HandleFunc("/Auth/Keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			createdKey = true
			// Verify the authenticated session token is now on the request.
			if !strings.Contains(r.Header.Get("Authorization"), `Token="session-tok"`) {
				t.Errorf("key creation not authenticated: %q", r.Header.Get("Authorization"))
			}
			return
		}
		_ = json.NewEncoder(w).Encode(authKeysResult{Items: []AuthKey{{AppName: "JellyOps", AccessToken: "durable-key"}}})
	})

	c := newClient(t, mux)
	key, err := c.Bootstrap(context.Background(), "admin", "pw", "JellyOps")
	if err != nil {
		t.Fatal(err)
	}
	if key != "durable-key" {
		t.Errorf("key = %q", key)
	}
	if !configPosted || !createdUser || !completed || !createdKey {
		t.Errorf("wizard steps skipped: config=%v user=%v complete=%v key=%v", configPosted, createdUser, completed, createdKey)
	}
}

func TestBootstrapAlreadyConfigured(t *testing.T) {
	mux := http.NewServeMux()
	// Wizard endpoints unavailable once configured.
	mux.HandleFunc("/Startup/Configuration", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	mux.HandleFunc("/Startup/User", func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not create user on a configured instance")
	})
	mux.HandleFunc("/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AuthenticationResult{AccessToken: "s"})
	})
	mux.HandleFunc("/Auth/Keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(authKeysResult{Items: []AuthKey{{AppName: "JellyOps", AccessToken: "k"}}})
		}
	})

	c := newClient(t, mux)
	key, err := c.Bootstrap(context.Background(), "admin", "pw", "JellyOps")
	if err != nil {
		t.Fatal(err)
	}
	if key != "k" {
		t.Errorf("key = %q", key)
	}
}

func TestListVirtualFolders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/Library/VirtualFolders", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]VirtualFolder{{Name: "Movies", Locations: []string{"/media/movies"}}})
	})
	c := newClient(t, mux)
	folders, err := c.ListVirtualFolders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 || folders[0].Name != "Movies" {
		t.Errorf("folders = %+v", folders)
	}
}

func TestAPIErrorSurfaced(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/Library/Refresh", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	c := newClient(t, mux)
	err := c.RefreshLibraries(context.Background())
	if !IsStatus(err, http.StatusInternalServerError) {
		t.Errorf("expected 500 APIError, got %v", err)
	}
}

func TestDiffLibraries(t *testing.T) {
	desired := []DesiredLibrary{
		{Name: "Movies", Paths: []string{"/media/movies"}},
		{Name: "TV", Paths: []string{"/media/tv", "/media/tv2"}},
	}
	existing := []VirtualFolder{
		{Name: "TV", Locations: []string{"/media/tv", "/media/old"}},
		{Name: "Home Videos", Locations: []string{"/media/home"}}, // hand-created, not managed
		{Name: "Legacy", Locations: []string{"/media/legacy"}},    // managed, no longer desired
	}
	managed := []string{"TV", "Legacy"}

	diff := DiffLibraries(desired, existing, managed, true)

	// Movies is new.
	if len(diff.ToCreate) != 1 || diff.ToCreate[0].Name != "Movies" {
		t.Errorf("ToCreate = %+v", diff.ToCreate)
	}
	// TV needs /media/tv2 added and /media/old removed.
	if got := diff.PathsToAdd["TV"]; len(got) != 1 || got[0] != "/media/tv2" {
		t.Errorf("PathsToAdd[TV] = %v", got)
	}
	if got := diff.PathsToRemove["TV"]; len(got) != 1 || got[0] != "/media/old" {
		t.Errorf("PathsToRemove[TV] = %v", got)
	}
	// Legacy is managed + undesired → pruned. Home Videos is untouched.
	if len(diff.ToRemove) != 1 || diff.ToRemove[0] != "Legacy" {
		t.Errorf("ToRemove = %v (must prune only managed, never hand-created)", diff.ToRemove)
	}
	if !diff.Changed() {
		t.Error("Changed() should be true")
	}
}

func TestDiffLibrariesNoPruneWithoutFlag(t *testing.T) {
	existing := []VirtualFolder{{Name: "Legacy", Locations: []string{"/x"}}}
	diff := DiffLibraries(nil, existing, []string{"Legacy"}, false)
	if len(diff.ToRemove) != 0 {
		t.Errorf("prune disabled but ToRemove = %v", diff.ToRemove)
	}
}

func TestEnforceReadOnlyOptions(t *testing.T) {
	// Write-enabled current options (Jellyfin defaults) get the write flags off,
	// with unrelated fields preserved.
	cur := json.RawMessage(`{"SaveSubtitlesWithMedia":true,"MetadataSavers":null,"PreferredMetadataLanguage":"en"}`)
	out, changed, err := EnforceReadOnlyOptions(cur)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if v, ok := m["MetadataSavers"].([]any); !ok || len(v) != 0 {
		t.Errorf("MetadataSavers = %v, want []", m["MetadataSavers"])
	}
	if m["SaveSubtitlesWithMedia"] != false {
		t.Errorf("SaveSubtitlesWithMedia = %v, want false", m["SaveSubtitlesWithMedia"])
	}
	if m["SaveLocalMetadata"] != false {
		t.Errorf("SaveLocalMetadata missing/true")
	}
	if m["PreferredMetadataLanguage"] != "en" {
		t.Errorf("unrelated field not preserved: %v", m["PreferredMetadataLanguage"])
	}
}

func TestEnforceReadOnlyOptionsIdempotent(t *testing.T) {
	// Already-enforced options report no change.
	cur := json.RawMessage(`{"SaveLocalMetadata":false,"MetadataSavers":[],"SaveSubtitlesWithMedia":false,"SaveLyricsWithMedia":false,"SaveTrickplayWithMedia":false}`)
	_, changed, err := EnforceReadOnlyOptions(cur)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("expected changed=false for already-enforced options")
	}
}

func TestEnforceReadOnlyOptionsNullInput(t *testing.T) {
	for _, in := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(``)} {
		out, changed, err := EnforceReadOnlyOptions(in)
		if err != nil {
			t.Fatalf("input %q: %v", in, err)
		}
		if !changed {
			t.Errorf("input %q: expected changed=true", in)
		}
		if !strings.Contains(string(out), `"MetadataSavers":[]`) {
			t.Errorf("input %q: out = %s", in, out)
		}
	}
}

func TestMergeLibraryOptionsDeepMerge(t *testing.T) {
	cur := json.RawMessage(`{
		"EnableTrickplayImageExtraction":false,
		"TypeOptions":{
			"Type":"Movie",
			"MetadataFetchers":["TheMovieDb"]
		},
		"PreferredMetadataLanguage":"en"
	}`)
	desired := json.RawMessage(`{
		"EnableTrickplayImageExtraction":true,
		"TypeOptions":{
			"ImageFetchers":["TheMovieDb"]
		}
	}`)
	out, changed, err := MergeLibraryOptions(cur, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["EnableTrickplayImageExtraction"] != true {
		t.Errorf("scalar override failed: %s", out)
	}
	if got["PreferredMetadataLanguage"] != "en" {
		t.Errorf("unrelated field clobbered: %s", out)
	}
	to, ok := got["TypeOptions"].(map[string]any)
	if !ok {
		t.Fatalf("TypeOptions not a map: %s", out)
	}
	if to["Type"] != "Movie" {
		t.Errorf("nested existing key lost: %s", out)
	}
	if to["MetadataFetchers"] == nil {
		t.Errorf("nested existing array lost: %s", out)
	}
	if to["ImageFetchers"] == nil {
		t.Errorf("nested new key not merged: %s", out)
	}
}

func TestMergeLibraryOptionsNoChange(t *testing.T) {
	cur := json.RawMessage(`{"EnableTrickplayImageExtraction":true,"OtherField":"keep"}`)
	desired := json.RawMessage(`{"EnableTrickplayImageExtraction":true}`)
	out, changed, err := MergeLibraryOptions(cur, desired)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("expected changed=false, got out=%s", out)
	}
}

func TestMergeLibraryOptionsEmptyDesired(t *testing.T) {
	cur := json.RawMessage(`{"EnableTrickplayImageExtraction":false}`)
	for _, d := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(``)} {
		out, changed, err := MergeLibraryOptions(cur, d)
		if err != nil {
			t.Fatalf("desired %q: %v", d, err)
		}
		if changed {
			t.Errorf("desired %q: expected changed=false", d)
		}
		if string(out) != string(cur) {
			t.Errorf("desired %q: expected original returned, got %s", d, out)
		}
	}
}

func TestMergeLibraryOptionsNullCurrent(t *testing.T) {
	desired := json.RawMessage(`{"EnableTrickplayImageExtraction":true}`)
	for _, c := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(``)} {
		out, changed, err := MergeLibraryOptions(c, desired)
		if err != nil {
			t.Fatalf("current %q: %v", c, err)
		}
		if !changed {
			t.Errorf("current %q: expected changed=true", c)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("current %q: %v", c, err)
		}
		if got["EnableTrickplayImageExtraction"] != true {
			t.Errorf("current %q: out = %s", c, out)
		}
	}
}

func TestMergeLibraryOptionsArrayOverwrite(t *testing.T) {
	cur := json.RawMessage(`{"MetadataSavers":["Nfo"]}`)
	desired := json.RawMessage(`{"MetadataSavers":[]}`)
	out, changed, err := MergeLibraryOptions(cur, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	arr, ok := got["MetadataSavers"].([]any)
	if !ok || len(arr) != 0 {
		t.Errorf("array overwrite failed: %s", out)
	}
}
