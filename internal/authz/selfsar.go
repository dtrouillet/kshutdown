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

package authz

import (
	"context"
	"fmt"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// CheckPatch returns true if the current user has patch permission on the given resource.
func CheckPatch(ctx context.Context, cs *kubernetes.Clientset, namespace, group, resource, name string) (bool, error) {
	sar, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx,
		&authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace: namespace,
					Verb:      "patch",
					Group:     group,
					Resource:  resource,
					Name:      name,
				},
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		return false, fmt.Errorf("selfsubjectaccessreview for %s/%s (%s): %w", namespace, name, resource, err)
	}
	return sar.Status.Allowed, nil
}

// CurrentUsername returns the Kubernetes username of the current authenticated caller
// by issuing a SelfSubjectReview (k8s ≥ 1.26).
func CurrentUsername(ctx context.Context, cs *kubernetes.Clientset) (string, error) {
	review, err := cs.AuthenticationV1().SelfSubjectReviews().Create(ctx,
		&authenticationv1.SelfSubjectReview{},
		metav1.CreateOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("selfsubjectreview: %w", err)
	}
	return review.Status.UserInfo.Username, nil
}
