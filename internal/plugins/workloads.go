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
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

// WorkloadLabel identifies a plugin companion workload.
const WorkloadLabel = "jellyfin.jellyops.io/workload"

// WorkloadName is the Deployment name for a plugin companion workload.
func WorkloadName(p *jellyfinv1alpha1.JellyfinPlugin, w jellyfinv1alpha1.PluginWorkload) string {
	return prefixed(p.Name, w.Name)
}

func workloadSelectorLabels(p *jellyfinv1alpha1.JellyfinPlugin, w jellyfinv1alpha1.PluginWorkload) map[string]string {
	return map[string]string{
		NameLabel:     w.Name,
		PluginLabel:   p.Name,
		WorkloadLabel: w.Name,
	}
}

// BuildWorkloadDeployment builds the Deployment for a plugin companion workload.
// The bound Jellyfin instance (jf) may be nil when the plugin is not yet bound;
// when present, its media folders are auto-mounted into the workload so the
// worker sees the same source media at the same paths as the Jellyfin pod.
func BuildWorkloadDeployment(jf *jellyfinv1alpha1.Jellyfin, p *jellyfinv1alpha1.JellyfinPlugin, w jellyfinv1alpha1.PluginWorkload) *appsv1.Deployment {
	replicas := int32(1)
	if w.Replicas != nil {
		replicas = *w.Replicas
	}
	pull := w.Image.PullPolicy
	labels := workloadSelectorLabels(p, w)
	labels[ManagedByLabel] = ManagedByValue

	// Start from the CR-declared volumes/mounts, then auto-inject the bound
	// instance's media (identity path mapping, spec §8.2). Hand-declared ones win.
	volumes := append([]corev1.Volume(nil), w.Volumes...)
	mounts := append([]corev1.VolumeMount(nil), w.VolumeMounts...)
	volumes, mounts = appendInstanceMedia(jf, volumes, mounts)

	container := corev1.Container{
		Name:            w.Name,
		Image:           w.Image.Reference,
		ImagePullPolicy: pull,
		Command:         w.Command,
		Args:            w.Args,
		Env:             w.Env,
		Ports:           w.Ports,
		Resources:       w.Resources,
		VolumeMounts:    mounts,
		SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: ptr.To(false)},
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: WorkloadName(p, w), Namespace: p.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: workloadSelectorLabels(p, w)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: workloadSelectorLabels(p, w)},
				Spec: corev1.PodSpec{
					Containers:                    []corev1.Container{container},
					Volumes:                       volumes,
					TerminationGracePeriodSeconds: w.TerminationGracePeriodSeconds,
					SecurityContext:               &corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
				},
			},
		},
	}
}

// appendInstanceMedia auto-mounts the bound Jellyfin instance's media folders
// into a companion workload, read-only, at the same paths the Jellyfin pod uses
// (identity path mapping, spec §8.2), reusing mediaVolumeAndMount so naming and
// mount paths match the instance exactly. A folder whose volume name or mount
// path is already declared on the workload is left to the hand-declared value.
func appendInstanceMedia(jf *jellyfinv1alpha1.Jellyfin, volumes []corev1.Volume, mounts []corev1.VolumeMount) ([]corev1.Volume, []corev1.VolumeMount) {
	if jf == nil {
		return volumes, mounts
	}
	haveVol := make(map[string]bool, len(volumes))
	for _, v := range volumes {
		haveVol[v.Name] = true
	}
	havePath := make(map[string]bool, len(mounts))
	for _, m := range mounts {
		havePath[m.MountPath] = true
	}
	for _, mf := range jf.Spec.Storage.Media {
		vol, mount := mediaVolumeAndMount(jf, mf)
		if haveVol[vol.Name] || havePath[mount.MountPath] {
			continue // hand-declared workload volume/mount takes precedence
		}
		// Workers only read source media, so force read-only regardless of the
		// instance's own read/write setting (also eases RWX/ROX multi-attach).
		mount.ReadOnly = true
		if vol.NFS != nil {
			vol.NFS.ReadOnly = true
		}
		if vol.PersistentVolumeClaim != nil {
			vol.PersistentVolumeClaim.ReadOnly = true
		}
		volumes = append(volumes, vol)
		mounts = append(mounts, mount)
		haveVol[vol.Name] = true
		havePath[mount.MountPath] = true
	}
	return volumes, mounts
}

// BuildPluginService builds a companion Service. The selector targets either the
// Jellyfin instance pod ("instance") or a named companion workload
// ("workload:<name>").
func BuildPluginService(p *jellyfinv1alpha1.JellyfinPlugin, s jellyfinv1alpha1.PluginService, instanceName string) (*corev1.Service, error) {
	selector, err := resolveServiceSelector(p, s, instanceName)
	if err != nil {
		return nil, err
	}
	svcType := s.Type
	if svcType == "" {
		svcType = corev1.ServiceTypeClusterIP
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name,
			Namespace: p.Namespace,
			Labels:    map[string]string{PluginLabel: p.Name, ManagedByLabel: ManagedByValue},
		},
		Spec: corev1.ServiceSpec{
			Type:     svcType,
			Selector: selector,
			Ports:    s.Ports,
		},
	}, nil
}

func resolveServiceSelector(p *jellyfinv1alpha1.JellyfinPlugin, s jellyfinv1alpha1.PluginService, instanceName string) (map[string]string, error) {
	switch {
	case s.Selector == jellyfinv1alpha1.ServiceTargetInstance:
		if instanceName == "" {
			return nil, fmt.Errorf("service %q targets the instance but the plugin is not bound to one", s.Name)
		}
		return map[string]string{NameLabel: AppName, InstanceLabel: instanceName}, nil
	case strings.HasPrefix(s.Selector, "workload:"):
		wl := strings.TrimPrefix(s.Selector, "workload:")
		if wl == "" {
			return nil, fmt.Errorf("service %q: empty workload selector", s.Name)
		}
		return map[string]string{NameLabel: wl, PluginLabel: p.Name, WorkloadLabel: wl}, nil
	default:
		return nil, fmt.Errorf("service %q: unknown selector %q (want %q or workload:<name>)", s.Name, s.Selector, jellyfinv1alpha1.ServiceTargetInstance)
	}
}
