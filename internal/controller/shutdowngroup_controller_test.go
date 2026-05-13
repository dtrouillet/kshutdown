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

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
)

func newReconciler() *ShutdownGroupReconciler {
	return &ShutdownGroupReconciler{
		Client: k8sClient,
		Scheme: k8sClient.Scheme(),
	}
}

func triggerDown(ctx context.Context, sg *kshutdownv1alpha1.ShutdownGroup, operator, reason string) {
	GinkgoHelper()
	patch := client.MergeFrom(sg.DeepCopy())
	if sg.Annotations == nil {
		sg.Annotations = make(map[string]string)
	}
	sg.Annotations[kshutdownv1alpha1.AnnotationCommand] = kshutdownv1alpha1.CommandDown
	sg.Annotations[kshutdownv1alpha1.AnnotationOperator] = operator
	sg.Annotations[kshutdownv1alpha1.AnnotationReason] = reason
	Expect(k8sClient.Patch(ctx, sg, patch)).To(Succeed())
}

func ptr[T any](v T) *T { return &v }

// --- TASK-19: happy path, double-down idempotence, CronJob ---

var _ = Describe("reconcileDown", func() {
	ctx := context.Background()
	var r *ShutdownGroupReconciler
	BeforeEach(func() { r = newReconciler() })

	Context("happy path — Deployment scaled down from unknown state", func() {
		const ns = "default"
		const sgName = "sg-deploy-test"
		const deployName = "api-server"
		const label = "app.kubernetes.io/part-of"
		const labelVal = "myapp-deploy"

		var deploy appsv1.Deployment
		var sg kshutdownv1alpha1.ShutdownGroup

		BeforeEach(func() {
			deploy = appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deployName,
					Namespace: ns,
					Labels:    map[string]string{label: labelVal},
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr(int32(3)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{label: labelVal},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{label: labelVal}},
						Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, &deploy)).To(Succeed())

			sg = kshutdownv1alpha1.ShutdownGroup{
				ObjectMeta: metav1.ObjectMeta{Name: sgName, Namespace: ns},
				Spec: kshutdownv1alpha1.ShutdownGroupSpec{
					Targets: []kshutdownv1alpha1.Target{
						{LabelSelector: map[string]string{label: labelVal}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, &sg)).To(Succeed())
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, &sg)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &deploy)).To(Succeed())
		})

		It("scales the deployment to 0 and sets status.state=down", func() {
			triggerDown(ctx, &sg, "user-abc", "incident P1")

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: sgName, Namespace: ns},
			})
			Expect(err).NotTo(HaveOccurred())

			By("checking status.state=down")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sgName, Namespace: ns}, &sg)).To(Succeed())
			Expect(sg.Status.State).To(Equal(kshutdownv1alpha1.StateDown))
			Expect(sg.Status.Operator).To(Equal("user-abc"))
			Expect(sg.Status.Reason).To(Equal("incident P1"))
			Expect(sg.Status.Since).NotTo(BeNil())

			By("checking snapshot is captured")
			Expect(sg.Status.Snapshot).To(HaveLen(1))
			Expect(sg.Status.Snapshot[0].Name).To(Equal(deployName))
			Expect(sg.Status.Snapshot[0].Kind).To(Equal("Deployment"))
			Expect(sg.Status.Snapshot[0].PreviousReplicas).To(Equal(int32(3)))

			By("checking deployment is scaled to 0")
			var updatedDeploy appsv1.Deployment
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: deployName, Namespace: ns}, &updatedDeploy)).To(Succeed())
			Expect(updatedDeploy.Spec.Replicas).NotTo(BeNil())
			Expect(*updatedDeploy.Spec.Replicas).To(Equal(int32(0)))

			By("checking skip-reconcile annotation on deployment")
			Expect(updatedDeploy.Annotations[kshutdownv1alpha1.AnnotationSkipReconcile]).To(Equal("true"))

			By("checking command annotation is consumed")
			Expect(sg.Annotations[kshutdownv1alpha1.AnnotationCommand]).To(BeEmpty())
			Expect(sg.Annotations[kshutdownv1alpha1.AnnotationReason]).To(BeEmpty())
			Expect(sg.Annotations[kshutdownv1alpha1.AnnotationOperator]).To(BeEmpty())

			By("checking history entry is recorded")
			Expect(sg.Status.History).To(HaveLen(1))
			Expect(sg.Status.History[0].Operation).To(Equal(kshutdownv1alpha1.CommandDown))
			Expect(sg.Status.History[0].Operator).To(Equal("user-abc"))
			Expect(sg.Status.History[0].Reason).To(Equal("incident P1"))
		})
	})

	// --- TASK-19: double-down idempotence ---

	Context("idempotent double-down — state already down", func() {
		const ns = "default"
		const sgName = "sg-double-down"
		const deployName = "worker"

		var deploy appsv1.Deployment
		var sg kshutdownv1alpha1.ShutdownGroup

		BeforeEach(func() {
			deploy = appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deployName,
					Namespace: ns,
					Labels:    map[string]string{"app": "worker"},
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr(int32(2)),
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "worker"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "worker"}},
						Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "w", Image: "nginx"}}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, &deploy)).To(Succeed())

			sg = kshutdownv1alpha1.ShutdownGroup{
				ObjectMeta: metav1.ObjectMeta{Name: sgName, Namespace: ns},
				Spec: kshutdownv1alpha1.ShutdownGroupSpec{
					Targets: []kshutdownv1alpha1.Target{{LabelSelector: map[string]string{"app": "worker"}}},
				},
			}
			Expect(k8sClient.Create(ctx, &sg)).To(Succeed())

			// First shutdown
			triggerDown(ctx, &sg, "user-xyz", "first shutdown")
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: sgName, Namespace: ns},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sgName, Namespace: ns}, &sg)).To(Succeed())
			Expect(sg.Status.State).To(Equal(kshutdownv1alpha1.StateDown))
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, &sg)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &deploy)).To(Succeed())
		})

		It("is a no-op and does not overwrite the snapshot", func() {
			originalSnapshot := sg.Status.Snapshot

			triggerDown(ctx, &sg, "user-xyz", "second shutdown attempt")
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: sgName, Namespace: ns},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sgName, Namespace: ns}, &sg)).To(Succeed())
			Expect(sg.Status.State).To(Equal(kshutdownv1alpha1.StateDown))
			Expect(sg.Status.Snapshot).To(Equal(originalSnapshot))
			Expect(sg.Annotations[kshutdownv1alpha1.AnnotationCommand]).To(BeEmpty())
		})
	})

	// --- TASK-19: CronJob target ---

	Context("CronJob target — active CronJob is suspended", func() {
		const ns = "default"
		const sgName = "sg-cronjob-test"
		const cronName = "invoice-generator"

		var cj batchv1.CronJob
		var sg kshutdownv1alpha1.ShutdownGroup

		BeforeEach(func() {
			cj = batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cronName,
					Namespace: ns,
					Labels:    map[string]string{"app": "invoices"},
				},
				Spec: batchv1.CronJobSpec{
					Schedule: "0 * * * *",
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers:    []corev1.Container{{Name: "job", Image: "busybox"}},
									RestartPolicy: corev1.RestartPolicyOnFailure,
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, &cj)).To(Succeed())

			sg = kshutdownv1alpha1.ShutdownGroup{
				ObjectMeta: metav1.ObjectMeta{Name: sgName, Namespace: ns},
				Spec: kshutdownv1alpha1.ShutdownGroupSpec{
					Targets: []kshutdownv1alpha1.Target{{LabelSelector: map[string]string{"app": "invoices"}}},
				},
			}
			Expect(k8sClient.Create(ctx, &sg)).To(Succeed())
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, &sg)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &cj)).To(Succeed())
		})

		It("suspends the CronJob and records previousReplicas=0", func() {
			triggerDown(ctx, &sg, "user-ops", "P2 incident")

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: sgName, Namespace: ns},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sgName, Namespace: ns}, &sg)).To(Succeed())
			Expect(sg.Status.State).To(Equal(kshutdownv1alpha1.StateDown))

			Expect(sg.Status.Snapshot).To(HaveLen(1))
			Expect(sg.Status.Snapshot[0].Kind).To(Equal("CronJob"))
			Expect(sg.Status.Snapshot[0].PreviousReplicas).To(Equal(int32(0)))

			var updatedCJ batchv1.CronJob
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cronName, Namespace: ns}, &updatedCJ)).To(Succeed())
			Expect(updatedCJ.Spec.Suspend).NotTo(BeNil())
			Expect(*updatedCJ.Spec.Suspend).To(BeTrue())
		})
	})

	// --- TASK-20: multi-namespace targets ---

	Context("multi-namespace targets", func() {
		const sgNs = "default"
		const sgName = "sg-multi-ns"
		const ns2 = "payments-cron"

		var deploy1, deploy2 appsv1.Deployment
		var sg kshutdownv1alpha1.ShutdownGroup
		var ns2Obj corev1.Namespace

		BeforeEach(func() {
			ns2Obj = corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns2}}
			Expect(k8sClient.Create(ctx, &ns2Obj)).To(Succeed())

			deploy1 = appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "api",
					Namespace: sgNs,
					Labels:    map[string]string{"app.kubernetes.io/part-of": "payment"},
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr(int32(2)),
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/part-of": "payment"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/part-of": "payment"}},
						Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "nginx"}}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, &deploy1)).To(Succeed())

			deploy2 = appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "worker",
					Namespace: ns2,
					Labels:    map[string]string{"app.kubernetes.io/part-of": "payment"},
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr(int32(1)),
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/part-of": "payment"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/part-of": "payment"}},
						Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "worker", Image: "nginx"}}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, &deploy2)).To(Succeed())

			sg = kshutdownv1alpha1.ShutdownGroup{
				ObjectMeta: metav1.ObjectMeta{Name: sgName, Namespace: sgNs},
				Spec: kshutdownv1alpha1.ShutdownGroupSpec{
					Targets: []kshutdownv1alpha1.Target{
						{LabelSelector: map[string]string{"app.kubernetes.io/part-of": "payment"}},
						{Namespace: ns2, LabelSelector: map[string]string{"app.kubernetes.io/part-of": "payment"}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, &sg)).To(Succeed())
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, &sg)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &deploy1)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &deploy2)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &ns2Obj)).To(Succeed())
		})

		It("scales deployments in both namespaces to 0", func() {
			triggerDown(ctx, &sg, "user-multi", "multi-ns test")

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: sgName, Namespace: sgNs},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sgName, Namespace: sgNs}, &sg)).To(Succeed())
			Expect(sg.Status.State).To(Equal(kshutdownv1alpha1.StateDown))
			Expect(sg.Status.Snapshot).To(HaveLen(2))

			var d1, d2 appsv1.Deployment
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "api", Namespace: sgNs}, &d1)).To(Succeed())
			Expect(*d1.Spec.Replicas).To(Equal(int32(0)))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "worker", Namespace: ns2}, &d2)).To(Succeed())
			Expect(*d2.Spec.Replicas).To(Equal(int32(0)))
		})
	})

	// --- TASK-21: scale failure — annotation not consumed ---

	Context("scale failure — annotation is not consumed on error", func() {
		const ns = "default"
		const sgName = "sg-failure-test"

		var sg kshutdownv1alpha1.ShutdownGroup

		BeforeEach(func() {
			sg = kshutdownv1alpha1.ShutdownGroup{
				ObjectMeta: metav1.ObjectMeta{Name: sgName, Namespace: ns},
				Spec: kshutdownv1alpha1.ShutdownGroupSpec{
					Targets: []kshutdownv1alpha1.Target{
						{LabelSelector: map[string]string{"app": "ghost"}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, &sg)).To(Succeed())

			// Pre-populate the snapshot with a non-existent deployment (simulates
			// crash-recovery state where the snapshot was written but the resource
			// was deleted before scaling could complete).
			patch := client.MergeFrom(sg.DeepCopy())
			sg.Status.Snapshot = []kshutdownv1alpha1.ResourceSnapshot{
				{Namespace: ns, Name: "ghost-deployment", Kind: "Deployment", PreviousReplicas: 3},
			}
			Expect(k8sClient.Status().Patch(ctx, &sg, patch)).To(Succeed())
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, &sg)).To(Succeed())
		})

		It("returns an error and leaves the command annotation intact", func() {
			triggerDown(ctx, &sg, "user-err", "error test")

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: sgName, Namespace: ns},
			})
			Expect(err).To(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sgName, Namespace: ns}, &sg)).To(Succeed())
			Expect(sg.Annotations[kshutdownv1alpha1.AnnotationCommand]).To(Equal(kshutdownv1alpha1.CommandDown))
			Expect(sg.Status.State).NotTo(Equal(kshutdownv1alpha1.StateDown))
		})
	})
})
