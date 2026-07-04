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
	"regexp"
	"strings"
	"testing"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var dns1123 = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func TestSanitizeDNS1123(t *testing.T) {
	cases := map[string]string{
		"Distributed Transcoding": "distributed-transcoding",
		"movies":                  "movies",
		"Foo_Bar.Baz":             "foo-bar-baz",
		"--weird--":               "weird",
	}
	for in, want := range cases {
		if got := SanitizeDNS1123(in); got != want {
			t.Errorf("SanitizeDNS1123(%q) = %q, want %q", in, got, want)
		}
	}
	// Fully-invalid input still yields a valid label.
	got := SanitizeDNS1123("###")
	if !dns1123.MatchString(got) {
		t.Errorf("SanitizeDNS1123(###) = %q is not DNS-1123", got)
	}
}

func TestSanitizeDNS1123LongInput(t *testing.T) {
	long := strings.Repeat("abc-", 40)
	got := SanitizeDNS1123(long)
	if len(got) > 63 {
		t.Errorf("length = %d, want <=63", len(got))
	}
	if !dns1123.MatchString(got) {
		t.Errorf("%q not DNS-1123", got)
	}
}

func TestClaimNames(t *testing.T) {
	jf := &jellyfinv1alpha1.Jellyfin{ObjectMeta: metav1.ObjectMeta{Name: "home-media"}}

	if got := ConfigClaimName(jf); got != "home-media-config" {
		t.Errorf("ConfigClaimName = %q", got)
	}
	jf.Spec.Storage.Config.ExistingClaim = "preexisting"
	if got := ConfigClaimName(jf); got != "preexisting" {
		t.Errorf("ConfigClaimName existingClaim = %q", got)
	}

	mf := jellyfinv1alpha1.MediaFolder{Name: "movies"}
	if got := MediaClaimName(jf, mf); got != "home-media-media-movies" {
		t.Errorf("MediaClaimName = %q", got)
	}
	mf.ExistingClaim = "movies-pvc"
	if got := MediaClaimName(jf, mf); got != "movies-pvc" {
		t.Errorf("MediaClaimName existingClaim = %q", got)
	}
}
