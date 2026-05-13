---
name: argocd-gitops-compat
description: >
  Load this skill when working on anything related to ArgoCD compatibility:
  operator reconcileDown/reconcileUp sequences, GitOps safety during shutdown,
  the Prune=false annotation on ad-hoc ShutdownGroups, or when writing
  installation/prerequisite documentation for ArgoCD environments.
allowed-tools: Read, Write, Edit, Bash, Glob, Grep
---

# ArgoCD GitOps compatibility — kshutdown reference

## How kshutdown stays GitOps-safe

kshutdown does **not** interact with ArgoCD programmatically. There is no token,
no API call, no modification of AppProject or Application resources.

The GitOps safety relies entirely on a **one-time ArgoCD configuration** that the
cluster admin applies at installation time: telling ArgoCD to ignore `spec.replicas`
during both diff and sync. Once configured, kshutdown can freely scale workloads
to 0 and ArgoCD will not restore them.

---

## Required ArgoCD prerequisite — install-time configuration

This must be documented in the kshutdown Helm chart README and installation guide.
Without it, ArgoCD will restore replicas on the next sync and kshutdown will not work
in ArgoCD-managed clusters.

### 1. `ignoreDifferences` in `argocd-cm` (cluster hub)

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-cm
  namespace: argocd
data:
  resource.customizations.ignoreDifferences.apps_Deployment: |
    jsonPointers:
      - /spec/replicas
  resource.customizations.ignoreDifferences.apps_StatefulSet: |
    jsonPointers:
      - /spec/replicas
```

This tells ArgoCD not to consider a `replicas` divergence as OutOfSync.
Without this, the Application shows OutOfSync during every shutdown — confusing
but not blocking. With it, ArgoCD reports Synced even when workloads are at 0.

### 2. `RespectIgnoreDifferences=true` on each managed Application

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: payments
  namespace: argocd
spec:
  syncPolicy:
    syncOptions:
      - RespectIgnoreDifferences=true
```

**This is the critical option.** Without it, ArgoCD ignores the field in the
diff display but still overwrites `spec.replicas` when it performs a sync.
`RespectIgnoreDifferences=true` makes ArgoCD pre-patch the desired state before
applying — effectively skipping the `replicas` field during the apply itself.

### Optional — `ignoreDifferencesOnResourceUpdates`

```yaml
data:
  resource.compareoptions: |
    ignoreDifferencesOnResourceUpdates: true
```

**Not required for correctness.** This is a performance optimization: when kshutdown
patches a Deployment's replicas, ArgoCD normally triggers a full Application refresh.
With this option, ArgoCD skips the refresh if the only change is on an ignored field.
Recommended for clusters with many Applications to reduce application-controller CPU.

---

## What ArgoCD does during shutdown (with prerequisites applied)

```
kshutdown scales Deployment payments-api to replicas: 0

ArgoCD detects live state changed
  → diff: replicas differs (0 vs 3 in Git)
  → ignoreDifferences: /spec/replicas → difference ignored
  → Application status: Synced ✓

ArgoCD auto-sync or manual sync triggered
  → RespectIgnoreDifferences=true → replicas field pre-patched out of desired state
  → apply: replicas NOT written to cluster
  → Deployment stays at 0 ✓
```

---

## What ArgoCD does during shutdown (without prerequisites)

```
kshutdown scales Deployment payments-api to replicas: 0

ArgoCD detects live state changed
  → diff: replicas differs (0 vs 3 in Git)
  → Application status: OutOfSync ✗

ArgoCD auto-sync triggered (selfHeal or next poll)
  → apply: spec.replicas = 3 written to cluster
  → Deployment restored to 3 replicas ✗ — kshutdown defeated
```

---

## CronJob — suspend field

CronJobs don't have `spec.replicas`. kshutdown suspends them via `spec.suspend: true`.
The ArgoCD prerequisite must also cover this field:

