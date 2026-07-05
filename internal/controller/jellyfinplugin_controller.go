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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
	"github.com/crunchymonkies/jellyops/internal/plugins"
)

// drainRequeueInterval is how often teardown re-checks that draining workload
// pods have terminated before deleting their Deployments.
const drainRequeueInterval = 5 * time.Second

// JellyfinPluginReconciler validates a plugin, owns its companion
// workloads/Services, and drives graceful teardown via a finalizer.
type JellyfinPluginReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=jellyfin.jellyops.io,resources=jellyfinplugins,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=jellyfin.jellyops.io,resources=jellyfinplugins/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=jellyfin.jellyops.io,resources=jellyfinplugins/finalizers,verbs=update
// +kubebuilder:rbac:groups=jellyfin.jellyops.io,resources=jellyfins,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile validates the plugin, reconciles companions, and reports status.
func (r *JellyfinPluginReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var p jellyfinv1alpha1.JellyfinPlugin
	if err := r.Get(ctx, req.NamespacedName, &p); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Finalizer-driven teardown.
	if !p.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &p)
	}
	if !controllerutil.ContainsFinalizer(&p, pluginFinalizer) {
		controllerutil.AddFinalizer(&p, pluginFinalizer)
		if err := r.Update(ctx, &p); err != nil {
			return ctrl.Result{}, err
		}
	}

	instance := r.boundInstance(ctx, &p)

	// ABI validation against the bound server version.
	abiOK, abiReason, abiMsg := r.validateABI(&p, instance)

	if abiOK {
		if err := r.reconcileWorkloads(ctx, &p, instance); err != nil {
			return ctrl.Result{}, fmt.Errorf("workloads: %w", err)
		}
		if err := r.reconcileServices(ctx, &p, instance); err != nil {
			return ctrl.Result{}, fmt.Errorf("services: %w", err)
		}
	}

	if err := r.updateStatus(ctx, &p, instance, abiOK, abiReason, abiMsg); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// boundInstance resolves the Jellyfin instance this plugin binds to, or nil.
func (r *JellyfinPluginReconciler) boundInstance(ctx context.Context, p *jellyfinv1alpha1.JellyfinPlugin) *jellyfinv1alpha1.Jellyfin {
	if p.Spec.JellyfinRef != nil && p.Spec.JellyfinRef.Name != "" {
		var jf jellyfinv1alpha1.Jellyfin
		if err := r.Get(ctx, types.NamespacedName{Name: p.Spec.JellyfinRef.Name, Namespace: p.Namespace}, &jf); err == nil {
			return &jf
		}
		return nil
	}
	// Selector-bound: pick the first instance whose selector matches.
	var list jellyfinv1alpha1.JellyfinList
	if err := r.List(ctx, &list, client.InNamespace(p.Namespace)); err != nil {
		return nil
	}
	for i := range list.Items {
		if pluginBinds(&list.Items[i], p) {
			return &list.Items[i]
		}
	}
	return nil
}

func (r *JellyfinPluginReconciler) validateABI(p *jellyfinv1alpha1.JellyfinPlugin, jf *jellyfinv1alpha1.Jellyfin) (ok bool, reason, msg string) {
	if p.Spec.Meta.TargetABI == "" {
		return true, "NoTargetABI", "No targetAbi declared; ABI check skipped"
	}
	if jf == nil {
		return false, "InstanceNotFound", "Bound Jellyfin instance not found"
	}
	server := plugins.ServerVersionFromImage(jf.Spec.Image)
	if server == "" {
		server = plugins.DefaultServerVersion
	}
	compat, err := plugins.ABICompatible(p.Spec.Meta.TargetABI, server)
	if err != nil {
		return false, "ABIParseError", err.Error()
	}
	if !compat {
		return false, "ABIMismatch", fmt.Sprintf("plugin targetAbi %s exceeds server version %s", p.Spec.Meta.TargetABI, server)
	}
	return true, "ABICompatible", fmt.Sprintf("targetAbi %s compatible with server %s", p.Spec.Meta.TargetABI, server)
}

func (r *JellyfinPluginReconciler) reconcileWorkloads(ctx context.Context, p *jellyfinv1alpha1.JellyfinPlugin, jf *jellyfinv1alpha1.Jellyfin) error {
	for _, w := range p.Spec.Workloads {
		desired := plugins.BuildWorkloadDeployment(jf, p, w)
		dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
		if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
			dep.Labels = desired.Labels
			// Selector is immutable; only assign on create.
			if dep.CreationTimestamp.IsZero() {
				dep.Spec.Selector = desired.Spec.Selector
			}
			dep.Spec.Replicas = desired.Spec.Replicas
			dep.Spec.Template = desired.Spec.Template
			return controllerutil.SetControllerReference(p, dep, r.Scheme)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *JellyfinPluginReconciler) reconcileServices(ctx context.Context, p *jellyfinv1alpha1.JellyfinPlugin, jf *jellyfinv1alpha1.Jellyfin) error {
	instanceName := ""
	if jf != nil {
		instanceName = jf.Name
	}
	for _, s := range p.Spec.Services {
		desired, err := plugins.BuildPluginService(p, s, instanceName)
		if err != nil {
			return err
		}
		svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
		if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
			svc.Labels = desired.Labels
			svc.Spec.Type = desired.Spec.Type
			svc.Spec.Selector = desired.Spec.Selector
			svc.Spec.Ports = desired.Spec.Ports
			return controllerutil.SetControllerReference(p, svc, r.Scheme)
		}); err != nil {
			return err
		}
	}
	return nil
}

