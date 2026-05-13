# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

**kshutdown** is a Kubernetes operator + kubectl plugin that lets operators shut down and restart a functional slice — a set of workloads spread across multiple namespaces and ArgoCD Applications — quickly, safely, and reversibly, without breaking the GitOps model.

The core mechanism is the native ArgoCD annotation `argocd.argoproj.io/skip-reconcile: "true"`, which prevents ArgoCD from reconciling a specific resource even during a sync triggered by a Git commit.

## Tech stack

| Component | Technology |
|-----------|-----------|
| Operator | Go + Kubebuilder |
| CLI | Go + Cobra (kubectl plugin) |
| CRDs | controller-gen (generated from Go types) |
| Kubernetes client | client-go (typed + dynamic) |
| Authorization | SelfSubjectAccessReview (native Kubernetes API) |
| Deployment | Helm chart, one operator per application cluster |
| CLI distribution | Go binary, installable via krew or curl |

## Planned project structure

```
kshutdown/
├── api/v1alpha1/
│   └── shutdowngroup_types.go       # CRD Go type definitions
├── cmd/
│   ├── operator/                    # Operator entry point
│   └── kubectl-kshutdown/           # CLI plugin entry point
│       ├── down.go / up.go / status.go / list.go / define.go / export.go
├── internal/
│   ├── controller/
│   │   └── shutdowngroup_controller.go  # reconcileDown() / reconcileUp()
│   └── authz/
│       └── selfsar.go               # SelfSubjectAccessReview helpers
└── config/
    ├── crd/                         # Generated CRD manifests
    ├── rbac/                        # Role shutdowngroup-actioner
    └── manager/                     # Operator Deployment manifest
```

## Architecture

### Components

**kshutdown-operator**: A Kubebuilder operator deployed in `kshutdown-system` on each application cluster. Has cluster-wide ServiceAccount limited to patching annotations and scaling workloads. Watches `ShutdownGroup` resources and reacts to command annotations. Never called directly by users.

**kubectl plugin (kshutdown)**: Uses the user's Rancher kubeconfig — no extra credentials. All requests go through the Rancher proxy (which injects user identity). Never talks directly to application namespaces.

**ShutdownGroup (CRD)**: Lives in the application namespace (e.g., `payments`). Describes the functional scope: which namespaces and which workloads. Deployed via GitOps like any resource.

### Execution flow

**Shutdown (`kubectl kshutdown down <name> --reason "..."`):**
1. CLI emits a `SelfSubjectAccessReview` for each target resource to verify user RBAC
2. If all resources are authorized, CLI adds a command annotation on the `ShutdownGroup`
3. Operator detects the annotation, saves replicas in `status.snapshot`, adds `skip-reconcile` on each resource, and scales to 0
4. Command annotation is consumed (removed) after execution — idempotent

**Startup (`kubectl kshutdown up <name>`):**
1. Same `SelfSubjectAccessReview` checks
2. Operator restores replicas from `status.snapshot`
3. Operator removes `skip-reconcile` annotation
4. ArgoCD resumes control naturally at the next sync cycle

### Authorization model

kshutdown has no own permission system. Authorization relies entirely on native Kubernetes RBAC as configured by Rancher: *if a user can scale a Deployment in a namespace, they can shut it down via kshutdown.* The `shutdowngroup-actioner` Role grants `get/list/patch` on `shutdowngroups` resources in the application namespace.

## Key design decisions

- **`skip-reconcile` over other approaches**: Suspending ArgoCD sync or modifying Git is too coarse or too slow. `skip-reconcile` is per-resource, native to ArgoCD, reversible, and survives Git commits.
- **Operator never called directly**: Users interact only with `ShutdownGroup` resources via annotations; the operator handles reconciliation.
- **`--partial` flag**: If a user lacks rights on some targets, the CLI refuses by default and lists forbidden resources. `--partial` acts only on authorized resources with an explicit warning.
- **Emergency `define`**: In an incident, a `ShutdownGroup` can be created ad-hoc outside Git. It must be annotated with `argocd.argoproj.io/sync-options: Prune=false` to avoid ArgoCD pruning it.

## ShutdownGroup CRD example

```yaml
apiVersion: kshutdown.io/v1alpha1
kind: ShutdownGroup
metadata:
  name: payment-stack
  namespace: payments
spec:
  targets:
    - labelSelector:
        app.kubernetes.io/part-of: payment
    - namespace: payments-cron
      labelSelector:
        app.kubernetes.io/part-of: payment
status:
  state: down
  since: "2026-05-12T14:32:07Z"
  operator: user-5xnr5
  reason: "incident P1 #4521"
  snapshot:
    - namespace: payments
      name: payments-api
      kind: Deployment
      previousReplicas: 3
```

## CLI commands

| Command | Description |
|---------|-------------|
| `kubectl kshutdown down <name> --reason "..."` | Shut down a functional slice (reason required) |
| `kubectl kshutdown up <name>` | Restart a functional slice |
| `kubectl kshutdown status <name>` | Detailed state (state, since, operator, reason, resources) |
| `kubectl kshutdown list` | List all accessible ShutdownGroups |
| `kubectl kshutdown history <name>` | Operation history |
| `kubectl kshutdown define <name>` | Create a ShutdownGroup ad-hoc in an incident |
| `kubectl kshutdown export <name>` | Export a ShutdownGroup as YAML for Git |

Common flags: `--reason`, `--namespace / -n`, `--partial`, `--dry-run`

## Scope (v0.1)

In scope: Deployment, StatefulSet, CronJob. Out of scope: web UI, Slack/PagerDuty notifications, custom non-scalable resources, HPA management (v0.2), multi-cluster central control.

## Claude Code workspace

### Skills — loaded automatically when relevant

| Skill | Triggers on |
|---|---|
| `kubebuilder-patterns` | controller, reconciler, CRD types, RBAC markers, envtest, Kubebuilder v4 |
| `kshutdown-crd` | ShutdownGroup types, status fields, snapshot logic, state machine, CLI↔CRD interaction |
| `argocd-gitops-compat` | ArgoCD sync, ignoreDifferences, RespectIgnoreDifferences, Prune=false, install doc |

### Commands — invoke manually

| Command | Usage |
|---|---|
| `/plan-feature <description>` | Plan a new feature end-to-end before writing code |
| `/new-reconciler <name>` | Scaffold a new reconcile branch with test |
| `/ship` | Pre-commit checklist: generated files, RBAC, tests, forbidden patterns, ArgoCD doc, Helm |

### Agent — invoke for code review

| Agent | Usage |
|---|---|
| `kshutdown-reviewer` | `@agent-kshutdown-reviewer` — reviews controller, CLI, CRD, RBAC against project invariants |

### Workflow

```
1. /plan-feature <what you want to build>
2. Implement task by task
3. @agent-kshutdown-reviewer — review each reconciler branch
4. /ship — final checklist before commit
```

### Key invariants — never violate

- `status.snapshot` is written BEFORE scaling, never overwritten if non-empty
- Command annotation is consumed AFTER successful execution
- Status updates use `Status().Patch()` + `MergeFrom(DeepCopy())` — never `Update()`
- Every error is wrapped: `fmt.Errorf("context: %w", err)`
- The operator never calls the ArgoCD API — GitOps safety is via `ignoreDifferences` prerequisite
- ArgoCD prerequisite: `ignoreDifferences` on `spec.replicas` + `RespectIgnoreDifferences=true`
