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

func testPlugin() *jellyfinv1alpha1.JellyfinPlugin {
	return &jellyfinv1alpha1.JellyfinPlugin{ObjectMeta: metav1.ObjectMeta{Name: "dt", Namespace: "media"}}
}

// testInstanceWithMedia is a Jellyfin instance with one inline-NFS and one
// existing-claim media folder, used to exercise media auto-injection.
func testInstanceWithMedia() *jellyfinv1alpha1.Jellyfin {
	return &jellyfinv1alpha1.Jellyfin{
		ObjectMeta: metav1.ObjectMeta{Name: "home-media", Namespace: "media"},
		Spec: jellyfinv1alpha1.JellyfinSpec{
			Storage: jellyfinv1alpha1.JellyfinStorage{
				Media: []jellyfinv1alpha1.MediaFolder{
					{Name: "movies", MountPath: "/media/movies", NFS: &jellyfinv1alpha1.NFSSource{Server: "10.0.0.1", Path: "/export/movies"}},
					{Name: "library", MountPath: "/media/library", ExistingClaim: "media-library"},
				},
			},
		},
	}
}

func TestBuildWorkloadDeployment(t *testing.T) {
	p := testPlugin()
	w := jellyfinv1alpha1.PluginWorkload{
		Name:     "worker",
		Image:    jellyfinv1alpha1.ImageSource{Reference: "ghcr.io/x/worker@sha256:abc"},
		Replicas: ptr.To(int32(3)),
		Args:     []string{"--server=x"},
	}
	dep := BuildWorkloadDeployment(nil, p, w)
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
	// Nil instance injects no media.
	if len(dep.Spec.Template.Spec.Volumes) != 0 {
		t.Errorf("nil-instance volumes = %d, want 0", len(dep.Spec.Template.Spec.Volumes))
	}
}

func TestBuildWorkloadDeploymentInjectsInstanceMedia(t *testing.T) {
	p := testPlugin()
	jf := testInstanceWithMedia()
	w := jellyfinv1alpha1.PluginWorkload{
		Name:         "worker",
		Image:        jellyfinv1alpha1.ImageSource{Reference: "r"},
		Volumes:      []corev1.Volume{{Name: "scratch", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
		VolumeMounts: []corev1.VolumeMount{{Name: "scratch", MountPath: "/tmp/worker"}},
	}
	dep := BuildWorkloadDeployment(jf, p, w)
	vols := dep.Spec.Template.Spec.Volumes
	mounts := dep.Spec.Template.Spec.Containers[0].VolumeMounts
	if len(vols) != 3 || len(mounts) != 3 { // scratch + 2 media
		t.Fatalf("vols=%d mounts=%d, want 3/3", len(vols), len(mounts))
	}

	byPath := map[string]corev1.VolumeMount{}
	for _, m := range mounts {
		byPath[m.MountPath] = m
	}
	for _, path := range []string{"/media/movies", "/media/library"} {
		m, ok := byPath[path]
		if !ok {
			t.Fatalf("missing auto-injected media mount at %s", path)
		}
		if !m.ReadOnly {
			t.Errorf("media mount %s is not read-only", path)
		}
	}

	byName := map[string]corev1.Volume{}
	for _, v := range vols {
		byName[v.Name] = v
	}
	if nv := byName["media-movies"]; nv.NFS == nil || !nv.NFS.ReadOnly {
		t.Errorf("nfs media volume not read-only: %+v", nv.NFS)
	}
	if pv := byName["media-library"]; pv.PersistentVolumeClaim == nil || !pv.PersistentVolumeClaim.ReadOnly {
		t.Errorf("pvc media volume not read-only: %+v", pv.PersistentVolumeClaim)
	}
}

func TestBuildWorkloadDeploymentMediaDedup(t *testing.T) {
	p := testPlugin()
	jf := testInstanceWithMedia()
	// Hand-declare a mount at the movies path: auto-inject must skip that folder
	// (the hand-declared, writable mount wins) but still inject the library folder.
	w := jellyfinv1alpha1.PluginWorkload{
		Name:         "worker",
		Image:        jellyfinv1alpha1.ImageSource{Reference: "r"},
		Volumes:      []corev1.Volume{{Name: "custom-movies", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
		VolumeMounts: []corev1.VolumeMount{{Name: "custom-movies", MountPath: "/media/movies"}},
	}
	dep := BuildWorkloadDeployment(jf, p, w)
	mounts := dep.Spec.Template.Spec.Containers[0].VolumeMounts

	moviesCount, libraryFound, moviesRO := 0, false, false
	for _, m := range mounts {
		switch m.MountPath {
		case "/media/movies":
			moviesCount++
			moviesRO = m.ReadOnly
		case "/media/library":
			libraryFound = true
		}
	}
	if moviesCount != 1 {
		t.Errorf("mounts at /media/movies = %d, want 1 (hand-declared, no auto-inject)", moviesCount)
	}
	if moviesRO {
		t.Error("hand-declared /media/movies must not be forced read-only")
	}
	if !libraryFound {
		t.Error("/media/library should still be auto-injected")
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
