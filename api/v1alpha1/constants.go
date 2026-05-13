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

package v1alpha1

const (
	// AnnotationCommand is set by the CLI to trigger a reconcile branch.
	// Value: "down" or "up".
	AnnotationCommand = "kshutdown.io/command"

	// AnnotationReason carries the operator-supplied justification for a shutdown.
	// Set by the CLI alongside AnnotationCommand, consumed by the operator.
	AnnotationReason = "kshutdown.io/reason"

	// AnnotationOperator carries the Kubernetes username who triggered the operation.
	// Set by the CLI alongside AnnotationCommand, consumed by the operator.
	AnnotationOperator = "kshutdown.io/operator"

	CommandDown = "down"
	CommandUp   = "up"

	// AnnotationSkipReconcile is applied by the operator to target workloads
	// to prevent ArgoCD from reverting scale-to-zero during a sync.
	AnnotationSkipReconcile = "argocd.argoproj.io/skip-reconcile"

	// AnnotationSyncOptions is applied by `kubectl kshutdown define` on ad-hoc
	// ShutdownGroups to prevent ArgoCD from pruning them.
	AnnotationSyncOptions = "argocd.argoproj.io/sync-options"
	SyncOptionPruneFalse  = "Prune=false"

	// MaxHistoryEntries is the maximum number of history entries retained in status.
	MaxHistoryEntries = 20
)
