# Makefile for Hibernator Operator
# ============================================================================

# ============================================================================
# Configuration
# ============================================================================

# Image configuration
IMG ?= ghcr.io/ardikabs/hibernator:latest
RUNNER_IMG ?= ghcr.io/ardikabs/hibernator-runner:latest

# Go configuration
GOBIN ?= $(shell go env GOPATH)/bin
GOCMD ?= go

# Tool binaries
CONTROLLER_GEN ?= $(GOBIN)/controller-gen
ENVTEST ?= $(GOBIN)/setup-envtest

# Test configuration
COVERAGE_DIR ?= .coverage
COVERAGE_PROFILE ?= $(COVERAGE_DIR)/coverage.out
COVERAGE_HTML ?= $(COVERAGE_DIR)/coverage.html
COVERAGE_THRESHOLD ?= 50

# Unit test packages (exclude e2e, cmd, and generated files)
UNIT_TEST_PKGS ?= ./api/... ./internal/...

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
generate: controller-gen ## Generate code (DeepCopy, CRDs).
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=config/crd/bases

.PHONY: fmt
fmt: ## Run go fmt.
	$(GOCMD) fmt ./...

.PHONY: vet
vet: ## Run go vet.
	$(GOCMD) vet ./...

.PHONY: lint
lint: ## Run golangci-lint (requires golangci-lint installed).
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; exit 1; }
	golangci-lint run ./...

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
test: test-unit ## Alias for test-unit.

.PHONY: test-unit
test-unit: envtest $(COVERAGE_DIR) ## Run unit tests with coverage.
	@echo "$(CYAN)Running unit tests...$(RESET)"
	@KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use -p path)" \
		$(GOCMD) test $(UNIT_TEST_PKGS) \
		-race \
		-coverprofile=$(COVERAGE_PROFILE) \
		-covermode=atomic \
		-v 2>&1 | grep -E '(^=== RUN|^--- PASS|^--- FAIL|^PASS|^FAIL|coverage:)'
	@echo ""
	@echo "$(CYAN)Coverage Summary:$(RESET)"
	@$(GOCMD) tool cover -func=$(COVERAGE_PROFILE) | tail -1
	@echo ""
	@echo "$(GREEN)Coverage report saved to: $(COVERAGE_PROFILE)$(RESET)"

.PHONY: test-unit-verbose
test-unit-verbose: envtest $(COVERAGE_DIR) ## Run unit tests with verbose output.
	@echo "$(CYAN)Running unit tests (verbose)...$(RESET)"
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use -p path)" \
		$(GOCMD) test $(UNIT_TEST_PKGS) \
		-race \
		-coverprofile=$(COVERAGE_PROFILE) \
		-covermode=atomic \
		-v

.PHONY: test-coverage
test-coverage: test-unit ## Run unit tests and generate HTML coverage report.
	@echo "$(CYAN)Generating HTML coverage report...$(RESET)"
	@$(GOCMD) tool cover -html=$(COVERAGE_PROFILE) -o $(COVERAGE_HTML)
	@echo "$(GREEN)HTML coverage report: $(COVERAGE_HTML)$(RESET)"
	@open $(COVERAGE_HTML) 2>/dev/null || xdg-open $(COVERAGE_HTML) 2>/dev/null || echo "Open $(COVERAGE_HTML) in your browser"

.PHONY: test-coverage-func
test-coverage-func: test-unit ## Run unit tests and show per-function coverage.
	@echo ""
	@echo "$(CYAN)Per-function coverage:$(RESET)"
	@$(GOCMD) tool cover -func=$(COVERAGE_PROFILE)

.PHONY: test-coverage-check
test-coverage-check: test-unit ## Check if coverage meets threshold (default: 50%).
	@echo ""
	@echo "$(CYAN)Checking coverage threshold ($(COVERAGE_THRESHOLD)%)...$(RESET)"
	@COVERAGE=$$($(GOCMD) tool cover -func=$(COVERAGE_PROFILE) | tail -1 | awk '{print $$3}' | sed 's/%//'); \
	if [ $$(echo "$$COVERAGE < $(COVERAGE_THRESHOLD)" | bc -l) -eq 1 ]; then \
		echo "$(RED)Coverage $$COVERAGE% is below threshold $(COVERAGE_THRESHOLD)%$(RESET)"; \
		exit 1; \
	else \
		echo "$(GREEN)Coverage $$COVERAGE% meets threshold $(COVERAGE_THRESHOLD)%$(RESET)"; \
	fi

.PHONY: test-all
test-all: test-unit test-e2e ## Run all tests (unit + e2e).

.PHONY: test-e2e
test-e2e: envtest ## Run E2E tests.
	@echo "$(CYAN)Running E2E tests...$(RESET)"
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use -p path)" \
		$(GOCMD) test ./test/e2e/... -v -ginkgo.v

.PHONY: test-pkg
test-pkg: envtest ## Run tests for a specific package. Usage: make test-pkg PKG=./internal/scheduler/...
	@if [ -z "$(PKG)" ]; then \
		echo "$(RED)Error: PKG is required. Usage: make test-pkg PKG=./internal/scheduler/...$(RESET)"; \
		exit 1; \
	fi
	@echo "$(CYAN)Running tests for $(PKG)...$(RESET)"
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use -p path)" \
		$(GOCMD) test $(PKG) -v -race -coverprofile=$(COVERAGE_DIR)/pkg-coverage.out
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
docker-build: ## Build docker images.
	@echo "$(CYAN)Building Docker images...$(RESET)"
	docker build -t $(IMG) -f Dockerfile --target controller .
	docker build -t $(RUNNER_IMG) -f Dockerfile --target runner .
	@echo "$(GREEN)Images built: $(IMG), $(RUNNER_IMG)$(RESET)"

.PHONY: docker-push
docker-push: ## Push docker images.
	docker push $(IMG)
	docker push $(RUNNER_IMG)

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

.PHONY: tools
tools: controller-gen envtest ## Install all required tools.
