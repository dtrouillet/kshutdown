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
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
)

func TestWebhooks(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Webhook Suite")
}

// makeCtx returns a context carrying a CREATE admission.Request for the given user.
func makeCtx(user string) context.Context {
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			UserInfo:  authenticationv1.UserInfo{Username: user},
		},
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

// makeUpdateCtx returns a context carrying an UPDATE admission.Request where oldSG is the
// previous object. Used to test the mutator's guard against overwriting an existing operator.
func makeUpdateCtx(user string, oldSG *kshutdownv1alpha1.ShutdownGroup) context.Context {
	raw, err := json.Marshal(oldSG)
	if err != nil {
		panic(err)
	}
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			UserInfo:  authenticationv1.UserInfo{Username: user},
			OldObject: runtime.RawExtension{Raw: raw},
		},
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

func ptr[T any](v T) *T { return &v }

// buildScheme returns a runtime.Scheme with the types needed by the fake client.
func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = kshutdownv1alpha1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

// allowAll is an SAR checker that always allows.
func allowAll(_ context.Context, _ client.Client, _ string, _ []string, _, _, _, _, _ string) (bool, error) {
	return true, nil
}

// denyAll is an SAR checker that always denies.
func denyAll(_ context.Context, _ client.Client, _ string, _ []string, _, _, _, _, _ string) (bool, error) {
	return false, nil
}

// denyDeployments denies access only to deployments.
func denyDeployments(_ context.Context, _ client.Client, _ string, _ []string, _, _, resource, _, _ string) (bool, error) {
	return resource != "deployments", nil
}

var _ = Describe("ShutdownGroupMutator", func() {
	const ns = "payments"

	var sg *kshutdownv1alpha1.ShutdownGroup

	BeforeEach(func() {
		sg = &kshutdownv1alpha1.ShutdownGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "payment-stack", Namespace: ns},
		}
	})

	It("does nothing when no command annotation is present", func() {
		ctx := makeCtx("alice")
		err := (&ShutdownGroupMutator{}).Default(ctx, sg)
		Expect(err).NotTo(HaveOccurred())
		Expect(sg.Annotations).To(BeNil())
	})

	It("injects operator from UserInfo when command=down is set", func() {
		sg.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "down"}
		ctx := makeCtx("alice")
		err := (&ShutdownGroupMutator{}).Default(ctx, sg)
		Expect(err).NotTo(HaveOccurred())
		Expect(sg.Annotations[kshutdownv1alpha1.AnnotationOperator]).To(Equal("alice"))
	})

	It("injects operator from UserInfo when command=up is set", func() {
		sg.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "up"}
		ctx := makeCtx("bob")
		err := (&ShutdownGroupMutator{}).Default(ctx, sg)
		Expect(err).NotTo(HaveOccurred())
		Expect(sg.Annotations[kshutdownv1alpha1.AnnotationOperator]).To(Equal("bob"))
	})

	It("overwrites an existing operator annotation with the real UserInfo identity", func() {
		sg.Annotations = map[string]string{
			kshutdownv1alpha1.AnnotationCommand:  "down",
			kshutdownv1alpha1.AnnotationOperator: "spoofed-identity",
		}
		ctx := makeCtx("alice")
		err := (&ShutdownGroupMutator{}).Default(ctx, sg)
		Expect(err).NotTo(HaveOccurred())
		Expect(sg.Annotations[kshutdownv1alpha1.AnnotationOperator]).To(Equal("alice"))
	})

	It("returns error when admission request is missing from context", func() {
		sg.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "down"}
		err := (&ShutdownGroupMutator{}).Default(context.Background(), sg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("retrieving admission request"))
	})

	It("does not overwrite operator on UPDATE when command annotation is unchanged", func() {
		// Simulates a user making an unrelated metadata change (e.g. adding a label)
		// while a command is already pending. The original operator must be preserved.
		oldSG := &kshutdownv1alpha1.ShutdownGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "payment-stack",
				Namespace: ns,
				Annotations: map[string]string{
					kshutdownv1alpha1.AnnotationCommand:  "down",
					kshutdownv1alpha1.AnnotationOperator: "original-operator",
				},
			},
		}
		sg.Annotations = map[string]string{
			kshutdownv1alpha1.AnnotationCommand:  "down",
			kshutdownv1alpha1.AnnotationOperator: "original-operator",
		}
		ctx := makeUpdateCtx("unrelated-user", oldSG)
		err := (&ShutdownGroupMutator{}).Default(ctx, sg)
		Expect(err).NotTo(HaveOccurred())
		Expect(sg.Annotations[kshutdownv1alpha1.AnnotationOperator]).To(Equal("original-operator"))
	})

	It("injects operator on UPDATE when command annotation is newly added", func() {
		oldSG := &kshutdownv1alpha1.ShutdownGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "payment-stack", Namespace: ns},
		}
		sg.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "down"}
		ctx := makeUpdateCtx("alice", oldSG)
		err := (&ShutdownGroupMutator{}).Default(ctx, sg)
		Expect(err).NotTo(HaveOccurred())
		Expect(sg.Annotations[kshutdownv1alpha1.AnnotationOperator]).To(Equal("alice"))
	})
})

