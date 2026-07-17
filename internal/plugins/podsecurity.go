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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

const (
	DefaultRunAsUser  int64 = 1000
	DefaultRunAsGroup int64 = 1000
	DefaultFsGroup    int64 = 1000
)

func effectiveBool(override *bool, fallback bool) bool {
	if override != nil {
		return *override
	}
	return fallback
}

func effectiveInt64(override *int64, fallback int64) int64 {
	if override != nil {
		return *override
	}
	return fallback
}

// applyPodSecurity sets the hardened pod-security defaults on a container and
// pod spec, merging any user overrides from ps. It is safe to call with ps==nil
// (pure defaults apply). Existing fields on the pod SecurityContext (e.g.
// SeccompProfile, SupplementalGroups from hw-accel) are preserved.
func applyPodSecurity(container *corev1.Container, pod *corev1.PodSpec, ps *jellyfinv1alpha1.PodSecuritySpec) {
	nonRoot := true
	uid := DefaultRunAsUser
	gid := DefaultRunAsGroup
	fsGroup := DefaultFsGroup
	var supplemental []int64

	if ps != nil {
		nonRoot = effectiveBool(ps.RunAsNonRoot, nonRoot)
		uid = effectiveInt64(ps.RunAsUser, uid)
		gid = effectiveInt64(ps.RunAsGroup, gid)
		fsGroup = effectiveInt64(ps.FsGroup, fsGroup)
		supplemental = ps.SupplementalGroups
	}

	// Container SecurityContext.
	if container.SecurityContext == nil {
		container.SecurityContext = &corev1.SecurityContext{}
	}
	container.SecurityContext.RunAsNonRoot = ptr.To(nonRoot)
	container.SecurityContext.RunAsUser = ptr.To(uid)
	container.SecurityContext.RunAsGroup = ptr.To(gid)
	container.SecurityContext.AllowPrivilegeEscalation = ptr.To(false)
	container.SecurityContext.Capabilities = &corev1.Capabilities{
		Drop: []corev1.Capability{"ALL"},
	}

	// Pod SecurityContext — merge, do not overwrite.
	if pod.SecurityContext == nil {
		pod.SecurityContext = &corev1.PodSecurityContext{}
	}
	pod.SecurityContext.RunAsNonRoot = ptr.To(nonRoot)
	pod.SecurityContext.RunAsUser = ptr.To(uid)
	pod.SecurityContext.RunAsGroup = ptr.To(gid)
	pod.SecurityContext.FSGroup = ptr.To(fsGroup)
	pod.SecurityContext.SupplementalGroups = append(pod.SecurityContext.SupplementalGroups, supplemental...)
}

// applyContainerSecurity sets the hardened container-level security context on
// an init container, inheriting the pod-level UID/GID identity. Use this for
// init containers that don't need pod-spec-level changes (those are set once
// by applyPodSecurity on the main container).
func applyContainerSecurity(container *corev1.Container, ps *jellyfinv1alpha1.PodSecuritySpec) {
	nonRoot := true
	uid := DefaultRunAsUser
	gid := DefaultRunAsGroup

	if ps != nil {
		nonRoot = effectiveBool(ps.RunAsNonRoot, nonRoot)
		uid = effectiveInt64(ps.RunAsUser, uid)
		gid = effectiveInt64(ps.RunAsGroup, gid)
	}

	if container.SecurityContext == nil {
		container.SecurityContext = &corev1.SecurityContext{}
	}
	container.SecurityContext.RunAsNonRoot = ptr.To(nonRoot)
	container.SecurityContext.RunAsUser = ptr.To(uid)
	container.SecurityContext.RunAsGroup = ptr.To(gid)
	container.SecurityContext.AllowPrivilegeEscalation = ptr.To(false)
	container.SecurityContext.Capabilities = &corev1.Capabilities{
		Drop: []corev1.Capability{"ALL"},
	}
}
