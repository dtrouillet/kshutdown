---
name: kubebuilder-patterns
description: >
  Load this skill when working on Go code for the kshutdown operator:
  controllers, reconcilers, CRD types, RBAC markers, status subresources,
  finalizers, envtest, or anything touching controller-runtime / client-go.
  Also load for any kubebuilder scaffold, scheme registration, or webhook work.
allowed-tools: Read, Write, Edit, Bash, Glob, Grep
---

# Kubebuilder Patterns — kshutdown operator

## Stack

- **Kubebuilder v4** scaffold (plugin `go/v4`, controller-runtime v0.23+, controller-gen v0.17+)
- **controller-runtime**: Manager, Reconciler, Client, EventHandler
- **controller-tools / controller-gen**: CRD + RBAC generation from markers
- **client-go**: typed client via controller-runtime, dynamic client for cross-GVK targets
- **envtest**: integration tests with a real API server
- **Kustomize v5** (breaking change from v3 projects — use `go get` not bash script)

### v4 vs v3 — breaking changes to be aware of

| Topic | v3 | v4 |
|---|---|---|
| Metrics protection | `kube-rbac-proxy` sidecar (gcr.io — **dead since March 2025**) | `WithAuthenticationAndAuthorization` in controller-runtime (built-in) |
| Kustomize | v4, installed via bash script | v5, installed via `go get` |
| Plugin identifier | `go/v3` | `go/v4` in PROJECT file |
| Scaffold init | `kubebuilder init --plugins=go/v3` | `kubebuilder init --plugins=go/v4` (default) |

**Never use `gcr.io/kubebuilder/kube-rbac-proxy`** — the image is unavailable. Always use the v4 metrics setup with `WithAuthenticationAndAuthorization`.

See also the `kshutdown-crd` skill for ShutdownGroup-specific types and states.

---

## Reconciler structure

Every reconciler in this project follows this skeleton — deviate only with a comment explaining why.

```go
func (r *ShutdownGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx)

    // 1. Fetch the resource — handle not-found gracefully
    var sg kshutdownv1alpha1.ShutdownGroup
    if err := r.Get(ctx, req.NamespacedName, &sg); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 2. Finalizer handling (before any mutation)
    if !sg.DeletionTimestamp.IsZero() {
        return r.reconcileDelete(ctx, &sg)
    }

    // 3. Dispatch on command annotation
    switch annotation := sg.Annotations[AnnotationCommand]; annotation {
    case CommandDown:
        return r.reconcileDown(ctx, &sg)
    case CommandUp:
        return r.reconcileUp(ctx, &sg)
    default:
        // No pending command — ensure state is consistent
        return r.reconcileIdle(ctx, &sg)
    }
}
```

**Rules:**
- Always `client.IgnoreNotFound` on the initial `Get` — the resource may have been deleted between the event and the reconcile.
- Check `DeletionTimestamp` before any mutation to handle finalizer cleanup.
- Return `ctrl.Result{}, nil` (no requeue) for expected terminal states.
- Return `ctrl.Result{}, err` only for transient errors that should be retried.
- Return `ctrl.Result{RequeueAfter: d}` for polling (e.g. waiting for pods to terminate).
- **Never** return both a non-zero Result and a non-nil error — the error already triggers a requeue.

---

## Status updates

Always use `Status().Patch()` — never `Status().Update()` to avoid conflicts.

```go
// Patch pattern — always work on a deep copy
patch := client.MergeFrom(sg.DeepCopy())
sg.Status.State = kshutdownv1alpha1.StateDown
sg.Status.Since = metav1.Now()
sg.Status.Operator = operatorName
if err := r.Status().Patch(ctx, sg, patch); err != nil {
    return ctrl.Result{}, fmt.Errorf("patching status: %w", err)
}
```

**Rules:**
- Status subresource is enabled on ShutdownGroup (`+kubebuilder:subresource:status`).
- Status must never be set in the same call as spec/metadata mutations — use two separate patches.
- `status.snapshot` is written once during `reconcileDown` and cleared during `reconcileUp`. Never overwrite a non-empty snapshot.

---

## Annotation handling

Command annotations are the interface between the CLI and the operator.

```go
const (
    AnnotationCommand = "kshutdown.io/command"
    CommandDown       = "down"
    CommandUp         = "up"
)

// Consume a command annotation after processing (idempotent)
func consumeAnnotation(ctx context.Context, c client.Client, obj client.Object, key string) error {
    patch := client.MergeFrom(obj.DeepCopy())
    annotations := obj.GetAnnotations()
    delete(annotations, key)
    obj.SetAnnotations(annotations)
    return c.Patch(ctx, obj, patch)
}
```

