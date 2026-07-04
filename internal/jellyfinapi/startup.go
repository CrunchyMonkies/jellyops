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
	"fmt"
	"net/http"
	"net/url"
)

// GetStartupConfiguration reads the first-run wizard configuration. A non-2xx
// result (e.g. 404 once setup is complete) is surfaced so the caller can treat
// the instance as already-configured.
func (c *Client) GetStartupConfiguration(ctx context.Context) (StartupConfiguration, error) {
	var cfg StartupConfiguration
	err := c.do(ctx, http.MethodGet, "/Startup/Configuration", nil, nil, &cfg)
	return cfg, err
}

// SetStartupConfiguration advances the wizard by posting locale settings
// (POST /Startup/Configuration). Jellyfin 10.11 requires this step before
// POST /Startup/User is routable — without it the user-creation call 404s.
func (c *Client) SetStartupConfiguration(ctx context.Context, cfg StartupConfiguration) error {
	if cfg.UICulture == "" {
		cfg.UICulture = "en-US"
	}
	if cfg.MetadataCountry == "" {
		cfg.MetadataCountry = "US"
	}
	if cfg.PreferredLanguage == "" {
		cfg.PreferredLanguage = "en"
	}
	return c.do(ctx, http.MethodPost, "/Startup/Configuration", nil, cfg, nil)
}

// GetStartupUser fetches the pending first-user state (GET /Startup/User).
// Jellyfin 10.11 requires this GET before POST /Startup/User becomes routable —
// without it the user-creation POST returns 404.
func (c *Client) GetStartupUser(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/Startup/User", nil, nil, nil)
}

// CreateStartupUser creates the initial admin user (POST /Startup/User).
func (c *Client) CreateStartupUser(ctx context.Context, name, password string) error {
	return c.do(ctx, http.MethodPost, "/Startup/User", nil,
		map[string]string{"Name": name, "Password": password}, nil)
}

// SetStartupRemoteAccess configures remote access during the wizard
// (POST /Startup/RemoteAccess). Automatic UPnP port mapping is left disabled.
func (c *Client) SetStartupRemoteAccess(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/Startup/RemoteAccess", nil,
		map[string]bool{"EnableRemoteAccess": true, "EnableAutomaticPortMapping": false}, nil)
}

// CompleteStartup finishes the first-run wizard (POST /Startup/Complete).
func (c *Client) CompleteStartup(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/Startup/Complete", nil, nil, nil)
}

// AuthenticateByName authenticates and returns the access token. It also sets
// the token on the client for subsequent calls.
func (c *Client) AuthenticateByName(ctx context.Context, username, password string) (string, error) {
	var res AuthenticationResult
	err := c.do(ctx, http.MethodPost, "/Users/AuthenticateByName", nil,
		map[string]string{"Username": username, "Pw": password}, &res)
	if err != nil {
		return "", err
	}
	if res.AccessToken == "" {
		return "", fmt.Errorf("authenticate: empty access token")
	}
	c.SetToken(res.AccessToken)
	return res.AccessToken, nil
}

// CreateAPIKey provisions a durable API key for the given app name and returns
// it. Jellyfin's POST /Auth/Keys does not return the key, so it is read back
// from GET /Auth/Keys by matching AppName.
func (c *Client) CreateAPIKey(ctx context.Context, app string) (string, error) {
	q := url.Values{"App": []string{app}}
	if err := c.do(ctx, http.MethodPost, "/Auth/Keys", q, nil, nil); err != nil {
		return "", err
	}
	var keys authKeysResult
	if err := c.do(ctx, http.MethodGet, "/Auth/Keys", nil, nil, &keys); err != nil {
		return "", err
	}
	for _, k := range keys.Items {
		if k.AppName == app && k.AccessToken != "" {
			return k.AccessToken, nil
		}
	}
	return "", fmt.Errorf("created API key for %q not found in key list", app)
}

// Bootstrap drives the first-run wizard end to end and returns a durable API
// key. It is idempotent: an already-configured instance (wizard endpoints
// unavailable) falls through to authentication + key minting.
func (c *Client) Bootstrap(ctx context.Context, username, password, app string) (string, error) {
	if _, err := c.GetStartupConfiguration(ctx); err == nil {
		// Wizard still available. The steps must run in order: Jellyfin 10.11
		// only routes POST /Startup/User after POST /Startup/Configuration has
		// advanced the wizard. Ignore "already done" conflicts so re-runs are safe.
		if err := c.SetStartupConfiguration(ctx, StartupConfiguration{}); err != nil && !IsStatus(err, http.StatusBadRequest) {
			return "", fmt.Errorf("set startup configuration: %w", err)
		}
		// GET must precede POST or Jellyfin 10.11 404s the user-creation call.
		if err := c.GetStartupUser(ctx); err != nil && !IsStatus(err, http.StatusBadRequest) {
			return "", fmt.Errorf("get startup user: %w", err)
		}
		if err := c.CreateStartupUser(ctx, username, password); err != nil && !IsStatus(err, http.StatusBadRequest) {
			return "", fmt.Errorf("create startup user: %w", err)
		}
		if err := c.SetStartupRemoteAccess(ctx); err != nil && !IsStatus(err, http.StatusBadRequest) {
			return "", fmt.Errorf("set startup remote access: %w", err)
		}
		if err := c.CompleteStartup(ctx); err != nil && !IsStatus(err, http.StatusBadRequest) {
			return "", fmt.Errorf("complete startup: %w", err)
		}
	}
	if _, err := c.AuthenticateByName(ctx, username, password); err != nil {
		return "", fmt.Errorf("authenticate: %w", err)
	}
	return c.CreateAPIKey(ctx, app)
}
