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
	"strconv"
	"strings"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

// EffectiveMeta returns the plugin metadata the operator should use. CR-provided
// spec.meta always takes precedence; image-label / meta.json fallback is resolved
// by the reconciler before the plugin reaches the builder.
func EffectiveMeta(p *jellyfinv1alpha1.JellyfinPlugin) jellyfinv1alpha1.PluginMeta {
	return p.Spec.Meta
}

// PluginFolderName is the directory name Jellyfin expects under /config/plugins,
// i.e. "<Name>_<Version>" (e.g. "Distributed Transcoding_0.0.1.0"). This is a
// filesystem path component, not a Kubernetes object name.
func PluginFolderName(m jellyfinv1alpha1.PluginMeta) string {
	name := m.Name
	if name == "" {
		name = "plugin"
	}
	if m.Version == "" {
		return name
	}
	return name + "_" + m.Version
}

// PluginSubPath returns the directory within the plugin image that holds the
// plugin files. spec.meta.subPath takes precedence over pluginImage.subPath.
func PluginSubPath(p *jellyfinv1alpha1.JellyfinPlugin) string {
	if p.Spec.Meta.SubPath != "" {
		return p.Spec.Meta.SubPath
	}
	return p.Spec.PluginImage.SubPath
}

// parseVersion parses a dotted numeric version ("12.0.0.0") into up to four
// components, padded with zeros. Non-numeric or empty input is an error.
func parseVersion(s string) ([4]int, error) {
	var out [4]int
	s = strings.TrimSpace(s)
	if s == "" {
		return out, fmt.Errorf("empty version")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 4 {
		return out, fmt.Errorf("version %q has more than 4 components", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return out, fmt.Errorf("version %q: invalid component %q", s, p)
		}
		if n < 0 {
			return out, fmt.Errorf("version %q: negative component", s)
		}
		out[i] = n
	}
	return out, nil
}

// compareVersions returns -1, 0, or 1 for a<b, a==b, a>b.
func compareVersions(a, b [4]int) int {
	for i := 0; i < 4; i++ {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

// DefaultServerVersion is assumed when the instance image tag carries no
// parseable version.
const DefaultServerVersion = "10.9.0.0"

// imageTag extracts the tag from an image reference, ignoring any digest and the
// registry host:port.
func imageTag(image string) string {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	seg := image
	if slash := strings.LastIndex(image, "/"); slash >= 0 {
		seg = image[slash+1:]
	}
	if i := strings.LastIndex(seg, ":"); i >= 0 {
		return seg[i+1:]
	}
	return ""
}

// ServerVersionFromImage derives the Jellyfin server version from an image tag,
// e.g. "ghcr.io/x/jellyfin:12.0.0-net10" -> "12.0.0" and the standard v-prefixed
// form "ghcr.io/x/jellyfin:v12.0.0-rc-jc-0.1.0" -> "12.0.0". Returns "" when the
// tag carries no parseable version.
func ServerVersionFromImage(image string) string {
	tag := imageTag(image)
	if tag == "" {
		return ""
	}
	ver := tag
	if i := strings.IndexAny(tag, "-+_"); i >= 0 {
		ver = tag[:i]
	}
	// Tolerate the conventional "v" prefix on semver tags (e.g. v12.0.0).
	ver = strings.TrimPrefix(ver, "v")
	if _, err := parseVersion(ver); err != nil {
		return ""
	}
	return ver
}

// ABICompatible reports whether a plugin targeting targetABI can load on a server
// running serverVersion. A plugin is compatible when the server version is at
// least its targetABI (Jellyfin loads plugins whose targetAbi <= app version).
func ABICompatible(targetABI, serverVersion string) (bool, error) {
	t, err := parseVersion(targetABI)
	if err != nil {
		return false, fmt.Errorf("targetAbi: %w", err)
	}
	s, err := parseVersion(serverVersion)
	if err != nil {
		return false, fmt.Errorf("server version: %w", err)
	}
	return compareVersions(s, t) >= 0, nil
}
