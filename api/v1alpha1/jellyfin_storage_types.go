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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
)

// JellyfinStorage configures the persistent storage for a Jellyfin instance.
type JellyfinStorage struct {
	// Config is the RWO PVC backing Jellyfin's /config (settings, metadata,
	// plugin state). Mounted writable so Jellyfin can mutate plugin meta.json.
	// +optional
	Config PVCSpec `json:"config,omitempty"`

	// Cache is an optional PVC for Jellyfin's transcode/image cache.
	// +optional
	Cache *PVCSpec `json:"cache,omitempty"`

	// Media is the set of library folders mounted into the Jellyfin pod. A folder
	// with a library block is also registered as a Jellyfin library via the API.
	// +listType=map
	// +listMapKey=name
	// +optional
	Media []MediaFolder `json:"media,omitempty"`
}

// PVCSpec describes a PersistentVolumeClaim the operator manages or references.
type PVCSpec struct {
	// ExistingClaim references an already-provisioned PVC by name. When set, Size
	// and StorageClassName are ignored and the operator creates nothing.
	// +optional
	ExistingClaim string `json:"existingClaim,omitempty"`

	// Size is the requested capacity when the operator provisions the PVC.
	// +optional
	Size resource.Quantity `json:"size,omitempty"`

	// StorageClassName selects the storage class for a provisioned PVC.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// AccessModes for a provisioned PVC. Defaults to ReadWriteOnce for config and
	// ReadWriteMany for shared media.
	// +optional
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`

	// MountPath overrides the default mount path inside the pod.
	// +optional
	MountPath string `json:"mountPath,omitempty"`
}

// MediaFolder is one library folder mounted into the Jellyfin pod. Exactly one
// source (nfs, pvc, or existingClaim) must be set.
// +kubebuilder:validation:XValidation:rule="[has(self.nfs), has(self.pvc), has(self.existingClaim)].filter(x, x).size() == 1",message="exactly one of nfs, pvc, or existingClaim must be set"
type MediaFolder struct {
	// Name identifies the folder, e.g. "movies". Used for the volume name and,
	// when no library.name is given, as the Jellyfin library display name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// MountPath is the absolute path the folder is mounted at inside the pod,
	// e.g. /media/movies.
	// +kubebuilder:validation:MinLength=1
	MountPath string `json:"mountPath"`

	// ReadOnly mounts the folder read-only. Defaults vary by source; NFS defaults
	// to read-only.
	// +optional
	ReadOnly bool `json:"readOnly,omitempty"`

	// NFS mounts an NFS export directly (inline) or via a provisioned PV+PVC.
	// +optional
	NFS *NFSSource `json:"nfs,omitempty"`

	// PVC provisions a dedicated PersistentVolumeClaim for this folder.
	// +optional
	PVC *PVCSpec `json:"pvc,omitempty"`

	// ExistingClaim mounts an already-provisioned PVC by name.
	// +optional
	ExistingClaim string `json:"existingClaim,omitempty"`

	// Library, when set, registers this folder as a Jellyfin library via the API
	// (requires spec.api.manageLibraries). Absent means mounted-but-not-registered.
	// +optional
	Library *LibrarySpec `json:"library,omitempty"`
}

// NFSSource targets an NFS export to be added as a Jellyfin library folder.
type NFSSource struct {
	// Server is the NFS server hostname or IP.
	// +kubebuilder:validation:MinLength=1
	Server string `json:"server"`

	// Path is the exported path on the server.
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// ReadOnly mounts the export read-only (default true for inline NFS).
	// +optional
	ReadOnly bool `json:"readOnly,omitempty"`

	// Provision, when true, creates a shared RWX PV+PVC backed by the export so
	// companion workers can reuse it (spec §8.2). When false (default) the
	// operator adds an inline NFSVolumeSource to the pod. MountOptions are only
	// honored in provisioned mode, so setting them forces provisioning.
	// +optional
	Provision bool `json:"provision,omitempty"`

	// StorageClassName for the provisioned PV+PVC.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// MountOptions applied to the provisioned PV, e.g. ["nfsvers=4.1","hard"].
	// Ignored for inline NFS (Kubernetes NFSVolumeSource has no mount options).
	// +optional
	MountOptions []string `json:"mountOptions,omitempty"`
}

// LibrarySpec registers a mounted media folder as a Jellyfin virtual folder.
type LibrarySpec struct {
	// Name is the library display name. Defaults to the MediaFolder name.
	// +optional
	Name string `json:"name,omitempty"`

	// CollectionType selects the library kind, e.g. movies, tvshows, music.
	// +kubebuilder:validation:Enum=movies;tvshows;music;musicvideos;homevideos;boxsets;books;mixed
	// +optional
	CollectionType string `json:"collectionType,omitempty"`

	// Options are opaque Jellyfin LibraryOptions passed through verbatim on
	// create/update.
	// +optional
	Options *runtime.RawExtension `json:"options,omitempty"`
}
