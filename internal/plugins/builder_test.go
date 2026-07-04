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
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

func baseInstance() *jellyfinv1alpha1.Jellyfin {
	return &jellyfinv1alpha1.Jellyfin{
		ObjectMeta: metav1.ObjectMeta{Name: "home-media", Namespace: "media"},
		Spec: jellyfinv1alpha1.JellyfinSpec{
			Storage: jellyfinv1alpha1.JellyfinStorage{
				Config: jellyfinv1alpha1.PVCSpec{ExistingClaim: "cfg"},
			},
		},
	}
}

func plugin(name, injection string) jellyfinv1alpha1.JellyfinPlugin {
	return jellyfinv1alpha1.JellyfinPlugin{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "media"},
		Spec: jellyfinv1alpha1.JellyfinPluginSpec{
			PluginImage: jellyfinv1alpha1.ImageSource{Reference: "ghcr.io/example/" + name + "@sha256:abc"},
			Meta:        jellyfinv1alpha1.PluginMeta{Name: "Distributed Transcoding", Version: "0.0.1.0"},
			Injection:   injection,
		},
	}
}

func findVolume(vols []corev1.Volume, name string) *corev1.Volume {
	for i := range vols {
		if vols[i].Name == name {
			return &vols[i]
		}
	}
	return nil
}

func findMount(mounts []corev1.VolumeMount, name string) *corev1.VolumeMount {
	for i := range mounts {
		if mounts[i].Name == name {
			return &mounts[i]
		}
	}
	return nil
}

func findContainer(cs []corev1.Container, name string) *corev1.Container {
	for i := range cs {
		if cs[i].Name == name {
			return &cs[i]
		}
	}
	return nil
}

