# Makefile for Hibernator Operator

# Image
IMG ?= ghcr.io/ardikabs/hibernator:latest
RUNNER_IMG ?= ghcr.io/ardikabs/hibernator-runner:latest

# Go
GOBIN ?= $(shell go env GOPATH)/bin
CONTROLLER_GEN ?= $(GOBIN)/controller-gen
ENVTEST ?= $(GOBIN)/setup-envtest

.PHONY: all
all: generate fmt vet build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

##@ Development

.PHONY: generate
generate: controller-gen ## Generate code (DeepCopy, CRDs).
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=config/crd/bases

.PHONY: fmt
fmt: ## Run go fmt.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: test
test: generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use -p path)" go test ./... -coverprofile cover.out

.PHONY: e2e
e2e: generate fmt vet envtest ## Run E2E tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use -p path)" go test ./test/e2e/... -v -ginkgo.v

##@ Build

.PHONY: build
build: generate fmt vet ## Build controller binary.
	go build -o bin/controller ./cmd/controller
	go build -o bin/runner ./cmd/runner

.PHONY: run
run: generate fmt vet ## Run controller from source.
	go run ./cmd/controller

.PHONY: docker-build
docker-build: ## Build docker images.
	docker build -t $(IMG) -f Dockerfile --target controller .
	docker build -t $(RUNNER_IMG) -f Dockerfile --target runner .

.PHONY: docker-push
docker-push: ## Push docker images.
	docker push $(IMG)
	docker push $(RUNNER_IMG)

##@ Deployment

.PHONY: install
install: generate ## Install CRDs into the cluster.
	kubectl apply -f config/crd/bases

.PHONY: uninstall
uninstall: ## Uninstall CRDs from the cluster.
	kubectl delete -f config/crd/bases

.PHONY: deploy
deploy: install ## Deploy controller to the cluster.
	kubectl apply -k config/default

.PHONY: undeploy
undeploy: ## Undeploy controller from the cluster.
	kubectl delete -k config/default

##@ Tools

.PHONY: controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	@test -s $(CONTROLLER_GEN) || go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest

.PHONY: envtest
envtest: ## Download envtest locally if necessary.
	@test -s $(ENVTEST) || go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
