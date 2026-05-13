---
name: kshutdown-crd
description: >
  Load this skill when working on ShutdownGroup types, status fields, snapshot
  logic, state machine, CRD validation markers, or any code that reads/writes
  ShutdownGroup spec or status. Also load when writing CLI commands that
  interact with ShutdownGroup resources (down, up, status, list, define, export).
allowed-tools: Read, Write, Edit, Bash, Glob, Grep
---

# kshutdown CRD — ShutdownGroup reference

## Go type definitions

```go
// api/v1alpha1/shutdowngroup_types.go

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Namespaced,shortName=sg
//+kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
//+kubebuilder:printcolumn:name="Since",type=date,JSONPath=`.status.since`
//+kubebuilder:printcolumn:name="Operator",type=string,JSONPath=`.status.operator`
//+kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.reason`

type ShutdownGroup struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   ShutdownGroupSpec   `json:"spec,omitempty"`
    Status ShutdownGroupStatus `json:"status,omitempty"`
}

type ShutdownGroupSpec struct {
    // Targets defines the set of workloads to manage.
    // Each target selects resources by labelSelector, optionally scoped to a namespace.
    // If namespace is omitted, the ShutdownGroup's own namespace is used.
    //+kubebuilder:validation:MinItems=1
    Targets []Target `json:"targets"`
}

type Target struct {
    // Namespace is the namespace to search in. Defaults to ShutdownGroup namespace.
    //+optional
    Namespace string `json:"namespace,omitempty"`

    // LabelSelector selects workloads within the namespace.
    //+kubebuilder:validation:Required
    LabelSelector map[string]string `json:"labelSelector"`
}

type ShutdownGroupStatus struct {
    // State is the current lifecycle state of the ShutdownGroup.
    //+kubebuilder:validation:Enum=up;down;unknown
    State ShutdownGroupState `json:"state,omitempty"`

    // Since is the timestamp of the last state transition.
    //+optional
    Since *metav1.Time `json:"since,omitempty"`

    // Operator is the Kubernetes username who triggered the last operation.
    //+optional
    Operator string `json:"operator,omitempty"`

    // Reason is the human-readable justification provided at shutdown time.
    //+optional
    Reason string `json:"reason,omitempty"`

    // Snapshot holds the replica counts captured before scale-down.
    // Written once during reconcileDown. Cleared during reconcileUp.
    // Must never be overwritten if non-empty (guard against double-down).
    //+optional
    Snapshot []ResourceSnapshot `json:"snapshot,omitempty"`

    // History records the last N operations for audit purposes.
    //+optional
    History []HistoryEntry `json:"history,omitempty"`
}

// ShutdownGroupState represents the lifecycle state.
//+kubebuilder:validation:Enum=up;down;unknown
type ShutdownGroupState string

const (
    StateUp      ShutdownGroupState = "up"
    StateDown    ShutdownGroupState = "down"
    StateUnknown ShutdownGroupState = "unknown"
)

type ResourceSnapshot struct {
    // Namespace of the captured resource.
    Namespace string `json:"namespace"`
    // Name of the captured resource.
    Name string `json:"name"`
    // Kind is Deployment, StatefulSet, or CronJob.
    //+kubebuilder:validation:Enum=Deployment;StatefulSet;CronJob
    Kind string `json:"kind"`
    // PreviousReplicas is the replica count before scale-down.
    // For CronJob, this stores the suspend state (0=active, 1=suspended).
    //+kubebuilder:validation:Minimum=0
    PreviousReplicas int32 `json:"previousReplicas"`
}

type HistoryEntry struct {
    // Operation is "down" or "up".
    //+kubebuilder:validation:Enum=down;up
    Operation string `json:"operation"`
    // At is the timestamp of the operation.
    At metav1.Time `json:"at"`
    // Operator is the Kubernetes username.
    Operator string `json:"operator"`
    // Reason is the justification (only present for "down" operations).
    //+optional
    Reason string `json:"reason,omitempty"`
}
```

---

## State machine

```
        ┌─────────────────────────────────────┐
        │                                     │
   [created]                                  │
        │                                     │
        ▼                                     │
     unknown  ──── annotation: down ────▶   down
                                              │
       up     ◀─── annotation: up  ──────────┘
        │
        └──── annotation: down ────▶   down
