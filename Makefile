# Makefile for Hibernator Operator
# ============================================================================

# ============================================================================
# Configuration
# ============================================================================

# Image configuration
IMG ?= ghcr.io/ardikabs/hibernator:latest
RUNNER_IMG ?= ghcr.io/ardikabs/hibernator-runner:latest
PLATFORMS ?= linux/amd64,linux/arm64
GOLANGCI_VERSION ?= 2.8.0

# Go configuration
GOBIN ?= $(shell go env GOPATH)/bin
GOCMD ?= go

# Tool binaries
CONTROLLER_GEN ?= $(GOBIN)/controller-gen
ENVTEST ?= $(GOBIN)/setup-envtest
MOCKERY ?= $(GOBIN)/mockery
PROTOC_GEN_GO ?= $(GOBIN)/protoc-gen-go
PROTOC_GEN_GO_GRPC ?= $(GOBIN)/protoc-gen-go-grpc

# Test configuration
COVERAGE_DIR ?= .coverage
COVERAGE_PROFILE ?= $(COVERAGE_DIR)/coverage.out
COVERAGE_HTML ?= $(COVERAGE_DIR)/coverage.html
COVERAGE_THRESHOLD ?= 50

# Unit test packages (exclude e2e, cmd, and generated files)
UNIT_TEST_PKGS ?= $(shell go list ./... | grep -vE '(/cmd/|/mocks|/test/e2e)')

