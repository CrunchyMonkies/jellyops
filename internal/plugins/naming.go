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

// Package plugins builds the Jellyfin pod template and companion objects from a
// Jellyfin instance and its bound JellyfinPlugins. It is deliberately
// client-free and side-effect-free so it is fully unit-testable without a
// cluster.
package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

// Well-known names, paths, and labels used across the built objects.
const (
	// AppName is the app.kubernetes.io/name label value for instance pods.
	AppName = "jellyfin"
	// WebAppName is the app.kubernetes.io/name for the web tier. It MUST differ from
	// AppName so web pods are not matched by instance-scoped selectors (the server
	// Service on :8096 and plugin gRPC services select on {name: AppName, instance},
	// and a web pod carrying AppName would pollute their endpoints).
	WebAppName = "jellyfin-web"
	// ManagedByValue marks objects owned by this operator.
	ManagedByValue = "jellyops"

	NameLabel      = "app.kubernetes.io/name"
	InstanceLabel  = "app.kubernetes.io/instance"
	ManagedByLabel = "app.kubernetes.io/managed-by"
	ComponentLabel = "app.kubernetes.io/component"
	// PluginLabel records which JellyfinPlugin owns a companion object.
	PluginLabel = "jellyfin.jellyops.io/plugin"

	JellyfinContainerName = "jellyfin"

	ConfigVolumeName = "config"
	CacheVolumeName  = "cache"

	ConfigMountPath = "/config"
	CacheMountPath  = "/cache"
	// PluginsDirPath is where Jellyfin discovers plugins.
	PluginsDirPath = "/config/plugins"
	// InstalledMarkerDir holds runOnce markers keyed by plugin folder name.
	InstalledMarkerDir = "/config/.jellyops/installed"
	// FirstRunMarkerDir holds firstrun-hook markers keyed by plugin folder name.
	// Separate from InstalledMarkerDir (which is for install.runOnce) so the baked
	// firstrun.sh hook and a custom install can each run-once independently.
	FirstRunMarkerDir = "/config/.jellyops/firstrun"
	// StagingSrcBase is where a plugin image volume is mounted for imageVolumeCopy.
	StagingSrcBase = "/plugins-src"

	// BootstrapHookFile is a standard hook baked at the plugin image root that
	// jellyops runs on every pod startup (if present) for imageVolumeCopy plugins.
	BootstrapHookFile = "bootstrap.sh"
	// FirstRunHookFile is a standard hook baked at the plugin image root that
	// jellyops runs once per instance (marker-gated) for imageVolumeCopy plugins.
	FirstRunHookFile = "firstrun.sh"

	// DefaultJellyfinPort is the Jellyfin HTTP port.
	DefaultJellyfinPort int32 = 8096
	// DefaultJellyfinImage is the stock/official server image used when the CR
	// does not override spec.image.
	DefaultJellyfinImage = "jellyfin/jellyfin:latest"

	// Web-as-volume mode (spec.web.mode: volume): the web image is mounted into
	// the server pod and the server hosts /web from it.
	//
	// WebContentVolumeName is the volume holding the mounted web image.
	WebContentVolumeName = "jellyfin-web-content"
	// WebContentMountPath is where the web assets are mounted in the server
	// container and where the server is pointed via JELLYFIN_WEB_DIR.
	WebContentMountPath = "/jellyfin-web"
	// DefaultJellyfinCommand overrides the fork image ENTRYPOINT to drop the
	// baked "--nowebclient" flag so the server hosts the web client itself. This
	// is the CrunchyMonkies fork's launcher path.
	DefaultJellyfinCommand = "/jellyfin/jellyfin"

	maxNameLen = 63
)

var (
	nonAlphaNum   = regexp.MustCompile(`[^a-z0-9]+`)
	trimDashesRE  = regexp.MustCompile(`(^-+)|(-+$)`)
	multiDashesRE = regexp.MustCompile(`-{2,}`)
)

// SanitizeDNS1123 converts an arbitrary string into a DNS-1123 label
// (lowercase alphanumerics and '-', starting/ending alphanumeric, <=63 chars).
// Empty or fully-invalid input yields a deterministic hash-based fallback.
func SanitizeDNS1123(s string) string {
	lower := strings.ToLower(s)
	lower = nonAlphaNum.ReplaceAllString(lower, "-")
	lower = multiDashesRE.ReplaceAllString(lower, "-")
	lower = trimDashesRE.ReplaceAllString(lower, "")
	if lower == "" {
		return "x" + shortHash(s)
	}
	return truncateName(lower)
}

// truncateName caps a name at 63 chars, appending a short content hash so
// distinct long inputs stay distinct and the result still ends alphanumeric.
func truncateName(s string) string {
	if len(s) <= maxNameLen {
		return s
	}
	h := shortHash(s)
	keep := maxNameLen - len(h) - 1
	return trimDashesRE.ReplaceAllString(s[:keep], "") + "-" + h
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// prefixed builds "<prefix>-<name>" and keeps it DNS-1123 safe and <=63 chars.
func prefixed(prefix, name string) string {
	return truncateName(prefix + "-" + SanitizeDNS1123(name))
}

// InstanceLabels are applied to instance-owned objects.
func InstanceLabels(jf *jellyfinv1alpha1.Jellyfin) map[string]string {
	return map[string]string{
		NameLabel:      AppName,
		InstanceLabel:  jf.Name,
		ManagedByLabel: ManagedByValue,
	}
}

// InstanceSelectorLabels are the immutable Deployment/Service selector labels.
func InstanceSelectorLabels(jf *jellyfinv1alpha1.Jellyfin) map[string]string {
	return map[string]string{
		NameLabel:     AppName,
		InstanceLabel: jf.Name,
	}
}

func imageVolumeName(p *jellyfinv1alpha1.JellyfinPlugin) string {
	return prefixed("plugin", p.Name)
}

func stagingContainerName(p *jellyfinv1alpha1.JellyfinPlugin) string {
	return prefixed("stage", p.Name)
}

func installContainerName(p *jellyfinv1alpha1.JellyfinPlugin) string {
	return prefixed("install", p.Name)
}

func hookContainerName(p *jellyfinv1alpha1.JellyfinPlugin) string {
	return prefixed("hook", p.Name)
}

func mediaVolumeName(mf jellyfinv1alpha1.MediaFolder) string {
	return prefixed("media", mf.Name)
}

// ConfigClaimName is the PVC name backing /config (existingClaim or derived).
func ConfigClaimName(jf *jellyfinv1alpha1.Jellyfin) string {
	if c := jf.Spec.Storage.Config.ExistingClaim; c != "" {
		return c
	}
	return truncateName(jf.Name + "-config")
}

// CacheClaimName is the PVC name backing the cache volume.
func CacheClaimName(jf *jellyfinv1alpha1.Jellyfin) string {
	if jf.Spec.Storage.Cache != nil && jf.Spec.Storage.Cache.ExistingClaim != "" {
		return jf.Spec.Storage.Cache.ExistingClaim
	}
	return truncateName(jf.Name + "-cache")
}

// MediaClaimName is the PVC name backing a PVC/provisioned-NFS media folder.
func MediaClaimName(jf *jellyfinv1alpha1.Jellyfin, mf jellyfinv1alpha1.MediaFolder) string {
	if mf.ExistingClaim != "" {
		return mf.ExistingClaim
	}
	if mf.PVC != nil && mf.PVC.ExistingClaim != "" {
		return mf.PVC.ExistingClaim
	}
	return prefixed(jf.Name+"-media", mf.Name)
}
