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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
	"github.com/crunchymonkies/jellyops/internal/plugins"
)

// JellyfinReconciler owns the instance Deployment, PVCs, Service, and Ingress,
// and composes the pod template from bound JellyfinPlugins.
type JellyfinReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=jellyfin.jellyops.io,resources=jellyfins,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=jellyfin.jellyops.io,resources=jellyfins/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=jellyfin.jellyops.io,resources=jellyfins/finalizers,verbs=update
// +kubebuilder:rbac:groups=jellyfin.jellyops.io,resources=jellyfinplugins,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete

// Reconcile converges the instance's owned objects toward desired state.
func (r *JellyfinReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var jf jellyfinv1alpha1.Jellyfin
	if err := r.Get(ctx, req.NamespacedName, &jf); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !jf.DeletionTimestamp.IsZero() {
		// Owned objects are garbage-collected via owner references.
		return ctrl.Result{}, nil
	}

	bound, err := BoundPlugins(ctx, r.Client, &jf)
	if err != nil {
		return ctrl.Result{}, err
	}
	healthy := HealthyPlugins(bound)

	if err := r.reconcileStorage(ctx, &jf); err != nil {
		return ctrl.Result{}, fmt.Errorf("storage: %w", err)
	}
	if err := r.reconcileDeployment(ctx, &jf, healthy); err != nil {
		return ctrl.Result{}, fmt.Errorf("deployment: %w", err)
	}
	if err := r.reconcileService(ctx, &jf); err != nil {
		return ctrl.Result{}, fmt.Errorf("service: %w", err)
	}
	if err := r.reconcileIngress(ctx, &jf); err != nil {
		return ctrl.Result{}, fmt.Errorf("ingress: %w", err)
	}
	if err := r.reconcileWeb(ctx, &jf); err != nil {
		return ctrl.Result{}, fmt.Errorf("web: %w", err)
	}
	if err := r.reconcileGateway(ctx, &jf); err != nil {
		return ctrl.Result{}, fmt.Errorf("gateway: %w", err)
	}

	if err := r.updateStatus(ctx, &jf, healthy); err != nil {
		log.Error(err, "status update failed")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// reconcileStorage provisions the config/cache PVCs and any PVC/PV-backed media
// folders. Folders referencing an existing claim or inline NFS are skipped.
func (r *JellyfinReconciler) reconcileStorage(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin) error {
	// Config PVC.
	if jf.Spec.Storage.Config.ExistingClaim == "" {
		if err := r.ensurePVC(ctx, jf, plugins.ConfigClaimName(jf), jf.Spec.Storage.Config,
			[]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, "10Gi"); err != nil {
			return err
		}
	}
	// Cache PVC.
	if c := jf.Spec.Storage.Cache; c != nil && c.ExistingClaim == "" {
		if err := r.ensurePVC(ctx, jf, plugins.CacheClaimName(jf), *c,
			[]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, "5Gi"); err != nil {
			return err
		}
	}
	// Media folders.
	for _, mf := range jf.Spec.Storage.Media {
		switch {
		case mf.ExistingClaim != "":
			continue
		case mf.NFS != nil && mf.NFS.Provision:
			if err := r.ensureProvisionedNFS(ctx, jf, mf); err != nil {
				return err
			}
		case mf.NFS != nil:
			// Inline NFS: no PVC/PV objects.
			continue
		case mf.PVC != nil && mf.PVC.ExistingClaim == "":
			if err := r.ensurePVC(ctx, jf, plugins.MediaClaimName(jf, mf), *mf.PVC,
				[]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, "100Gi"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *JellyfinReconciler) ensurePVC(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin, name string, spec jellyfinv1alpha1.PVCSpec, defaultModes []corev1.PersistentVolumeAccessMode, defaultSize string) error {
	size := spec.Size
	if size.IsZero() {
		size = resource.MustParse(defaultSize)
	}
	modes := spec.AccessModes
	if len(modes) == 0 {
		modes = defaultModes
	}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: jf.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, pvc, func() error {
		pvc.Labels = plugins.InstanceLabels(jf)
		if pvc.CreationTimestamp.IsZero() {
			// PVC spec is immutable after creation; only set on create.
			pvc.Spec.AccessModes = modes
			pvc.Spec.Resources = corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: size}}
			if spec.StorageClassName != "" {
				pvc.Spec.StorageClassName = ptr.To(spec.StorageClassName)
			}
		}
		return controllerutil.SetControllerReference(jf, pvc, r.Scheme)
	})
	return err
}

// ensureProvisionedNFS creates a statically-bound RWX PV backed by the NFS export
// plus its PVC, so companion workers can share the same paths (spec §8.2). The PV
// is cluster-scoped and cannot own-ref the namespaced instance; it uses reclaim
// policy Delete so it is cleaned up when the PVC is removed.
func (r *JellyfinReconciler) ensureProvisionedNFS(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin, mf jellyfinv1alpha1.MediaFolder) error {
	claim := plugins.MediaClaimName(jf, mf)
	pvName := claim + "-pv"
	capacity := resource.MustParse("1Ti") // NFS ignores capacity; required by the API.
	scName := mf.NFS.StorageClassName

	pv := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: pvName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, pv, func() error {
		pv.Labels = plugins.InstanceLabels(jf)
		pv.Spec.Capacity = corev1.ResourceList{corev1.ResourceStorage: capacity}
		pv.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
		pv.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimDelete
		pv.Spec.MountOptions = mf.NFS.MountOptions
		pv.Spec.StorageClassName = scName
		pv.Spec.NFS = &corev1.NFSVolumeSource{Server: mf.NFS.Server, Path: mf.NFS.Path, ReadOnly: mf.NFS.ReadOnly}
		pv.Spec.ClaimRef = &corev1.ObjectReference{Namespace: jf.Namespace, Name: claim}
		return nil
	}); err != nil {
		return err
	}

	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: claim, Namespace: jf.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, pvc, func() error {
		pvc.Labels = plugins.InstanceLabels(jf)
		if pvc.CreationTimestamp.IsZero() {
			pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
			pvc.Spec.Resources = corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: capacity}}
			pvc.Spec.VolumeName = pvName
			pvc.Spec.StorageClassName = ptr.To(scName)
		}
		return controllerutil.SetControllerReference(jf, pvc, r.Scheme)
	})
	return err
}

