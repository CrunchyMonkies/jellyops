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

package controller

import (
	"context"
	"sort"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

// pluginBinds reports whether plugin p is bound to instance jf, via an explicit
// jellyfinRef or via the instance's pluginSelector labels. This single predicate
// is used by both desired-state derivation and the watch/enqueue mapper so the
// two can never diverge.
func pluginBinds(jf *jellyfinv1alpha1.Jellyfin, p *jellyfinv1alpha1.JellyfinPlugin) bool {
	if p.Namespace != jf.Namespace {
		return false
	}
	if p.Spec.JellyfinRef != nil && p.Spec.JellyfinRef.Name == jf.Name {
		return true
	}
	if jf.Spec.PluginSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(jf.Spec.PluginSelector)
		if err != nil {
			return false
		}
		if sel.Matches(labels.Set(p.Labels)) {
			return true
		}
	}
	return false
}

// BoundPlugins returns the plugins bound to jf, sorted by name for determinism.
func BoundPlugins(ctx context.Context, c client.Client, jf *jellyfinv1alpha1.Jellyfin) ([]jellyfinv1alpha1.JellyfinPlugin, error) {
	var list jellyfinv1alpha1.JellyfinPluginList
	if err := c.List(ctx, &list, client.InNamespace(jf.Namespace)); err != nil {
		return nil, err
	}
	var bound []jellyfinv1alpha1.JellyfinPlugin
	for i := range list.Items {
		if pluginBinds(jf, &list.Items[i]) {
			bound = append(bound, list.Items[i])
		}
	}
	sort.Slice(bound, func(i, j int) bool { return bound[i].Name < bound[j].Name })
	return bound, nil
}

// HealthyPlugins filters out plugins that must not enter the pod template: those
// explicitly ABI-incompatible or in the Failed phase. Newly-created plugins with
// no status yet are admitted (they are validated concurrently by the plugin
// reconciler).
func HealthyPlugins(plugins []jellyfinv1alpha1.JellyfinPlugin) []jellyfinv1alpha1.JellyfinPlugin {
	var out []jellyfinv1alpha1.JellyfinPlugin
	for i := range plugins {
		p := &plugins[i]
		if p.Status.Phase == jellyfinv1alpha1.PluginPhaseFailed {
			continue
		}
		if c := apimeta.FindStatusCondition(p.Status.Conditions, conditionABICompatible); c != nil && c.Status == metav1.ConditionFalse {
			continue
		}
		out = append(out, *p)
	}
	return out
}

// InstancesForPlugin returns the names of instances in the plugin's namespace
// that the plugin is bound to. Used by the plugin→instance watch mapper.
func InstancesForPlugin(ctx context.Context, c client.Client, p *jellyfinv1alpha1.JellyfinPlugin) ([]string, error) {
	var list jellyfinv1alpha1.JellyfinList
	if err := c.List(ctx, &list, client.InNamespace(p.Namespace)); err != nil {
		return nil, err
	}
	var names []string
	for i := range list.Items {
		if pluginBinds(&list.Items[i], p) {
			names = append(names, list.Items[i].Name)
		}
	}
	return names, nil
}
