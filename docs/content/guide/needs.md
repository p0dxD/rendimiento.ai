# Needs and the services catalog

## Needs

An app should be able to say *what* it depends on and leave the *how* to the platform:

```yaml
services:
  - name: api
    needs:
      - postgres                              # a database for this app
      - redis                                 # a cache for this app
      - service: jobsentry/ollama-internal    # another app's service
        env: OLLAMA_HOST
```

### `postgres`

rendimiento runs **one Postgres per app** (shared by all its services that need it):

- a Deployment and Service named `postgres` in the app's namespace (`postgres:17-alpine`, configurable), on a Longhorn volume `postgres-data` (5 Gi, configurable), with the Recreate strategy;
- a Secret `postgres-credentials` created **once** with a random password (never regenerated: the database is initialized with it), holding `username` (`app`), `password`, `database` (the app's name with `_` for `-`), `host`, `port` and `uri`;
- in each service that needs it: `DATABASE_URL` (the `uri`) and the standard `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD`, `PGDATABASE`, all read from the Secret by reference.

```yaml
postgres: { version: "16", size: 20Gi, resources: { memory: 512Mi, memoryLimit: 1Gi } }
```

### `redis`

One Redis per app, as a **cache**: no persistence, least-recently-used keys evicted past `maxMemory` (64 MB by default), password in `redis-credentials`, `REDIS_URL` injected.

### Another service

`{service: namespace/name}` resolves the Service's app port and injects `http://name.namespace.svc.cluster.local:port` (or `http://name:port` for a service of the same app) into `<NAME>_URL`, or the `env` you give. If the service does not exist, the app shows an error naming it rather than starting broken.

### From the app's side

Nothing to configure. Connect with the variables; `DATABASE_URL` works with nearly every driver and ORM. The app page's **Provided for this app** card shows the database or cache, its health, which services use it, and how to reach it from your machine:

```bash
kubectl -n <app> port-forward svc/postgres 5432
kubectl -n <app> get secret postgres-credentials -o jsonpath='{.data.uri}' | base64 -d
```

!!! note "Detection pre-selects needs"
    When the code uses a Postgres or Redis client library (`psycopg`, `asyncpg`, `pg`, `ioredis`, `pgx`, the PostgreSQL JDBC driver, Jedis…), the wizard ticks the need for you.

## The services catalog

The **Services** page lists every Service in the cluster, so an app can find what to call. For each:

- **where it comes from**: an app (with the repository folder and release), an add-on, an ArgoCD app, a Helm release, or a hand-made deployment; and its images, with links to their registry pages;
- **what it exposes**: ports, protocol, public URLs, a LAN address (a link on the card itself when it's a web UI, such as Grafana or MinIO's console), the health check, documented endpoints, and whether network policies restrict who can connect;
- **how to connect**: the cluster address, the short address from the same app, and a `rendimiento.yaml` snippet to paste (for databases, a `secretEnv` form, since credentials belong in secrets);
- **who uses it**: every workload whose environment variables point at it (by cluster DNS name, short name within the namespace, or LAN address). Values stored in secrets cannot be seen.

Services are grouped by **category**: Databases, Caches & queues, AI, Storage, Monitoring & analytics, Web & APIs, Developer tools, Platform. Categories are guessed from the protocol, name and image (not the registry host), or set with `catalog.category`.

Describe your own services for others with a `catalog:` block ([reference](spec.md#catalog)); the ollama and linguistic services in jobsentry are examples.
