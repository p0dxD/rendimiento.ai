# Data and state

rendimiento keeps state in three places on purpose. Knowing which is which answers most "where does X come from?" questions.

```mermaid
flowchart LR
    subgraph git[Git: what you want]
        spec[rendimiento.yaml<br/>in each app repo]
        defs[addons/*.yaml<br/>in p0dxD/gitops]
    end
    subgraph k8s[Kubernetes: what should run now]
        app[App objects<br/>spec + released images]
        addon[Addon objects]
        secrets[Secrets]
    end
    subgraph pg[Postgres: what happened]
        runs[runs, steps, logs]
        rel[releases]
        apps[apps]
        sess[sessions]
        adds[add-on settings and runs]
    end
    spec -- "push → run → release" --> rel --> app
    defs -- sync --> addon
```

| Question | Answer lives in | Why |
|---|---|---|
| How should this app be built and run? | `rendimiento.yaml` (git) | Reviewed in PRs, versioned, deploys on merge. |
| Which images are running right now? | the `App` object (`spec.images`, `spec.release`) | The controller needs it; `kubectl get apps.rendimiento.ai` shows it; the cluster keeps working if the database is down. |
| What cluster software is installed, and how? | `addons/*.yaml` (git) → `Addon` objects | Same reasons. |
| What happened (runs, logs, releases, who logged in)? | Postgres | Relational, append-heavy, queryable history. |
| Passwords and tokens | Kubernetes Secrets | Never in git in plain text, never in Postgres. |

The consequences are useful: losing the database loses history but not what is running; deleting an `App` object is recovered by the next release; git always tells you what was intended.

## The database

Postgres 17, one instance (`rendimiento-db`), accessed with **pgx** (the standard high-performance Go driver) through `internal/store`. There is no ORM: queries are plain SQL next to the code that uses them, which keeps them readable and explicit.

### Schema

```mermaid
erDiagram
    apps ||--o{ runs : ""
    runs ||--|{ steps : ""
    apps ||--o{ releases : ""
    runs ||--o| releases : ""
    apps ||--o{ app_addons : ""
    addons ||--o{ addon_runs : ""
    apps {
        bigint id PK
        text name UK
        text repo
        bigint installation_id
        text default_branch
        jsonb spec
    }
    runs {
        bigint id PK
        bigint app_id FK
        text sha
        text branch
        text event
        bool deploy
        text status
        int attempts
        bigint check_run
    }
    steps {
        bigint run_id PK
        text step_id PK
        text kind
        text status
        text digest
        text log
    }
    releases {
        bigint id PK
        bigint app_id FK
        bigint number
        jsonb images
        jsonb spec
        bigint rollback_of
    }
    sessions {
        text token_hash PK
        text login
        timestamptz expires_at
    }
    addons {
        text name PK
        bool enabled
        jsonb settings
        timestamptz last_scheduled
    }
    addon_runs {
        bigint id PK
        text addon
        text status
        jsonb results
        text log
    }
    app_addons {
        bigint app_id PK
        text addon PK
        bool enabled
    }
```

### Migrations

Migrations are plain SQL files in `internal/store/migrations/`, **embedded** into the binary and applied in filename order at startup (`Store.Migrate`), each once, recorded in `schema_migrations`, under a table lock so two processes never apply the same one. To change the schema, add the next numbered file; never edit one that has already run.

```sql
--8<-- "internal/store/migrations/0004_addons.sql"
```

### The run queue

CI runs are a **queue in Postgres**, no message broker needed:

```go
--8<-- "internal/store/store.go:claim"
```

`FOR UPDATE SKIP LOCKED` lets any number of workers pull from the queue at the same time: each claims a different row, and none waits for another. This is the standard pattern for job queues on Postgres.

### Logs

Each step's log is a text column, appended in chunks (`right(log || chunk, 1 MiB)`): the head is dropped past 1 MiB and the tail, where errors are, is kept. Add-on run logs are stored the same way, truncated in the middle past 512 KiB.

## Kubernetes objects

Two custom resources, defined in Go in `api/v1alpha1` and turned into CRDs by `controller-gen` ([reference](../reference/crds.md)):

- **`App`** (`apps.rendimiento.ai`, cluster-scoped): `spec` is the app's services and jobs (the same Go types as `rendimiento.yaml`), `images`, `release`, `suspend`, `adopt`, `sharedNamespace`, and the need settings; `status` is the phase, a message and per-service health.
- **`Addon`** (`addons.rendimiento.ai`, cluster-scoped, short name `radd`): source, values, gates and switches in `spec`; phase, preview, inventory, hooks and hash in `status`.

Both are **cluster-scoped** because an app owns its namespace and an add-on spans namespaces.

!!! warning "Regenerate the CRDs when you change the types"
    The API server silently drops fields that are not in the CRD's schema. After changing `api/v1alpha1` or `internal/spec`, run `make generate` (the test targets do it for you) and apply the new CRD.