func (r *JellyfinReconciler) reconcileDeployment(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin, healthy []jellyfinv1alpha1.JellyfinPlugin) error {
	desired, err := plugins.BuildDeployment(jf, healthy)
	if err != nil {
		return err
	}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: jf.Name, Namespace: jf.Namespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Labels = desired.Labels
		dep.Spec = desired.Spec
		return controllerutil.SetControllerReference(jf, dep, r.Scheme)
	})
	return err
}

func (r *JellyfinReconciler) reconcileService(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin) error {
	port := plugins.DefaultJellyfinPort
	if jf.Spec.Service.Port != 0 {
		port = jf.Spec.Service.Port
	}
	svcType := jf.Spec.Service.Type
	if svcType == "" {
		svcType = corev1.ServiceTypeClusterIP
	}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: jf.Name, Namespace: jf.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Labels = plugins.InstanceLabels(jf)
		svc.Annotations = jf.Spec.Service.Annotations
		svc.Spec.Type = svcType
		svc.Spec.Selector = plugins.InstanceSelectorLabels(jf)
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "http",
			Port:       port,
			TargetPort: intstrFromInt(plugins.DefaultJellyfinPort),
			Protocol:   corev1.ProtocolTCP,
		}}
		return controllerutil.SetControllerReference(jf, svc, r.Scheme)
	})
	return err
}

func (r *JellyfinReconciler) reconcileIngress(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin) error {
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: jf.Name, Namespace: jf.Namespace}}
	if jf.Spec.Ingress == nil {
		// Delete a previously-created Ingress if the spec dropped it.
		if err := r.Delete(ctx, ing); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}
	spec := jf.Spec.Ingress
	port := plugins.DefaultJellyfinPort
	if jf.Spec.Service.Port != 0 {
		port = jf.Spec.Service.Port
	}
	pathType := networkingv1.PathTypePrefix
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ing, func() error {
		ing.Labels = plugins.InstanceLabels(jf)
		ing.Annotations = spec.Annotations
		if spec.ClassName != "" {
			ing.Spec.IngressClassName = ptr.To(spec.ClassName)
		}
		ing.Spec.Rules = []networkingv1.IngressRule{{
			Host: spec.Host,
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{
					Path:     "/",
					PathType: &pathType,
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
						Name: jf.Name,
						Port: networkingv1.ServiceBackendPort{Number: port},
					}},
				}},
			}},
		}}
		if spec.TLS != nil {
			ing.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{spec.Host}, SecretName: spec.TLS.SecretName}}
		}
		return controllerutil.SetControllerReference(jf, ing, r.Scheme)
	})
	return err
}

