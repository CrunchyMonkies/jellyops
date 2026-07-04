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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
	"github.com/crunchymonkies/jellyops/internal/jellyfinapi"
)

// fakeAPI is an in-memory APIClient for testing the reconciler without a running
// Jellyfin.
type fakeAPI struct {
	folders         []jellyfinapi.VirtualFolder
	added           []jellyfinapi.DesiredLibrary
	removed         []string
	bootstrapCalled bool
}

func (f *fakeAPI) SetToken(string) {}
func (f *fakeAPI) Bootstrap(context.Context, string, string, string) (string, error) {
	f.bootstrapCalled = true
	return "key-123", nil
}
func (f *fakeAPI) AuthenticateByName(context.Context, string, string) (string, error) {
	return "tok", nil
}
func (f *fakeAPI) ListVirtualFolders(context.Context) ([]jellyfinapi.VirtualFolder, error) {
	return f.folders, nil
}
func (f *fakeAPI) AddVirtualFolder(_ context.Context, lib jellyfinapi.DesiredLibrary, _ bool) error {
	f.added = append(f.added, lib)
	return nil
}
func (f *fakeAPI) RemoveVirtualFolder(_ context.Context, name string, _ bool) error {
	f.removed = append(f.removed, name)
	return nil
}
func (f *fakeAPI) AddMediaPath(context.Context, string, string, bool) error    { return nil }
func (f *fakeAPI) RemoveMediaPath(context.Context, string, string, bool) error { return nil }
func (f *fakeAPI) RefreshLibraries(context.Context) error                      { return nil }

var _ = Describe("JellyfinAPIReconciler", func() {
	var ns string

	BeforeEach(func() { ns = newNamespace() })

	reconcileAPI := func(r *JellyfinAPIReconciler, name string) (ctrl.Result, error) {
		return r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}})
	}

	It("no-ops when spec.api is unset", func() {
		jf := &jellyfinv1alpha1.Jellyfin{ObjectMeta: metav1.ObjectMeta{Name: "noapi", Namespace: ns}}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())
		r := &JellyfinAPIReconciler{Client: k8sClient, Scheme: scheme.Scheme}
		res, err := reconcileAPI(r, "noapi")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
	})

	It("requeues without authenticating while the instance is not Ready", func() {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "notready", Namespace: ns},
			Spec:       jellyfinv1alpha1.JellyfinSpec{API: &jellyfinv1alpha1.JellyfinAPISpec{Mode: "bootstrap"}},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())

		fake := &fakeAPI{}
		r := &JellyfinAPIReconciler{Client: k8sClient, Scheme: scheme.Scheme,
			NewAPIClient: func(string, string) (APIClient, error) { return fake, nil }}
		res, err := reconcileAPI(r, "notready")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).NotTo(BeZero())
		Expect(fake.bootstrapCalled).To(BeFalse())

		var got jellyfinv1alpha1.Jellyfin
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "notready", Namespace: ns}, &got)).To(Succeed())
		c := apimeta.FindStatusCondition(got.Status.Conditions, conditionAPIReady)
		Expect(c).NotTo(BeNil())
		Expect(c.Status).To(Equal(metav1.ConditionFalse))
	})

	It("bootstraps, persists a Secret, and reconciles libraries when Ready", func() {
		jf := &jellyfinv1alpha1.Jellyfin{
			ObjectMeta: metav1.ObjectMeta{Name: "ready", Namespace: ns},
			Spec: jellyfinv1alpha1.JellyfinSpec{
				API: &jellyfinv1alpha1.JellyfinAPISpec{Mode: "bootstrap", GeneratedSecretName: "ready-api", ManageLibraries: true},
				Storage: jellyfinv1alpha1.JellyfinStorage{Media: []jellyfinv1alpha1.MediaFolder{{
					Name: "movies", MountPath: "/media/movies",
					ExistingClaim: "movies-pvc",
					Library:       &jellyfinv1alpha1.LibrarySpec{Name: "Movies", CollectionType: "movies"},
				}}},
			},
		}
		Expect(k8sClient.Create(ctx, jf)).To(Succeed())

		// Mark the instance Ready (subresource) so the API loop proceeds.
		apimeta.SetStatusCondition(&jf.Status.Conditions, metav1.Condition{Type: conditionReady, Status: metav1.ConditionTrue, Reason: "Test", Message: "ready"})
		Expect(k8sClient.Status().Update(ctx, jf)).To(Succeed())

		fake := &fakeAPI{}
		r := &JellyfinAPIReconciler{Client: k8sClient, Scheme: scheme.Scheme,
			NewAPIClient: func(string, string) (APIClient, error) { return fake, nil }}
		_, err := reconcileAPI(r, "ready")
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.bootstrapCalled).To(BeTrue())
		Expect(fake.added).To(HaveLen(1))
		Expect(fake.added[0].Name).To(Equal("Movies"))

		var sec corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "ready-api", Namespace: ns}, &sec)).To(Succeed())
		Expect(string(sec.Data["apiKey"])).To(Equal("key-123"))

		var got jellyfinv1alpha1.Jellyfin
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "ready", Namespace: ns}, &got)).To(Succeed())
		Expect(got.Status.ManagedLibraries).To(ContainElement("Movies"))
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionAPIReady)).To(BeTrue())
	})
})
