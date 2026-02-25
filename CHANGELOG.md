
<a name="v1.2.0"></a>
## [v1.2.0](https://github.com/ardikabs/hibernator/compare/v1.1.3...v1.2.0) (2026-02-25)

### ✨ Features

* **cmd:** kubectl-hibernator prototyping ([#47](https://github.com/ardikabs/hibernator/issues/47))

### 🐛 Bug Fixes

* **scheduler:** proper schedule boundary handling ([#44](https://github.com/ardikabs/hibernator/issues/44))


<a name="v1.1.3"></a>
## [v1.1.3](https://github.com/ardikabs/hibernator/compare/v1.1.2...v1.1.3) (2026-02-20)


<a name="v1.1.2"></a>
## [v1.1.2](https://github.com/ardikabs/hibernator/compare/v1.1.1...v1.1.2) (2026-02-20)

### 🐛 Bug Fixes

* **scheduler:** proper schedule boundary handling ([#44](https://github.com/ardikabs/hibernator/issues/44))


<a name="v1.1.1"></a>
## [v1.1.1](https://github.com/ardikabs/hibernator/compare/v1.1.0...v1.1.1) (2026-02-14)


<a name="v1.1.0"></a>
## [v1.1.0](https://github.com/ardikabs/hibernator/compare/v1.0.0...v1.1.0) (2026-02-13)

### ✨ Features

* **api:** new semantic for ExecutionOperationSummary status
* **scheduler:** add default schedule buffer to 1-minute
* **scheduler:** support use case for full-day operation


<a name="v1.0.0"></a>
## v1.0.0 (2026-02-12)

### ✨ Features

* initial helm chart release
* support multi-platform build
* fixed runner SA and EKS token signing
* complete the failing scenario, including the restore management
* runner and tests ([#1](https://github.com/ardikabs/hibernator/issues/1))
* readiness check for controller, streaming, and scheduler
* **controller:** improve schedule exception lifecycle and deterministic selection
* **executor:** enrich RDS Parameters to support more usecases
* **executor:** add logger parameter to executors for streaming operations
* **executor:** instead of concrete, change to interface based client
* **executor:** introduce waitConfig semantic for wait on complete scene

### 🐛 Bug Fixes

* use target reference on handle error recovery phase reset
* reconciler infinite loop due to redundant watch from scheduleexception controller
* streaming client didnt stop gracefully during network failure
* workloadselector now adopt kubernetes LabelSelector

### 🛠️ Code Refactoring

* set architectural decision about error handling
* error handling between control plane and data plane (runner)
* await process, rename ID to Id for aws related executor
* refine e2e, replace with more clarity
* decided with simple target naming convention
* most likely refactor the pattern and refine E2E
* **api:** rename waitConfig to awaitCompletion for clearer intention

