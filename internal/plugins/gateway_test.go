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

	// Rules: 4 rules — the bare-root "/" -> "/web/" redirect first, then the
	// /web ConfigurationPage API carve-out (to the server), then /web (web tier),
	// then / (server).
	if len(route.Spec.Rules) != 4 {
		t.Fatalf("rules len = %d, want 4", len(route.Spec.Rules))
	}

	// Rule 0: exact "/" -> 302 redirect to "/web/" (no backend; a RequestRedirect
	// filter). Restores stock-server behaviour so clients loading the bare origin
	// (e.g. the mobile apps' WebView) reach the web client instead of the API docs.
	rRedirect := route.Spec.Rules[0]
	if len(rRedirect.Matches) != 1 || rRedirect.Matches[0].Path == nil {
		t.Fatalf("rule[0] matches = %v, want one path match", rRedirect.Matches)
	}
	if *rRedirect.Matches[0].Path.Type != gatewayv1.PathMatchExact || *rRedirect.Matches[0].Path.Value != "/" {
		t.Errorf("rule[0] path = %v %q, want Exact /", *rRedirect.Matches[0].Path.Type, *rRedirect.Matches[0].Path.Value)
	}
	if len(rRedirect.BackendRefs) != 0 {
		t.Errorf("rule[0] should have no backendRefs, got %d", len(rRedirect.BackendRefs))
	}
	if len(rRedirect.Filters) != 1 || rRedirect.Filters[0].Type != gatewayv1.HTTPRouteFilterRequestRedirect {
		t.Fatalf("rule[0] filters = %v, want one RequestRedirect", rRedirect.Filters)
	}
	rr := rRedirect.Filters[0].RequestRedirect
	if rr == nil || rr.StatusCode == nil || *rr.StatusCode != 302 {
		t.Errorf("rule[0] redirect statusCode = %v, want 302", rr)
	}
	if rr.Path == nil || rr.Path.Type != gatewayv1.FullPathHTTPPathModifier || rr.Path.ReplaceFullPath == nil || *rr.Path.ReplaceFullPath != "/web/" {
		t.Errorf("rule[0] redirect path = %v, want ReplaceFullPath /web/", rr.Path)
	}

	// Rule 1: /web/{C,c}onfigurationpage* -> server service (the plugin-config API
	// that lives under /web but is not a SPA asset).
	r0 := route.Spec.Rules[1]
	if len(r0.Matches) != 2 {
		t.Fatalf("rule[1] matches len = %d, want 2", len(r0.Matches))
	}
	if *r0.Matches[0].Path.Value != "/web/ConfigurationPages" {
		t.Errorf("rule[1] first path = %q, want /web/ConfigurationPages", *r0.Matches[0].Path.Value)
	}
	if string(r0.BackendRefs[0].Name) != "home-media" || r0.BackendRefs[0].Port == nil || int(*r0.BackendRefs[0].Port) != 8096 {
		t.Errorf("rule[1] backend = %q:%v, want home-media:8096", r0.BackendRefs[0].Name, r0.BackendRefs[0].Port)
	}

	// Rule 2: /web -> web service.
	r1 := route.Spec.Rules[2]
	if len(r1.Matches) != 1 {
		t.Fatalf("rule[1] matches len = %d", len(r1.Matches))
	}
	if r1.Matches[0].Path == nil || *r1.Matches[0].Path.Type != gatewayv1.PathMatchPathPrefix {
		t.Error("rule[1] path type should be PathPrefix")
	}
	if *r1.Matches[0].Path.Value != "/web" {
		t.Errorf("rule[1] path = %q, want /web", *r1.Matches[0].Path.Value)
	}
	if len(r1.BackendRefs) != 1 {
		t.Fatalf("rule[1] backendRefs len = %d", len(r1.BackendRefs))
	}
	if string(r1.BackendRefs[0].Name) != "home-media-web" {
		t.Errorf("rule[1] backend name = %q, want home-media-web", r1.BackendRefs[0].Name)
	}
	if r1.BackendRefs[0].Port == nil || int(*r1.BackendRefs[0].Port) != 80 {
		t.Errorf("rule[1] backend port = %v, want 80", r1.BackendRefs[0].Port)
	}

	// Rule 3: / -> server service.
	r2 := route.Spec.Rules[3]
	if len(r2.Matches) != 1 {
		t.Fatalf("rule[3] matches len = %d", len(r2.Matches))
	}
	if *r2.Matches[0].Path.Value != "/" {
		t.Errorf("rule[3] path = %q, want /", *r2.Matches[0].Path.Value)
	}
	if len(r2.BackendRefs) != 1 {
		t.Fatalf("rule[3] backendRefs len = %d", len(r2.BackendRefs))
	}
	if string(r2.BackendRefs[0].Name) != "home-media" {
		t.Errorf("rule[3] backend name = %q, want home-media", r2.BackendRefs[0].Name)
	}
	if r2.BackendRefs[0].Port == nil || int(*r2.BackendRefs[0].Port) != 8096 {
		t.Errorf("rule[3] backend port = %v, want 8096", r2.BackendRefs[0].Port)
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

	// Order: the exact "/" redirect first, then the /web ConfigurationPage API
	// carve-out, then /web (web tier), then / (default).
	if *route.Spec.Rules[0].Matches[0].Path.Type != gatewayv1.PathMatchExact || *route.Spec.Rules[0].Matches[0].Path.Value != "/" {
		t.Error("first rule must be the exact / -> /web/ redirect")
	}
	if *route.Spec.Rules[1].Matches[0].Path.Value != "/web/ConfigurationPages" {
		t.Error("second rule must match the /web ConfigurationPage carve-out")
	}
	if *route.Spec.Rules[2].Matches[0].Path.Value != "/web" {
		t.Error("third rule must match /web")
	}
	if *route.Spec.Rules[3].Matches[0].Path.Value != "/" {
		t.Error("fourth rule must match / (default)")
	}
}
