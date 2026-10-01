# Flows, step by step

The same few paths through the system, drawn as sequences. Function names are real, so you can follow each arrow into the code.

## Onboarding a repository

```mermaid
sequenceDiagram
    actor You
    participant UI
    participant API as api.Server
    participant P as platform.Platform
    participant GH as GitHub
    participant K as Kubernetes
    You->>UI: New app → pick a repo
    UI->>API: POST /api/propose
    API->>P: Propose(installation, repo, branch)
    P->>GH: read the tree (RepoFS)
    P->>P: detect.Detect → generate.Generate
    P->>K: is its namespace already in use? (InspectNamespace)
    P-->>UI: Proposal: spec, generated files, migration info
    You->>UI: adjust the form → Deploy
    UI->>API: POST /api/apps
    API->>P: Onboard(request)
    P->>P: store.CreateApp
    P->>K: create App object (waiting for a build)
    alt repo has no rendimiento.yaml
        P->>GH: OpenPR(rendimiento.yaml [+ Dockerfile])
        Note over GH: the PR branch builds and gets a check.<br/>Merging it triggers the first deploy
    else repo already has one
        P->>P: QueueRun(default branch) → the first build starts now
    end
```

- **Detection** (`internal/detect`) looks at each folder: `go.mod`, `package.json` (Next.js, Vite…), `pyproject.toml`/`requirements.txt` (FastAPI, Flask, Django), `pom.xml`/`build.gradle`, an `index.html`, an existing `Dockerfile`, and client libraries for Postgres and Redis. It returns the language, port, test command and needs, with the reasons.
- **Generation** (`internal/generate`) turns that into services, a domain under your default zone, and a Dockerfile from `templates/dockerfiles/` for folders without one.
- **Migration**: if the namespace the app would use already has something running (deployed by ArgoCD or `kubectl`), the proposal describes it and offers to **adopt** it (see [adoption](controller.md#adoption-taking-over-what-is-already-running)).

## A push: from commit to running code

```mermaid
sequenceDiagram
    participant GH as GitHub
    participant API as api.Server
    participant P as platform.Platform
    participant DB as Postgres
    participant W as CI worker
    participant X as KubeExecutor
    participant BK as BuildKit pool
    participant K as Kubernetes
    participant C as App controller
    GH->>API: POST /api/webhooks/github (push)
    API->>API: verify HMAC signature
    API->>P: HandlePush(repo, branch, sha)
    P->>DB: apps for this repo
    P->>GH: rendimiento.yaml at this commit
    P->>DB: CreateRun + steps (queued)
    W->>DB: ClaimRun (FOR UPDATE SKIP LOCKED)
    W->>GH: create check run "in progress"
    W->>GH: files changed since the last release
    Note over W: unchanged services reuse their image
    loop each step, in dependency order, ≤ MAX_PARALLEL_STEPS at once
        W->>X: Execute(step)
        X->>K: pod: clone → (plan) → test or build
        X->>BK: buildctl build … (from the pod)
        BK-->>X: image pushed, digest
    end
    W->>DB: release n (image digests + spec)
    W->>K: update App object (pointApp)
    W->>GH: check run ✓ / ✗
    K-->>C: App changed
    C->>K: apply Deployments, Services, Ingresses… (server-side apply)
    C->>K: rolling update, health rolls up into App status
    Note over P: verification: wait until healthy, check every service<br/>for 5 min, roll back if one broke
```

- Pushes to **other branches** stop after the builds: nothing is released; the check run shows on the commit and on any pull request.
- **Manual runs** (the *Run* button) resolve the branch to its current commit first, so the image, check and change detection all refer to a real commit.
- If the platform restarts mid-run, `RequeueOrphans` queues the run again on startup (up to two attempts) and old build pods are deleted.

## Releases and rollback

```mermaid
flowchart LR
    run1[run #41 ✓] --> r1[release #7<br/>web@sha256:aa…<br/>api@sha256:bb…]
    run2[run #42 ✓] --> r2[release #8<br/>web@sha256:cc…<br/>api@sha256:bb… reused]
    r1 -. "Roll back to #7" .-> r3[release #9<br/>= #7's images and spec]
    r3 --> app[App object<br/>release: 9]
```

A release pins every service to an image **digest**, so what runs is exactly what was built and tested. Rolling back never rebuilds: it creates a new release with the old images and spec and points the `App` object at it.

## An add-on change in git

```mermaid
sequenceDiagram
    participant You
    participant G as p0dxD/gitops
    participant API as api.Server
    participant S as addon.Syncer
    participant K as Kubernetes
    participant AC as Addon controller
    You->>G: commit addons/umami.yaml (or Install in the UI, which commits it)
    G->>API: push webhook
    API->>S: Trigger()
    S->>G: read addons/*.yaml at the new commit
    S->>K: create / update / delete Addon objects
    K-->>AC: Addon changed
    AC->>AC: render (Helm template / kustomize build)
    AC->>K: dry-run every object → preview
    alt adopting and something would change, or manual sync
        AC->>K: status: Blocked / OutOfSync (nothing applied)
    else
        AC->>K: pre hooks → apply (as rendimiento-addons) → prune → post hooks
        AC->>K: status: Synced
    end
```

The sync also runs every 3 minutes in case a webhook was missed, and the controller resyncs every add-on every 5 minutes, which also repairs drift.

## A Renovate run

```mermaid
sequenceDiagram
    participant Sch as renovate.Runner (scheduler)
    participant DB as Postgres
    participant GH as GitHub
    participant K as Kubernetes
    participant R as Renovate pod
    participant CI as rendimiento CI
    Sch->>DB: is a run due? claim the slot (only one wins)
    Sch->>GH: fresh installation token, permissions, bot identity
    Sch->>K: secret (token, config.js) + pod in rendimiento-builds
    R->>GH: for each repo: open or update update branches and PRs
    GH->>CI: push webhooks for the renovate/* branches
    CI->>GH: build them, report checks
    R-->>Sch: JSON log
    Sch->>DB: run result per repo, readable log
    Note over R,GH: next run: PRs whose checks passed are auto-merged<br/>(patch and minor, per the config) → a normal deploy
```

## Needs: a database for an app

```mermaid
sequenceDiagram
    participant C as App controller
    participant K as Kubernetes
    C->>C: spec says services[api].needs: [postgres]
    C->>K: namespace (if not shared)
    C->>K: postgres-credentials Secret (only if missing: generated once)
    C->>K: postgres Deployment + Service + postgres-data volume
    C->>K: api Deployment with DATABASE_URL, PGHOST… from the Secret
```
