#!/usr/bin/env bash
# Runs `make test` on a worker node instead of here. main is the k3s
# control plane: compiling and running envtest on it slows the API server
# (Longhorn's CSI sidecars lose their leases) and fills its SD card.
#
# The working tree (including uncommitted changes) is shipped into a pod on
# a worker with a Postgres sidecar and a node-local cache (Go modules and
# build cache, envtest binaries, npm), the steps of `make test` run there,
# and the regenerated files (deepcopy, CRDs) are copied back.
set -euo pipefail

NS=${TEST_NAMESPACE:-rendimiento-builds}
POD=rendimiento-test-$(date +%s)
CACHE=rendimiento-test-cache
GO_IMAGE=${GO_IMAGE:-golang:1.27}
NODE_IMAGE=${NODE_IMAGE:-node:20-bookworm}
CONTROLLER_GEN=${CONTROLLER_GEN:-v0.22.0}
SETUP_ENVTEST=${SETUP_ENVTEST:-v0.25.1}
ENVTEST_K8S=${ENVTEST_K8S:-1.37.0}
# Comma-separated node names that must not run the tests (set in local.mk).
EXCLUDE_EXPR=""
if [ -n "${TEST_EXCLUDE_NODES:-}" ]; then
  EXCLUDE_EXPR="              - { key: kubernetes.io/hostname, operator: NotIn, values: [${TEST_EXCLUDE_NODES}] }"
fi

cd "$(dirname "$0")/.."

kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: $CACHE
  namespace: $NS
  labels: { app.kubernetes.io/part-of: rendimiento-dev }
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: local-path
  resources: { requests: { storage: 10Gi } }
EOF

kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: $POD
  namespace: $NS
  labels: { app.kubernetes.io/part-of: rendimiento-dev }
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  activeDeadlineSeconds: 3600
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
          - matchExpressions:
              # Never the control plane, nor the nodes in TEST_EXCLUDE_NODES
              # (e.g. a GPU node, or one that cannot enforce NetworkPolicy).
              - { key: node-role.kubernetes.io/control-plane, operator: DoesNotExist }
$EXCLUDE_EXPR
  containers:
    - name: go
      image: $GO_IMAGE
      command: [sleep, "3600"]
      workingDir: /src
      env:
        - { name: GOMODCACHE, value: /cache/gomod }
        - { name: GOCACHE, value: /cache/gobuild }
        - { name: GOBIN, value: /cache/bin }
        - { name: TEST_DATABASE_URL, value: "postgres://postgres:test@127.0.0.1:5432/rendimiento" }
      resources:
        # 1 CPU reserved (it bursts to the node's idle cores) so a test run
        # never keeps a BuildKit daemon from being scheduled.
        requests: { cpu: "1", memory: 2Gi }
        limits: { memory: 5Gi }
      volumeMounts: [{ name: src, mountPath: /src }, { name: cache, mountPath: /cache }]
    - name: node
      image: $NODE_IMAGE
      command: [sleep, "3600"]
      env: [{ name: npm_config_cache, value: /cache/npm }]
      resources: { requests: { cpu: 250m, memory: 512Mi } }
      volumeMounts: [{ name: src, mountPath: /src }, { name: cache, mountPath: /cache }]
    - name: postgres
      image: postgres:17-alpine
      env:
        - { name: POSTGRES_PASSWORD, value: test }
        - { name: POSTGRES_DB, value: rendimiento }
      resources: { requests: { cpu: 100m, memory: 128Mi } }
  volumes:
    - { name: src, emptyDir: {} }
    - { name: cache, persistentVolumeClaim: { claimName: $CACHE } }
EOF
trap 'kubectl delete pod -n "$NS" "$POD" --wait=false >/dev/null 2>&1 || true' EXIT

echo "test-remote: starting $POD…"
kubectl wait -n "$NS" --for=condition=Ready "pod/$POD" --timeout=10m >/dev/null
echo "test-remote: on $(kubectl get pod -n "$NS" "$POD" -o jsonpath='{.spec.nodeName}'); shipping the working tree"
git ls-files -co --exclude-standard -z | tar --null -T - -czf - | kubectl exec -i -n "$NS" "$POD" -c go -- tar xzf - -C /src

go_status=0
kubectl exec -n "$NS" "$POD" -c go -- bash -euo pipefail -c "
  export PATH=/cache/bin:\$PATH
  git config --global --add safe.directory /src 2>/dev/null || true
  [ -x /cache/bin/controller-gen ] && controller-gen --version | grep -q $CONTROLLER_GEN || go install sigs.k8s.io/controller-tools/cmd/controller-gen@$CONTROLLER_GEN
  [ -x /cache/bin/setup-envtest ] || go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$SETUP_ENVTEST
  until pg_isready -h 127.0.0.1 >/dev/null 2>&1 || (echo > /dev/tcp/127.0.0.1/5432) 2>/dev/null; do sleep 1; done
  # The built UI is not in git; vet and tests only need something to embed.
  [ -n \"\$(ls -A web/dist 2>/dev/null)\" ] || { mkdir -p web/dist && echo placeholder > web/dist/index.html; }
  echo '== generate'
  controller-gen object paths=./api/... paths=./internal/spec/...
  controller-gen crd paths=./api/... output:crd:dir=deploy/crds
  echo '== vet'
  go vet ./...
  echo '== test'
  KUBEBUILDER_ASSETS=\$(setup-envtest use $ENVTEST_K8S -p path --bin-dir /cache/envtest) go test -p 1 ./...
" || go_status=$?

# Bring back what generate produced, so the repo matches what was tested.
kubectl exec -n "$NS" "$POD" -c go -- tar czf - api/v1alpha1/zz_generated.deepcopy.go internal/spec/zz_generated.deepcopy.go deploy/crds | tar xzf - -C .

ui_status=0
kubectl exec -n "$NS" "$POD" -c node -- bash -euo pipefail -c "
  cd /src/web
  echo '== ui typecheck'
  npm ci --prefer-offline --no-audit --no-fund --loglevel=error
  npm run typecheck
" || ui_status=$?

if [ "$go_status" -ne 0 ] || [ "$ui_status" -ne 0 ]; then
  echo "test-remote: FAILED (go=$go_status ui=$ui_status)"
  exit 1
fi
echo "test-remote: all passed"
