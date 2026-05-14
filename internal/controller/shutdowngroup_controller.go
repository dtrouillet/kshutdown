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
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
)

// ShutdownGroupReconciler reconciles a ShutdownGroup object.
type ShutdownGroupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kshutdown.io,resources=shutdowngroups,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=kshutdown.io,resources=shutdowngroups/status,verbs=get;patch
// +kubebuilder:rbac:groups=kshutdown.io,resources=shutdowngroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;patch

func (r *ShutdownGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var sg kshutdownv1alpha1.ShutdownGroup
	if err := r.Get(ctx, req.NamespacedName, &sg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !sg.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	log.Info("reconciling", "state", sg.Status.State, "command", sg.Annotations[kshutdownv1alpha1.AnnotationCommand])

	switch sg.Annotations[kshutdownv1alpha1.AnnotationCommand] {
	case kshutdownv1alpha1.CommandDown:
		return r.reconcileDown(ctx, &sg)
	case kshutdownv1alpha1.CommandUp:
		return r.reconcileUp(ctx, &sg)
	default:
		return ctrl.Result{}, nil
	}
}

func (r *ShutdownGroupReconciler) reconcileDown(ctx context.Context, sg *kshutdownv1alpha1.ShutdownGroup) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("shutdowngroup", sg.Namespace+"/"+sg.Name)

	// No-op: already down — consume annotation and return.
	if sg.Status.State == kshutdownv1alpha1.StateDown {
		log.Info("already down, consuming annotation")
		return ctrl.Result{}, r.consumeAnnotations(ctx, sg,
			kshutdownv1alpha1.AnnotationCommand,
			kshutdownv1alpha1.AnnotationReason,
			kshutdownv1alpha1.AnnotationOperator,
		)
	}

	operator := sg.Annotations[kshutdownv1alpha1.AnnotationOperator]
	reason := sg.Annotations[kshutdownv1alpha1.AnnotationReason]

	// Capture snapshot before any scaling (write-once: skipped on crash-recovery re-entry).
	if len(sg.Status.Snapshot) == 0 {
		snapshots, err := r.collectTargets(ctx, sg)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("collecting targets: %w", err)
		}
		patch := client.MergeFrom(sg.DeepCopy())
		sg.Status.Snapshot = snapshots
		sg.Status.Operator = operator
		sg.Status.Reason = reason
		if err := r.Status().Patch(ctx, sg, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("patching snapshot into status: %w", err)
		}
		log.Info("snapshot captured", "resources", len(snapshots))
	}

	// Scale each target to zero.
	for _, entry := range sg.Status.Snapshot {
		switch entry.Kind {
		case "CronJob":
			if err := r.suspendCronJob(ctx, entry); err != nil {
				return ctrl.Result{}, fmt.Errorf("suspending %s/%s: %w", entry.Namespace, entry.Name, err)
			}
		default:
			if err := r.scaleToZero(ctx, entry); err != nil {
				return ctrl.Result{}, fmt.Errorf("scaling %s %s/%s to zero: %w", entry.Kind, entry.Namespace, entry.Name, err)
			}
		}
	}

	// Mark state as down — only after all scaling succeeds.
	now := metav1.Now()
	patch := client.MergeFrom(sg.DeepCopy())
	sg.Status.State = kshutdownv1alpha1.StateDown
	sg.Status.Since = &now
	if err := r.Status().Patch(ctx, sg, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching state to down: %w", err)
	}

	if err := r.appendHistory(ctx, sg, kshutdownv1alpha1.HistoryEntry{
		Operation: kshutdownv1alpha1.CommandDown,
		At:        now,
		Operator:  operator,
		Reason:    reason,
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("appending history after shutdown: %w", err)
	}

	log.Info("shutdown complete", "operator", operator, "reason", reason, "resources", len(sg.Status.Snapshot))

	if err := r.consumeAnnotations(ctx, sg,
		kshutdownv1alpha1.AnnotationCommand,
		kshutdownv1alpha1.AnnotationReason,
		kshutdownv1alpha1.AnnotationOperator,
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("consuming annotations after shutdown: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *ShutdownGroupReconciler) reconcileUp(ctx context.Context, sg *kshutdownv1alpha1.ShutdownGroup) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("shutdowngroup", sg.Namespace+"/"+sg.Name)

	// No-op: already up — consume annotation and return.
	if sg.Status.State == kshutdownv1alpha1.StateUp {
		log.Info("already up, consuming annotation")
		return ctrl.Result{}, r.consumeAnnotations(ctx, sg,
			kshutdownv1alpha1.AnnotationCommand,
			kshutdownv1alpha1.AnnotationReason,
			kshutdownv1alpha1.AnnotationOperator,
		)
	}

	// Error: no snapshot to restore from.
	if len(sg.Status.Snapshot) == 0 {
		return ctrl.Result{}, fmt.Errorf("cannot reconcileUp from state %q with empty snapshot", sg.Status.State)
	}

	operator := sg.Annotations[kshutdownv1alpha1.AnnotationOperator]
	reason := sg.Annotations[kshutdownv1alpha1.AnnotationReason]
	resourceCount := len(sg.Status.Snapshot)

	// Restore each target from snapshot.
	for _, entry := range sg.Status.Snapshot {
		switch entry.Kind {
		case "CronJob":
			if err := r.unsuspendCronJob(ctx, entry); err != nil {
				return ctrl.Result{}, fmt.Errorf("unsuspending %s/%s: %w", entry.Namespace, entry.Name, err)
			}
		default:
			if err := r.restoreReplicas(ctx, entry); err != nil {
				return ctrl.Result{}, fmt.Errorf("restoring %s %s/%s: %w", entry.Kind, entry.Namespace, entry.Name, err)
			}
		}
	}

	// Mark state as up and clear snapshot — only after all restores succeed.
	now := metav1.Now()
	patch := client.MergeFrom(sg.DeepCopy())
	sg.Status.State = kshutdownv1alpha1.StateUp
	sg.Status.Since = &now
	sg.Status.Operator = operator
	sg.Status.Reason = reason
	sg.Status.Snapshot = nil
	if err := r.Status().Patch(ctx, sg, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching state to up: %w", err)
	}

	if err := r.appendHistory(ctx, sg, kshutdownv1alpha1.HistoryEntry{
		Operation: kshutdownv1alpha1.CommandUp,
		At:        now,
		Operator:  operator,
		Reason:    reason,
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("appending history after startup: %w", err)
	}

	log.Info("startup complete", "operator", operator, "resources", resourceCount)

	if err := r.consumeAnnotations(ctx, sg,
		kshutdownv1alpha1.AnnotationCommand,
		kshutdownv1alpha1.AnnotationReason,
		kshutdownv1alpha1.AnnotationOperator,
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("consuming annotations after startup: %w", err)
	}
	return ctrl.Result{}, nil
}

// restoreReplicas patches spec.replicas back to the value captured in the snapshot.
func (r *ShutdownGroupReconciler) restoreReplicas(ctx context.Context, snap kshutdownv1alpha1.ResourceSnapshot) error {
	key := types.NamespacedName{Namespace: snap.Namespace, Name: snap.Name}
	replicas := snap.PreviousReplicas
	switch snap.Kind {
	case "Deployment":
		var obj appsv1.Deployment
		if err := r.Get(ctx, key, &obj); err != nil {
			return fmt.Errorf("getting deployment %s/%s: %w", snap.Namespace, snap.Name, err)
		}
		if obj.Spec.Replicas != nil && *obj.Spec.Replicas == replicas {
			return nil
		}
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Spec.Replicas = &replicas
		if err := r.Patch(ctx, &obj, patch); err != nil {
			return fmt.Errorf("restoring replicas for deployment %s/%s: %w", snap.Namespace, snap.Name, err)
		}
	case "StatefulSet":
		var obj appsv1.StatefulSet
		if err := r.Get(ctx, key, &obj); err != nil {
			return fmt.Errorf("getting statefulset %s/%s: %w", snap.Namespace, snap.Name, err)
		}
		if obj.Spec.Replicas != nil && *obj.Spec.Replicas == replicas {
			return nil
		}
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Spec.Replicas = &replicas
		if err := r.Patch(ctx, &obj, patch); err != nil {
			return fmt.Errorf("restoring replicas for statefulset %s/%s: %w", snap.Namespace, snap.Name, err)
		}
	}
	return nil
}

// unsuspendCronJob patches spec.suspend=false only if the CronJob was active before shutdown
// (previousReplicas==0). If it was already suspended (previousReplicas==1), it is left as-is.
func (r *ShutdownGroupReconciler) unsuspendCronJob(ctx context.Context, snap kshutdownv1alpha1.ResourceSnapshot) error {
	if snap.PreviousReplicas != 0 {
		return nil
	}
	var obj batchv1.CronJob
	if err := r.Get(ctx, types.NamespacedName{Namespace: snap.Namespace, Name: snap.Name}, &obj); err != nil {
		return fmt.Errorf("getting cronjob %s/%s: %w", snap.Namespace, snap.Name, err)
	}
	if obj.Spec.Suspend == nil || !*obj.Spec.Suspend {
		return nil
	}
	f := false
	patch := client.MergeFrom(obj.DeepCopy())
	obj.Spec.Suspend = &f
	if err := r.Patch(ctx, &obj, patch); err != nil {
		return fmt.Errorf("unsuspending cronjob %s/%s: %w", snap.Namespace, snap.Name, err)
	}
	return nil
}

// collectTargets lists all Deployment, StatefulSet, and CronJob resources
// matching the ShutdownGroup's targets and returns a snapshot of their current state.
func (r *ShutdownGroupReconciler) collectTargets(ctx context.Context, sg *kshutdownv1alpha1.ShutdownGroup) ([]kshutdownv1alpha1.ResourceSnapshot, error) {
	var snapshots []kshutdownv1alpha1.ResourceSnapshot

	for _, target := range sg.Spec.Targets {
		ns := target.Namespace
		if ns == "" {
			ns = sg.Namespace
		}
		labels := client.MatchingLabels(target.LabelSelector)

		var deployments appsv1.DeploymentList
		if err := r.List(ctx, &deployments, client.InNamespace(ns), labels); err != nil {
			return nil, fmt.Errorf("listing deployments in %s: %w", ns, err)
		}
		for _, d := range deployments.Items {
			replicas := int32(1)
			if d.Spec.Replicas != nil {
				replicas = *d.Spec.Replicas
			}
			snapshots = append(snapshots, kshutdownv1alpha1.ResourceSnapshot{
				Namespace:        ns,
				Name:             d.Name,
				Kind:             "Deployment",
				PreviousReplicas: replicas,
			})
		}

		var statefulsets appsv1.StatefulSetList
		if err := r.List(ctx, &statefulsets, client.InNamespace(ns), labels); err != nil {
			return nil, fmt.Errorf("listing statefulsets in %s: %w", ns, err)
		}
		for _, s := range statefulsets.Items {
			replicas := int32(1)
			if s.Spec.Replicas != nil {
				replicas = *s.Spec.Replicas
			}
			snapshots = append(snapshots, kshutdownv1alpha1.ResourceSnapshot{
				Namespace:        ns,
				Name:             s.Name,
				Kind:             "StatefulSet",
				PreviousReplicas: replicas,
			})
		}

		var cronjobs batchv1.CronJobList
		if err := r.List(ctx, &cronjobs, client.InNamespace(ns), labels); err != nil {
			return nil, fmt.Errorf("listing cronjobs in %s: %w", ns, err)
		}
		for _, c := range cronjobs.Items {
			previousReplicas := int32(0) // 0 = was active
			if c.Spec.Suspend != nil && *c.Spec.Suspend {
				previousReplicas = 1 // 1 = was already suspended
			}
			snapshots = append(snapshots, kshutdownv1alpha1.ResourceSnapshot{
				Namespace:        ns,
				Name:             c.Name,
				Kind:             "CronJob",
				PreviousReplicas: previousReplicas,
			})
		}
	}

	return snapshots, nil
}

// scaleToZero patches spec.replicas=0 on Deployment or StatefulSet.
func (r *ShutdownGroupReconciler) scaleToZero(ctx context.Context, snap kshutdownv1alpha1.ResourceSnapshot) error {
	zero := int32(0)
	key := types.NamespacedName{Namespace: snap.Namespace, Name: snap.Name}
	switch snap.Kind {
	case "Deployment":
		var obj appsv1.Deployment
		if err := r.Get(ctx, key, &obj); err != nil {
			return fmt.Errorf("getting deployment %s/%s: %w", snap.Namespace, snap.Name, err)
		}
		if obj.Spec.Replicas != nil && *obj.Spec.Replicas == 0 {
			return nil
		}
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Spec.Replicas = &zero
		if err := r.Patch(ctx, &obj, patch); err != nil {
			return fmt.Errorf("scaling deployment %s/%s to 0: %w", snap.Namespace, snap.Name, err)
		}
	case "StatefulSet":
		var obj appsv1.StatefulSet
		if err := r.Get(ctx, key, &obj); err != nil {
			return fmt.Errorf("getting statefulset %s/%s: %w", snap.Namespace, snap.Name, err)
		}
		if obj.Spec.Replicas != nil && *obj.Spec.Replicas == 0 {
			return nil
		}
		patch := client.MergeFrom(obj.DeepCopy())
		obj.Spec.Replicas = &zero
		if err := r.Patch(ctx, &obj, patch); err != nil {
			return fmt.Errorf("scaling statefulset %s/%s to 0: %w", snap.Namespace, snap.Name, err)
		}
	}
	return nil
}

// suspendCronJob patches spec.suspend=true on a CronJob.
func (r *ShutdownGroupReconciler) suspendCronJob(ctx context.Context, snap kshutdownv1alpha1.ResourceSnapshot) error {
	var obj batchv1.CronJob
	if err := r.Get(ctx, types.NamespacedName{Namespace: snap.Namespace, Name: snap.Name}, &obj); err != nil {
		return fmt.Errorf("getting cronjob %s/%s: %w", snap.Namespace, snap.Name, err)
	}
	if obj.Spec.Suspend != nil && *obj.Spec.Suspend {
		return nil
	}
	t := true
	patch := client.MergeFrom(obj.DeepCopy())
	obj.Spec.Suspend = &t
	if err := r.Patch(ctx, &obj, patch); err != nil {
		return fmt.Errorf("suspending cronjob %s/%s: %w", snap.Namespace, snap.Name, err)
	}
	return nil
}

// appendHistory prepends an entry to status.history, capped at MaxHistoryEntries.
func (r *ShutdownGroupReconciler) appendHistory(ctx context.Context, sg *kshutdownv1alpha1.ShutdownGroup, entry kshutdownv1alpha1.HistoryEntry) error {
	patch := client.MergeFrom(sg.DeepCopy())
	history := append([]kshutdownv1alpha1.HistoryEntry{entry}, sg.Status.History...)
	if len(history) > kshutdownv1alpha1.MaxHistoryEntries {
		history = history[:kshutdownv1alpha1.MaxHistoryEntries]
	}
	sg.Status.History = history
	if err := r.Status().Patch(ctx, sg, patch); err != nil {
		return fmt.Errorf("appending history entry: %w", err)
	}
	return nil
}

// consumeAnnotations removes the given annotation keys from the ShutdownGroup in a single patch.
func (r *ShutdownGroupReconciler) consumeAnnotations(ctx context.Context, sg *kshutdownv1alpha1.ShutdownGroup, keys ...string) error {
	patch := client.MergeFrom(sg.DeepCopy())
	annotations := sg.GetAnnotations()
	for _, key := range keys {
		delete(annotations, key)
	}
	sg.SetAnnotations(annotations)
	if err := r.Patch(ctx, sg, patch); err != nil {
		return fmt.Errorf("consuming annotations %v: %w", keys, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ShutdownGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kshutdownv1alpha1.ShutdownGroup{}).
		Named("shutdowngroup").
		Complete(r)
}
