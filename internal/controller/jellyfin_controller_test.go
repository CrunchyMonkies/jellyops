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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

var _ = Describe("JellyfinReconciler", func() {
	var (
		r  *JellyfinReconciler
		ns string
	)

	BeforeEach(func() {
		r = &JellyfinReconciler{Client: k8sClient, Scheme: scheme.Scheme}
		ns = newNamespace()
	})

	reconcileInstance := func(name string) {
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
	}

	It("creates the Deployment, Service, config PVC, and Ingress", func() {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "home-media", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinSpec{
				Storage: jellyfinv1alpha1.JellyfinStorage{
					Media: []jellyfinv1alpha1.MediaFolder{{
						Name: "movies", MountPath: "/media/movies", ReadOnly: true,
						NFS: &jellyfinv1alpha1.NFSSource{Server: "10.0.0.10", Path: "/export/movies"},
					}},
				},
				Ingress: &jellyfinv1alpha1.IngressSpec{ClassName: "nginx", Host: "jf.example.com"},
			},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
		reconcileInstance("home-media")

		var dep appsv1.Deployment
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "home-media", Namespace: ns}, &dep)).To(Succeed())
		Expect(dep.Spec.Template.Spec.Containers[0].Name).To(Equal("jellyfin"))
		Expect(dep.OwnerReferences).To(HaveLen(1))
		Expect(dep.OwnerReferences[0].Kind).To(Equal("Jellyfin"))

		var svc corev1.Service
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "home-media", Namespace: ns}, &svc)).To(Succeed())
		Expect(svc.Spec.Ports[0].Port).To(Equal(int32(8096)))

		var pvc corev1.PersistentVolumeClaim
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "home-media-config", Namespace: ns}, &pvc)).To(Succeed())

		var ing networkingv1.Ingress
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "home-media", Namespace: ns}, &ing)).To(Succeed())
		Expect(ing.Spec.Rules[0].Host).To(Equal("jf.example.com"))
	})

	It("grows the config PVC when spec size increases, and never shrinks it", func() {
		sc := &storagev1.StorageClass{
			ObjectMeta:           metav1.ObjectMeta{Name: "expandable-" + ns},
			Provisioner:          "example.com/mock",
			AllowVolumeExpansion: ptr.To(true),
		}
		Expect(k8sClient.Create(ctx, sc)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, sc)).To(Succeed()) })

		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "grow", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinSpec{Storage: jellyfinv1alpha1.JellyfinStorage{
				Config: jellyfinv1alpha1.PVCSpec{Size: resource.MustParse("10Gi"), StorageClassName: sc.Name},
			}},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
		reconcileInstance("grow")

		key := types.NamespacedName{Name: "grow-config", Namespace: ns}
		var pvc corev1.PersistentVolumeClaim
		Expect(k8sClient.Get(ctx, key, &pvc)).To(Succeed())
		Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("10Gi")))

		// The API only resizes bound claims; envtest has no provisioner, so drive
		// the phase directly.
		pvc.Status.Phase = corev1.ClaimBound
		Expect(k8sClient.Status().Update(ctx, &pvc)).To(Succeed())

		// Growing: a size bump in the CR must reach the PVC. Silently ignoring it
		// lets the volume fill up and take Jellyfin down.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "grow", Namespace: ns}, jf)).To(Succeed())
		jf.Spec.Storage.Config.Size = resource.MustParse("40Gi")
		Expect(k8sClient.Update(ctx, jf)).To(Succeed())
		reconcileInstance("grow")

		Expect(k8sClient.Get(ctx, key, &pvc)).To(Succeed())
		Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("40Gi")))

		// Shrinking: volumes cannot shrink, so the request is ignored rather than
		// sent to the API, where it would fail every reconcile.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "grow", Namespace: ns}, jf)).To(Succeed())
		jf.Spec.Storage.Config.Size = resource.MustParse("5Gi")
		Expect(k8sClient.Update(ctx, jf)).To(Succeed())
		reconcileInstance("grow")

		Expect(k8sClient.Get(ctx, key, &pvc)).To(Succeed())
		Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("40Gi")))
	})

	It("keeps reconciling when the storage class forbids expansion", func() {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "noexpand", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinSpec{Storage: jellyfinv1alpha1.JellyfinStorage{
				Config: jellyfinv1alpha1.PVCSpec{Size: resource.MustParse("10Gi")},
			}},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
		reconcileInstance("noexpand")

		key := types.NamespacedName{Name: "noexpand-config", Namespace: ns}
		var pvc corev1.PersistentVolumeClaim
		Expect(k8sClient.Get(ctx, key, &pvc)).To(Succeed())
		pvc.Status.Phase = corev1.ClaimBound
		Expect(k8sClient.Status().Update(ctx, &pvc)).To(Succeed())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "noexpand", Namespace: ns}, jf)).To(Succeed())
		jf.Spec.Storage.Config.Size = resource.MustParse("40Gi")
		Expect(k8sClient.Update(ctx, jf)).To(Succeed())

		// No storage class, so the API forbids expansion. Reconcile must still
		// succeed rather than retrying a permanently failing update forever.
		reconcileInstance("noexpand")

		Expect(k8sClient.Get(ctx, key, &pvc)).To(Succeed())
		Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("10Gi")))
	})

	It("provisions a trickplay PVC mounted at the hardcoded data path", func() {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "tp", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinSpec{Storage: jellyfinv1alpha1.JellyfinStorage{
				Config:    jellyfinv1alpha1.PVCSpec{ExistingClaim: "cfg"},
				Trickplay: &jellyfinv1alpha1.PVCSpec{Size: resource.MustParse("50Gi")},
			}},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
		reconcileInstance("tp")

		var pvc corev1.PersistentVolumeClaim
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tp-trickplay", Namespace: ns}, &pvc)).To(Succeed())
		Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("50Gi")))

		var dep appsv1.Deployment
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tp", Namespace: ns}, &dep)).To(Succeed())
		var mounts []corev1.VolumeMount
		for _, c := range dep.Spec.Template.Spec.Containers {
			if c.Name == "jellyfin" {
				mounts = c.VolumeMounts
			}
		}
		Expect(mounts).To(ContainElement(corev1.VolumeMount{
			Name: "trickplay", MountPath: "/config/data/trickplay",
		}))
	})

	It("provisions a PV+PVC for provisioned NFS media", func() {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "prov", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinSpec{Storage: jellyfinv1alpha1.JellyfinStorage{
				Config: jellyfinv1alpha1.PVCSpec{ExistingClaim: "cfg"},
				Media: []jellyfinv1alpha1.MediaFolder{{
					Name: "tv", MountPath: "/media/tv",
					NFS: &jellyfinv1alpha1.NFSSource{Server: "10.0.0.10", Path: "/export/tv", Provision: true, MountOptions: []string{"nfsvers=4.1"}},
				}},
			}},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
		reconcileInstance("prov")

		claim := "prov-media-tv"
		var pvc corev1.PersistentVolumeClaim
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: claim, Namespace: ns}, &pvc)).To(Succeed())
		Expect(pvc.Spec.AccessModes).To(ContainElement(corev1.ReadWriteMany))

		var pv corev1.PersistentVolume
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: claim + "-pv"}, &pv)).To(Succeed())
		Expect(pv.Spec.NFS.Server).To(Equal("10.0.0.10"))
		Expect(pv.Spec.MountOptions).To(ContainElement("nfsvers=4.1"))
	})

	It("creates web Deployment and Service when spec.web is set", func() {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "web-test", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinSpec{
				Storage: jellyfinv1alpha1.JellyfinStorage{Config: jellyfinv1alpha1.PVCSpec{ExistingClaim: "cfg"}},
				Web: &jellyfinv1alpha1.WebSpec{
					Image:    "ghcr.io/crunchymonkies/jellyfin-web:latest",
					Replicas: ptr.To(int32(2)),
				},
			},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
		reconcileInstance("web-test")

		var dep appsv1.Deployment
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "web-test-web", Namespace: ns}, &dep)).To(Succeed())
		Expect(dep.Spec.Template.Spec.Containers[0].Name).To(Equal("web"))
		Expect(*dep.Spec.Replicas).To(Equal(int32(2)))
		Expect(dep.OwnerReferences).To(HaveLen(1))
		Expect(dep.OwnerReferences[0].Kind).To(Equal("Jellyfin"))
		Expect(dep.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort).To(Equal(int32(80)))

		var svc corev1.Service
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "web-test-web", Namespace: ns}, &svc)).To(Succeed())
		Expect(svc.Spec.Ports[0].Port).To(Equal(int32(80)))
		Expect(svc.Spec.Selector["app.kubernetes.io/component"]).To(Equal("web"))
	})

	It("deletes web Deployment and Service when spec.web is removed", func() {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "web-del", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinSpec{
				Storage: jellyfinv1alpha1.JellyfinStorage{Config: jellyfinv1alpha1.PVCSpec{ExistingClaim: "cfg"}},
				Web: &jellyfinv1alpha1.WebSpec{
					Image: "web:1",
				},
			},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
		reconcileInstance("web-del")

		// Verify web objects exist.
		var dep appsv1.Deployment
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "web-del-web", Namespace: ns}, &dep)).To(Succeed())

		// Remove web from spec.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "web-del", Namespace: ns}, jf)).To(Succeed())
		jf.Spec.Web = nil
		Expect(k8sClient.Update(ctx, jf)).To(Succeed())
		reconcileInstance("web-del")

		// Web Deployment should be gone.
		err := k8sClient.Get(ctx, types.NamespacedName{Name: "web-del-web", Namespace: ns}, &dep)
		Expect(err).To(HaveOccurred())
	})

	It("creates an HTTPRoute when spec.gateway is set", func() {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "gw-test", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinSpec{
				Storage: jellyfinv1alpha1.JellyfinStorage{Config: jellyfinv1alpha1.PVCSpec{ExistingClaim: "cfg"}},
				Web:     &jellyfinv1alpha1.WebSpec{Image: "web:1"},
				Gateway: &jellyfinv1alpha1.GatewaySpec{
					GatewayRef: jellyfinv1alpha1.GatewayReference{
						Name:      "main-gw",
						Namespace: "infra",
					},
					Hostname: "jellyfin.example.com",
				},
			},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
		reconcileInstance("gw-test")

		var route gatewayv1.HTTPRoute
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "gw-test", Namespace: ns}, &route)).To(Succeed())
		Expect(route.OwnerReferences).To(HaveLen(1))
		Expect(route.OwnerReferences[0].Kind).To(Equal("Jellyfin"))
		Expect(route.Spec.Hostnames).To(HaveLen(1))
		Expect(string(route.Spec.Hostnames[0])).To(Equal("jellyfin.example.com"))
		Expect(route.Spec.Rules).To(HaveLen(5))
		// Rule 0: exact / -> 302 redirect to /web/ (no backend).
		Expect(*route.Spec.Rules[0].Matches[0].Path.Type).To(Equal(gatewayv1.PathMatchExact))
		Expect(*route.Spec.Rules[0].Matches[0].Path.Value).To(Equal("/"))
		Expect(route.Spec.Rules[0].BackendRefs).To(BeEmpty())
		Expect(route.Spec.Rules[0].Filters[0].Type).To(Equal(gatewayv1.HTTPRouteFilterRequestRedirect))
		Expect(*route.Spec.Rules[0].Filters[0].RequestRedirect.Path.ReplaceFullPath).To(Equal("/web/"))
		// Rule 1a: /web/configurationpage + Sec-Fetch-Mode: navigate -> web service.
		Expect(*route.Spec.Rules[1].Matches[0].Path.Value).To(Equal("/web/configurationpage"))
		Expect(string(route.Spec.Rules[1].Matches[0].Headers[0].Name)).To(Equal("Sec-Fetch-Mode"))
		Expect(route.Spec.Rules[1].Matches[0].Headers[0].Value).To(Equal("navigate"))
		Expect(string(route.Spec.Rules[1].BackendRefs[0].Name)).To(Equal("gw-test-web"))
		// Rule 1b: /web ConfigurationPage API carve-out -> server service.
		Expect(*route.Spec.Rules[2].Matches[0].Path.Value).To(Equal("/web/ConfigurationPages"))
		Expect(route.Spec.Rules[2].Matches).To(HaveLen(4))
		Expect(string(route.Spec.Rules[2].BackendRefs[0].Name)).To(Equal("gw-test"))
		// Rule 2: /web -> web service.
		Expect(*route.Spec.Rules[3].Matches[0].Path.Value).To(Equal("/web"))
		Expect(string(route.Spec.Rules[3].BackendRefs[0].Name)).To(Equal("gw-test-web"))
		// Rule 3: / -> server service.
		Expect(*route.Spec.Rules[4].Matches[0].Path.Value).To(Equal("/"))
		Expect(string(route.Spec.Rules[4].BackendRefs[0].Name)).To(Equal("gw-test"))
	})

	It("deletes the HTTPRoute when spec.gateway is removed", func() {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "gw-del", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinSpec{
				Storage: jellyfinv1alpha1.JellyfinStorage{Config: jellyfinv1alpha1.PVCSpec{ExistingClaim: "cfg"}},
				Web:     &jellyfinv1alpha1.WebSpec{Image: "web:1"},
				Gateway: &jellyfinv1alpha1.GatewaySpec{
					GatewayRef: jellyfinv1alpha1.GatewayReference{Name: "gw"},
					Hostname:   "jf.local",
				},
			},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
		reconcileInstance("gw-del")

		var route gatewayv1.HTTPRoute
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "gw-del", Namespace: ns}, &route)).To(Succeed())

		// Remove gateway from spec.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "gw-del", Namespace: ns}, jf)).To(Succeed())
		jf.Spec.Gateway = nil
		Expect(k8sClient.Update(ctx, jf)).To(Succeed())
		reconcileInstance("gw-del")

		err := k8sClient.Get(ctx, types.NamespacedName{Name: "gw-del", Namespace: ns}, &route)
		Expect(err).To(HaveOccurred())
	})

	It("re-rolls the instance when a bound plugin appears (cross-watch)", func() {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "wm", Namespace: ns},
			Spec:       jellyfinv1alpha1.JellyfinSpec{Storage: jellyfinv1alpha1.JellyfinStorage{Config: jellyfinv1alpha1.PVCSpec{ExistingClaim: "cfg"}}},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
		reconcileInstance("wm")

		p := &jellyfinv1alpha1.JellyfinPlugin{
			ObjectMeta: metav1.ObjectMeta{Name: "dt", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinPluginSpec{
				JellyfinRef: &corev1.LocalObjectReference{Name: "wm"},
				PluginImage: jellyfinv1alpha1.ImageSource{Reference: "ghcr.io/x/dt@sha256:abc"},
				Meta:        jellyfinv1alpha1.PluginMeta{Name: "DT", Version: "1.0.0.0"},
			},
		}
		Expect(k8sClient.Create(ctx, p)).To(Succeed())

		// The watch mapper should map the plugin back to the instance.
		reqs := r.mapPluginToInstances(ctx, p)
		Expect(reqs).To(ContainElement(ctrl.Request{NamespacedName: types.NamespacedName{Name: "wm", Namespace: ns}}))

		// Re-reconcile: the plugin's image volume must now be in the pod template.
		reconcileInstance("wm")
		var dep appsv1.Deployment
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "wm", Namespace: ns}, &dep)).To(Succeed())
		found := false
		for _, v := range dep.Spec.Template.Spec.Volumes {
			if v.Image != nil {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "expected an image volume for the bound plugin")
	})
})

// newNamespace creates a uniquely-named namespace and returns its name.
func newNamespace() string {
	nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "test-"}}
	Expect(k8sClient.Create(ctx, nsObj)).To(Succeed())
	return nsObj.Name
}
