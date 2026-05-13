**kshutdown**

Architecture & Decisions

Version 0.1  —  Mai 2026

# **1. But du projet**

kshutdown est un outil Kubernetes permettant aux exploitants d'éteindre et de rallumer un pan fonctionnel — un ensemble de workloads répartis sur plusieurs namespaces et applications ArgoCD — de manière rapide, sécurisée et réversible, sans casser le modèle GitOps.

Le besoin est né d'un problème récurrent en environnement GitOps strict : en situation d'incident (P1/P2), un exploitant doit pouvoir arrêter immédiatement un ensemble de services applicatifs sans devoir ouvrir des pull requests sur plusieurs dépôts Git, sans attendre des pipelines CI/CD, et sans que les controllers GitOps ne révoquent son action.

### **Problèmes résolus**

- Aucune primitive native ArgoCD pour éteindre un pan fonctionnel multi-applications et multi-namespaces

- Le scale manuel à 0 d'un Deployment est révoqué par ArgoCD au prochain sync (selfHeal)

- Les approches existantes (désactiver la sync, modifier Git en urgence, suspendre l'ApplicationSet) sont soit trop lentes, soit trop grossières, soit risquées

- Pas de traçabilité de qui a éteint quoi, quand, et pourquoi

# **2. Contexte technique**

## **2.1 Infrastructure cible**

| **Composant** | **Description** |
| --- | --- |
| GitOps | Infrastructure 100 % as-code, source de vérité dans Git |
| ArgoCD | Déploiement via ApplicationSets générés par l'ApplicationSetController |
| Rancher | Gestion des clusters applicatifs, RBAC, et accès utilisateurs |
| Auth utilisateurs | Rancher impersonate les utilisateurs auprès des clusters applicatifs |
| Identité Kubernetes | Username Rancher stable (ex. user-5xnr5), groupes : system:authenticated + system:cattle:authenticated |
| ArgoCD auth | DEX + ADFS, RBAC ArgoCD basé sur les groupes AD (indépendant de Kubernetes) |

## **2.2 Topologie des clusters**

- Un cluster ArgoCD dédié, accessible uniquement aux administrateurs Kubernetes

- Un ou plusieurs clusters applicatifs, accessibles aux équipes via Rancher

- kshutdown est déployé sur chaque cluster applicatif (pas sur le cluster ArgoCD)

# **3. Levier technique : skip-reconcile**

Le mécanisme central de kshutdown repose sur l'annotation native ArgoCD :

argocd.argoproj.io/skip-reconcile: "true"

Posée sur une ressource Kubernetes (Deployment, StatefulSet, etc.), cette annotation indique à ArgoCD de ne pas réconcilier cette ressource spécifique, même lors d'un sync déclenché par un commit Git. ArgoCD verra la ressource comme OutOfSync mais ne la modifiera pas.

### **Pourquoi ce levier et pas un autre**

| **Approche** | **Verdict** | **Raison** |
| --- | --- | --- |
| Scale manuel sans annotation | Rejeté | Révoqué par ArgoCD au prochain sync (selfHeal) |
| Suspendre la sync ArgoCD | Rejeté | Trop grossier — toute l'Application est suspendue |
| Suspendre l'ApplicationSet | Rejeté | Impact sur toutes les Applications générées |
| ignoreApplicationDifferences | Rejeté | Annulé lors d'un sync déclenché par un commit Git |
| Modifier Git en urgence | Rejeté | Lent, risqué, pas atomique sur plusieurs repos |
| skip-reconcile par ressource | Retenu | Natif ArgoCD, granulaire, réversible, résistant aux commits |

# **4. Architecture**

## **4.1 Composants**

### **kshutdown-operator**

- Un opérateur Go (Kubebuilder) déployé dans le namespace kshutdown-system de chaque cluster applicatif

- Dispose d'un ServiceAccount avec des droits cluster-wide limités : patch des annotations et scale des workloads

- Watch les ressources ShutdownGroup et réagit aux annotations de commande

- N'est jamais appelé directement par les utilisateurs

### **kubectl plugin kshutdown (CLI)**

- Plugin kubectl installé sur les postes des exploitants

- Utilise le kubeconfig Rancher de l'utilisateur — aucun credential supplémentaire

- Toutes les requêtes transitent par le proxy Rancher, qui injecte l'identité de l'utilisateur

- Ne parle jamais directement aux namespaces applicatifs

### **ShutdownGroup (CRD)**

- Vit dans le namespace applicatif (ex. namespace payments)

- Décrit le périmètre fonctionnel : quels namespaces et quels workloads

- Défini dans Git et déployé via GitOps comme n'importe quelle ressource

- Peut être créé manuellement en urgence via la CLI (kubectl kshutdown define ...)

## **4.2 CRD ShutdownGroup**

Exemple de ressource :

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

## **4.3 Flux d****'****exécution**

### **Extinction**

- L'exploitant exécute : kubectl kshutdown down payment-stack --reason "incident P1"

- La CLI émet un SelfSubjectAccessReview pour chaque ressource cible : "puis-je scaler ce Deployment ?"

- Le kube-apiserver répond en fonction du RBAC Rancher de l'utilisateur

- Si toutes les ressources sont autorisées, la CLI pose une annotation de commande sur le ShutdownGroup

- L'operator détecte l'annotation, sauvegarde les replicas dans le status, pose skip-reconcile sur chaque ressource, et scale à 0

- L'annotation de commande est consommée (retirée) après exécution — idempotent

### **Rallumage**

- L'exploitant exécute : kubectl kshutdown up payment-stack

- Mêmes vérifications SelfSubjectAccessReview

- L'operator restaure les replicas depuis le status.snapshot

- L'operator retire l'annotation skip-reconcile sur chaque ressource

- ArgoCD reprend la main naturellement au prochain cycle de sync

# **5. Modèle d****'****autorisation**

## **5.1 Principe**

kshutdown ne définit pas son propre système de droits. L'autorisation repose entièrement sur le RBAC Kubernetes natif, tel que Rancher le configure. La règle est simple :

*Si un utilisateur peut scaler un Deployment dans un namespace, alors il peut éteindre ce Deployment via kshutdown.*

Aucun mapping utilisateur spécifique à kshutdown. Aucune adhérence à DEX, ADFS, ou aux groupes AD au niveau Kubernetes. Le RBAC Rancher est l'unique source de vérité.

## **5.2 SelfSubjectAccessReview**

La CLI utilise l'API SelfSubjectAccessReview de Kubernetes pour vérifier les droits de l'utilisateur courant sur chaque ressource cible, avant toute action. Cette API répond en fonction de l'identité impersonnée par Rancher — transparente pour l'utilisateur, fiable pour kshutdown.

## **5.3 Droits requis sur le ShutdownGroup**

Pour qu'un exploitant puisse actionner un ShutdownGroup, il doit avoir le droit de le patcher dans le namespace applicatif. Ce droit est accordé via un Role et un RoleBinding standards dans le namespace, gérés par Rancher comme pour n'importe quelle ressource :

apiVersion: rbac.authorization.k8s.io/v1

kind: Role

metadata:

  name: shutdowngroup-actioner

  namespace: payments

rules:

  - apiGroups: ["kshutdown.io"]

    resources: ["shutdowngroups"]

    verbs: ["get", "list", "patch"]

## **5.4 Cas partiel**

Si l'utilisateur n'a pas les droits sur toutes les ressources ciblées par un ShutdownGroup, la CLI refuse l'action par défaut et liste les ressources interdites. L'option --partial permet de n'éteindre que les ressources autorisées, avec un avertissement explicite.

# **6. Création manuelle en urgence**

En situation d'incident, si aucun ShutdownGroup n'est défini pour le pan à éteindre, la CLI permet à un administrateur de le créer à la volée :

kubectl kshutdown define payment-emergency \

  --namespace payments --selector app.kubernetes.io/part-of=payment \

  --namespace payments-cron --selector app.kubernetes.io/part-of=payment

Ce ShutdownGroup est créé directement dans le cluster. Il vit hors de Git temporairement. Pour éviter qu'ArgoCD ne le supprime (prune), il doit être créé dans un namespace non prunné ou annoté avec argocd.argoproj.io/sync-options: Prune=false.

Après l'incident, la CLI propose de l'exporter pour l'intégrer dans Git :

kubectl kshutdown export payment-emergency > gitops/payments/shutdowngroup-emergency.yaml

L'urgence d'aujourd'hui devient la préparation de demain.

# **7. Interface CLI**

| **Commande** | **Description** |
| --- | --- |
| kubectl kshutdown down <name> | Éteindre un pan fonctionnel |
| kubectl kshutdown up <name> | Rallumer un pan fonctionnel |
| kubectl kshutdown status <name> | État détaillé (state, since, operator, reason, resources) |
| kubectl kshutdown list | Lister tous les ShutdownGroups accessibles |
| kubectl kshutdown history <name> | Historique des opérations |
| kubectl kshutdown define <name> | Créer un ShutdownGroup en urgence |
| kubectl kshutdown export <name> | Exporter un ShutdownGroup en YAML pour Git |

Options communes :

- --reason : raison de l'opération (obligatoire pour down)

- --namespace (-n) : namespace du ShutdownGroup

- --partial : agir uniquement sur les ressources autorisées

- --dry-run : simuler l'action sans l'exécuter

# **8. Stack technique**

| **Composant** | **Technologie** |
| --- | --- |
| Operator | Go + Kubebuilder |
| CLI | Go + Cobra (kubectl plugin) |
| CRDs | controller-gen (généré depuis les types Go) |
| Client Kubernetes | client-go (typed + dynamic) |
| Autorisation | SelfSubjectAccessReview (API Kubernetes native) |
| Déploiement | Helm chart, un operator par cluster applicatif |
| Distribution CLI | Binaire Go, installable via krew ou curl |

# **9. Structure du projet Go**

kshutdown/

├── api/

│   └── v1alpha1/

│       └── shutdowngroup_types.go

├── cmd/

│   ├── operator/          # point d'entrée de l'operator

│   └── kubectl-kshutdown/ # point d'entrée du plugin CLI

│       ├── down.go

│       ├── up.go

│       ├── status.go

│       ├── list.go

│       ├── define.go

│       └── export.go

├── internal/

│   ├── controller/

│   │   └── shutdowngroup_controller.go

│   │       ├── reconcileDown()

│   │       └── reconcileUp()

│   └── authz/

│       └── selfsar.go     # SelfSubjectAccessReview helpers

└── config/

    ├── crd/               # manifests CRD générés

    ├── rbac/              # Role shutdowngroup-actioner

    └── manager/           # Deployment de l'operator

# **10. Hors scope (v0.1)**

- Interface web / UI graphique

- Notifications (Slack, PagerDuty) — à intégrer ultérieurement

- Support des ressources custom non-scalables (hors Deployment, StatefulSet, CronJob)

- Gestion des HPA (désactivation pendant le shutdown) — v0.2

- Multi-cluster depuis un point central

*Document généré le 12 mai 2026 — sujet à révision au fil de l**'**implémentation.*

	kshutdown — Architecture & Decisions
