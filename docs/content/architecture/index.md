# Architecture

How rendimiento is put together, from the whole down to each subsystem. Every chapter links to the code; the [code map](../reference/code-map.md) lists every file and function.

1. **[Big picture](overview.md)**: one binary, its parts, how they are wired, the package layers.
2. **[Flows, step by step](flows.md)**: sequence diagrams for onboarding, a push, a release, a rollback, an add-on sync, a Renovate run.
3. **[The CI engine](pipeline.md)**: planning, the run queue, build pods, change detection, logs.
4. **[Building images with BuildKit](builds.md)**: BuildKit, multi-stage Dockerfiles, Railpack, caching, the daemon pool.
5. **[The app controller](controller.md)**: reconciliation, rendering, server-side apply, adoption, health, DNS and certificates.
6. **[The add-on engine](addons.md)**: Helm and kustomize rendering, previews, adoption, manual sync, hooks, git as the source of truth.
7. **[API, auth and the UI](api-ui.md)**: the HTTP API, login, webhooks, live updates, the React app.
8. **[Data and state](data.md)**: what lives in Postgres, in Kubernetes and in git, and why.
9. **[Security model](security.md)**: identities, isolation, secrets, and the gaps.