**Rules:**
- Always consume (delete) the command annotation **after** successful execution, not before.
- `reconcileDown` and `reconcileUp` must be idempotent: re-running them on an already-down/up resource must be a no-op or safe.
- Check `status.state` at the start of each reconcile branch to skip if already in the target state.

---

## Cross-namespace client operations

kshutdown targets resources in multiple namespaces. Use the standard client with explicit namespace:

```go
// List targets matching a labelSelector in a given namespace
var deployments appsv1.DeploymentList
if err := r.List(ctx, &deployments,
    client.InNamespace(target.Namespace),
    client.MatchingLabels(target.LabelSelector),
); err != nil {
    return ctrl.Result{}, fmt.Errorf("listing deployments in %s: %w", target.Namespace, err)
}
```

For resources not known at compile time, use the dynamic client via `r.Client` with `Unstructured`:

```go
obj := &unstructured.Unstructured{}
obj.SetGroupVersionKind(schema.GroupVersionKind{
    Group:   "apps",
    Version: "v1",
    Kind:    "Deployment",
})
if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, obj); err != nil { ... }
```

---

## RBAC markers

Place markers directly above the `Reconcile` function. Run `make generate manifests` after any change.

```go
//+kubebuilder:rbac:groups=kshutdown.io,resources=shutdowngroups,verbs=get;list;watch;patch
//+kubebuilder:rbac:groups=kshutdown.io,resources=shutdowngroups/status,verbs=get;patch
//+kubebuilder:rbac:groups=kshutdown.io,resources=shutdowngroups/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;patch
//+kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;patch
//+kubebuilder:rbac:groups=authorization.k8s.io,resources=selfsubjectaccessreviews,verbs=create
```

**Rules:**
- Scope RBAC to the minimum needed — no `*` verbs, no wildcard resources.
- `selfsubjectaccessreviews` is only used by the CLI, not the operator. Keep markers in separate files.
- After adding a marker, always run `make manifests` and commit the generated RBAC YAML.

---

## CRD markers (on types)

```go
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Namespaced,shortName=sg
//+kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
//+kubebuilder:printcolumn:name="Since",type=date,JSONPath=`.status.since`
//+kubebuilder:printcolumn:name="Operator",type=string,JSONPath=`.status.operator`
//+kubebuilder:validation:Enum=down;up;unknown
```

---

## Error wrapping

Always wrap errors with context — never return a bare `err`:

```go
// Good
return ctrl.Result{}, fmt.Errorf("scaling deployment %s/%s to 0: %w", ns, name, err)

// Bad
return ctrl.Result{}, err
```

---

## Logging

Use structured logging with consistent fields:

```go
log := log.FromContext(ctx).WithValues(
    "shutdowngroup", req.NamespacedName,
    "state", sg.Status.State,
)
log.Info("starting reconcile")
log.Error(err, "failed to patch deployment", "deployment", deploy.Name, "namespace", deploy.Namespace)
```

---

## envtest — test structure

```go
var _ = Describe("ShutdownGroup controller", func() {
    ctx := context.Background()

    It("should scale down deployments and set status.state=down", func() {
        sg := &kshutdownv1alpha1.ShutdownGroup{...}
        Expect(k8sClient.Create(ctx, sg)).To(Succeed())

        // Trigger reconcile via annotation
        patch := client.MergeFrom(sg.DeepCopy())
        sg.Annotations = map[string]string{AnnotationCommand: "down"}
        Expect(k8sClient.Patch(ctx, sg, patch)).To(Succeed())

        // Poll until status reflects the change
        Eventually(func(g Gomega) {
            Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())
            g.Expect(sg.Status.State).To(Equal(kshutdownv1alpha1.StateDown))
            g.Expect(sg.Status.Snapshot).NotTo(BeEmpty())
        }, timeout, interval).Should(Succeed())
    })
})
```

**Rules:**
- Use `Eventually` for state assertions — never `time.Sleep`.
- Always clean up with `AfterEach` using `k8sClient.Delete`.
- Test both the happy path and idempotence (run the same reconcile twice).

---

## Common mistakes to avoid

| Mistake | Correct pattern |
|---|---|
| `Status().Update()` | `Status().Patch()` with `MergeFrom` |
| Returning `err` without wrapping | `fmt.Errorf("context: %w", err)` |
| Mutating `obj` before `client.MergeFrom(obj.DeepCopy())` | Always snapshot first |
| Re-running reconcileDown when state is already `down` | Check `status.state` first |
| Deleting annotation before confirming execution success | Consume annotation only after success |
| Hardcoding namespace in RBAC markers | Use explicit namespace in client calls |
