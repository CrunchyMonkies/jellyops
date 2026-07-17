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

package jellyfinapi

import (
	"encoding/json"
	"testing"
)

func ptrString(s string) *string { return &s }
func ptrInt64(i int64) *int64    { return &i }

// TestEnforceServerConfigPreservesUnmanaged is the critical case: the full-object POST
// to /System/Configuration must not clobber fields the operator does not manage.
func TestEnforceServerConfigPreservesUnmanaged(t *testing.T) {
	cur := json.RawMessage(`{
		"ServerName":"old",
		"PreviousVersionStr":"12.0.0",
		"IsStartupWizardCompleted":true,
		"MinResumePct":5,
		"MetadataCountryCode":"US"
	}`)

	out, changed, err := EnforceServerConfig(cur, DesiredServerConfig{
		ServerName:            ptrString("home-media"),
		QuickConnectAvailable: ptrBool(false),
		MinResumePct:          ptrInt32(3),
		MaxResumePct:          ptrInt32(92),
		CorsHosts:             []string{"https://example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	// Unmanaged fields survive untouched.
	if got["PreviousVersionStr"] != "12.0.0" || got["IsStartupWizardCompleted"] != true || got["MetadataCountryCode"] != "US" {
		t.Errorf("unmanaged fields clobbered: %s", out)
	}
	// Managed fields applied.
	if got["ServerName"] != "home-media" || got["QuickConnectAvailable"] != false {
		t.Errorf("general fields not applied: %s", out)
	}
	if got["MinResumePct"] != float64(3) || got["MaxResumePct"] != float64(92) {
		t.Errorf("playback fields not applied: %s", out)
	}
	hosts, ok := got["CorsHosts"].([]any)
	if !ok || len(hosts) != 1 || hosts[0] != "https://example.com" {
		t.Errorf("CorsHosts not applied: %s", out)
	}
}

func TestEnforceServerConfigNoChangeWhenAlreadySet(t *testing.T) {
	cur := json.RawMessage(`{"ServerName":"home-media","MinResumePct":3}`)
	out, changed, err := EnforceServerConfig(cur, DesiredServerConfig{
		ServerName:   ptrString("home-media"),
		MinResumePct: ptrInt32(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("expected changed=false, got %s", out)
	}
}

func TestEnforceServerConfigEmptyDesiredNoChange(t *testing.T) {
	cur := json.RawMessage(`{"ServerName":"home-media"}`)
	d := DesiredServerConfig{}
	if !d.Empty() {
		t.Fatal("expected Empty()=true for zero DesiredServerConfig")
	}
	_, changed, err := EnforceServerConfig(cur, d)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("empty desired must not change config")
	}
}

func TestEnforceServerConfigInt64AndSlowResponse(t *testing.T) {
	cur := json.RawMessage(`{}`)
	out, changed, err := EnforceServerConfig(cur, DesiredServerConfig{
		SlowResponseThresholdMs: ptrInt64(750),
	})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["SlowResponseThresholdMs"] != float64(750) {
		t.Errorf("int64 field wrong: %s", out)
	}
}

// TestEnforceBrandingPreservesSplashLocation confirms the operator only writes the
// three managed branding fields and never clobbers the server-managed
// SplashscreenLocation on the full-object POST.
func TestEnforceBrandingPreservesSplashLocation(t *testing.T) {
	cur := json.RawMessage(`{
		"LoginDisclaimer":"old",
		"CustomCss":"",
		"SplashscreenEnabled":false,
		"SplashscreenLocation":"/config/splash.png"
	}`)

	out, changed, err := EnforceBranding(cur, DesiredBranding{
		LoginDisclaimer:     ptrString("Welcome"),
		SplashscreenEnabled: ptrBool(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["SplashscreenLocation"] != "/config/splash.png" {
		t.Errorf("SplashscreenLocation clobbered: %s", out)
	}
	if got["LoginDisclaimer"] != "Welcome" || got["SplashscreenEnabled"] != true {
		t.Errorf("managed branding fields not applied: %s", out)
	}
	// CustomCss was not declared -> left as-is.
	if got["CustomCss"] != "" {
		t.Errorf("unmanaged CustomCss changed: %s", out)
	}
}
