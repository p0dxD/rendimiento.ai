#!/usr/bin/env bash
# The Go side of `make test`, in a golang image: what rendimiento runs on
# every push to its own repository (rendimiento.yaml, builds: platform) and
# what hack/test-remote.sh runs in its pod. The UI is checked by the
# image's build (npm run typecheck in the Dockerfile).
#
# It needs TEST_DATABASE_URL (or DATABASE_URL), an empty Postgres database,
# and keeps its tools under $TOOLS (default /cache/bin, the test's cache).
set -euo pipefail

CONTROLLER_GEN=${CONTROLLER_GEN:-v0.22.0}
SETUP_ENVTEST=${SETUP_ENVTEST:-v0.25.1}
ENVTEST_K8S=${ENVTEST_K8S:-1.37.0}
TOOLS=${TOOLS:-/cache/bin}
export TEST_DATABASE_URL=${TEST_DATABASE_URL:-${DATABASE_URL:-}}
export GOBIN=$TOOLS PATH=$TOOLS:$PATH

cd "$(dirname "$0")/.."
git config --global --add safe.directory "$PWD" 2>/dev/null || true
controller-gen --version 2>/dev/null | grep -q "$CONTROLLER_GEN" || go install "sigs.k8s.io/controller-tools/cmd/controller-gen@$CONTROLLER_GEN"
[ -x "$TOOLS/setup-envtest" ] || go install "sigs.k8s.io/controller-runtime/tools/setup-envtest@$SETUP_ENVTEST"
# The built UI is not in git; vet and tests only need something to embed.
[ -n "$(ls -A web/dist 2>/dev/null)" ] || { mkdir -p web/dist && echo placeholder > web/dist/index.html; }

echo '== generate'
controller-gen object paths=./api/... paths=./internal/spec/...
controller-gen crd paths=./api/... output:crd:dir=deploy/crds
if [ -n "${CHECK_GENERATED:-}" ] && ! git diff --quiet -- api internal/spec deploy/crds; then
  git diff --stat -- api internal/spec deploy/crds
  echo "generated files are out of date: run make generate (or make test-remote) and commit them"
  exit 1
fi
echo '== vet'
go vet ./...
echo '== test'
# -p 1: store, platform and api tests share one test database.
KUBEBUILDER_ASSETS=$(setup-envtest use "$ENVTEST_K8S" -p path --bin-dir "$(dirname "$TOOLS")/envtest") go test -p 1 ./...