func (r *JellyfinReconciler) reconcileWeb(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin) error {
	webDepName := plugins.WebDeploymentName(jf)
	webSvcName := plugins.WebServiceName(jf)

	if jf.Spec.Web == nil {
		// Best-effort cleanup of previously-created web objects.
		dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: webDepName, Namespace: jf.Namespace}}
		if err := r.Delete(ctx, dep); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: webSvcName, Namespace: jf.Namespace}}
		if err := r.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}

	desired := plugins.BuildWebDeployment(jf)
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: webDepName, Namespace: jf.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Labels = desired.Labels
		dep.Spec = desired.Spec
		return controllerutil.SetControllerReference(jf, dep, r.Scheme)
	}); err != nil {
		return err
	}

	desiredSvc := plugins.BuildWebService(jf)
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: webSvcName, Namespace: jf.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Labels = desiredSvc.Labels
		svc.Annotations = desiredSvc.Annotations
		svc.Spec.Type = desiredSvc.Spec.Type
		svc.Spec.Selector = desiredSvc.Spec.Selector
		svc.Spec.Ports = desiredSvc.Spec.Ports
		return controllerutil.SetControllerReference(jf, svc, r.Scheme)
	})
	return err
}

func (r *JellyfinReconciler) reconcileGateway(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin) error {
	routeName := plugins.HTTPRouteName(jf)
	route := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: jf.Namespace}}

	if jf.Spec.Gateway == nil {
		// Best-effort cleanup.
		if err := r.Delete(ctx, route); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}

	desired := plugins.BuildHTTPRoute(jf)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, route, func() error {
		route.Labels = desired.Labels
		route.Annotations = desired.Annotations
		route.Spec = desired.Spec
		return controllerutil.SetControllerReference(jf, route, r.Scheme)
	})
	return err
}

func (r *JellyfinReconciler) updateStatus(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin, healthy []jellyfinv1alpha1.JellyfinPlugin) error {
	var dep appsv1.Deployment
	ready := false
	if err := r.Get(ctx, types.NamespacedName{Name: jf.Name, Namespace: jf.Namespace}, &dep); err == nil {
		desired := int32(1)
		if dep.Spec.Replicas != nil {
			desired = *dep.Spec.Replicas
		}
		ready = dep.Status.AvailableReplicas >= desired && desired > 0
	}

	jf.Status.ObservedGeneration = jf.Generation
	port := plugins.DefaultJellyfinPort
	if jf.Spec.Service.Port != 0 {
		port = jf.Spec.Service.Port
	}
	jf.Status.Endpoints.Service = fmt.Sprintf("http://%s.%s.svc:%d", jf.Name, jf.Namespace, port)
	if jf.Spec.Ingress != nil {
		scheme := "http"
		if jf.Spec.Ingress.TLS != nil {
			scheme = "https"
		}
		jf.Status.Endpoints.Ingress = fmt.Sprintf("%s://%s", scheme, jf.Spec.Ingress.Host)
	}

	loaded := make([]jellyfinv1alpha1.LoadedPlugin, 0, len(healthy))
	for i := range healthy {
		m := healthy[i].Spec.Meta
		loaded = append(loaded, jellyfinv1alpha1.LoadedPlugin{
			Name: healthy[i].Name, PluginName: m.Name, Version: m.Version, GUID: m.GUID,
		})
	}
	jf.Status.LoadedPlugins = loaded

	if ready {
		jf.Status.Phase = "Ready"
		setCondition(&jf.Status.Conditions, conditionReady, metav1.ConditionTrue, "DeploymentAvailable", "Jellyfin deployment is available", jf.Generation)
		setCondition(&jf.Status.Conditions, conditionPluginsLoaded, metav1.ConditionTrue, "PodReady", fmt.Sprintf("%d plugin(s) mounted", len(loaded)), jf.Generation)
	} else {
		jf.Status.Phase = "Pending"
		setCondition(&jf.Status.Conditions, conditionReady, metav1.ConditionFalse, "DeploymentNotAvailable", "Waiting for Jellyfin deployment to become available", jf.Generation)
		setCondition(&jf.Status.Conditions, conditionPluginsLoaded, metav1.ConditionFalse, "PodNotReady", "Pod not yet ready", jf.Generation)
	}
	return writeJellyfinStatus(ctx, r.Client, jf)
}

func setCondition(conds *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, msg string, gen int64) {
	apimeta.SetStatusCondition(conds, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: gen,
	})
}

// SetupWithManager wires the controller and the plugin→instance watch so that
// changes to a bound plugin roll the instance's pod template.
func (r *JellyfinReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&jellyfinv1alpha1.Jellyfin{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&networkingv1.Ingress{}).
		Watches(&jellyfinv1alpha1.JellyfinPlugin{}, handler.EnqueueRequestsFromMapFunc(r.mapPluginToInstances)).
		Named("jellyfin").
		Complete(r)
}

func (r *JellyfinReconciler) mapPluginToInstances(ctx context.Context, obj client.Object) []reconcile.Request {
	p, ok := obj.(*jellyfinv1alpha1.JellyfinPlugin)
	if !ok {
		return nil
	}
	names, err := InstancesForPlugin(ctx, r.Client, p)
	if err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(names))
	for _, n := range names {
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: n, Namespace: p.Namespace}})
	}
	return reqs
}
