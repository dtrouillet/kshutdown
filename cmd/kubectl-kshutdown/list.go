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
	"time"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List ShutdownGroups in a namespace (all namespaces if -n is omitted)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := clientFactory()
			if err != nil {
				return fmt.Errorf("building client: %w", err)
			}

			var list kshutdownv1alpha1.ShutdownGroupList
			listOpts := []client.ListOption{}
			if namespace != "" {
				listOpts = append(listOpts, client.InNamespace(namespace))
			}
			if err := c.List(cmd.Context(), &list, listOpts...); err != nil {
				return fmt.Errorf("listing shutdowngroups: %w", err)
			}

			if len(list.Items) == 0 {
				fmt.Println("No ShutdownGroups found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "NAME\tNAMESPACE\tSTATE\tSINCE\tOPERATOR"); err != nil {
				return err
			}
			for _, sg := range list.Items {
				since := "-"
				if sg.Status.Since != nil {
					since = formatAge(sg.Status.Since.Time)
				}
				state := string(sg.Status.State)
				if state == "" {
					state = "unknown"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					sg.Name, sg.Namespace, state, since, sg.Status.Operator)
			}
			return w.Flush()
		},
	}
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
