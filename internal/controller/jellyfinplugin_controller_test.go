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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
)

var _ = Describe("JellyfinPluginReconciler", func() {
	var (
		r  *JellyfinPluginReconciler
		ns string
	)

	BeforeEach(func() {
		r = &JellyfinPluginReconciler{Client: k8sClient, Scheme: scheme.Scheme}
		ns = newNamespace()
	})

	makeInstance := func(name, image string) {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       jellyfinv1alpha1.JellyfinSpec{Image: image, Storage: jellyfinv1alpha1.JellyfinStorage{Config: jellyfinv1alpha1.PVCSpec{ExistingClaim: "cfg"}}},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
	}

	reconcilePlugin := func(name string) (ctrl.Result, error) {
		return r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}})
	}

	It("validates ABI, adds a finalizer, and creates companion workload + service", func() {
		makeInstance("home-media", "ghcr.io/x/jellyfin:12.0.0-net10")
		p := &jellyfinv1alpha1.JellyfinPlugin{
			ObjectMeta: metav1.ObjectMeta{Name: "dt", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinPluginSpec{
				JellyfinRef: &corev1.LocalObjectReference{Name: "home-media"},
				PluginImage: jellyfinv1alpha1.ImageSource{Reference: "ghcr.io/x/dt@sha256:abc"},
				Meta:        jellyfinv1alpha1.PluginMeta{Name: "Distributed Transcoding", Version: "0.0.1.0", TargetABI: "12.0.0.0"},
				Workloads: []jellyfinv1alpha1.PluginWorkload{{
					Name: "worker", Image: jellyfinv1alpha1.ImageSource{Reference: "ghcr.io/x/worker@sha256:def"}, Replicas: ptr.To(int32(3)),
				}},
				Services: []jellyfinv1alpha1.PluginService{{
					Name: "dt-grpc", Selector: "instance",
					Ports: []corev1.ServicePort{{Name: "grpc", Port: 9090}},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, p)).To(Succeed())

		_, err := reconcilePlugin("dt")
		Expect(err).NotTo(HaveOccurred())

		var got jellyfinv1alpha1.JellyfinPlugin
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "dt", Namespace: ns}, &got)).To(Succeed())
		Expect(got.Finalizers).To(ContainElement(pluginFinalizer))
		Expect(got.Status.ABICompatible).To(BeTrue())

		var dep appsv1.Deployment
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "dt-worker", Namespace: ns}, &dep)).To(Succeed())
		Expect(*dep.Spec.Replicas).To(Equal(int32(3)))
		Expect(dep.OwnerReferences[0].Kind).To(Equal("JellyfinPlugin"))

		var svc corev1.Service
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "dt-grpc", Namespace: ns}, &svc)).To(Succeed())
		Expect(svc.Spec.Selector).To(HaveKeyWithValue("app.kubernetes.io/instance", "home-media"))
	})

	It("marks an ABI-incompatible plugin Failed and creates no workloads", func() {
		makeInstance("srv", "ghcr.io/x/jellyfin:12.0.0-net10")
		p := &jellyfinv1alpha1.JellyfinPlugin{
			ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinPluginSpec{
				JellyfinRef: &corev1.LocalObjectReference{Name: "srv"},
				PluginImage: jellyfinv1alpha1.ImageSource{Reference: "ghcr.io/x/bad@sha256:abc"},
				Meta:        jellyfinv1alpha1.PluginMeta{Name: "Bad", Version: "1.0", TargetABI: "99.0.0.0"},
				Workloads:   []jellyfinv1alpha1.PluginWorkload{{Name: "worker", Image: jellyfinv1alpha1.ImageSource{Reference: "ghcr.io/x/worker@sha256:def"}}},
			},
		}
		Expect(k8sClient.Create(ctx, p)).To(Succeed())
		_, err := reconcilePlugin("bad")
		Expect(err).NotTo(HaveOccurred())

		var got jellyfinv1alpha1.JellyfinPlugin
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bad", Namespace: ns}, &got)).To(Succeed())
		Expect(got.Status.ABICompatible).To(BeFalse())
		Expect(got.Status.Phase).To(Equal(jellyfinv1alpha1.PluginPhaseFailed))

		var dep appsv1.Deployment
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "bad-worker", Namespace: ns}, &dep)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("drains and removes companion workloads on delete", func() {
		makeInstance("d", "ghcr.io/x/jellyfin:12.0.0")
		p := &jellyfinv1alpha1.JellyfinPlugin{
			ObjectMeta: metav1.ObjectMeta{Name: "del", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinPluginSpec{
				JellyfinRef: &corev1.LocalObjectReference{Name: "d"},
				PluginImage: jellyfinv1alpha1.ImageSource{Reference: "ghcr.io/x/del@sha256:abc"},
				Meta:        jellyfinv1alpha1.PluginMeta{Name: "Del", Version: "1.0.0.0", TargetABI: "12.0.0.0"},
				Workloads:   []jellyfinv1alpha1.PluginWorkload{{Name: "worker", Image: jellyfinv1alpha1.ImageSource{Reference: "ghcr.io/x/worker@sha256:def"}}},
			},
		}
		Expect(k8sClient.Create(ctx, p)).To(Succeed())
		_, err := reconcilePlugin("del")
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Delete(ctx, p)).To(Succeed())
		// Reconcile the deletion; with no running pods, drain completes and the
		// finalizer is removed so the object is fully deleted.
		_, err = reconcilePlugin("del")
		Expect(err).NotTo(HaveOccurred())

		var got jellyfinv1alpha1.JellyfinPlugin
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "del", Namespace: ns}, &got)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})
