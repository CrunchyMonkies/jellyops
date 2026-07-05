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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

func testJellyfinWithGateway() *jellyfinv1alpha1.Jellyfin {
	return &jellyfinv1alpha1.Jellyfin{
		ObjectMeta: metav1.ObjectMeta{Name: "home-media", Namespace: "media"},
		Spec: jellyfinv1alpha1.JellyfinSpec{
			Web: &jellyfinv1alpha1.WebSpec{Image: "web:1"},
			Gateway: &jellyfinv1alpha1.GatewaySpec{
				GatewayRef: jellyfinv1alpha1.GatewayReference{
					Name:        "main-gw",
					Namespace:   "infra",
					SectionName: "https",
				},
				Hostname:    "jellyfin.example.com",
				Annotations: map[string]string{"test": "value"},
			},
		},
	}
}

func TestBuildHTTPRoute(t *testing.T) {
	jf := testJellyfinWithGateway()
	route := BuildHTTPRoute(jf)

	if route.Name != "home-media" {
		t.Errorf("name = %q, want %q", route.Name, "home-media")
	}
	if route.Namespace != "media" {
		t.Errorf("namespace = %q, want %q", route.Namespace, "media")
	}

	// Annotations.
	if route.Annotations["test"] != "value" {
		t.Errorf("annotations = %v", route.Annotations)
	}

	// ParentRef.
	if len(route.Spec.ParentRefs) != 1 {
		t.Fatalf("parentRefs len = %d, want 1", len(route.Spec.ParentRefs))
	}
	pr := route.Spec.ParentRefs[0]
	if string(pr.Name) != "main-gw" {
		t.Errorf("parentRef name = %q, want %q", pr.Name, "main-gw")
	}
	if pr.Namespace == nil || string(*pr.Namespace) != "infra" {
		t.Errorf("parentRef namespace = %v, want infra", pr.Namespace)
	}
	if pr.SectionName == nil || string(*pr.SectionName) != "https" {
		t.Errorf("parentRef sectionName = %v, want https", pr.SectionName)
	}
	if pr.Group == nil || string(*pr.Group) != "gateway.networking.k8s.io" {
		t.Errorf("parentRef group = %v, want gateway.networking.k8s.io", pr.Group)
	}
	if pr.Kind == nil || string(*pr.Kind) != "Gateway" {
		t.Errorf("parentRef kind = %v, want Gateway", pr.Kind)
	}

	// Hostnames.
	if len(route.Spec.Hostnames) != 1 || string(route.Spec.Hostnames[0]) != "jellyfin.example.com" {
		t.Errorf("hostnames = %v, want [jellyfin.example.com]", route.Spec.Hostnames)
	}

	// Rules: must be 2 rules, /web first, / second.
	if len(route.Spec.Rules) != 2 {
		t.Fatalf("rules len = %d, want 2", len(route.Spec.Rules))
	}

	// Rule 1: /web -> web service.
	r1 := route.Spec.Rules[0]
	if len(r1.Matches) != 1 {
		t.Fatalf("rule[0] matches len = %d", len(r1.Matches))
	}
	if r1.Matches[0].Path == nil || *r1.Matches[0].Path.Type != gatewayv1.PathMatchPathPrefix {
		t.Error("rule[0] path type should be PathPrefix")
	}
	if *r1.Matches[0].Path.Value != "/web" {
		t.Errorf("rule[0] path = %q, want /web", *r1.Matches[0].Path.Value)
	}
	if len(r1.BackendRefs) != 1 {
		t.Fatalf("rule[0] backendRefs len = %d", len(r1.BackendRefs))
	}
	if string(r1.BackendRefs[0].Name) != "home-media-web" {
		t.Errorf("rule[0] backend name = %q, want home-media-web", r1.BackendRefs[0].Name)
	}
	if r1.BackendRefs[0].Port == nil || int(*r1.BackendRefs[0].Port) != 80 {
		t.Errorf("rule[0] backend port = %v, want 80", r1.BackendRefs[0].Port)
	}

	// Rule 2: / -> server service.
	r2 := route.Spec.Rules[1]
	if len(r2.Matches) != 1 {
		t.Fatalf("rule[1] matches len = %d", len(r2.Matches))
	}
	if *r2.Matches[0].Path.Value != "/" {
		t.Errorf("rule[1] path = %q, want /", *r2.Matches[0].Path.Value)
	}
	if len(r2.BackendRefs) != 1 {
		t.Fatalf("rule[1] backendRefs len = %d", len(r2.BackendRefs))
	}
	if string(r2.BackendRefs[0].Name) != "home-media" {
		t.Errorf("rule[1] backend name = %q, want home-media", r2.BackendRefs[0].Name)
	}
	if r2.BackendRefs[0].Port == nil || int(*r2.BackendRefs[0].Port) != 8096 {
		t.Errorf("rule[1] backend port = %v, want 8096", r2.BackendRefs[0].Port)
	}
}

func TestBuildHTTPRouteMinimal(t *testing.T) {
	// Minimal: no namespace, no sectionName.
	jf := &jellyfinv1alpha1.Jellyfin{
		ObjectMeta: metav1.ObjectMeta{Name: "jf", Namespace: "ns"},
		Spec: jellyfinv1alpha1.JellyfinSpec{
			Web: &jellyfinv1alpha1.WebSpec{Image: "web:1"},
			Gateway: &jellyfinv1alpha1.GatewaySpec{
				GatewayRef: jellyfinv1alpha1.GatewayReference{Name: "gw"},
				Hostname:   "jf.local",
			},
		},
	}
	route := BuildHTTPRoute(jf)
	pr := route.Spec.ParentRefs[0]
	if pr.Namespace != nil {
		t.Errorf("expected nil namespace, got %v", pr.Namespace)
	}
	if pr.SectionName != nil {
		t.Errorf("expected nil sectionName, got %v", pr.SectionName)
	}
}

func TestHTTPRouteRuleOrderMostSpecificFirst(t *testing.T) {
	jf := testJellyfinWithGateway()
	route := BuildHTTPRoute(jf)

	// First rule must be the more specific /web path.
	if *route.Spec.Rules[0].Matches[0].Path.Value != "/web" {
		t.Error("first rule must match /web (most specific)")
	}
	if *route.Spec.Rules[1].Matches[0].Path.Value != "/" {
		t.Error("second rule must match / (default)")
	}
}
