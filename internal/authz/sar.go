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

	authorizationv1 "k8s.io/api/authorization/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CheckFor performs a SubjectAccessReview on behalf of the given user (not the caller).
// groups may be nil. The verb is typically "patch" for kshutdown targets.
// Uses the manager client so the operator ServiceAccount performs the SAR call.
func CheckFor(
	ctx context.Context,
	c client.Client,
	user string,
	groups []string,
	namespace, group, resource, name, verb string,
) (bool, error) {
	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   user,
			Groups: groups,
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Group:     group,
				Resource:  resource,
				Name:      name,
			},
		},
	}
	if err := c.Create(ctx, sar); err != nil {
		return false, fmt.Errorf("subjectaccessreview %s %s/%s (%s): %w", verb, namespace, name, resource, err)
	}
	return sar.Status.Allowed, nil
}