func TestBuildDeploymentDefaults(t *testing.T) {
	dep, err := BuildDeployment(baseInstance(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 1 {
		t.Errorf("replicas = %d, want 1", *dep.Spec.Replicas)
	}
	if dep.Spec.Strategy.Type != "Recreate" {
		t.Errorf("strategy = %s, want Recreate", dep.Spec.Strategy.Type)
	}
	jf := findContainer(dep.Spec.Template.Spec.Containers, JellyfinContainerName)
	if jf == nil {
		t.Fatal("jellyfin container missing")
	}
	if jf.Image != DefaultJellyfinImage {
		t.Errorf("image = %s, want default", jf.Image)
	}
	if findVolume(dep.Spec.Template.Spec.Volumes, ConfigVolumeName) == nil {
		t.Error("config volume missing")
	}
}

func TestImageVolumeInjection(t *testing.T) {
	pod, err := BuildPodTemplateSpec(baseInstance(), []jellyfinv1alpha1.JellyfinPlugin{plugin("dt", jellyfinv1alpha1.InjectionImageVolume)})
	if err != nil {
		t.Fatal(err)
	}
	// No staging init container in imageVolume mode.
	if len(pod.Spec.InitContainers) != 0 {
		t.Errorf("expected no init containers, got %d", len(pod.Spec.InitContainers))
	}
	// Image volume present.
	if findVolume(pod.Spec.Volumes, "plugin-dt") == nil {
		t.Fatal("image volume plugin-dt missing")
	}
	// Jellyfin mounts it read-only at the plugin dir with the plugin subPath.
	jf := &pod.Spec.Containers[0]
	m := findMount(jf.VolumeMounts, "plugin-dt")
	if m == nil {
		t.Fatal("jellyfin should mount the image volume")
	}
	if !m.ReadOnly {
		t.Error("plugin mount must be read-only")
	}
	if m.MountPath != "/config/plugins/Distributed Transcoding_0.0.1.0" {
		t.Errorf("mountPath = %q", m.MountPath)
	}
}

func TestImageVolumeCopyInjection(t *testing.T) {
	pod, err := BuildPodTemplateSpec(baseInstance(), []jellyfinv1alpha1.JellyfinPlugin{plugin("dt", jellyfinv1alpha1.InjectionImageVolumeCopy)})
	if err != nil {
		t.Fatal(err)
	}
	stage := findContainer(pod.Spec.InitContainers, "stage-dt")
	if stage == nil {
		t.Fatal("staging init container missing")
	}
	// Staging mounts the image volume RO and the writable config PVC.
	if m := findMount(stage.VolumeMounts, "plugin-dt"); m == nil || !m.ReadOnly {
		t.Error("staging must mount image volume read-only")
	}
	if findMount(stage.VolumeMounts, ConfigVolumeName) == nil {
		t.Error("staging must mount config PVC writable")
	}
	// The copy command targets the plugins dir.
	joined := strings.Join(stage.Command, " ")
	if !strings.Contains(joined, "cp -a") || !strings.Contains(joined, "Distributed Transcoding_0.0.1.0") {
		t.Errorf("unexpected staging command: %q", joined)
	}
	// Jellyfin must NOT mount the image volume in copy mode.
	if findMount(pod.Spec.Containers[0].VolumeMounts, "plugin-dt") != nil {
		t.Error("jellyfin should not mount image volume in imageVolumeCopy mode")
	}
}

func TestInstallScriptRunOnceIgnore(t *testing.T) {
	p := plugin("dt", jellyfinv1alpha1.InjectionImageVolumeCopy)
	p.Spec.Install = &jellyfinv1alpha1.PluginInstall{
		Script:        "echo hi",
		RunOnce:       true,
		FailurePolicy: jellyfinv1alpha1.FailurePolicyIgnore,
	}
	pod, err := BuildPodTemplateSpec(baseInstance(), []jellyfinv1alpha1.JellyfinPlugin{p})
	if err != nil {
		t.Fatal(err)
	}
	inst := findContainer(pod.Spec.InitContainers, "install-dt")
	if inst == nil {
		t.Fatal("install container missing")
	}
	// Body carried via env var, not quoted into the command.
	if findEnv(inst.Env, installScriptEnvVar) != "echo hi" {
		t.Error("install body should be in env var")
	}
	wrapper := inst.Command[2]
	if !strings.Contains(wrapper, "MARKER=") || !strings.Contains(wrapper, `if [ -f "$MARKER" ]`) {
		t.Errorf("runOnce guard missing: %q", wrapper)
	}
	if !strings.Contains(wrapper, "exit 0") {
		t.Errorf("Ignore policy should swallow errors: %q", wrapper)
	}
	// Ordering: staging must come before install.
	if idx(pod.Spec.InitContainers, "stage-dt") > idx(pod.Spec.InitContainers, "install-dt") {
		t.Error("staging must precede install")
	}
}

func TestInstallFailPolicy(t *testing.T) {
	p := plugin("dt", jellyfinv1alpha1.InjectionImageVolume)
	p.Spec.Install = &jellyfinv1alpha1.PluginInstall{
		Script:        "do-thing",
		FailurePolicy: jellyfinv1alpha1.FailurePolicyFail,
	}
	pod, _ := BuildPodTemplateSpec(baseInstance(), []jellyfinv1alpha1.JellyfinPlugin{p})
	wrapper := findContainer(pod.Spec.InitContainers, "install-dt").Command[2]
	if !strings.Contains(wrapper, "set -e") {
		t.Errorf("Fail policy should use set -e: %q", wrapper)
	}
	if strings.Contains(wrapper, "failurePolicy=Ignore") {
		t.Errorf("Fail policy should not swallow errors: %q", wrapper)
	}
}

func TestInstallTimeout(t *testing.T) {
	p := plugin("dt", jellyfinv1alpha1.InjectionImageVolume)
	p.Spec.Install = &jellyfinv1alpha1.PluginInstall{Script: "x", TimeoutSeconds: ptr.To(int32(30))}
	pod, _ := BuildPodTemplateSpec(baseInstance(), []jellyfinv1alpha1.JellyfinPlugin{p})
	wrapper := findContainer(pod.Spec.InitContainers, "install-dt").Command[2]
	if !strings.Contains(wrapper, "timeout 30") {
		t.Errorf("timeout wrapping missing: %q", wrapper)
	}
}

func TestInstallCommandForm(t *testing.T) {
	p := plugin("dt", jellyfinv1alpha1.InjectionImageVolume)
	p.Spec.Install = &jellyfinv1alpha1.PluginInstall{Command: []string{"cp", "a b"}, Args: []string{"c"}}
	pod, _ := BuildPodTemplateSpec(baseInstance(), []jellyfinv1alpha1.JellyfinPlugin{p})
	body := findEnv(findContainer(pod.Spec.InitContainers, "install-dt").Env, installScriptEnvVar)
	if body != `'cp' 'a b' 'c'` {
		t.Errorf("command form body = %q", body)
	}
}

func TestInstallScriptAndCommandRejected(t *testing.T) {
	p := plugin("dt", jellyfinv1alpha1.InjectionImageVolume)
	p.Spec.Install = &jellyfinv1alpha1.PluginInstall{Script: "x", Command: []string{"y"}}
	if _, err := BuildPodTemplateSpec(baseInstance(), []jellyfinv1alpha1.JellyfinPlugin{p}); err == nil {
		t.Error("expected error when both script and command set")
	}
}

func TestMediaInlineNFS(t *testing.T) {
	jf := baseInstance()
	jf.Spec.Storage.Media = []jellyfinv1alpha1.MediaFolder{{
		Name: "movies", MountPath: "/media/movies", ReadOnly: true,
		NFS: &jellyfinv1alpha1.NFSSource{Server: "10.0.0.10", Path: "/export/movies"},
	}}
	pod, _ := BuildPodTemplateSpec(jf, nil)
	v := findVolume(pod.Spec.Volumes, "media-movies")
	if v == nil || v.NFS == nil {
		t.Fatal("expected inline NFS volume")
	}
	if v.NFS.Server != "10.0.0.10" || !v.NFS.ReadOnly {
		t.Errorf("unexpected NFS source: %+v", v.NFS)
	}
}

func TestMediaProvisionedNFSUsesPVC(t *testing.T) {
	jf := baseInstance()
	jf.Spec.Storage.Media = []jellyfinv1alpha1.MediaFolder{{
		Name: "tv", MountPath: "/media/tv",
		NFS: &jellyfinv1alpha1.NFSSource{Server: "10.0.0.10", Path: "/export/tv", Provision: true},
	}}
	pod, _ := BuildPodTemplateSpec(jf, nil)
	v := findVolume(pod.Spec.Volumes, "media-tv")
	if v == nil || v.PersistentVolumeClaim == nil {
		t.Fatal("provisioned NFS should use a PVC volume")
	}
	if v.PersistentVolumeClaim.ClaimName != "home-media-media-tv" {
		t.Errorf("claim name = %q", v.PersistentVolumeClaim.ClaimName)
	}
}

func TestMediaMountOptionsForcePVC(t *testing.T) {
	jf := baseInstance()
	jf.Spec.Storage.Media = []jellyfinv1alpha1.MediaFolder{{
		Name: "tv", MountPath: "/media/tv",
		NFS: &jellyfinv1alpha1.NFSSource{Server: "s", Path: "/p", MountOptions: []string{"nfsvers=4.1"}},
	}}
	pod, _ := BuildPodTemplateSpec(jf, nil)
	v := findVolume(pod.Spec.Volumes, "media-tv")
	if v.PersistentVolumeClaim == nil {
		t.Error("mountOptions should force PVC-backed volume (inline NFS can't carry them)")
	}
}

func TestHardwareAccelVAAPI(t *testing.T) {
	jf := baseInstance()
	jf.Spec.HardwareAcceleration = &jellyfinv1alpha1.HardwareAccel{Type: "vaapi", DevicePath: "/dev/dri/renderD128", RenderGroupGID: ptr.To(int64(44))}
	pod, _ := BuildPodTemplateSpec(jf, nil)
	if findVolume(pod.Spec.Volumes, "dri-device") == nil {
		t.Error("dri-device volume missing")
	}
	if len(pod.Spec.SecurityContext.SupplementalGroups) != 1 || pod.Spec.SecurityContext.SupplementalGroups[0] != 44 {
		t.Errorf("render group not set: %+v", pod.Spec.SecurityContext.SupplementalGroups)
	}
}

func TestHardwareAccelNvidia(t *testing.T) {
	jf := baseInstance()
	jf.Spec.HardwareAcceleration = &jellyfinv1alpha1.HardwareAccel{Type: "nvidia", RuntimeClassName: "nvidia"}
	pod, _ := BuildPodTemplateSpec(jf, nil)
	jfc := &pod.Spec.Containers[0]
	if _, ok := jfc.Resources.Limits["nvidia.com/gpu"]; !ok {
		t.Error("nvidia gpu limit missing")
	}
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "nvidia" {
		t.Error("runtimeClassName not set")
	}
}

func TestDeterministicOutput(t *testing.T) {
	jf := baseInstance()
	// Pass plugins out of order; output must be stable and order-independent.
	pluginsA := []jellyfinv1alpha1.JellyfinPlugin{plugin("bbb", jellyfinv1alpha1.InjectionImageVolume), plugin("aaa", jellyfinv1alpha1.InjectionImageVolume)}
	pluginsB := []jellyfinv1alpha1.JellyfinPlugin{plugin("aaa", jellyfinv1alpha1.InjectionImageVolume), plugin("bbb", jellyfinv1alpha1.InjectionImageVolume)}
	a, _ := BuildPodTemplateSpec(jf, pluginsA)
	b, _ := BuildPodTemplateSpec(jf, pluginsB)
	if !reflect.DeepEqual(a, b) {
		t.Error("pod template must be deterministic regardless of plugin input order")
	}
}

// helpers

func findEnv(env []corev1.EnvVar, name string) string {
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

func idx(cs []corev1.Container, name string) int {
	for i := range cs {
		if cs[i].Name == name {
			return i
		}
	}
	return -1
}
