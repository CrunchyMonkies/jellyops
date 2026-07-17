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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func ptrBool(b bool) *bool    { return &b }
func ptrInt32(i int32) *int32 { return &i }

// TestEnforceEncodingOptionsPreservesUnmanaged is the critical case: the full-object
// POST must not clobber QSV/VAAPI (or any other) fields the operator doesn't manage.
func TestEnforceEncodingOptionsPreservesUnmanaged(t *testing.T) {
	cur := json.RawMessage(`{
		"HardwareAccelerationType":"qsv",
		"VaapiDevice":"/dev/dri/renderD128",
		"EnableThrottling":false,
		"EnableSegmentDeletion":false
	}`)

	out, changed, err := EnforceEncodingOptions(cur, DesiredEncoding{
		EnableThrottling:      ptrBool(true),
		ThrottleDelaySeconds:  ptrInt32(180),
		EnableSegmentDeletion: ptrBool(true),
		SegmentKeepSeconds:    ptrInt32(720),
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
	if got["HardwareAccelerationType"] != "qsv" || got["VaapiDevice"] != "/dev/dri/renderD128" {
		t.Errorf("unmanaged fields clobbered: %s", out)
	}
	// Managed fields applied.
	if got["EnableThrottling"] != true || got["EnableSegmentDeletion"] != true {
		t.Errorf("throttling/segment-deletion not enabled: %s", out)
	}
	if got["ThrottleDelaySeconds"] != float64(180) || got["SegmentKeepSeconds"] != float64(720) {
		t.Errorf("delay/keep seconds wrong: %s", out)
	}
}

func TestEnforceEncodingOptionsNoChangeWhenAlreadySet(t *testing.T) {
	cur := json.RawMessage(`{"EnableThrottling":true,"OtherField":"keep"}`)
	out, changed, err := EnforceEncodingOptions(cur, DesiredEncoding{EnableThrottling: ptrBool(true)})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("expected changed=false, got out=%s", out)
	}
	// Unchanged path returns the original bytes.
	if string(out) != string(cur) {
		t.Errorf("expected original returned, got %s", out)
	}
}

func TestEnforceEncodingOptionsUnsetLeavesFields(t *testing.T) {
	cur := json.RawMessage(`{"EnableThrottling":false}`)
	// Only DelaySeconds set; Enabled left nil must not force EnableThrottling.
	out, changed, err := EnforceEncodingOptions(cur, DesiredEncoding{ThrottleDelaySeconds: ptrInt32(90)})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true (delay added)")
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["EnableThrottling"] != false {
		t.Errorf("EnableThrottling should be left false when Enabled unset: %s", out)
	}
	if got["ThrottleDelaySeconds"] != float64(90) {
		t.Errorf("ThrottleDelaySeconds not applied: %s", out)
	}
}

func TestEnforceEncodingOptionsNullOrEmptyInput(t *testing.T) {
	for _, in := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(``)} {
		out, changed, err := EnforceEncodingOptions(in, DesiredEncoding{EnableThrottling: ptrBool(true)})
		if err != nil {
			t.Fatalf("input %q: %v", in, err)
		}
		if !changed {
			t.Errorf("input %q: expected changed=true", in)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("input %q: %v", in, err)
		}
		if got["EnableThrottling"] != true {
			t.Errorf("input %q: out = %s", in, out)
		}
	}
}

// TestEncodingConfigRoundTrip exercises GET + POST against a fake server.
func TestEncodingConfigRoundTrip(t *testing.T) {
	var posted json.RawMessage
	mux := http.NewServeMux()
	mux.HandleFunc("/System/Configuration/encoding", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"EnableThrottling":false,"VaapiDevice":"/dev/dri/renderD128"}`))
		case http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			posted = b
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
		}
	})
	c := newClient(t, mux)

	cur, err := c.GetEncodingConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	updated, changed, err := EnforceEncodingOptions(cur, DesiredEncoding{EnableThrottling: ptrBool(true)})
	if err != nil || !changed {
		t.Fatalf("enforce: changed=%v err=%v", changed, err)
	}
	if err := c.UpdateEncodingConfig(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(posted, &got); err != nil {
		t.Fatal(err)
	}
	if got["EnableThrottling"] != true || got["VaapiDevice"] != "/dev/dri/renderD128" {
		t.Errorf("posted config wrong: %s", posted)
	}
}
