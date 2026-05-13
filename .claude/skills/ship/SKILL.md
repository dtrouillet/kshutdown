---
name: ship
description: >
  Run the pre-commit / pre-PR checklist for kshutdown: verify generated files,
  tests, RBAC markers, documentation, and ArgoCD compatibility notes.
  Invoke before every commit on controller, CLI, or CRD changes.
argument-hint: "[optional: scope — api | controller | cli | all]"
user-invocable: true
allowed-tools: Read, Write, Edit, Bash, Glob, Grep
---

# /ship — kshutdown pre-commit checklist

Work through each section in order. Fix issues before moving to the next section.
Report results as a checklist with ✅ (pass), ❌ (fail — blocked), ⚠️ (warning — review needed).

---

## 1. Generated files are up to date

```bash
make generate
make manifests
```

Then check for uncommitted changes in generated files:
```bash
git diff --name-only config/crd/ config/rbac/ api/
```

- ✅ No diff — generated files are current
- ❌ Diff present — commit the generated files or re-run make

---

## 2. RBAC markers are complete and minimal

Grep all RBAC markers in the codebase:
```bash
grep -r "+kubebuilder:rbac" --include="*.go" .
```

Verify:
- Every verb used by the controller has a corresponding marker
- No `*` wildcard verbs or resources
- `selfsubjectaccessreviews` marker is in the CLI code, not the controller
- `status` subresource has its own marker (`resources=shutdowngroups/status`)
- `finalizers` subresource has its own marker (`resources=shutdowngroups/finalizers`)

---

## 3. Tests pass

```bash
make test
```

Verify coverage includes:
- `reconcileDown` happy path
- `reconcileUp` happy path
- Idempotence: double-down is a no-op
- Idempotence: double-up is a no-op
- `reconcileUp` with empty snapshot returns error
- CronJob suspend/unsuspend
- Cross-namespace target resolution

Report any failing tests as ❌ with the test name and failure message.

---

## 4. No forbidden patterns in controller code

Grep for common mistakes:
```bash
# Status().Update() is forbidden — must use Status().Patch()
grep -r "Status().Update()" --include="*.go" internal/

# Bare error returns without wrapping
grep -rn "return ctrl.Result{}, err$" --include="*.go" internal/

# time.Sleep in controller code
grep -rn "time.Sleep" --include="*.go" internal/
```

- ✅ No matches for any of the above
- ❌ Match found — fix before committing

---

## 5. CLI flags are consistent

Check that all CLI commands implement the standard flags:
```bash
grep -r "\"--reason\"" --include="*.go" cmd/
grep -r "\"--namespace\"" --include="*.go" cmd/
grep -r "\"--partial\"" --include="*.go" cmd/
grep -r "\"--dry-run\"" --include="*.go" cmd/
```

Verify:
- `down` has `--reason` (required), `--namespace`, `--partial`, `--dry-run`
- `up` has `--namespace`, `--partial`, `--dry-run`
- `status`, `list`, `history` are read-only (no mutation flags)
- `define` adds `argocd.argoproj.io/sync-options: Prune=false` annotation

---

## 6. ArgoCD compatibility

If changes touch `spec.replicas`, `spec.suspend`, or any field that ArgoCD
manages from Git, verify that the installation checklist in `README.md` or
`docs/argocd-prerequisites.md` still covers it:

```bash
grep -r "RespectIgnoreDifferences" docs/ README.md
grep -r "ignoreDifferences" docs/ README.md
grep -r "spec/replicas" docs/ README.md
grep -r "spec/suspend" docs/ README.md
```

- ✅ All affected fields are documented in the ArgoCD prerequisites section
- ⚠️ New field added but not documented — add it before merging

---

## 7. Helm chart is consistent

```bash
# CRD in chart matches generated CRD
diff config/crd/bases/ charts/kshutdown/crds/

# RBAC in chart matches generated RBAC
diff config/rbac/ charts/kshutdown/templates/rbac/
```

- ✅ No diff
- ❌ Diff — copy generated files to chart or regenerate

---

## 8. Commit message format

Verify the staged commit message follows the convention:
```
<type>(<scope>): <subject>

<body — what and why, not how>

<footer — breaking changes, closes issues>
```

Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`
Scopes: `api`, `controller`, `cli`, `helm`, `docs`

Examples:
```
feat(api): add timeout field to ShutdownGroupSpec
fix(controller): guard against double-down overwriting snapshot
test(controller): add idempotence cases for reconcileUp
docs(helm): document ArgoCD ignoreDifferences prerequisite for CronJob
```

---

## Final report

After all sections, output:

```
## Ship report

✅ Generated files up to date
✅ RBAC markers complete and minimal
✅ Tests pass (42 passed, 0 failed)
✅ No forbidden patterns
✅ CLI flags consistent
✅ ArgoCD prerequisites documented
⚠️  Helm chart diff — 2 files need copy from config/
✅ Commit message format valid

Blocked: No
Ready to commit: Yes (after fixing ⚠️)
```
