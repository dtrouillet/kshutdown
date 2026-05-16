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

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
	"github.com/dtrouillet/kshutdown/internal/authz"
)

// +kubebuilder:webhook:path=/mutate-kshutdown-io-v1alpha1-shutdowngroup,mutating=true,failurePolicy=Ignore,sideEffects=None,groups=kshutdown.io,resources=shutdowngroups,verbs=create;update,versions=v1alpha1,name=mshutdowngroup.kb.io,admissionReviewVersions=v1

// ShutdownGroupMutator injects kshutdown.io/operator from the admission request UserInfo
// whenever kshutdown.io/command is set, ensuring the operator field reflects the real
// Kubernetes identity whether the user goes through the CLI or kubectl directly.
type ShutdownGroupMutator struct{}

// SetupWebhookWithManager registers the mutator with the controller-runtime manager.
func (m *ShutdownGroupMutator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &kshutdownv1alpha1.ShutdownGroup{}).
		WithDefaulter(m).
		Complete()
}

func (m *ShutdownGroupMutator) Default(ctx context.Context, sg *kshutdownv1alpha1.ShutdownGroup) error {
	newCmd := sg.Annotations[kshutdownv1alpha1.AnnotationCommand]
	if newCmd == "" {
		return nil
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("retrieving admission request: %w", err)
	}

	// On UPDATE, only inject when the command annotation is newly set or changed.
	// If the command was already present and unchanged, an unrelated metadata update
	// must not overwrite the original operator identity already stored in the annotation.
	if req.Operation == admissionv1.Update && len(req.OldObject.Raw) > 0 {
		var oldSG kshutdownv1alpha1.ShutdownGroup
		if err := json.Unmarshal(req.OldObject.Raw, &oldSG); err != nil {
			return fmt.Errorf("decoding old ShutdownGroup: %w", err)
		}
		if oldSG.Annotations[kshutdownv1alpha1.AnnotationCommand] == newCmd {
			return nil
		}
	}

	if sg.Annotations == nil {
		sg.Annotations = make(map[string]string)
	}
	sg.Annotations[kshutdownv1alpha1.AnnotationOperator] = req.UserInfo.Username
	return nil
}

// +kubebuilder:webhook:path=/validate-kshutdown-io-v1alpha1-shutdowngroup,mutating=false,failurePolicy=Fail,sideEffects=None,groups=kshutdown.io,resources=shutdowngroups,verbs=create;update,versions=v1alpha1,name=vshutdowngroup.kb.io,admissionReviewVersions=v1

// ShutdownGroupValidator enforces server-side authorization on command annotations.
// It intercepts CREATE and UPDATE on ShutdownGroup and verifies via SubjectAccessReview
// that the authenticated user has patch permission on every target workload before
// allowing the annotation to be written. This is the only authoritative security gate —
// the CLI's SelfSubjectAccessReview checks are a UX convenience only.
//
// Threat model note: kshutdown.io/partial=true is trusted as-is. Any principal that
// can patch a ShutdownGroup can self-assert partial mode. This is intentional — a
// principal with patch on ShutdownGroup already has enough access to write any
// annotation and could bypass strict validation regardless. The webhook still requires
// at least one authorized target, preventing a fully-unauthorized actor from proceeding.
type ShutdownGroupValidator struct {
	client.Client
	// checkPermission is the SAR call used to validate a user's access to a workload.
	// Injected in tests; nil in production falls back to authz.CheckFor.
	checkPermission func(ctx context.Context, c client.Client,
		user string, groups []string,
		namespace, group, resource, name, verb string,
	) (bool, error)
}

// +kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create

// SetupWebhookWithManager registers the validator with the controller-runtime manager.
func (v *ShutdownGroupValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &kshutdownv1alpha1.ShutdownGroup{}).
		WithValidator(v).
		Complete()
}

func (v *ShutdownGroupValidator) ValidateCreate(ctx context.Context, sg *kshutdownv1alpha1.ShutdownGroup) (admission.Warnings, error) {
	cmd := sg.Annotations[kshutdownv1alpha1.AnnotationCommand]
	if cmd == "" {
		return nil, nil
	}
	return v.validateCommand(ctx, cmd, sg)
}

func (v *ShutdownGroupValidator) ValidateUpdate(ctx context.Context, oldSG, newSG *kshutdownv1alpha1.ShutdownGroup) (admission.Warnings, error) {
	oldCmd := oldSG.Annotations[kshutdownv1alpha1.AnnotationCommand]
	newCmd := newSG.Annotations[kshutdownv1alpha1.AnnotationCommand]

	// Annotation unchanged or being removed — allow immediately.
	if newCmd == "" || newCmd == oldCmd {
		return nil, nil
	}

	return v.validateCommand(ctx, newCmd, newSG)
}

func (v *ShutdownGroupValidator) ValidateDelete(_ context.Context, _ *kshutdownv1alpha1.ShutdownGroup) (admission.Warnings, error) {
	return nil, nil
}

