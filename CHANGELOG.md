# Changelog

What changed in each release, for people who run rendimiento. Every change is in the [merged pull requests](https://github.com/rendimiento-ai/rendimiento/pulls?q=is%3Apr+is%3Amerged); a release's notes on GitHub list them all.

## Unreleased

- **A release that doesn't answer fails, and says why.** On one computer, a published port accepted connections even when nothing listened behind it, so a broken release looked healthy. Now it fails, saying where the service listens inside its container, whether it looks for Redis or Postgres on `localhost`, and its last log lines. (#136)
- **Build tasks can have a throwaway Postgres and a kept cache** (`postgres: true`, `cache: true`), as tests can, for a task such as a browser test that drives the app with its database. (#135)
- **Browser tests of the UI on every push**, in English and Spanish, with every kind of app. They found two bugs, fixed here: the activity stream failed on every Spanish page, and without a cluster the UI asked for cluster-only data. (#137)
- **Each release publishes its Helm chart** at `oci://ghcr.io/rendimiento-ai/charts/rendimiento`, with the release's version, so it installs that release's image. The book now says what the repository publishes. (#138)

## v0.1.2 (2026-11-05)

- **Docker:** BuildKit runs on the computer's network, so builds can push to the local registry (`localhost:5000`). (#134)

## v0.1.1 (2026-11-05)

- **The installer** sets up a containerd that is installed but not yet set up for this user, and gives the service a `PATH` with `sbin` (where `iptables` lives). A busy Postgres port no longer stops rendimiento from starting. (#131, #133)
- **On one computer**, a service that stops fails its release with its last log lines; each service answers to its name on its app's network; the app's address includes the proxy's port. (#132)
- **The welcome page** shows the install command. (#130)
- **Libraries and builds-only apps**: an app may have only builds or tasks, with nothing to deploy; the New app wizard handles a repository with no services. Every layer is tested with the same examples (`internal/spec/ejemplos`). (#127, #128, #129)

## v0.1.0 (2026-11-05)

The first release: the program for Linux (amd64 and arm64), installable on one computer with `curl -fsSL https://rendimiento.ai/instalar | sh`, and the image for amd64 and arm64 at `ghcr.io/rendimiento-ai/rendimiento`.
