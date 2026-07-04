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

package plugins

import (
	"testing"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

func TestPluginFolderName(t *testing.T) {
	cases := []struct {
		name string
		meta jellyfinv1alpha1.PluginMeta
		want string
	}{
		{"name and version", jellyfinv1alpha1.PluginMeta{Name: "Distributed Transcoding", Version: "0.0.1.0"}, "Distributed Transcoding_0.0.1.0"},
		{"no version", jellyfinv1alpha1.PluginMeta{Name: "Foo"}, "Foo"},
		{"empty", jellyfinv1alpha1.PluginMeta{}, "plugin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PluginFolderName(tc.meta); got != tc.want {
				t.Errorf("PluginFolderName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPluginSubPath(t *testing.T) {
	p := &jellyfinv1alpha1.JellyfinPlugin{Spec: jellyfinv1alpha1.JellyfinPluginSpec{
		PluginImage: jellyfinv1alpha1.ImageSource{SubPath: "/img"},
		Meta:        jellyfinv1alpha1.PluginMeta{SubPath: "/meta"},
	}}
	if got := PluginSubPath(p); got != "/meta" {
		t.Errorf("meta.subPath should win, got %q", got)
	}
	p.Spec.Meta.SubPath = ""
	if got := PluginSubPath(p); got != "/img" {
		t.Errorf("pluginImage.subPath fallback, got %q", got)
	}
}

func TestABICompatible(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		server  string
		want    bool
		wantErr bool
	}{
		{"exact match", "12.0.0.0", "12.0.0.0", true, false},
		{"server newer", "12.0.0.0", "12.1.0.0", true, false},
		{"server older", "12.0.0.0", "11.9.9.9", false, false},
		{"short server version", "12.0.0.0", "12.0.0", true, false},
		{"bad target", "abc", "12.0.0.0", false, true},
		{"bad server", "12.0.0.0", "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ABICompatible(tc.target, tc.server)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Errorf("ABICompatible(%q,%q) = %v, want %v", tc.target, tc.server, got, tc.want)
			}
		})
	}
}