```

**Transition rules:**

| From | Annotation | Action | To |
|---|---|---|---|
| `unknown` | `down` | Save snapshot, scale to 0 | `down` |
| `up` | `down` | Save snapshot, scale to 0 | `down` |
| `down` | `down` | **No-op** — already down, consume annotation | `down` |
| `down` | `up` | Restore replicas from snapshot, clear snapshot | `up` |
| `up` | `up` | **No-op** — already up, consume annotation | `up` |
| `unknown` | `up` | **Error** — no snapshot to restore from, reject with event | `unknown` |

---

## Annotations

### Command annotations (CLI → operator)

| Annotation | Value | Set by | Consumed by |
|---|---|---|---|
| `kshutdown.io/command` | `down` or `up` | CLI | Operator (deleted after execution) |

### Emergency / GitOps safety annotation

```yaml
# Applied by `kubectl kshutdown define` on ad-hoc ShutdownGroups
argocd.argoproj.io/sync-options: Prune=false
```
Prevents ArgoCD from deleting ShutdownGroups created outside of Git during an incident.

---

## Snapshot rules — critical invariants

1. **Write-once**: `reconcileDown` must check `len(status.snapshot) > 0` before writing. If non-empty, skip capture and treat as no-op (idempotent double-down).
2. **Capture before scale**: Always write the snapshot to status *before* scaling to 0. If the operator crashes between scale and status update, the next reconcile will re-enter `reconcileDown` and the write-once guard prevents data loss.
3. **Clear on up**: `reconcileUp` clears the snapshot only *after* all replicas are restored successfully.
4. **CronJob special case**: CronJobs don't have replicas. Store `previousReplicas: 0` for active, `previousReplicas: 1` for already-suspended. On restore, set `suspend: false` only if `previousReplicas == 0`.

---

## Supported workload kinds (v0.1 scope)

| Kind | Scale-down mechanism | Scale-up mechanism |
|---|---|---|
| `Deployment` | Patch `spec.replicas = 0` | Patch `spec.replicas = snapshot.previousReplicas` |
| `StatefulSet` | Patch `spec.replicas = 0` | Patch `spec.replicas = snapshot.previousReplicas` |
| `CronJob` | Patch `spec.suspend = true` | Patch `spec.suspend = false` (if was active) |

Out of scope for v0.1: HPA, custom resources, DaemonSet, Job.

---

## ShutdownGroup YAML example

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
    - namespace: payments
      name: payments-worker
      kind: Deployment
      previousReplicas: 2
    - namespace: payments-cron
      name: invoice-generator
      kind: CronJob
      previousReplicas: 0
```

---

## CLI ↔ CRD interaction

| CLI command | CRD action |
|---|---|
| `kubectl kshutdown down <name> --reason "..."` | Adds `kshutdown.io/command: down` annotation + sets `status.reason` via patch before annotating |
| `kubectl kshutdown up <name>` | Adds `kshutdown.io/command: up` annotation |
| `kubectl kshutdown status <name>` | Reads `status.*` fields — no mutation |
| `kubectl kshutdown list` | Lists all ShutdownGroups in accessible namespaces — no mutation |
| `kubectl kshutdown history <name>` | Reads `status.history` — no mutation |
| `kubectl kshutdown define <name>` | Creates ShutdownGroup with `argocd.argoproj.io/sync-options: Prune=false` |
| `kubectl kshutdown export <name>` | Reads spec, strips status and runtime metadata, outputs clean YAML |

---

## SelfSubjectAccessReview — authorization check

Before adding any command annotation, the CLI emits one `SelfSubjectAccessReview` per target resource:

```go
sar := &authorizationv1.SelfSubjectAccessReview{
    Spec: authorizationv1.SelfSubjectAccessReviewSpec{
        ResourceAttributes: &authorizationv1.ResourceAttributes{
            Namespace: target.Namespace,
            Verb:      "patch",
            Group:     "apps",
            Resource:  "deployments",
            Name:      deploy.Name,
        },
    },
}
if err := client.Create(ctx, sar); err != nil { ... }
if !sar.Status.Allowed {
    forbidden = append(forbidden, target)
}
```

**Rules:**
- Default behavior: if any target is forbidden, abort and list all forbidden resources.
- `--partial` flag: proceed with authorized targets only, emit a warning listing skipped resources.
- The operator never performs RBAC checks — it trusts the CLI to have validated.

---

## Constants

```go
const (
    // Annotations managed by the CLI
    AnnotationCommand = "kshutdown.io/command"
    CommandDown       = "down"
    CommandUp         = "up"

    AnnotationSyncOptions   = "argocd.argoproj.io/sync-options"
    SyncOptionPrunefalse    = "Prune=false"

    // API group
    GroupVersion = "kshutdown.io/v1alpha1"
)
```
