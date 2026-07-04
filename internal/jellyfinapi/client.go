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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a single Jellyfin instance over HTTP.
type Client struct {
	base  *url.URL
	hc    *http.Client
	token string

	clientName string
	device     string
	deviceID   string
	version    string
}

// New builds a Client for baseURL (e.g. http://jf.media.svc:8096). deviceID
// should be stable per instance so Jellyfin treats the operator as one client.
func New(baseURL, deviceID string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		base:       u,
		hc:         hc,
		clientName: "JellyOps",
		device:     "operator",
		deviceID:   deviceID,
		version:    "0.1.0",
	}, nil
}

// SetToken sets the API token used for authenticated calls.
func (c *Client) SetToken(t string) { c.token = t }

// Token returns the currently-set token.
func (c *Client) Token() string { return c.token }

// authHeader builds the MediaBrowser Authorization header. When a token is set
// it is included; otherwise the header carries only client identity (needed for
// AuthenticateByName and the first-run wizard).
func (c *Client) authHeader() string {
	parts := []string{
		fmt.Sprintf("Client=%q", c.clientName),
		fmt.Sprintf("Device=%q", c.device),
		fmt.Sprintf("DeviceId=%q", c.deviceID),
		fmt.Sprintf("Version=%q", c.version),
	}
	if c.token != "" {
		parts = append(parts, fmt.Sprintf("Token=%q", c.token))
	}
	return "MediaBrowser " + strings.Join(parts, ", ")
}

// do performs an HTTP request against path, optionally sending body as JSON and
// decoding a JSON response into out (both may be nil).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + path
	if query != nil {
		u.RawQuery = query.Encode()
	}

	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.authHeader())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: string(snippet)}
	}

	if out != nil {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	return nil
}

// APIError describes a non-2xx Jellyfin response.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("jellyfin %s %s: status %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// IsStatus reports whether err is an APIError with the given status code.
func IsStatus(err error, code int) bool {
	var apiErr *APIError
	if e, ok := err.(*APIError); ok {
		apiErr = e
	}
	return apiErr != nil && apiErr.StatusCode == code
}
