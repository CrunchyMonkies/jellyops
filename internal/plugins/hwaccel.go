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
	// Hardware acceleration backends (spec.hardwareAcceleration.type).
	AccelVAAPI  = "vaapi"
	AccelQSV    = "qsv"
	AccelNvidia = "nvidia"

	// DRIDeviceVolumeName is the volume that exposes the render device(s).
	DRIDeviceVolumeName = "dri-device"
	// DRIDirPath is the DRM device directory on Linux hosts.
	DRIDirPath = "/dev/dri"

	// RenderDeviceEnvVar pins the render node for the startup scan (set from
	// spec.hardwareAcceleration.devicePath).
	RenderDeviceEnvVar = "JELLYOPS_RENDER_DEVICE"
	// RenderVendorEnvVar restricts the startup scan to one PCI vendor id.
	RenderVendorEnvVar = "JELLYOPS_RENDER_VENDOR"
	// intelPCIVendorID is Intel's PCI vendor id as sysfs reports it; qsv is
	// Intel-only, so the scan prefers an Intel node on multi-GPU hosts.
	intelPCIVendorID = "0x8086"

	nvidiaGPUResource = "nvidia.com/gpu"
)

// renderSetupScript runs before the Jellyfin binary. The render node is not
// always renderD128 (multi-GPU hosts and device plugins yield renderD129,
// renderD130, ...), so the script scans /dev/dri for the node this container
// can actually see and points Jellyfin's encoding.xml (VaapiDevice/QsvDevice)
// at it. JELLYOPS_RENDER_DEVICE skips the scan; JELLYOPS_RENDER_VENDOR
// restricts it by PCI vendor, falling back to the first node when no vendor
// matches.
const renderSetupScript = `dev="${JELLYOPS_RENDER_DEVICE:-}"
if [ -z "$dev" ] && [ -n "${JELLYOPS_RENDER_VENDOR:-}" ]; then
  for d in /dev/dri/renderD*; do
    [ -c "$d" ] || continue
    v="$(cat "/sys/class/drm/$(basename "$d")/device/vendor" 2>/dev/null || true)"
    if [ "$v" = "$JELLYOPS_RENDER_VENDOR" ]; then dev="$d"; break; fi
  done
fi
if [ -z "$dev" ]; then
  for d in /dev/dri/renderD*; do
    if [ -c "$d" ]; then dev="$d"; break; fi
  done
fi
if [ -n "$dev" ]; then
  echo "jellyops: using render device $dev"
  cfgdir="${JELLYFIN_CONFIG_DIR:-${JELLYFIN_DATA_DIR:-/config}/config}"
  mkdir -p "$cfgdir"
  f="$cfgdir/encoding.xml"
  if [ ! -f "$f" ]; then
    printf '<?xml version="1.0" encoding="utf-8"?>\n<EncodingOptions>\n  <VaapiDevice>%s</VaapiDevice>\n  <QsvDevice>%s</QsvDevice>\n</EncodingOptions>\n' "$dev" "$dev" >"$f"
  else
    for tag in VaapiDevice QsvDevice; do
      if grep -q "<$tag>" "$f"; then
        sed -i "s#<$tag>[^<]*</$tag>#<$tag>$dev</$tag>#" "$f"
      else
        sed -i "s#</EncodingOptions>#  <$tag>$dev</$tag>\n</EncodingOptions>#" "$f"
      fi
    done
  fi
else
  echo "jellyops: no render device found under /dev/dri" >&2
fi
`

// applyHardwareAccel attaches a transcoding device to the pod/container.
func applyHardwareAccel(pod *corev1.PodSpec, hw *jellyfinv1alpha1.HardwareAccel) {
	if hw == nil {
		return
	}
	jellyfin := &pod.Containers[0]
	switch hw.Type {
	case AccelVAAPI, AccelQSV:
		switch {
		case hw.DeviceResource != "":
			// Device-plugin path: the plugin injects the render node and the request
			// schedules the pod onto a GPU node. No hostPath, so the pod can run
			// under the "restricted" Pod Security Standards.
			setResourceLimitOne(jellyfin, corev1.ResourceName(hw.DeviceResource))
		case hw.DevicePath != "":
			// Explicitly pinned device node.
			pod.Volumes = append(pod.Volumes, corev1.Volume{
				Name: DRIDeviceVolumeName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{Path: hw.DevicePath, Type: ptr.To(corev1.HostPathCharDev)},
				},
			})
			jellyfin.VolumeMounts = append(jellyfin.VolumeMounts, corev1.VolumeMount{Name: DRIDeviceVolumeName, MountPath: hw.DevicePath})
		default:
			// Mount the whole /dev/dri: the render node's number varies by host
			// (renderD128, D129, ...), so expose them all and let the startup scan
			// pick the right one.
			pod.Volumes = append(pod.Volumes, corev1.Volume{
				Name: DRIDeviceVolumeName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{Path: DRIDirPath, Type: ptr.To(corev1.HostPathDirectory)},
				},
			})
			jellyfin.VolumeMounts = append(jellyfin.VolumeMounts, corev1.VolumeMount{Name: DRIDeviceVolumeName, MountPath: DRIDirPath})
		}

		// Startup wrapper: resolve the render node visible to this container and
		// sync Jellyfin's encoding config to it before the server starts.
		if hw.DevicePath != "" {
			jellyfin.Env = append(jellyfin.Env, corev1.EnvVar{Name: RenderDeviceEnvVar, Value: hw.DevicePath})
		}
		if hw.Type == AccelQSV {
			jellyfin.Env = append(jellyfin.Env, corev1.EnvVar{Name: RenderVendorEnvVar, Value: intelPCIVendorID})
		}
		jellyfin.Command = []string{"/bin/sh", "-c", renderSetupScript + "exec " + DefaultJellyfinCommand}

		// The render group lets a non-root container open the device.
		if hw.RenderGroupGID != nil {
			if pod.SecurityContext == nil {
				pod.SecurityContext = &corev1.PodSecurityContext{}
			}
			pod.SecurityContext.SupplementalGroups = append(pod.SecurityContext.SupplementalGroups, *hw.RenderGroupGID)
		}
	case AccelNvidia:
		setResourceLimitOne(jellyfin, nvidiaGPUResource)
		if hw.RuntimeClassName != "" {
			pod.RuntimeClassName = ptr.To(hw.RuntimeClassName)
		}
	}
}

func setResourceLimitOne(c *corev1.Container, name corev1.ResourceName) {
	if c.Resources.Limits == nil {
		c.Resources.Limits = corev1.ResourceList{}
	}
	c.Resources.Limits[name] = resourceQuantityOne()
}
