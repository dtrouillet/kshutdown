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
	"text/tabwriter"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
)

func newHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history <name>",
		Short: "Show the operation history of a ShutdownGroup",
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

			if len(sg.Status.History) == 0 {
				fmt.Fprintln(os.Stdout, "No history recorded.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "OPERATION\tAT\tOPERATOR\tREASON")
			for _, h := range sg.Status.History {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					h.Operation,
					h.At.Format("2006-01-02T15:04:05Z"),
					h.Operator,
					orDash(h.Reason),
				)
			}
			return w.Flush()
		},
	}
}
