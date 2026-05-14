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
	"sigs.k8s.io/yaml"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
)

func newExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export <name>",
		Short: "Export a ShutdownGroup as clean YAML suitable for GitOps",
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

			// Strip runtime metadata — keep name, namespace, labels, annotations only.
			sg.ResourceVersion = ""
			sg.UID = ""
			sg.Generation = 0
			sg.CreationTimestamp.Reset()
			sg.ManagedFields = nil
			sg.Finalizers = nil

			// Strip status — it is operator-owned, not part of the GitOps definition.
			sg.Status = kshutdownv1alpha1.ShutdownGroupStatus{}

			out, err := yaml.Marshal(&sg)
			if err != nil {
				return fmt.Errorf("marshalling to YAML: %w", err)
			}

			_, err = fmt.Fprint(os.Stdout, string(out))
			return err
		},
	}
}