func (v *ShutdownGroupValidator) validateCommand(ctx context.Context, cmd string, sg *kshutdownv1alpha1.ShutdownGroup) (admission.Warnings, error) {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("retrieving admission request: %w", err)
	}

	user := req.UserInfo.Username
	groups := req.UserInfo.Groups

	var forbidden []string
	var authorized int

	switch cmd {
	case kshutdownv1alpha1.CommandDown:
		forbidden, authorized, err = v.checkDownTargets(ctx, sg, user, groups)
	case kshutdownv1alpha1.CommandUp:
		forbidden, authorized, err = v.checkUpTargets(ctx, sg, user, groups)
	default:
		return nil, fmt.Errorf("unknown command annotation value: %q", cmd)
	}
	if err != nil {
		return nil, err
	}

	if len(forbidden) == 0 {
		return nil, nil
	}

	// --partial: allow if at least one target is authorized and the annotation is set.
	if sg.Annotations[kshutdownv1alpha1.AnnotationPartial] == "true" &&
		cmd == kshutdownv1alpha1.CommandDown &&
		authorized > 0 {
		return admission.Warnings{fmt.Sprintf(
			"%d resource(s) skipped (insufficient permissions): %s",
			len(forbidden), strings.Join(forbidden, ", "),
		)}, nil
	}

	return nil, fmt.Errorf("insufficient permissions on %d resource(s): %s",
		len(forbidden), strings.Join(forbidden, ", "))
}

// checkDownTargets lists all workloads matching spec.targets and checks patch permission.
func (v *ShutdownGroupValidator) checkDownTargets(
	ctx context.Context,
	sg *kshutdownv1alpha1.ShutdownGroup,
	user string, groups []string,
) (forbidden []string, authorized int, err error) {
	check := v.sarCheck()

	for _, target := range sg.Spec.Targets {
		ns := target.Namespace
		if ns == "" {
			ns = sg.Namespace
		}
		labels := client.MatchingLabels(target.LabelSelector)

		var deployments appsv1.DeploymentList
		if err := v.List(ctx, &deployments, client.InNamespace(ns), labels); err != nil {
			return nil, 0, fmt.Errorf("listing deployments in %s: %w", ns, err)
		}
		for _, d := range deployments.Items {
			allowed, err := check(ctx, v.Client, user, groups, ns, "apps", "deployments", d.Name, "patch")
			if err != nil {
				return nil, 0, fmt.Errorf("checking patch permission for Deployment %s/%s: %w", ns, d.Name, err)
			}
			if allowed {
				authorized++
			} else {
				forbidden = append(forbidden, fmt.Sprintf("Deployment %s/%s", ns, d.Name))
			}
		}

		var statefulsets appsv1.StatefulSetList
		if err := v.List(ctx, &statefulsets, client.InNamespace(ns), labels); err != nil {
			return nil, 0, fmt.Errorf("listing statefulsets in %s: %w", ns, err)
		}
		for _, s := range statefulsets.Items {
			allowed, err := check(ctx, v.Client, user, groups, ns, "apps", "statefulsets", s.Name, "patch")
			if err != nil {
				return nil, 0, fmt.Errorf("checking patch permission for StatefulSet %s/%s: %w", ns, s.Name, err)
			}
			if allowed {
				authorized++
			} else {
				forbidden = append(forbidden, fmt.Sprintf("StatefulSet %s/%s", ns, s.Name))
			}
		}

		var cronjobs batchv1.CronJobList
		if err := v.List(ctx, &cronjobs, client.InNamespace(ns), labels); err != nil {
			return nil, 0, fmt.Errorf("listing cronjobs in %s: %w", ns, err)
		}
		for _, cj := range cronjobs.Items {
			allowed, err := check(ctx, v.Client, user, groups, ns, "batch", "cronjobs", cj.Name, "patch")
			if err != nil {
				return nil, 0, fmt.Errorf("checking patch permission for CronJob %s/%s: %w", ns, cj.Name, err)
			}
			if allowed {
				authorized++
			} else {
				forbidden = append(forbidden, fmt.Sprintf("CronJob %s/%s", ns, cj.Name))
			}
		}
	}
	return forbidden, authorized, nil
}

// checkUpTargets checks patch permission against each entry in status.snapshot.
func (v *ShutdownGroupValidator) checkUpTargets(
	ctx context.Context,
	sg *kshutdownv1alpha1.ShutdownGroup,
	user string, groups []string,
) (forbidden []string, authorized int, err error) {
	check := v.sarCheck()

	for _, snap := range sg.Status.Snapshot {
		var group, resource string
		switch snap.Kind {
		case "Deployment":
			group, resource = "apps", "deployments"
		case "StatefulSet":
			group, resource = "apps", "statefulsets"
		case "CronJob":
			group, resource = "batch", "cronjobs"
		default:
			continue
		}
		allowed, err := check(ctx, v.Client, user, groups, snap.Namespace, group, resource, snap.Name, "patch")
		if err != nil {
			return nil, 0, fmt.Errorf("checking patch permission for %s %s/%s: %w", snap.Kind, snap.Namespace, snap.Name, err)
		}
		if allowed {
			authorized++
		} else {
			forbidden = append(forbidden, fmt.Sprintf("%s %s/%s", snap.Kind, snap.Namespace, snap.Name))
		}
	}
	return forbidden, authorized, nil
}

// sarCheck returns the active SAR check function — test-injected or the real authz.CheckFor.
func (v *ShutdownGroupValidator) sarCheck() func(
	ctx context.Context, c client.Client,
	user string, groups []string,
	namespace, group, resource, name, verb string,
) (bool, error) {
	if v.checkPermission != nil {
		return v.checkPermission
	}
	return authz.CheckFor
}
