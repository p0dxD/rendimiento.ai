# Security

rendimiento builds and deploys code, holds a GitHub App's credentials and manages workloads with wide permissions on a cluster. A vulnerability in it can matter a great deal, so please report one privately.

## Reporting a vulnerability

Use GitHub's **Report a vulnerability** button on this repository's *Security* tab (private vulnerability reporting). Don't open a public issue or pull request for it. Please include what you found, how to reproduce it, the version (`v0.1.3`, or the commit), and whether it was on a cluster or on one computer (`MODE=local`).

You'll get an answer within a few days. Once a fix is released, the advisory is published with credit to you, unless you'd rather not be named.

## Supported versions

Only the latest release and `main` get security fixes for now.

## What is in scope

The platform itself: its API and UI, the MCP endpoint and agent keys, webhooks, the CI pipeline and its build isolation, the controllers, the installer (`/instalar`), the Helm chart and the published images. The threat model and the known gaps are in [Security](docs/content/architecture/security.md); a gap listed there is known, but a new way to exploit it is still worth reporting.