```yaml
resource.customizations.ignoreDifferences.batch_CronJob: |
  jsonPointers:
    - /spec/suspend
```

And the Application must have `RespectIgnoreDifferences=true` — same as Deployments.

---

## Prune=false on ad-hoc ShutdownGroups

For ShutdownGroups created outside Git via `kubectl kshutdown define` during an incident:

```go
// cmd/kubectl-kshutdown/define.go

func buildAdHocShutdownGroup(name, namespace string, targets []Target) *kshutdownv1alpha1.ShutdownGroup {
    return &kshutdownv1alpha1.ShutdownGroup{
        ObjectMeta: metav1.ObjectMeta{
            Name:      name,
            Namespace: namespace,
            Annotations: map[string]string{
                // Prevents ArgoCD from pruning this ShutdownGroup on next sync
                // since it does not exist in Git
                "argocd.argoproj.io/sync-options": "Prune=false",
            },
        },
        Spec: kshutdownv1alpha1.ShutdownGroupSpec{
            Targets: targets,
        },
    }
}
```

**Why this matters:** Without `Prune=false`, ArgoCD deletes the ad-hoc ShutdownGroup
on the next sync (it's not in Git). The workloads remain scaled to 0 with no
ShutdownGroup left to trigger `reconcileUp` — orphaned frozen state.

---

## Operator code — no ArgoCD interaction needed

The operator does not call the ArgoCD API. It only patches Kubernetes resources
on the local (application) cluster. The reconcileDown and reconcileUp sequences
are purely Kubernetes operations:

```
reconcileDown:
  1. Capture snapshot → write to status.snapshot
  2. For each target: scale to 0 (Deployment/StatefulSet) or suspend (CronJob)
  3. Consume command annotation
  4. Update status.state = down

reconcileUp:
  1. For each snapshot entry: restore replicas / unsuspend
  2. Clear status.snapshot
  3. Consume command annotation
  4. Update status.state = up
```

ArgoCD sees the scaling operations, ignores the `replicas` field (prerequisite),
and does not interfere. No annotation on the workloads, no API call to the hub.

---

## Constants

```go
const (
    // Applied to ad-hoc ShutdownGroups by the CLI (define command only)
    // Prevents ArgoCD from pruning ShutdownGroups not present in Git
    AnnotationSyncOptions = "argocd.argoproj.io/sync-options"
    SyncOptionPruneFalse  = "Prune=false"
)
```

---

## Installation checklist — ArgoCD environments

Include this in the kshutdown Helm chart README:

```markdown
### ArgoCD prerequisite (required)

If your cluster uses ArgoCD, add the following to `argocd-cm` on the hub cluster:

​```yaml
resource.customizations.ignoreDifferences.apps_Deployment: |
  jsonPointers:
    - /spec/replicas
resource.customizations.ignoreDifferences.apps_StatefulSet: |
  jsonPointers:
    - /spec/replicas
resource.customizations.ignoreDifferences.batch_CronJob: |
  jsonPointers:
    - /spec/suspend
​```

And add `RespectIgnoreDifferences=true` to the syncOptions of every Application
whose workloads are managed by a ShutdownGroup:

​```yaml
spec:
  syncPolicy:
    syncOptions:
      - RespectIgnoreDifferences=true
​```

Without these two settings, ArgoCD will restore replicas on the next sync.
```

---

## What never to do

| Action | Why |
|---|---|
| Deploy kshutdown in an ArgoCD cluster without the prerequisites | ArgoCD restores replicas on next sync — silent failure |
| Add `RespectIgnoreDifferences=true` without `ignoreDifferences` | No effect — the option only applies to fields already listed in ignoreDifferences |
| Forget `Prune=false` on ad-hoc ShutdownGroups | ArgoCD deletes the ShutdownGroup → workloads frozen forever at 0 replicas |
| Configure `ignoreDifferences` globally without `RespectIgnoreDifferences` | ArgoCD shows Synced in the UI but still overwrites replicas on sync |
