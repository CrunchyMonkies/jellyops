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
	"k8s.io/utils/ptr"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

func testPlugin() *jellyfinv1alpha1.JellyfinPlugin {
	return &jellyfinv1alpha1.JellyfinPlugin{ObjectMeta: metav1.ObjectMeta{Name: "dt", Namespace: "media"}}
}

func TestBuildWorkloadDeployment(t *testing.T) {
	p := testPlugin()
	w := jellyfinv1alpha1.PluginWorkload{
		Name:     "worker",
		Image:    jellyfinv1alpha1.ImageSource{Reference: "ghcr.io/x/worker@sha256:abc"},
		Replicas: ptr.To(int32(3)),
		Args:     []string{"--server=x"},
	}
	dep := BuildWorkloadDeployment(p, w)
	if dep.Name != "dt-worker" {
		t.Errorf("name = %q", dep.Name)
	}
	if *dep.Spec.Replicas != 3 {
		t.Errorf("replicas = %d", *dep.Spec.Replicas)
	}
	if dep.Spec.Template.Spec.Containers[0].Image != "ghcr.io/x/worker@sha256:abc" {
		t.Error("image mismatch")
	}
	// Selector labels must match template labels.
	for k, v := range dep.Spec.Selector.MatchLabels {
		if dep.Spec.Template.Labels[k] != v {
			t.Errorf("selector/template label mismatch for %s", k)
		}
	}
}

func TestBuildPluginServiceSelector(t *testing.T) {
	p := testPlugin()

	svc, err := BuildPluginService(p, jellyfinv1alpha1.PluginService{Name: "grpc", Selector: "instance"}, "home-media")
	if err != nil {
		t.Fatal(err)
	}
	if svc.Spec.Selector[InstanceLabel] != "home-media" {
		t.Errorf("instance selector = %v", svc.Spec.Selector)
	}

	svc, err = BuildPluginService(p, jellyfinv1alpha1.PluginService{Name: "w", Selector: "workload:worker"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if svc.Spec.Selector[WorkloadLabel] != "worker" {
		t.Errorf("workload selector = %v", svc.Spec.Selector)
	}

	if _, err := BuildPluginService(p, jellyfinv1alpha1.PluginService{Name: "x", Selector: "instance"}, ""); err == nil {
		t.Error("expected error for instance selector without a bound instance")
	}
	if _, err := BuildPluginService(p, jellyfinv1alpha1.PluginService{Name: "x", Selector: "bogus"}, "i"); err == nil {
		t.Error("expected error for unknown selector")
	}
}
