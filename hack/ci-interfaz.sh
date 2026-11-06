#!/usr/bin/env bash
# The browser tests (web/e2e) in the Playwright image: what rendimiento runs
# on every push to its own repository (rendimiento.yaml, tasks: interfaz).
# It builds the UI and the test server (hack/interfaz), starts the server on
# the task's empty Postgres (DATABASE_URL), and drives it with Playwright.
#
# The image has Node and the browsers; Go is installed under /cache (the
# task's cache) the first time, with the version in GO_VERSION.
set -euo pipefail

GO_VERSION=${GO_VERSION:-1.27.3}
: "${DATABASE_URL:?an empty Postgres database}"
cd "$(dirname "$0")/.."
git config --global --add safe.directory "$PWD" 2>/dev/null || true
start=$(date +%s)
section() { echo "== $1 ($(($(date +%s) - start))s)"; }

section go
GOROOT_DIR=/cache/go-$GO_VERSION
if [ ! -x "$GOROOT_DIR/bin/go" ]; then
  arch=$(dpkg --print-architecture)
  echo "   installing Go $GO_VERSION ($arch); the cache keeps it for the next runs"
  rm -rf "$GOROOT_DIR.tmp" && mkdir -p "$GOROOT_DIR.tmp"
  curl -fsSL "https://go.dev/dl/go$GO_VERSION.linux-$arch.tar.gz" | tar -xz -C "$GOROOT_DIR.tmp" --strip-components=1
  mv "$GOROOT_DIR.tmp" "$GOROOT_DIR"
fi
export PATH=$GOROOT_DIR/bin:$PATH GOTOOLCHAIN=local
go version

section 'build the UI'
(cd web && npm ci --no-audit --no-fund && npm run build)

section 'build the test server'
go build -o /tmp/interfaz ./hack/interfaz

section 'start the test server'
log=/tmp/interfaz.log
/tmp/interfaz -addr 127.0.0.1:8090 -ui web/dist -estado web/e2e/.estado.json >"$log" 2>&1 &
server=$!
trap 'kill $server 2>/dev/null || true' EXIT
for _ in $(seq 60); do
  if curl -fs http://127.0.0.1:8090/healthz >/dev/null; then break; fi
  if ! kill -0 $server 2>/dev/null; then cat "$log"; echo "the test server stopped"; exit 1; fi
  sleep 1
done
curl -fs http://127.0.0.1:8090/healthz >/dev/null || { cat "$log"; echo "the test server did not answer in a minute"; exit 1; }

section 'browser tests'
status=0
(cd web && npx playwright test -c e2e) || status=$?
if [ $status -ne 0 ] || [ -s "$log" ]; then
  echo "== the test server's log"
  cat "$log"
fi
section done
exit $status
