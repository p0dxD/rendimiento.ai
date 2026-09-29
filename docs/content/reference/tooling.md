# Tooling

## Make targets

| Target | Does |
|---|---|
| `make generate` | controller-gen: deepcopy code and CRDs |
| `make test` | generate, vet, all tests, UI typecheck, *here* |
| `make test-remote` | the same on a worker node (`hack/test-remote.sh`) |
| `make test-db` | a throwaway Postgres 17 on `127.0.0.1:55432` |
| `make itest` | integration builds on the cluster (`-tags integration`) |
| `make ui` / `make build` | the UI / a local binary |
| `make image` | build and push the platform image with BuildKit |
| `make railpack-image` | build and push the Railpack CLI image |
| `make deploy` | `kubectl apply -k deploy` |
| `make docs-codemap` | regenerate the [code map](code-map.md) |

## Generators and helpers in `hack/`

| Tool | Does |
|---|---|
| `hack/test-remote.sh` | runs the test suite in a pod on a worker, with a Postgres sidecar and a node-local cache ([Testing](../develop/testing.md#remote-tests)) |
| `hack/codemap` | walks the repository with `go/ast` and writes a table of every package, type and function with its doc comment's first sentence |
| `hack/undoc` | lists exported names without a doc comment; the goal is zero |

## The book

```bash
python3 -m venv .venv && . .venv/bin/activate
pip install -r docs/requirements.txt
cd docs && mkdocs serve        # live preview on http://127.0.0.1:8000
mkdocs build --strict          # what the image build runs; fails on broken links
```

- **MkDocs Material** renders Markdown from `docs/content/`. Navigation is in `docs/mkdocs.yml`.
- **Diagrams** are Mermaid code blocks (` ```mermaid `), drawn in the browser.
- **Code excerpts** are included from the source, so they never go stale:
  `--8<-- "internal/dns/dns.go:provider"` includes the lines between `// --8<-- [start:provider]` and `// --8<-- [end:provider]`. A path with `:A:B` includes lines A to B.
- **The image** (`docs/Dockerfile`) has three stages: Go regenerates the code map, Python builds the site, and unprivileged nginx serves it on :8080. The repository's `rendimiento.yaml` deploys it, with a LAN address from MetalLB.

## Useful commands

```bash
kubectl get app,radd                                   # everything rendimiento manages
kubectl -n rendimiento-system logs deploy/rendimiento -f
kubectl -n rendimiento-builds get pods -w              # builds as they happen
kubectl -n devops-tools get pods -l app=buildkitd -o wide
kubectl annotate radd <name> rendimiento.ai/paused=true
```
