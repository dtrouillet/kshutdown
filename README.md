# kshutdown

Kubernetes operator + kubectl plugin to shut down and restart a functional slice — a set of workloads spread across multiple namespaces and ArgoCD Applications — quickly, safely, and reversibly, without breaking the GitOps model.

## Description

kshutdown solves a recurring problem in strict GitOps environments: during an incident (P1/P2), an operator must be able to immediately stop a set of application services without opening pull requests on multiple Git repositories, without waiting for CI/CD pipelines, and without having GitOps controllers revert their actions.

ArgoCD compatibility relies on `ignoreDifferences` + `RespectIgnoreDifferences=true` configured on each ArgoCD Application, which prevents ArgoCD from reverting scaled-to-zero replicas during a sync.

## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### ArgoCD prerequisite — required before use

> **This step is mandatory.** Without it, ArgoCD will restore `spec.replicas` on the next sync, negating any shutdown performed by kshutdown.

kshutdown scales workloads to zero. ArgoCD will revert `spec.replicas` changes on the next sync unless you configure `ignoreDifferences` with `RespectIgnoreDifferences=true` on each ArgoCD Application that manages workloads targeted by a ShutdownGroup.

**Apply this patch to every ArgoCD Application in scope:**

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: my-application
  namespace: argocd
spec:
  ignoreDifferences:
    - group: apps
      kind: Deployment
      jsonPointers:
        - /spec/replicas
    - group: apps
      kind: StatefulSet
      jsonPointers:
        - /spec/replicas
  syncPolicy:
    syncOptions:
      - RespectIgnoreDifferences=true
```

Without `RespectIgnoreDifferences=true`, `ignoreDifferences` only affects the UI diff — ArgoCD will still revert the replica count during a sync triggered by a Git commit.

For CronJob targets, add:

```yaml
    - group: batch
      kind: CronJob
      jsonPointers:
        - /spec/suspend
```

## Images & Releases

### Operator image (GHCR)

```sh
docker pull ghcr.io/dtrouillet/kshutdown:latest       # last commit on master
docker pull ghcr.io/dtrouillet/kshutdown:0.1.0        # specific release
```

The image is published automatically on every push to `master` (`latest`) and on every `v*.*.*` tag (semver tags).

### Helm repository (GitHub Pages)

```sh
helm repo add kshutdown https://dtrouillet.github.io/kshutdown
helm repo update
helm search repo kshutdown
```

The chart is published automatically on every `v*.*.*` tag via the `helm-release` GitHub Actions workflow.

> **One-time setup (repo owner):** enable GitHub Pages in repository Settings → Pages → Source: `gh-pages` branch, root `/`. The `helm-release` workflow creates the branch on first run.

---

## Helm chart — operator deployment

### Prerequisites

- Helm 4 (`curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-4 | bash`)
- ArgoCD prerequisite configured on each Application in scope (see below)

### Install

```sh
helm install kshutdown charts/kshutdown \
  --namespace kshutdown-system \
  --create-namespace \
  --set image.repository=ghcr.io/dtrouillet/kshutdown \
  --set image.tag=0.1.0
```

### Upgrade

```sh
helm upgrade kshutdown charts/kshutdown \
  --namespace kshutdown-system \
  --set image.tag=0.2.0
```

### Uninstall

```sh
helm uninstall kshutdown --namespace kshutdown-system
# CRDs are NOT deleted automatically — remove manually if needed:
# kubectl delete crd shutdowngroups.kshutdown.io
```

### Key values

| Value | Default | Description |
|-------|---------|-------------|
| `image.repository` | `ghcr.io/dtrouillet/kshutdown` | Operator image repository |
| `image.tag` | `latest` | Image tag |
| `replicaCount` | `1` | Number of operator replicas |
| `leaderElection.enabled` | `true` | Enable leader election (required for >1 replica) |
| `metrics.enabled` | `false` | Expose Prometheus metrics endpoint |
| `resources` | 500m/128Mi limits | Container resource limits |
| `nodeSelector` | `{}` | Node selector for the operator pod |

---

## kubectl plugin — installation and usage

### Install

Build the plugin and place it on your `PATH`:

```sh
make build-plugin
cp bin/kubectl-kshutdown /usr/local/bin/kubectl-kshutdown
# or via krew (once published):
# kubectl krew install kshutdown
```

Verify:
```sh
kubectl kshutdown --help
```

### Commands

| Command | Description |
|---------|-------------|
| `kubectl kshutdown down <name> -n <ns> --reason "..."` | Shut down a functional slice (reason required) |
| `kubectl kshutdown up <name> -n <ns>` | Restart a functional slice |
| `kubectl kshutdown status <name> -n <ns>` | Detailed state (state, since, operator, reason, snapshot) |
| `kubectl kshutdown list [-n <ns>]` | List all ShutdownGroups (all namespaces if -n omitted) |
| `kubectl kshutdown history <name> -n <ns>` | Operation history |
| `kubectl kshutdown define <name> -n <ns> --namespace <tns> --selector <sel>` | Create a ShutdownGroup ad-hoc during an incident |
| `kubectl kshutdown export <name> -n <ns>` | Export clean YAML for GitOps |

Common flags: `--namespace / -n`, `--partial`, `--dry-run`, `--kubeconfig`

### Emergency shutdown workflow

```sh
# 1. Shut down payment stack during P1 incident
kubectl kshutdown down payment-stack -n payments --reason "incident P1 #4521"

# 2. Check status
kubectl kshutdown status payment-stack -n payments

# 3. Restart when incident is resolved
kubectl kshutdown up payment-stack -n payments

# 4. Verify history
kubectl kshutdown history payment-stack -n payments
```

### Ad-hoc ShutdownGroup (no pre-existing CRD)

```sh
kubectl kshutdown define payment-emergency -n payments \
  --namespace payments --selector app.kubernetes.io/part-of=payment \
  --namespace payments-cron --selector app.kubernetes.io/part-of=payment

# Export for GitOps after the incident
kubectl kshutdown export payment-emergency -n payments > gitops/payments/shutdowngroup-emergency.yaml
```

### RBAC — required user permissions

The user must have `get;list;patch` on `shutdowngroups` in the target namespace:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: shutdowngroup-actioner
  namespace: payments
rules:
  - apiGroups: ["kshutdown.io"]
    resources: ["shutdowngroups"]
    verbs: ["get", "list", "patch"]
```

---

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/kshutdown:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/kshutdown:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/kshutdown:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/kshutdown/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

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

