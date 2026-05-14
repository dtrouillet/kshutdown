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

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <name>",
		Short: "Show the detailed status of a ShutdownGroup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			c, _, err := clientFactory()
			if err != nil {
				return fmt.Errorf("building client: %w", err)
			}

			ns := namespace
			if ns == "" {
				return fmt.Errorf("--namespace / -n is required")
			}

			var sg kshutdownv1alpha1.ShutdownGroup
			if err := c.Get(cmd.Context(), types.NamespacedName{Name: name, Namespace: ns}, &sg); err != nil {
				return fmt.Errorf("getting shutdowngroup %s/%s: %w", ns, name, err)
			}

			state := string(sg.Status.State)
			if state == "" {
				state = "unknown"
			}

			fmt.Printf("Name:       %s\n", sg.Name)
			fmt.Printf("Namespace:  %s\n", sg.Namespace)
			fmt.Printf("State:      %s\n", state)

			if sg.Status.Since != nil {
				fmt.Printf("Since:      %s (%s ago)\n",
					sg.Status.Since.Format("2006-01-02T15:04:05Z"),
					formatAge(sg.Status.Since.Time))
			} else {
				fmt.Printf("Since:      -\n")
			}

			fmt.Printf("Operator:   %s\n", orDash(sg.Status.Operator))
			fmt.Printf("Reason:     %s\n", orDash(sg.Status.Reason))

			fmt.Printf("\nTargets (%d):\n", len(sg.Spec.Targets))
			for _, t := range sg.Spec.Targets {
				tns := t.Namespace
				if tns == "" {
					tns = sg.Namespace
				}
				fmt.Printf("  namespace=%s selector=%v\n", tns, t.LabelSelector)
			}

			if len(sg.Status.Snapshot) > 0 {
				fmt.Printf("\nSnapshot (%d resources):\n", len(sg.Status.Snapshot))
				for _, s := range sg.Status.Snapshot {
					fmt.Printf("  %-12s %s/%s  previousReplicas=%d\n",
						s.Kind, s.Namespace, s.Name, s.PreviousReplicas)
				}
			}

			return nil
		},
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
