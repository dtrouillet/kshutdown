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
	"strings"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
)

func newDefineCmd() *cobra.Command {
	var namespaces []string
	var selectors []string

	cmd := &cobra.Command{
		Use:   "define <name>",
		Short: "Create a ShutdownGroup ad-hoc (for use during incidents)",
		Long: `Create a ShutdownGroup outside of Git for emergency use.
The resource is annotated with argocd.argoproj.io/sync-options: Prune=false
to prevent ArgoCD from deleting it during a sync.

--namespace and --selector flags are positional pairs:
  --namespace payments --selector app.kubernetes.io/part-of=payment \
  --namespace payments-cron --selector app.kubernetes.io/part-of=payment`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			ns := namespace
			if ns == "" {
				return fmt.Errorf("--namespace / -n is required (namespace for the ShutdownGroup itself)")
			}

			if len(namespaces) == 0 || len(selectors) == 0 {
				return fmt.Errorf("at least one --namespace/--selector pair is required")
			}
			if len(namespaces) != len(selectors) {
				return fmt.Errorf(
					"--namespace and --selector must appear the same number of times (got %d and %d)",
					len(namespaces), len(selectors),
				)
			}

			targets := make([]kshutdownv1alpha1.Target, 0, len(namespaces))
			for i := range namespaces {
				labels, err := parseLabelSelector(selectors[i])
				if err != nil {
					return fmt.Errorf("parsing selector %q: %w", selectors[i], err)
				}
				targets = append(targets, kshutdownv1alpha1.Target{
					Namespace:     namespaces[i],
					LabelSelector: labels,
				})
			}

			c, _, err := clientFactory()
			if err != nil {
				return fmt.Errorf("building client: %w", err)
			}

			sg := &kshutdownv1alpha1.ShutdownGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
					Annotations: map[string]string{
						kshutdownv1alpha1.AnnotationSyncOptions: kshutdownv1alpha1.SyncOptionPruneFalse,
					},
				},
				Spec: kshutdownv1alpha1.ShutdownGroupSpec{
					Targets: targets,
				},
			}

			if err := c.Create(cmd.Context(), sg); err != nil {
				return fmt.Errorf("creating shutdowngroup %s/%s: %w", ns, name, err)
			}

			fmt.Printf("ShutdownGroup %s/%s created.\n", ns, name)
			fmt.Printf("Annotated with %s=%s to prevent ArgoCD pruning.\n",
				kshutdownv1alpha1.AnnotationSyncOptions, kshutdownv1alpha1.SyncOptionPruneFalse)
			fmt.Printf("Export for GitOps when ready: kubectl kshutdown export %s -n %s\n", name, ns)
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&namespaces, "namespace", nil,
		"Namespace for a target (repeatable, paired with --selector)")
	cmd.Flags().StringArrayVar(&selectors, "selector", nil,
		"Label selector for a target, e.g. app=foo (repeatable, paired with --namespace)")
	return cmd
}

// parseLabelSelector parses a "key=value[,key=value]" string into a map.
func parseLabelSelector(s string) (map[string]string, error) {
	if s == "" {
		return nil, fmt.Errorf("selector must not be empty")
	}
	result := make(map[string]string)
	for pair := range strings.SplitSeq(s, ",") {
		pair = strings.TrimSpace(pair)
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, fmt.Errorf("invalid selector token %q: expected key=value", pair)
		}
		result[parts[0]] = parts[1]
	}
	return result, nil
}
