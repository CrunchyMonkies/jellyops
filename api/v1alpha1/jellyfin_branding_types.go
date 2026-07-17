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

// BrandingSpec declaratively manages Jellyfin's Dashboard → Branding settings. These
// live in the "branding" named configuration, reconciled over the HTTP API
// (GET/POST /System/Configuration/branding), so this block requires spec.api. Every
// field is an optional pointer: nil means "leave Jellyfin's current value untouched".
// The server-managed SplashscreenLocation is never written (round-tripped untouched).
type BrandingSpec struct {
	// LoginDisclaimer is markdown shown on the login screen.
	// +optional
	LoginDisclaimer *string `json:"loginDisclaimer,omitempty"`

	// CustomCss is injected into the web client.
	// +optional
	CustomCss *string `json:"customCss,omitempty"`

	// SplashscreenEnabled toggles the login splash screen.
	// +optional
	SplashscreenEnabled *bool `json:"splashscreenEnabled,omitempty"`
}
