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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

// HTTPRouteName returns the HTTPRoute name for a Jellyfin instance.
func HTTPRouteName(jf *jellyfinv1alpha1.Jellyfin) string {
	return truncateName(jf.Name)
}

// BuildHTTPRoute builds the desired Gateway API HTTPRoute for an instance.
//
// Split mode (jf.Spec.Web != nil): routes /web to the separate web-tier
// Service and everything else to the server Service, with carve-outs for the
// Jellyfin plugin-configuration API that lives under /web but is served by the
// server.
//
// Non-split mode (jf.Spec.Web == nil): the Jellyfin server serves both the API
// and the web client itself, so only two rules are emitted — the bare-root
// redirect and the catch-all to the server Service.
func BuildHTTPRoute(jf *jellyfinv1alpha1.Jellyfin) *gatewayv1.HTTPRoute {
	gw := jf.Spec.Gateway

	parentRef := gatewayv1.ParentReference{
		Group: ptrTo(gatewayv1.Group("gateway.networking.k8s.io")),
		Kind:  ptrTo(gatewayv1.Kind("Gateway")),
		Name:  gatewayv1.ObjectName(gw.GatewayRef.Name),
	}
	if gw.GatewayRef.Namespace != "" {
		parentRef.Namespace = ptrTo(gatewayv1.Namespace(gw.GatewayRef.Namespace))
	}
	if gw.GatewayRef.SectionName != "" {
		parentRef.SectionName = ptrTo(gatewayv1.SectionName(gw.GatewayRef.SectionName))
	}

	serverSvcName := gatewayv1.ObjectName(jf.Name)
	serverPort := gatewayv1.PortNumber(DefaultJellyfinPort)

	defaultPrefix := gatewayv1.PathMatchPathPrefix
	rootExact := gatewayv1.PathMatchExact

	// Rule 0 (most specific of all — an Exact match): redirect the bare
	// root "/" to "/web/". A stock Jellyfin server serves the web client
	// itself and redirects "/" -> "/web/index.html"; a headless server
	// build (web client delegated to the separate web tier) instead
	// redirects "/" to its API docs (/api-docs/swagger). Clients that load
	// the bare origin — notably the Jellyfin mobile apps, which load the
	// server root into a WebView and expect the web client — then land on
	// Swagger and fail ("connection cannot be established"). Restoring the
	// "/" -> "/web/" redirect at the gateway makes the origin behave like a
	// stock server. Exact "/" outranks the PathPrefix "/" default below, so
	// only the bare root is redirected; every API path still reaches the
	// server.
	//
	// When SSO.AutoLoginRedirect is enabled the redirect target is instead
	// the OAuth2 plugin's authorize endpoint (default "/sso/authorize"), so
	// unauthenticated visitors are sent straight to Keycloak. The /sso/*
	// paths reach the server Service via the existing PathPrefix "/" catch-all
	// rule (Rule 3), so no additional route rule is needed.
	rootRedirectRule := gatewayv1.HTTPRouteRule{
		Matches: []gatewayv1.HTTPRouteMatch{{
			Path: &gatewayv1.HTTPPathMatch{Type: &rootExact, Value: ptrTo("/")},
		}},
		Filters: []gatewayv1.HTTPRouteFilter{{
			Type: gatewayv1.HTTPRouteFilterRequestRedirect,
			RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
				StatusCode: ptrTo(302),
				Path: &gatewayv1.HTTPPathModifier{
					Type:            gatewayv1.FullPathHTTPPathModifier,
					ReplaceFullPath: ptrTo(rootRedirectTarget(gw)),
				},
			},
		}},
	}

	// Rule 3 (default): / -> server Service. Present in both split and
	// non-split modes as the final catch-all.
	defaultServerRule := gatewayv1.HTTPRouteRule{
		Matches: []gatewayv1.HTTPRouteMatch{{
			Path: &gatewayv1.HTTPPathMatch{
				Type:  &defaultPrefix,
				Value: ptrTo("/"),
			},
		}},
		BackendRefs: []gatewayv1.HTTPBackendRef{{
			BackendRef: gatewayv1.BackendRef{
				BackendObjectReference: gatewayv1.BackendObjectReference{
					Name: serverSvcName,
					Port: &serverPort,
				},
			},
		}},
	}

	var rules []gatewayv1.HTTPRouteRule
	if jf.Spec.Web != nil && jf.Spec.Web.EffectiveMode() == jellyfinv1alpha1.WebModeDeployment {
		// Split mode: a separate web-tier Service hosts the Jellyfin web client.
		// Route /web to it, with carve-outs so the plugin-configuration API
		// endpoints that happen to live under /web still reach the server.
		webSvcName := gatewayv1.ObjectName(WebServiceName(jf))
		webPort := gatewayv1.PortNumber(DefaultWebPort)
		webPrefix := gatewayv1.PathMatchPathPrefix
		headerExact := gatewayv1.HeaderMatchExact

		rules = []gatewayv1.HTTPRouteRule{
			rootRedirectRule,
			// Rule 1a (most specific): a full-page navigation to the SPA route
			// "/web/configurationpage" (a hard refresh or a directly-opened link)
			// must load the web client so the SPA can boot and then fetch the page
			// — it must NOT get the raw config-page HTML fragment. A top-level
			// browser navigation carries "Sec-Fetch-Mode: navigate"; the SPA's own
			// data fetch (Rule 1b) does not. So navigations go to the web tier and
			// only the singular page route needs this carve-out (the plural list
			// endpoint is never navigated to). Header + path outranks Rule 1b's
			// path-only match, so this wins for navigations and Rule 1b wins for
			// the XHR data fetch.
			{
				Matches: []gatewayv1.HTTPRouteMatch{
					{
						Path:    &gatewayv1.HTTPPathMatch{Type: &webPrefix, Value: ptrTo("/web/configurationpage")},
						Headers: []gatewayv1.HTTPHeaderMatch{{Type: &headerExact, Name: "Sec-Fetch-Mode", Value: "navigate"}},
					},
					{
						Path:    &gatewayv1.HTTPPathMatch{Type: &webPrefix, Value: ptrTo("/web/ConfigurationPage")},
						Headers: []gatewayv1.HTTPHeaderMatch{{Type: &headerExact, Name: "Sec-Fetch-Mode", Value: "navigate"}},
					},
				},
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: webSvcName,
							Port: &webPort,
						},
					},
				}},
			},
			// Rule 1b: Jellyfin serves its plugin-configuration API under /web
			// (GET /web/ConfigurationPages and /web/configurationpage?name=...).
			// Those are API endpoints, not SPA assets, so the SPA's data fetches
			// must reach the server Service even though the web tier owns the rest
			// of /web. Without this, the dashboard's plugin drawer fetches HTML
			// instead of JSON and crashes ("r.map is not a function"), and opening
			// a plugin config page fetches the SPA index instead of the page HTML
			// and crashes in loadView ("Cannot read properties of undefined
			// (reading 'classList')").
			//
			// Gateway API PathPrefix matches on whole path segments, so the plural
			// list endpoint (ConfigurationPages) and the singular page endpoint
			// (ConfigurationPage) are distinct segments and both must be listed.
			// Both casings are matched: the new React dashboard fetches the
			// LOWERCASE "/web/configurationpage" (its SPA route path reused as the
			// fetch URL), while older callers / the SDK use PascalCase. The server
			// routes case-insensitively; navigations are already peeled off by
			// Rule 1a.
			{
				Matches: []gatewayv1.HTTPRouteMatch{
					{Path: &gatewayv1.HTTPPathMatch{Type: &webPrefix, Value: ptrTo("/web/ConfigurationPages")}},
					{Path: &gatewayv1.HTTPPathMatch{Type: &webPrefix, Value: ptrTo("/web/configurationpages")}},
					{Path: &gatewayv1.HTTPPathMatch{Type: &webPrefix, Value: ptrTo("/web/ConfigurationPage")}},
					{Path: &gatewayv1.HTTPPathMatch{Type: &webPrefix, Value: ptrTo("/web/configurationpage")}},
				},
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: serverSvcName,
							Port: &serverPort,
						},
					},
				}},
			},
			// Rule 2: /web -> web-tier Service.
			{
				Matches: []gatewayv1.HTTPRouteMatch{{
					Path: &gatewayv1.HTTPPathMatch{
						Type:  &webPrefix,
						Value: ptrTo("/web"),
					},
				}},
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: webSvcName,
							Port: &webPort,
						},
					},
				}},
			},
			defaultServerRule,
		}
	} else {
		// Non-split mode: the Jellyfin server serves the web client itself. All
		// traffic — including /web — routes to the server Service. Only the
		// bare-root redirect and the default catch-all are needed.
		rules = []gatewayv1.HTTPRouteRule{
			rootRedirectRule,
			defaultServerRule,
		}
	}

	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        HTTPRouteName(jf),
			Namespace:   jf.Namespace,
			Labels:      InstanceLabels(jf),
			Annotations: gw.Annotations,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{parentRef},
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(gw.Hostname)},
			Rules:     rules,
		},
	}
}

// rootRedirectTarget returns the path that the bare "/" entry redirect points to.
// When SSO auto-login is enabled it returns the OAuth2 authorize endpoint; otherwise
// it returns "/web/" to preserve the default stock-server behaviour.
func rootRedirectTarget(gw *jellyfinv1alpha1.GatewaySpec) string {
	if gw.SSO != nil && gw.SSO.AutoLoginRedirect {
		if gw.SSO.AuthorizePath != "" {
			return gw.SSO.AuthorizePath
		}
		return "/sso/authorize"
	}
	return "/web/"
}

func ptrTo[T any](v T) *T {
	return &v
}
