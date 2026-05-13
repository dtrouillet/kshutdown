---
name: plan-feature
description: >
  Plan a new kshutdown feature end-to-end: from a natural language description,
  produce a structured task breakdown covering API types, controller logic,
  CLI commands, tests, and documentation. Invoke when the user says
  "plan", "I want to add", "how do I implement", or describes a new capability.
argument-hint: "[feature description]"
user-invocable: true
allowed-tools: Read, Glob, Grep
---

# /plan-feature — kshutdown feature planning

You are planning a new feature for the kshutdown operator. Read the project
structure and existing code before producing the plan.

## Step 1 — Understand the request

Restate the feature in one sentence. Identify which components are affected:
- `api/v1alpha1/` — new or modified CRD types
- `internal/controller/` — reconciler changes
- `internal/authz/` — RBAC checks
- `cmd/kubectl-kshutdown/` — CLI commands
- `config/` — RBAC manifests, CRD updates
- Helm chart — values, templates
- Documentation

## Step 2 — Scan existing code

Use Read and Grep to check:
- Current `ShutdownGroup` spec and status fields
- Existing reconciler branches (`reconcileDown`, `reconcileUp`, `reconcileIdle`)
- Existing CLI commands and flags
- Existing RBAC markers

## Step 3 — Produce the plan

Output a structured plan with the following sections.

### Summary
One paragraph describing what the feature does and why.

### Design decisions
Bullet list of key choices made and alternatives considered.
Flag anything that touches the ArgoCD compatibility prerequisites.

### API changes (if any)
New or modified fields in `ShutdownGroup` spec or status.
Include the Go struct snippet and kubebuilder markers.

### Controller changes
Which reconcile branch is affected. New functions needed.
Idempotence and ordering considerations.

### CLI changes
New commands or flags. SelfSubjectAccessReview checks needed.

### RBAC changes
New verbs or resources needed. Marker snippets.

### Test plan
- Unit tests: which functions, which edge cases
- envtest integration tests: which scenarios, which assertions
- Manual verification steps

### Documentation
- README sections to update
- ArgoCD prerequisite checklist impact (if replicas/suspend handling changes)
- `kubectl kshutdown --help` output impact

### Task list
Ordered, atomic tasks ready to execute one by one.
Format: `[ ] TASK-N — <verb> <what> in <file>`

Example:
```
[ ] TASK-1 — Add `timeout` field to ShutdownGroupSpec in api/v1alpha1/shutdowngroup_types.go
[ ] TASK-2 — Add kubebuilder validation marker (+kubebuilder:validation:Minimum=0) on timeout field
[ ] TASK-3 — Run make generate manifests to regenerate CRD
[ ] TASK-4 — Add timeout handling in reconcileDown in internal/controller/shutdowngroup_controller.go
[ ] TASK-5 — Add --timeout flag to cmd/kubectl-kshutdown/down.go
[ ] TASK-6 — Add envtest case: shutdown with timeout expires → status reflects timeout
[ ] TASK-7 — Update README installation section with timeout field documentation
```

## Rules

- Every task must be atomic and independently committable.
- Tasks that generate code (`make generate`, `make manifests`) are explicit tasks.
- Never plan changes to ArgoCD hub resources — kshutdown does not touch the hub.
- If the feature affects `spec.replicas` or `spec.suspend` handling, flag the
  ArgoCD `ignoreDifferences` prerequisite in the Documentation section.
- If unsure about a design decision, present two options with trade-offs
  rather than picking one silently.