# Colors for output
CYAN := \033[36m
GREEN := \033[32m
YELLOW := \033[33m
RED := \033[31m
RESET := \033[0m

# ============================================================================
# Default target
# ============================================================================

.PHONY: all
all: generate fmt vet build

# ============================================================================
##@ General
# ============================================================================

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\n$(CYAN)Usage:$(RESET)\n  make $(GREEN)<target>$(RESET)\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  $(GREEN)%-20s$(RESET) %s\n", $$1, $$2 } /^##@/ { printf "\n$(CYAN)%s$(RESET)\n", substr($$0, 5) }' $(MAKEFILE_LIST)

# ============================================================================
##@ Development
# ============================================================================

.PHONY: generate
generate: controller-gen ## Generate code (DeepCopy, CRDs) and sync to Helm chart.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=config/crd/bases
	@echo "$(CYAN)Syncing CRDs to Helm chart...$(RESET)"
	@cp -f config/crd/bases/*.yaml charts/hibernator/crds/
	@echo "$(GREEN)CRDs synced to charts/hibernator/crds/$(RESET)"

.PHONY: generate-proto
generate-proto: protoc-gen-go protoc-gen-go-grpc ## Generate protobuf code from .proto files.
	@echo "$(CYAN)Generating protobuf code...$(RESET)"
	@command -v protoc >/dev/null 2>&1 || { echo "$(RED)Error: protoc not installed. Install from https://grpc.io/docs/protoc-installation/$(RESET)"; exit 1; }
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/streaming/v1alpha1/execution.proto
	@echo "$(GREEN)Protobuf code generated$(RESET)"

.PHONY: clean-proto
clean-proto: ## Clean generated protobuf files.
	@echo "$(CYAN)Cleaning generated protobuf files...$(RESET)"
	@rm -f api/streaming/v1alpha1/*.pb.go
	@echo "$(GREEN)Protobuf files cleaned$(RESET)"

.PHONY: fmt
fmt: ## Run go fmt.
	$(GOCMD) fmt ./...

.PHONY: vet
vet: ## Run go vet.
	$(GOCMD) vet ./...

bin/golangci-lint: bin/golangci-lint-${GOLANGCI_VERSION}
	@ln -sf golangci-lint-${GOLANGCI_VERSION} bin/golangci-lint

bin/golangci-lint-${GOLANGCI_VERSION}:
	@mkdir -p bin
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b bin v$(GOLANGCI_VERSION)
	@mv bin/golangci-lint "$@"

.PHONY: lint
lint: bin/golangci-lint ## Run golangci-lint (requires golangci-lint installed).
	@echo 'Linting code...'
	@bin/golangci-lint run

.PHONY: tidy
tidy: ## Run go mod tidy.
	$(GOCMD) mod tidy

.PHONY: verify
verify: generate fmt vet ## Verify code is properly formatted and generated.
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "$(RED)Error: Working directory is dirty after generate/fmt$(RESET)"; \
		git status --porcelain; \
		exit 1; \
	fi

# ============================================================================
##@ Testing
# ============================================================================

.PHONY: test
test: test-unit ## Run unit tests with coverage
	@echo ""
	@echo "$(CYAN)Coverage Summary:$(RESET)"
	@$(GOCMD) tool cover -func=$(COVERAGE_PROFILE) | tail -1
	@echo ""
	@echo "$(GREEN)Coverage report saved to: $(COVERAGE_PROFILE)$(RESET)"
	@echo "$(CYAN)Checking coverage threshold ($(COVERAGE_THRESHOLD)%)...$(RESET)"
	@echo ""
	@COVERAGE=$$($(GOCMD) tool cover -func=$(COVERAGE_PROFILE) | tail -1 | awk '{print $$3}' | sed 's/%//'); \
	if [ $$(echo "$$COVERAGE < $(COVERAGE_THRESHOLD)" | bc -l) -eq 1 ]; then \
		echo "$(RED)Coverage $$COVERAGE% is below threshold $(COVERAGE_THRESHOLD)%$(RESET)"; \
		exit 1; \
	else \
		echo "$(GREEN)Coverage $$COVERAGE% meets threshold $(COVERAGE_THRESHOLD)%$(RESET)"; \
	fi

.PHONY: test-unit
test-unit: $(COVERAGE_DIR) ## Run unit tests.
	@echo "$(CYAN)Running unit tests...$(RESET)"
	@$(GOCMD) test $(UNIT_TEST_PKGS) \
		-race \
		-cover \
		-coverprofile=$(COVERAGE_PROFILE) \
		-covermode=atomic \
		-v 2>&1

.PHONY: test-all
test-all: test test-e2e ## Run all tests (unit + e2e).

.PHONY: test-e2e
test-e2e: envtest ## Run E2E tests.
	@echo "$(CYAN)Running E2E tests...$(RESET)"
	$(GOCMD) test ./test/e2e/... -v -tags=e2e -ginkgo.v

.PHONY: test-pkg
test-pkg: ## Run tests for a specific package. Usage: make test-pkg PKG=./internal/scheduler/...
	@if [ -z "$(PKG)" ]; then \
		echo "$(RED)Error: PKG is required. Usage: make test-pkg PKG=./internal/scheduler/...$(RESET)"; \
		exit 1; \
	fi
	@echo "$(CYAN)Running tests for $(PKG)...$(RESET)"
	@$(GOCMD) test $(PKG) -v -race -coverprofile=$(COVERAGE_DIR)/pkg-coverage.out
	@$(GOCMD) tool cover -func=$(COVERAGE_DIR)/pkg-coverage.out | tail -1

$(COVERAGE_DIR):
	@mkdir -p $(COVERAGE_DIR)

.PHONY: clean-coverage
clean-coverage: ## Clean coverage files.
	@rm -rf $(COVERAGE_DIR)
	@echo "$(GREEN)Coverage files cleaned$(RESET)"

# ============================================================================
##@ Build
# ============================================================================

.PHONY: build
build: generate fmt vet ## Build controller and runner binaries.
	@echo "$(CYAN)Building binaries...$(RESET)"
	$(GOCMD) build -o bin/controller ./cmd/controller
	$(GOCMD) build -o bin/runner ./cmd/runner
	@echo "$(GREEN)Binaries built: bin/controller, bin/runner$(RESET)"

.PHONY: build-controller
build-controller: ## Build controller binary only.
	$(GOCMD) build -o bin/controller ./cmd/controller

.PHONY: build-runner
build-runner: ## Build runner binary only.
	$(GOCMD) build -o bin/runner ./cmd/runner

.PHONY: run
run: generate fmt vet ## Run controller from source.
	$(GOCMD) run ./cmd/controller

.PHONY: docker-build
docker-build: docker-build-controller docker-build-runner ## Build all docker images and push to registry.

.PHONY: docker-build-controller
docker-build-controller: ## Build controller docker image and push to registry.
	@echo "$(CYAN)Building Controller Docker image...$(RESET)"
	docker buildx build --push -t $(IMG) --platform $(PLATFORMS) -f Dockerfile --target controller .
	@echo "$(GREEN)Controller image built: $(IMG)$(RESET)"

.PHONY: docker-build-runner
docker-build-runner: ## Build runner docker image and push to registry.
	@echo "$(CYAN)Building Runner Docker image...$(RESET)"
	docker buildx build --push -t $(RUNNER_IMG) --platform $(PLATFORMS) -f Dockerfile --target runner .
	@echo "$(GREEN)Runner image built: $(RUNNER_IMG)$(RESET)"

.PHONY: clean
clean: clean-coverage ## Clean build artifacts and coverage files.
	@rm -rf bin/
	@echo "$(GREEN)Build artifacts cleaned$(RESET)"

# ============================================================================
##@ Deployment
# ============================================================================

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

# ============================================================================
##@ Tools
# ============================================================================

.PHONY: controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	@test -s $(CONTROLLER_GEN) || { \
		echo "$(CYAN)Installing controller-gen...$(RESET)"; \
		go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest; \
	}

.PHONY: envtest
envtest: ## Download envtest locally if necessary.
	@test -s $(ENVTEST) || { \
		echo "$(CYAN)Installing setup-envtest...$(RESET)"; \
		go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest; \
	}

.PHONY: mockery
mockery: ## Download mockery locally if necessary.
	@test -s $(MOCKERY) || { \
		echo "$(CYAN)Installing mockery...$(RESET)"; \
		go install github.com/vektra/mockery/v2@latest; \
	}

.PHONY: protoc-gen-go
protoc-gen-go: ## Download protoc-gen-go locally if necessary.
	@test -s $(PROTOC_GEN_GO) || { \
		echo "$(CYAN)Installing protoc-gen-go...$(RESET)"; \
		go install google.golang.org/protobuf/cmd/protoc-gen-go@latest; \
	}

.PHONY: protoc-gen-go-grpc
protoc-gen-go-grpc: ## Download protoc-gen-go-grpc locally if necessary.
	@test -s $(PROTOC_GEN_GO_GRPC) || { \
		echo "$(CYAN)Installing protoc-gen-go-grpc...$(RESET)"; \
		go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest; \
	}

.PHONY: mocks-eks
mocks-eks: mockery ## Generate mocks for EKS executor clients.
	@echo "$(CYAN)Generating mocks for EKS executor...$(RESET)"
	$(MOCKERY) --name=EKSClient --dir=./internal/executor/eks --output=./internal/executor/eks/mocks --outpkg=mocks
	$(MOCKERY) --name=STSClient --dir=./internal/executor/eks --output=./internal/executor/eks/mocks --outpkg=mocks
	$(MOCKERY) --name=K8SClient --dir=./internal/executor/eks --output=./internal/executor/eks/mocks --outpkg=mocks
	@echo "$(GREEN)EKS mocks generated$(RESET)"

.PHONY: mocks-ec2
mocks-ec2: mockery ## Generate mocks for EC2 executor client.
	@echo "$(CYAN)Generating mocks for EC2 executor...$(RESET)"
	$(MOCKERY) --name=EC2Client --dir=./internal/executor/ec2 --output=./internal/executor/ec2/mocks --outpkg=mocks
	@echo "$(GREEN)EC2 mocks generated$(RESET)"

.PHONY: mocks-karpenter
mocks-karpenter: mockery ## Generate mocks for Karpenter executor client.
	@echo "$(CYAN)Generating mocks for Karpenter executor...$(RESET)"
	$(MOCKERY) --name=Client --dir=./internal/executor/karpenter --output=./internal/executor/karpenter/mocks --outpkg=mocks
	@echo "$(GREEN)Karpenter mocks generated$(RESET)"

.PHONY: mocks-rds
mocks-rds: mockery ## Generate mocks for RDS executor clients.
	@echo "$(CYAN)Generating mocks for RDS executor...$(RESET)"
	$(MOCKERY) --name=RDSClient --dir=./internal/executor/rds --output=./internal/executor/rds/mocks --outpkg=mocks
	$(MOCKERY) --name=STSClient --dir=./internal/executor/rds --output=./internal/executor/rds/mocks --outpkg=mocks
	@echo "$(GREEN)RDS mocks generated$(RESET)"

.PHONY: mocks-all
mocks-all: mocks-eks mocks-ec2 mocks-karpenter mocks-rds ## Generate all executor mocks.
	@echo "$(GREEN)All mocks generated successfully$(RESET)"

.PHONY: tools
tools: controller-gen envtest mockery protoc-gen-go protoc-gen-go-grpc ## Install all required tools.