// finalize gracefully drains companion workloads before their owner-referenced
// objects are garbage-collected, then removes the finalizer. Scaling to zero
// first lets pods honor preStop hooks and terminationGracePeriodSeconds so the
// workload's own drain logic (e.g. jellycode's SIGTERM handler) can run.
func (r *JellyfinPluginReconciler) finalize(ctx context.Context, p *jellyfinv1alpha1.JellyfinPlugin) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(p, pluginFinalizer) {
		return ctrl.Result{}, nil
	}

	draining := false
	for _, w := range p.Spec.Workloads {
		var dep appsv1.Deployment
		name := plugins.WorkloadName(p, w)
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: p.Namespace}, &dep); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return ctrl.Result{}, err
		}
		if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
			dep.Spec.Replicas = new(int32)
			if err := r.Update(ctx, &dep); err != nil {
				return ctrl.Result{}, err
			}
		}
		if dep.Status.Replicas > 0 {
			draining = true
		}
	}
	if draining {
		// Requeue until pods have terminated, then delete + drop finalizer.
		return ctrl.Result{RequeueAfter: drainRequeueInterval}, nil
	}

	for _, w := range p.Spec.Workloads {
		dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: plugins.WorkloadName(p, w), Namespace: p.Namespace}}
		if err := r.Delete(ctx, dep); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	controllerutil.RemoveFinalizer(p, pluginFinalizer)
	if err := r.Update(ctx, p); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *JellyfinPluginReconciler) updateStatus(ctx context.Context, p *jellyfinv1alpha1.JellyfinPlugin, jf *jellyfinv1alpha1.Jellyfin, abiOK bool, abiReason, abiMsg string) error {
	p.Status.ObservedGeneration = p.Generation
	p.Status.ABICompatible = abiOK
	p.Status.LoadedVersion = p.Spec.Meta.Version

	abiStatus := metav1.ConditionTrue
	if !abiOK {
		abiStatus = metav1.ConditionFalse
	}
	setCondition(&p.Status.Conditions, conditionABICompatible, abiStatus, abiReason, abiMsg, p.Generation)

	// Companion workload readiness.
	var desiredWL, readyWL int32
	for _, w := range p.Spec.Workloads {
		var dep appsv1.Deployment
		if err := r.Get(ctx, types.NamespacedName{Name: plugins.WorkloadName(p, w), Namespace: p.Namespace}, &dep); err == nil {
			if dep.Spec.Replicas != nil {
				desiredWL += *dep.Spec.Replicas
			}
			readyWL += dep.Status.ReadyReplicas
		}
	}
	p.Status.WorkloadReadyReplicas = readyWL
	workloadsReady := len(p.Spec.Workloads) == 0 || (desiredWL > 0 && readyWL >= desiredWL)

	switch {
	case !abiOK:
		p.Status.Phase = jellyfinv1alpha1.PluginPhaseFailed
		p.Status.Injected = false
		setCondition(&p.Status.Conditions, conditionWorkersAvailable, metav1.ConditionFalse, "ABIMismatch", "Plugin not injected due to ABI mismatch", p.Generation)
	case jf == nil:
		p.Status.Phase = jellyfinv1alpha1.PluginPhasePending
		setCondition(&p.Status.Conditions, conditionWorkersAvailable, metav1.ConditionFalse, "NoInstance", "Not bound to an instance", p.Generation)
	default:
		p.Status.Injected = true
		installed := p.Spec.Install == nil
		p.Status.Installed = installed
		setCondition(&p.Status.Conditions, conditionInjected, metav1.ConditionTrue, "Bound", "Plugin bound and injected into the instance pod template", p.Generation)
		if installed {
			setCondition(&p.Status.Conditions, conditionInstalled, metav1.ConditionTrue, "NoInstallStep", "No install step declared", p.Generation)
		}
		if workloadsReady {
			p.Status.Phase = jellyfinv1alpha1.PluginPhaseLoaded
			setCondition(&p.Status.Conditions, conditionWorkersAvailable, metav1.ConditionTrue, "Ready", fmt.Sprintf("%d/%d workload replicas ready", readyWL, desiredWL), p.Generation)
		} else {
			p.Status.Phase = jellyfinv1alpha1.PluginPhaseWorkloadsReady
			setCondition(&p.Status.Conditions, conditionWorkersAvailable, metav1.ConditionFalse, "Scaling", fmt.Sprintf("%d/%d workload replicas ready", readyWL, desiredWL), p.Generation)
		}
	}
	return r.Status().Update(ctx, p)
}

// SetupWithManager wires the plugin controller.
func (r *JellyfinPluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&jellyfinv1alpha1.JellyfinPlugin{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		// Re-reconcile bound plugins when their instance changes so companion
		// workloads pick up edits to the instance's media folders.
		Watches(&jellyfinv1alpha1.Jellyfin{}, handler.EnqueueRequestsFromMapFunc(r.mapInstanceToPlugins)).
		Named("jellyfinplugin").
		Complete(r)
}

// mapInstanceToPlugins enqueues every JellyfinPlugin bound to a changed Jellyfin
// instance, so their workloads/services reconcile against the new instance state.
func (r *JellyfinPluginReconciler) mapInstanceToPlugins(ctx context.Context, obj client.Object) []reconcile.Request {
	jf, ok := obj.(*jellyfinv1alpha1.Jellyfin)
	if !ok {
		return nil
	}
	bound, err := BoundPlugins(ctx, r.Client, jf)
	if err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(bound))
	for i := range bound {
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      bound[i].Name,
			Namespace: bound[i].Namespace,
		}})
	}
	return reqs
}
