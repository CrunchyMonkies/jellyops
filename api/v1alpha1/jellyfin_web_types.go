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

import corev1 "k8s.io/api/core/v1"

// Web serving modes.
const (
	// WebModeDeployment runs a separate nginx web-tier Deployment + Service (default).
	WebModeDeployment = "deployment"
	// WebModeVolume mounts the web Image as a read-only image volume into the
	// Jellyfin server pod and has the server host /web from it (so server-side
	// static-file plugins like File Transformation can rewrite the web client).
	WebModeVolume = "volume"
)

// WebSpec configures how the Jellyfin web client is served — either as a separate
// web-tier Deployment (nginx jellyfin-web image), or mounted into the server pod as
// an image volume the server serves itself.
type WebSpec struct {
	// Image is the web container image. When empty the operator does not default;
	// the CR author is expected to set this to the distro web image.
	// +optional
	Image string `json:"image,omitempty"`

	// Mode selects how the web client is served. "deployment" (default) runs a
	// separate nginx web-tier Deployment. "volume" mounts Image as a read-only
	// image volume into the Jellyfin server pod and has the server host /web from
	// it (drops --nowebclient, points --webdir at the mount), so server-side
	// static-file plugins like File Transformation can transform the web client.
	// +kubebuilder:validation:Enum=deployment;volume
	// +kubebuilder:default=deployment
	// +optional
	Mode string `json:"mode,omitempty"`

	// SubPath is the directory within the web Image holding the built assets
	// (index.html, chunks). Used only in "volume" mode. For the nginx jellyfin-web
	// image this is "usr/share/nginx/html/web"; empty means the image root is the
	// web dir.
	// +optional
	SubPath string `json:"subPath,omitempty"`

	// PullPolicy for the web image volume in "volume" mode. Defaults to IfNotPresent.
	// +optional
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`

	// Replicas is the desired number of web-tier pods (deployment mode). Defaults to 1.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Resources sets the web container resource requests/limits.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Service configures the ClusterIP Service fronting the web Deployment.
	// +optional
	Service ServiceSpec `json:"service,omitempty"`

	// PodAnnotations are merged onto the web pod template metadata.
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`
}

// EffectiveMode returns the web serving mode, defaulting to WebModeDeployment when unset.
func (w *WebSpec) EffectiveMode() string {
	if w == nil || w.Mode == "" {
		return WebModeDeployment
	}
	return w.Mode
}

// GatewaySpec configures a Gateway API HTTPRoute for the Jellyfin instance,
// routing /web to the web-tier Service and everything else to the server Service.
type GatewaySpec struct {
	// GatewayRef identifies the Gateway resource this HTTPRoute attaches to.
	GatewayRef GatewayReference `json:"gatewayRef"`

	// Hostname is the DNS name the HTTPRoute matches on.
	// +kubebuilder:validation:MinLength=1
	Hostname string `json:"hostname"`

	// Annotations are applied to the HTTPRoute.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// SSO configures OIDC/Keycloak auto-login behaviour at the gateway.
	// +optional
	SSO *GatewaySSO `json:"sso,omitempty"`
}

// GatewaySSO toggles gateway-level auto-login redirect to the OAuth2 plugin's authorize endpoint.
type GatewaySSO struct {
	// AutoLoginRedirect, when true, makes the entry route redirect to AuthorizePath
	// instead of /web/, so unauthenticated visitors are sent straight to Keycloak.
	// +optional
	AutoLoginRedirect bool `json:"autoLoginRedirect,omitempty"`

	// AuthorizePath is the plugin authorize endpoint. Defaults to "/sso/authorize".
	// +optional
	// +kubebuilder:default="/sso/authorize"
	AuthorizePath string `json:"authorizePath,omitempty"`
}

// GatewayReference identifies a Gateway resource.
type GatewayReference struct {
	// Name is the Gateway resource name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace is the Gateway's namespace. When empty the HTTPRoute's own
	// namespace is assumed.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// SectionName is a specific listener on the Gateway (e.g. "https").
	// +optional
	SectionName string `json:"sectionName,omitempty"`
}
