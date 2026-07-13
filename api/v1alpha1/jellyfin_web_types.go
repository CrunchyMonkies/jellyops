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

// WebSpec configures a separate web-tier Deployment serving the Jellyfin web
// client (typically an nginx-based jellyfin-web image on :80).
type WebSpec struct {
	// Image is the web-tier container image. When empty the operator does not
	// default; the CR author is expected to set this to the distro web image.
	// +optional
	Image string `json:"image,omitempty"`

	// Replicas is the desired number of web-tier pods. Defaults to 1.
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
