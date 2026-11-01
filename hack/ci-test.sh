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

# Compiling with an empty cache prints nothing for many minutes: each
# section says when it starts, and a line every minute says it is alive.
start=$(date +%s)
section() { echo "== $1 ($(($(date +%s) - start))s)"; }
(while sleep 60; do echo "   … still working, $(($(date +%s) - start))s"; done) &
heartbeat=$!
trap 'kill $heartbeat 2>/dev/null || true' EXIT

section tools
if ! controller-gen --version 2>/dev/null | grep -q "$CONTROLLER_GEN"; then
  echo "   installing controller-gen $CONTROLLER_GEN (the cache is empty or old)"
  go install "sigs.k8s.io/controller-tools/cmd/controller-gen@$CONTROLLER_GEN"
fi
if [ ! -x "$TOOLS/setup-envtest" ]; then
  echo "   installing setup-envtest $SETUP_ENVTEST"
  go install "sigs.k8s.io/controller-runtime/tools/setup-envtest@$SETUP_ENVTEST"
fi
# The built UI is not in git; vet and tests only need something to embed.
[ -n "$(ls -A web/dist 2>/dev/null)" ] || { mkdir -p web/dist && echo placeholder > web/dist/index.html; }

section generate
controller-gen object paths=./api/... paths=./internal/spec/...
controller-gen crd paths=./api/... output:crd:dir=deploy/crds
if [ -n "${CHECK_GENERATED:-}" ] && ! git diff --quiet -- api internal/spec deploy/crds; then
  git diff --stat -- api internal/spec deploy/crds
  echo "generated files are out of date: run make generate (or make test-remote) and commit them"
  exit 1
fi
section 'vet (compiles everything: the longest part with an empty cache)'
go vet ./...
section test
KUBEBUILDER_ASSETS=$(setup-envtest use "$ENVTEST_K8S" -p path --bin-dir "$(dirname "$TOOLS")/envtest")
export KUBEBUILDER_ASSETS
# store, platform and api share one test database, so they run one at a
# time; the rest (internal/controller's envtest among them) run beside
# them, two packages at a time (with the memory the test step has).
shared='/internal/(api|platform|store)$'
out=$(mktemp -d)
go test -v -p 1 $(go list ./... | grep -E "$shared") >"$out/shared.log" 2>&1 &
a=$!
go test -v -p 2 $(go list ./... | grep -vE "$shared") >"$out/rest.log" 2>&1 &
b=$!
# Each package as it finishes, so a long run shows where it is (tail ends
# with this script, and the rest of the pipe with it).
tail -n +1 -F --pid=$$ "$out/shared.log" "$out/rest.log" 2>/dev/null | grep --line-buffered -E '^(ok|FAIL)[[:space:]]' | sed -u 's/^/   finished: /' &
failed=()
wait $a || failed+=("$out/shared.log")
wait $b || failed+=("$out/rest.log")
sleep 2 # the last packages' lines, before the summary
section summary

# What `go test` prints without -v: one line per package.
grep -hE '^(ok|FAIL)[[:space:]]' "$out"/*.log | sort -k2
for log in "${failed[@]}"; do
  # Each failed test with its output, then the end of the log (build
  # errors, panics and timeouts are there).
  echo "== failures in $(basename "$log" .log) packages"
  awk '/^=== RUN/ && index($3, "/") == 0 { buf = "" } { buf = buf $0 "\n" } /^--- FAIL/ { printf "%s", buf; buf = "" }' "$log"
  tail -40 "$log"
done
echo '== slowest tests'
# awk, not head: head stops reading early, sort then dies of SIGPIPE, and
# with pipefail the whole step failed (exit 141) after every test passed.
grep -hE '^--- (PASS|FAIL): ' "$out"/*.log | sed -E 's/^--- [A-Z]+: ([^ ]+) \(([0-9.]+)s\)$/\2s \1/' | sort -rn | awk 'NR <= 12'
[ ${#failed[@]} -eq 0 ]
