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

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
	"github.com/dtrouillet/kshutdown/internal/authz"
)

func newDownCmd() *cobra.Command {
	var reason string
	var partial bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "down <name>",
		Short: "Shut down a functional slice managed by a ShutdownGroup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			if reason == "" {
				return fmt.Errorf("--reason is required")
			}
			ns := namespace
			if ns == "" {
				return fmt.Errorf("--namespace / -n is required")
			}

			c, cs, err := clientFactory()
			if err != nil {
				return fmt.Errorf("building client: %w", err)
			}

			var sg kshutdownv1alpha1.ShutdownGroup
			if err := c.Get(cmd.Context(), types.NamespacedName{Name: name, Namespace: ns}, &sg); err != nil {
				return fmt.Errorf("getting shutdowngroup %s/%s: %w", ns, name, err)
			}

			if sg.Status.State == kshutdownv1alpha1.StateDown {
				fmt.Printf("ShutdownGroup %s/%s is already down.\n", ns, name)
				return nil
			}

			forbidden, err := checkDownPermissions(cmd.Context(), c, cs, &sg)
			if err != nil {
				return err
			}

			if len(forbidden) > 0 {
				fmt.Fprintf(os.Stderr, "Permission denied on %d resource(s):\n", len(forbidden))
				for _, f := range forbidden {
					fmt.Fprintf(os.Stderr, "  %s\n", f)
				}
				if !partial {
					return fmt.Errorf("aborting: insufficient permissions (use --partial to proceed with authorized resources only)")
				}
				fmt.Fprintf(os.Stderr, "Warning: proceeding with authorized resources only (--partial)\n")
			}

			if dryRun {
				fmt.Printf("[dry-run] Would shut down ShutdownGroup %s/%s (reason: %s)\n", ns, name, reason)
				return nil
			}

			username, err := authz.CurrentUsername(cmd.Context(), cs)
			if err != nil {
				return fmt.Errorf("resolving current username: %w", err)
			}

			patch := client.MergeFrom(sg.DeepCopy())
			if sg.Annotations == nil {
				sg.Annotations = make(map[string]string)
			}
			sg.Annotations[kshutdownv1alpha1.AnnotationCommand] = kshutdownv1alpha1.CommandDown
			sg.Annotations[kshutdownv1alpha1.AnnotationReason] = reason
			sg.Annotations[kshutdownv1alpha1.AnnotationOperator] = username

			if err := c.Patch(cmd.Context(), &sg, patch); err != nil {
				return fmt.Errorf("patching shutdowngroup %s/%s: %w", ns, name, err)
			}

			fmt.Printf("Shutdown triggered for %s/%s (operator: %s, reason: %s)\n", ns, name, username, reason)
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Justification for the shutdown (required)")
	cmd.Flags().BoolVar(&partial, "partial", false, "Proceed with authorized resources only if some are forbidden")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate the action without applying it")
	return cmd
}

// checkDownPermissions checks patch permission for each workload targeted by the ShutdownGroup.
// Returns a list of denied resource identifiers.
func checkDownPermissions(
	ctx context.Context,
	c client.Client,
	cs *kubernetes.Clientset,
	sg *kshutdownv1alpha1.ShutdownGroup,
) ([]string, error) {
	var forbidden []string

	for _, target := range sg.Spec.Targets {
		tns := target.Namespace
		if tns == "" {
			tns = sg.Namespace
		}
		labels := client.MatchingLabels(target.LabelSelector)

		var deployments appsv1.DeploymentList
		if err := c.List(ctx, &deployments, client.InNamespace(tns), labels); err != nil {
			return nil, fmt.Errorf("listing deployments in %s: %w", tns, err)
		}
		for _, d := range deployments.Items {
			allowed, err := authz.CheckPatch(ctx, cs, tns, "apps", "deployments", d.Name)
			if err != nil {
				return nil, err
			}
			if !allowed {
				forbidden = append(forbidden, fmt.Sprintf("Deployment %s/%s", tns, d.Name))
			}
		}

		var statefulsets appsv1.StatefulSetList
		if err := c.List(ctx, &statefulsets, client.InNamespace(tns), labels); err != nil {
			return nil, fmt.Errorf("listing statefulsets in %s: %w", tns, err)
		}
		for _, s := range statefulsets.Items {
			allowed, err := authz.CheckPatch(ctx, cs, tns, "apps", "statefulsets", s.Name)
			if err != nil {
				return nil, err
			}
			if !allowed {
				forbidden = append(forbidden, fmt.Sprintf("StatefulSet %s/%s", tns, s.Name))
			}
		}

		var cronjobs batchv1.CronJobList
		if err := c.List(ctx, &cronjobs, client.InNamespace(tns), labels); err != nil {
			return nil, fmt.Errorf("listing cronjobs in %s: %w", tns, err)
		}
		for _, cj := range cronjobs.Items {
			allowed, err := authz.CheckPatch(ctx, cs, tns, "batch", "cronjobs", cj.Name)
			if err != nil {
				return nil, err
			}
			if !allowed {
				forbidden = append(forbidden, fmt.Sprintf("CronJob %s/%s", tns, cj.Name))
			}
		}
	}

	return forbidden, nil
}
