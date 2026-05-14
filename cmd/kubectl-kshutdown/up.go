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
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
	"github.com/dtrouillet/kshutdown/internal/authz"
)

func newUpCmd() *cobra.Command {
	var partial bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "up <name>",
		Short: "Restart a functional slice managed by a ShutdownGroup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

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

			if sg.Status.State == kshutdownv1alpha1.StateUp {
				fmt.Fprintf(os.Stdout, "ShutdownGroup %s/%s is already up.\n", ns, name)
				return nil
			}

			if len(sg.Status.Snapshot) == 0 {
				return fmt.Errorf("ShutdownGroup %s/%s has no snapshot — cannot restore (state: %s)", ns, name, sg.Status.State)
			}

			// Check patch permission for each resource in the snapshot.
			var forbidden []string
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
				allowed, err := authz.CheckPatch(cmd.Context(), cs, snap.Namespace, group, resource, snap.Name)
				if err != nil {
					return err
				}
				if !allowed {
					forbidden = append(forbidden, fmt.Sprintf("%s %s/%s", snap.Kind, snap.Namespace, snap.Name))
				}
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
				fmt.Fprintf(os.Stdout, "[dry-run] Would restart ShutdownGroup %s/%s\n", ns, name)
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
			sg.Annotations[kshutdownv1alpha1.AnnotationCommand] = kshutdownv1alpha1.CommandUp
			sg.Annotations[kshutdownv1alpha1.AnnotationOperator] = username

			if err := c.Patch(cmd.Context(), &sg, patch); err != nil {
				return fmt.Errorf("patching shutdowngroup %s/%s: %w", ns, name, err)
			}

			fmt.Fprintf(os.Stdout, "Restart triggered for %s/%s (operator: %s)\n", ns, name, username)
			return nil
		},
	}

	cmd.Flags().BoolVar(&partial, "partial", false, "Proceed with authorized resources only if some are forbidden")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate the action without applying it")
	return cmd
}