var _ = Describe("ShutdownGroupValidator", func() {
	const ns = "payments"

	var (
		fakeClient client.Client
		validator  *ShutdownGroupValidator
		deploy     *appsv1.Deployment
		stateful   *appsv1.StatefulSet
		cj         *batchv1.CronJob
		sg         *kshutdownv1alpha1.ShutdownGroup
	)

	BeforeEach(func() {
		scheme := buildScheme()
		deploy = &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: ns, Labels: map[string]string{"app": "payment"}},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr(int32(3)),
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payment"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payment"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "nginx"}}},
				},
			},
		}
		stateful = &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: ns, Labels: map[string]string{"app": "payment"}},
			Spec: appsv1.StatefulSetSpec{
				Replicas: ptr(int32(1)),
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payment"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payment"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "db", Image: "postgres"}}},
				},
			},
		}
		cj = &batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{Name: "invoicer", Namespace: ns, Labels: map[string]string{"app": "payment"}},
			Spec: batchv1.CronJobSpec{
				Schedule: "0 * * * *",
				JobTemplate: batchv1.JobTemplateSpec{
					Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers:    []corev1.Container{{Name: "job", Image: "busybox"}},
							RestartPolicy: corev1.RestartPolicyOnFailure,
						},
					}},
				},
			},
		}
		sg = &kshutdownv1alpha1.ShutdownGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "payment-stack", Namespace: ns},
			Spec: kshutdownv1alpha1.ShutdownGroupSpec{
				Targets: []kshutdownv1alpha1.Target{
					{LabelSelector: map[string]string{"app": "payment"}},
				},
			},
		}

		fakeClient = fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(deploy, stateful, cj, sg).
			Build()
	})

	// --- ValidateCreate ---

	Describe("ValidateCreate", func() {
		It("allows creation without a command annotation", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: allowAll}
			ctx := makeCtx("user-a")
			warnings, err := validator.ValidateCreate(ctx, sg)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})

		It("allows creation with command=down when user has permissions", func() {
			sg.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "down"}
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: allowAll}
			ctx := makeCtx("user-a")
			warnings, err := validator.ValidateCreate(ctx, sg)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})

		It("denies creation with command=down when user lacks permissions", func() {
			sg.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "down"}
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: denyAll}
			ctx := makeCtx("user-noauth")
			_, err := validator.ValidateCreate(ctx, sg)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("insufficient permissions"))
		})
	})

	// --- ValidateUpdate: command annotation not changing ---

	Describe("ValidateUpdate — no command change", func() {
		It("allows update when command annotation is absent in both old and new", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: denyAll}
			ctx := makeCtx("user-noauth")
			// spec change only — no SAR should be triggered
			oldSG := sg.DeepCopy()
			newSG := sg.DeepCopy()
			newSG.Spec.Targets = append(newSG.Spec.Targets, kshutdownv1alpha1.Target{
				LabelSelector: map[string]string{"other": "label"},
			})
			warnings, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})

		It("allows update that removes an existing command annotation", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: denyAll}
			ctx := makeCtx("user-noauth")
			oldSG := sg.DeepCopy()
			oldSG.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "down"}
			newSG := sg.DeepCopy()
			// annotation removed — allow
			warnings, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})

		It("allows update that keeps the same command annotation (operator consuming annotations)", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: denyAll}
			ctx := makeCtx("user-noauth")
			oldSG := sg.DeepCopy()
			oldSG.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "down"}
			newSG := oldSG.DeepCopy()
			// same command — no new command, operator is consuming it
			warnings, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})
	})

	// --- ValidateUpdate: down command ---

	Describe("ValidateUpdate — down command", func() {
		It("allows down when user has patch on all targets", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: allowAll}
			ctx := makeCtx("authorized-user")
			oldSG := sg.DeepCopy()
			newSG := sg.DeepCopy()
			newSG.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "down"}
			warnings, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})

		It("denies down when user lacks patch on all targets", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: denyAll}
			ctx := makeCtx("unauthorized-user")
			oldSG := sg.DeepCopy()
			newSG := sg.DeepCopy()
			newSG.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "down"}
			_, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("insufficient permissions"))
			Expect(err.Error()).To(ContainSubstring("api"))
			Expect(err.Error()).To(ContainSubstring("db"))
			Expect(err.Error()).To(ContainSubstring("invoicer"))
		})

		It("denies down when user lacks patch on some targets (no --partial annotation)", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: denyDeployments}
			ctx := makeCtx("partial-user")
			oldSG := sg.DeepCopy()
			newSG := sg.DeepCopy()
			newSG.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "down"}
			_, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Deployment"))
		})

		It("allows down with warning when --partial is set and at least one target is authorized", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: denyDeployments}
			ctx := makeCtx("partial-user")
			oldSG := sg.DeepCopy()
			newSG := sg.DeepCopy()
			newSG.Annotations = map[string]string{
				kshutdownv1alpha1.AnnotationCommand: "down",
				kshutdownv1alpha1.AnnotationPartial: "true",
			}
			warnings, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(HaveLen(1))
			Expect(warnings[0]).To(ContainSubstring("skipped"))
			Expect(warnings[0]).To(ContainSubstring("Deployment"))
		})

		It("denies down even with --partial when ALL targets are forbidden", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: denyAll}
			ctx := makeCtx("no-perms-user")
			oldSG := sg.DeepCopy()
			newSG := sg.DeepCopy()
			newSG.Annotations = map[string]string{
				kshutdownv1alpha1.AnnotationCommand: "down",
				kshutdownv1alpha1.AnnotationPartial: "true",
			}
			_, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("insufficient permissions"))
		})
	})

	// --- ValidateUpdate: up command ---

	Describe("ValidateUpdate — up command", func() {
		var sgWithSnapshot *kshutdownv1alpha1.ShutdownGroup

		BeforeEach(func() {
			sgWithSnapshot = sg.DeepCopy()
			sgWithSnapshot.Status.State = kshutdownv1alpha1.StateDown
			sgWithSnapshot.Status.Snapshot = []kshutdownv1alpha1.ResourceSnapshot{
				{Namespace: ns, Name: "api", Kind: "Deployment", PreviousReplicas: 3},
				{Namespace: ns, Name: "db", Kind: "StatefulSet", PreviousReplicas: 1},
				{Namespace: ns, Name: "invoicer", Kind: "CronJob", PreviousReplicas: 0},
			}
		})

		It("allows up when user has patch on all snapshot resources", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: allowAll}
			ctx := makeCtx("authorized-user")
			oldSG := sgWithSnapshot.DeepCopy()
			newSG := sgWithSnapshot.DeepCopy()
			newSG.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "up"}
			warnings, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})

		It("denies up when user lacks patch on some snapshot resources", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: denyDeployments}
			ctx := makeCtx("partial-user")
			oldSG := sgWithSnapshot.DeepCopy()
			newSG := sgWithSnapshot.DeepCopy()
			newSG.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "up"}
			_, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Deployment"))
		})

		It("allows up when snapshot is empty (no-op for the operator)", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: denyAll}
			ctx := makeCtx("any-user")
			emptySG := sg.DeepCopy()
			oldSG := emptySG.DeepCopy()
			newSG := emptySG.DeepCopy()
			newSG.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "up"}
			warnings, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})
	})

	// --- ValidateDelete ---

	Describe("ValidateDelete", func() {
		It("always allows deletion", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: denyAll}
			ctx := makeCtx("any-user")
			warnings, err := validator.ValidateDelete(ctx, sg)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})
	})

	// --- unknown command ---

	Describe("unknown command annotation", func() {
		It("denies an unknown command value", func() {
			validator = &ShutdownGroupValidator{Client: fakeClient, checkPermission: allowAll}
			ctx := makeCtx("user-a")
			oldSG := sg.DeepCopy()
			newSG := sg.DeepCopy()
			newSG.Annotations = map[string]string{kshutdownv1alpha1.AnnotationCommand: "nuke"}
			_, err := validator.ValidateUpdate(ctx, oldSG, newSG)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown command"))
		})
	})
})
