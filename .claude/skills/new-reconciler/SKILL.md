---
name: new-reconciler
description: >
  Scaffold a new reconciler branch or sub-function for the kshutdown controller.
  Invoke when adding a new reconcile path (e.g. reconcileTimeout, reconcilePartial),
  a new target kind handler, or a new status transition.
argument-hint: "[reconciler name or description]"
user-invocable: true
allowed-tools: Read, Write, Edit, Glob, Grep
---

# /new-reconciler — kshutdown reconciler scaffold

You are scaffolding a new reconciler function or branch for the kshutdown operator.

## Step 1 — Read existing controller

Read `internal/controller/shutdowngroup_controller.go` in full before writing anything.
Understand:
- The existing dispatch switch on `AnnotationCommand`
- The `reconcileDown` and `reconcileUp` signatures and return patterns
- How status patches are done (`Status().Patch()` with `MergeFrom`)
- How errors are wrapped (`fmt.Errorf("context: %w", err)`)
- The logging convention (`log.FromContext(ctx).WithValues(...)`)

## Step 2 — Identify the new function

From the user's description, determine:
- Function name (use `reconcile<Name>` convention)
- Which annotation or condition triggers it
- Which status fields it reads and writes
- Which target kinds it affects (Deployment, StatefulSet, CronJob)
- Whether it needs a new entry in the dispatch switch

## Step 3 — Scaffold the function

Apply these rules without exception:

### Signature
```go
func (r *ShutdownGroupReconciler) reconcile<Name>(
    ctx context.Context,
    sg *kshutdownv1alpha1.ShutdownGroup,
) (ctrl.Result, error) {
```

### Idempotence guard
Check current state before doing anything:
```go
if sg.Status.State == kshutdownv1alpha1.State<Target> {
    // Already in target state — consume annotation and return
    return ctrl.Result{}, consumeAnnotation(ctx, r.Client, sg, AnnotationCommand)
}
```

### Status patch
Always use MergeFrom on a DeepCopy taken before any mutation:
```go
patch := client.MergeFrom(sg.DeepCopy())
sg.Status.State = kshutdownv1alpha1.State<Target>
sg.Status.Since = &metav1.Time{Time: time.Now()}
if err := r.Status().Patch(ctx, sg, patch); err != nil {
    return ctrl.Result{}, fmt.Errorf("patching status after <name>: %w", err)
}
```

### Error wrapping
Every error must be wrapped with context:
```go
return ctrl.Result{}, fmt.Errorf("<action> <resource> %s/%s: %w", ns, name, err)
```

### Logging
```go
log := log.FromContext(ctx).WithValues(
    "shutdowngroup", sg.Name,
    "namespace", sg.Namespace,
)
log.Info("starting reconcile<Name>")
```

### Annotation consumption
Always last, only on success:
```go
if err := consumeAnnotation(ctx, r.Client, sg, AnnotationCommand); err != nil {
    return ctrl.Result{}, fmt.Errorf("consuming command annotation: %w", err)
}
return ctrl.Result{}, nil
```

## Step 4 — Wire into the dispatch switch

If the new function is triggered by a command annotation, add it to the switch
in `Reconcile()`:
```go
case Command<Name>:
    return r.reconcile<Name>(ctx, &sg)
```

If it is triggered by a condition (not an annotation), add it to `reconcileIdle`.

## Step 5 — Scaffold the test

Create or extend `internal/controller/shutdowngroup_controller_test.go`:

```go
It("should <describe behavior>", func() {
    // Arrange
    sg := buildTestShutdownGroup("<name>", "<namespace>", targets)
    Expect(k8sClient.Create(ctx, sg)).To(Succeed())

    // Act — trigger via annotation
    patch := client.MergeFrom(sg.DeepCopy())
    sg.Annotations = map[string]string{AnnotationCommand: Command<Name>}
    Expect(k8sClient.Patch(ctx, sg, patch)).To(Succeed())

    // Assert — poll until state reflects the change
    Eventually(func(g Gomega) {
        Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())
        g.Expect(sg.Status.State).To(Equal(kshutdownv1alpha1.State<Target>))
    }, timeout, interval).Should(Succeed())

    // Assert idempotence — run again, expect same result
    patch = client.MergeFrom(sg.DeepCopy())
    sg.Annotations = map[string]string{AnnotationCommand: Command<Name>}
    Expect(k8sClient.Patch(ctx, sg, patch)).To(Succeed())
    Eventually(func(g Gomega) {
        Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sg), sg)).To(Succeed())
        g.Expect(sg.Status.State).To(Equal(kshutdownv1alpha1.State<Target>))
    }, timeout, interval).Should(Succeed())
})
```

## Step 6 — Output

Produce in order:
1. The complete new function (ready to paste into the controller file)
2. The dispatch switch addition (if needed)
3. Any new constants needed
4. The test case
5. A one-line summary of what to run after: `make generate manifests` if types changed,
   or just `make test` if controller-only changes
