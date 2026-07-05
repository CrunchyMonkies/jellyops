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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

func testJellyfinWithWeb() *jellyfinv1alpha1.Jellyfin {
	return &jellyfinv1alpha1.Jellyfin{
		ObjectMeta: metav1.ObjectMeta{Name: "home-media", Namespace: "media"},
		Spec: jellyfinv1alpha1.JellyfinSpec{
			Web: &jellyfinv1alpha1.WebSpec{
				Image:    "ghcr.io/crunchymonkies/jellyfin-web:latest",
				Replicas: ptr.To(int32(2)),
				PodAnnotations: map[string]string{
					"prometheus.io/scrape": "true",
				},
			},
		},
	}
}

func TestWebDeploymentName(t *testing.T) {
	jf := testJellyfinWithWeb()
	if got := WebDeploymentName(jf); got != "home-media-web" {
		t.Errorf("WebDeploymentName() = %q, want %q", got, "home-media-web")
	}
}

func TestWebServiceName(t *testing.T) {
	jf := testJellyfinWithWeb()
	if got := WebServiceName(jf); got != "home-media-web" {
		t.Errorf("WebServiceName() = %q, want %q", got, "home-media-web")
	}
}

func TestBuildWebDeployment(t *testing.T) {
	jf := testJellyfinWithWeb()
	dep := BuildWebDeployment(jf)

	if dep.Name != "home-media-web" {
		t.Errorf("name = %q, want %q", dep.Name, "home-media-web")
	}
	if dep.Namespace != "media" {
		t.Errorf("namespace = %q, want %q", dep.Namespace, "media")
	}
	if *dep.Spec.Replicas != 2 {
		t.Errorf("replicas = %d, want 2", *dep.Spec.Replicas)
	}

	// Labels.
	if dep.Labels[ComponentLabel] != "web" {
		t.Errorf("component label = %q, want %q", dep.Labels[ComponentLabel], "web")
	}
	if dep.Labels[InstanceLabel] != "home-media" {
		t.Errorf("instance label = %q, want %q", dep.Labels[InstanceLabel], "home-media")
	}

	// Selector must include component=web to avoid collisions.
	sel := dep.Spec.Selector.MatchLabels
	if sel[ComponentLabel] != "web" {
		t.Errorf("selector component = %q, want %q", sel[ComponentLabel], "web")
	}

	// Container.
	if len(dep.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(dep.Spec.Template.Spec.Containers))
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if c.Name != "web" {
		t.Errorf("container name = %q, want %q", c.Name, "web")
	}
	if c.Image != "ghcr.io/crunchymonkies/jellyfin-web:latest" {
		t.Errorf("image = %q", c.Image)
	}
	if len(c.Ports) != 1 || c.Ports[0].ContainerPort != 80 {
		t.Errorf("container port = %v, want [80]", c.Ports)
	}
	if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet == nil {
		t.Fatal("readiness probe not set")
	}
	if c.ReadinessProbe.HTTPGet.Path != "/health" {
		t.Errorf("readiness path = %q, want /health", c.ReadinessProbe.HTTPGet.Path)
	}
	if c.LivenessProbe == nil || c.LivenessProbe.HTTPGet == nil {
		t.Fatal("liveness probe not set")
	}
	if c.LivenessProbe.HTTPGet.Path != "/health" {
		t.Errorf("liveness path = %q, want /health", c.LivenessProbe.HTTPGet.Path)
	}

	// Pod annotations.
	ann := dep.Spec.Template.ObjectMeta.Annotations
	if ann["prometheus.io/scrape"] != "true" {
		t.Errorf("pod annotation missing: %v", ann)
	}
}

func TestBuildWebDeploymentDefaultReplicas(t *testing.T) {
	jf := &jellyfinv1alpha1.Jellyfin{
		ObjectMeta: metav1.ObjectMeta{Name: "jf", Namespace: "ns"},
		Spec: jellyfinv1alpha1.JellyfinSpec{
			Web: &jellyfinv1alpha1.WebSpec{Image: "web:1"},
		},
	}
	dep := BuildWebDeployment(jf)
	if *dep.Spec.Replicas != 1 {
		t.Errorf("default replicas = %d, want 1", *dep.Spec.Replicas)
	}
}

func TestBuildWebService(t *testing.T) {
	jf := testJellyfinWithWeb()
	svc := BuildWebService(jf)

	if svc.Name != "home-media-web" {
		t.Errorf("name = %q, want %q", svc.Name, "home-media-web")
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("type = %v, want ClusterIP", svc.Spec.Type)
	}
	if svc.Spec.Selector[ComponentLabel] != "web" {
		t.Errorf("selector component = %q, want %q", svc.Spec.Selector[ComponentLabel], "web")
	}
	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(svc.Spec.Ports))
	}
	p := svc.Spec.Ports[0]
	if p.Name != "http" || p.Port != 80 {
		t.Errorf("port = {Name:%q Port:%d}, want {http 80}", p.Name, p.Port)
	}
}

func TestWebSelectorLabelsDoNotCollideWithInstance(t *testing.T) {
	jf := testJellyfinWithWeb()
	instSel := InstanceSelectorLabels(jf)
	webSel := WebSelectorLabels(jf)

	// The web selector must have a label key that the instance selector does not,
	// so Deployments/Services don't accidentally cross-select.
	if _, ok := instSel[ComponentLabel]; ok {
		t.Fatal("instance selector should not have component label")
	}
	if _, ok := webSel[ComponentLabel]; !ok {
		t.Fatal("web selector must have component label")
	}
}
