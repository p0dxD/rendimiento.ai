#!/usr/bin/env bash
# Filters the rendered platform (stdin → stdout) so a deploy keeps the image
# rendimiento updated itself to (Environment → Update): a release's image,
# pinned by digest, that the overlay's (often :latest) would otherwise undo.
# Usage: kubectl kustomize … | hack/keep-image.sh IMAGE | kubectl apply -f -
set -euo pipefail
image=$1
live=$(kubectl -n "${NAMESPACE:-rendimiento-system}" get deploy rendimiento \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="rendimiento")].image}' 2>/dev/null || true)
case "$live" in
  *@sha256:*) echo "keeping the running image $live" >&2 ;;
  *) exec cat ;;
esac
# Only the platform's own image line: IMAGE followed by a tag, a digest or
# nothing (not rendimiento-railpack and the like).
awk -v image="$image" -v live="$live" '
  { s = $0; sub(/^[ \t]+/, "", s) }
  s == "image: " image || index(s, "image: " image ":") == 1 || index(s, "image: " image "@") == 1 {
    sub(/image: .*/, "image: " live)
  }
  { print }'
