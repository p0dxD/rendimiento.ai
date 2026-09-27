export PATH := $(HOME)/.local/go/bin:$(HOME)/go/bin:$(PATH)
TEST_DATABASE_URL ?= postgres://postgres:test@127.0.0.1:55432/rendimiento
IMAGE ?= registry.example.lan:5000/rendimiento

.PHONY: generate ui build test test-db itest image railpack-image deploy

generate: ## deepcopy + CRD from api/v1alpha1
	controller-gen object paths=./api/... paths=./internal/spec/...
	controller-gen crd paths=./api/... output:crd:dir=deploy/crds

ui:
	cd web && npm ci && npm run build

build: ui
	go build -o rendimiento ./cmd/rendimiento

test-db: ## disposable Postgres for tests
	docker run -d --rm --name rendimiento-testpg -e POSTGRES_PASSWORD=test -e POSTGRES_DB=rendimiento -p 127.0.0.1:55432:5432 postgres:17-alpine

# -p 1: store, platform and api tests share one test database.
# generate first: a stale CRD makes the API server silently drop new fields.
test: generate
	@# envtest control planes left by an interrupted run keep using CPU and disk.
	-@pkill -f '[k]ubebuilder-envtest/k8s/.*/(kube-apiserver|etcd)' ; rm -rf /tmp/k8s_test_framework_*
	go vet ./...
	KUBEBUILDER_ASSETS=$$(setup-envtest use -p path) TEST_DATABASE_URL=$(TEST_DATABASE_URL) go test -p 1 ./...
	cd web && npm run typecheck

itest: ## real build on the cluster's buildkitd
	go test -tags integration ./internal/pipeline -run TestBuildOnCluster -v -timeout 25m

# Build on the cluster's BuildKit (worker3), not on this node: main is also
# the k3s control plane, and a local compile starves its SQLite datastore.
BUILDKIT_PORT ?= 12345
image:
	@kubectl port-forward -n devops-tools svc/buildkitd $(BUILDKIT_PORT):1234 >/dev/null 2>&1 & pf=$$!; \
	trap "kill $$pf 2>/dev/null" EXIT; sleep 3; \
	docker run --rm --network host -v $(CURDIR):/src:ro --entrypoint buildctl moby/buildkit:v0.18.2 \
	  --addr tcp://127.0.0.1:$(BUILDKIT_PORT) build --frontend dockerfile.v0 \
	  --local context=/src --local dockerfile=/src \
	  --output type=image,name=$(IMAGE):latest,push=true,registry.insecure=true \
	  --import-cache type=registry,ref=$(IMAGE):buildcache,registry.insecure=true \
	  --export-cache type=registry,ref=$(IMAGE):buildcache,mode=max,registry.insecure=true

RAILPACK_VERSION ?= 0.40.0
railpack-image: ## the railpack CLI image build pods use (RAILPACK_IMAGE)
	@kubectl port-forward -n devops-tools svc/buildkitd $(BUILDKIT_PORT):1234 >/dev/null 2>&1 & pf=$$!; \
	trap "kill $$pf 2>/dev/null" EXIT; sleep 3; \
	docker run --rm --network host -v $(CURDIR)/deploy/railpack:/src:ro --entrypoint buildctl moby/buildkit:v0.18.2 \
	  --addr tcp://127.0.0.1:$(BUILDKIT_PORT) build --frontend dockerfile.v0 \
	  --local context=/src --local dockerfile=/src --opt build-arg:VERSION=$(RAILPACK_VERSION) \
	  --output type=image,name=$(IMAGE)-railpack:$(RAILPACK_VERSION),push=true,registry.insecure=true

deploy:
	kubectl apply -k deploy
