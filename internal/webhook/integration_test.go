//go:build integration

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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kshutdowniov1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
)

const (
	integTimeout  = 10 * time.Second
	integInterval = 250 * time.Millisecond
)

var _ = Describe("ShutdownGroupValidator integration", func() {
	const ns = "default"

	var (
		sg     *kshutdowniov1alpha1.ShutdownGroup
		deploy *appsv1.Deployment
	)

	BeforeEach(func() {
		// Reset allowed users for each test.
		allowedUsers = map[string]bool{}

		deploy = &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("integ-deploy-%d", GinkgoParallelProcess()),
				Namespace: ns,
				Labels:    map[string]string{"integ": "true"},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: func() *int32 { r := int32(2); return &r }(),
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"integ": "true"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"integ": "true"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
				},
			},
		}
		Expect(integClient.Create(integCtx, deploy)).To(Succeed())

		sg = &kshutdowniov1alpha1.ShutdownGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("integ-sg-%d", GinkgoParallelProcess()),
				Namespace: ns,
			},
			Spec: kshutdowniov1alpha1.ShutdownGroupSpec{
				Targets: []kshutdowniov1alpha1.Target{
					{LabelSelector: map[string]string{"integ": "true"}},
				},
			},
		}
		Expect(integClient.Create(integCtx, sg)).To(Succeed())
	})

	AfterEach(func() {
		_ = integClient.Delete(integCtx, sg)
		_ = integClient.Delete(integCtx, deploy)
	})

	It("allows command=down when the user has permissions", func() {
		allowedUsers["authorized-user"] = true

		authClient, err := impersonatingClient("authorized-user")
		Expect(err).NotTo(HaveOccurred())

		// Fetch fresh copy through the impersonating client.
		var freshSG kshutdowniov1alpha1.ShutdownGroup
		Expect(authClient.Get(integCtx,
			types.NamespacedName{Name: sg.Name, Namespace: ns}, &freshSG)).To(Succeed())

		patch := client.MergeFrom(freshSG.DeepCopy())
		freshSG.Annotations = map[string]string{kshutdowniov1alpha1.AnnotationCommand: "down"}
		Expect(authClient.Patch(integCtx, &freshSG, patch)).To(Succeed())
	})

	It("denies command=down when the user lacks permissions", func() {
		// denied-user is absent from allowedUsers → checker returns false.
		authClient, err := impersonatingClient("denied-user")
		Expect(err).NotTo(HaveOccurred())

		var freshSG kshutdowniov1alpha1.ShutdownGroup
		Expect(authClient.Get(integCtx,
			types.NamespacedName{Name: sg.Name, Namespace: ns}, &freshSG)).To(Succeed())

		patch := client.MergeFrom(freshSG.DeepCopy())
		freshSG.Annotations = map[string]string{kshutdowniov1alpha1.AnnotationCommand: "down"}
		err = authClient.Patch(integCtx, &freshSG, patch)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("insufficient permissions"))
	})

	It("allows command=down with partial when some targets are authorized", func() {
		// The integCheckPermission grants by username — "partial-user" is allowed.
		allowedUsers["partial-user"] = true

		authClient, err := impersonatingClient("partial-user")
		Expect(err).NotTo(HaveOccurred())

		var freshSG kshutdowniov1alpha1.ShutdownGroup
		Expect(authClient.Get(integCtx,
			types.NamespacedName{Name: sg.Name, Namespace: ns}, &freshSG)).To(Succeed())

		patch := client.MergeFrom(freshSG.DeepCopy())
		freshSG.Annotations = map[string]string{
			kshutdowniov1alpha1.AnnotationCommand: "down",
			kshutdowniov1alpha1.AnnotationPartial: "true",
		}
		// Should succeed since the user is authorized for the deployment target.
		Expect(authClient.Patch(integCtx, &freshSG, patch)).To(Succeed())
	})
})
