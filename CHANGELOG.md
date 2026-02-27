<a name="v1.3.0"></a>

## [v1.3.0](https://github.com/ardikabs/hibernator/compare/v1.2.1...v1.3.0) (2026-02-27)

### ✨ Features

* **cmd:** kubectl-hibernator more subcommand ([#51](https://github.com/ardikabs/hibernator/issues/51))

### 🐛 Bug Fixes

* recovery attempt handling and status updater with exclusion support ([#58](https://github.com/ardikabs/hibernator/issues/58))
* **executor:** handle notfound and alreadyexists error ([#54](https://github.com/ardikabs/hibernator/issues/54))

### 🛠️ Code Refactoring

* move validationwebhook to internal ([#57](https://github.com/ardikabs/hibernator/issues/57))


<a name="v1.2.1"></a>

## [v1.2.1](https://github.com/ardikabs/hibernator/compare/v1.2.0...v1.2.1) (2026-02-26)

### 🐛 Bug Fixes

* **executor:** handle notfound and alreadyexists error ([#54](https://github.com/ardikabs/hibernator/issues/54))


<a name="v1.2.0"></a>

## [v1.2.0](https://github.com/ardikabs/hibernator/compare/v1.1.3...v1.2.0) (2026-02-25)

### ✨ Features

* **cmd:** kubectl-hibernator prototyping ([#47](https://github.com/ardikabs/hibernator/issues/47))

### 🐛 Bug Fixes

* **internal:** handling RDS operation start and stop ([#43](https://github.com/ardikabs/hibernator/issues/43))
* **schedule:** add validation for same window ([#35](https://github.com/ardikabs/hibernator/issues/35))
* **scheduler:** proper schedule boundary handling ([#44](https://github.com/ardikabs/hibernator/issues/44))
* **webhook:** 1-minute wakeup warn on validation webhook ([#46](https://github.com/ardikabs/hibernator/issues/46))

### 🧹 Miscellaneous

* pipe all output to stderr for sync-version

<a name="v1.1.3"></a>

## [v1.1.3](https://github.com/ardikabs/hibernator/compare/v1.1.2...v1.1.3) (2026-02-20)

### 🐛 Bug Fixes

* **webhook:** 1-minute wakeup warn on validation webhook ([#46](https://github.com/ardikabs/hibernator/issues/46))


<a name="v1.1.2"></a>

## [v1.1.2](https://github.com/ardikabs/hibernator/compare/v1.1.1...v1.1.2) (2026-02-20)

### 🐛 Bug Fixes

* **internal:** handling RDS operation start and stop ([#43](https://github.com/ardikabs/hibernator/issues/43))
* **scheduler:** proper schedule boundary handling ([#44](https://github.com/ardikabs/hibernator/issues/44))

### 🧹 Miscellaneous

* pipe all output to stderr for sync-version

<a name="v1.1.1"></a>

## [v1.1.1](https://github.com/ardikabs/hibernator/compare/v1.1.0...v1.1.1) (2026-02-14)

### 🐛 Bug Fixes

* **schedule:** add validation for same window ([#35](https://github.com/ardikabs/hibernator/issues/35)) ([#36](https://github.com/ardikabs/hibernator/issues/36))


<a name="v1.1.0"></a>

## [v1.1.0](https://github.com/ardikabs/hibernator/compare/v1.0.0...v1.1.0) (2026-02-13)

### ✨ Features

* **api:** new semantic for ExecutionOperationSummary status
* **scheduler:** add default schedule buffer to 1-minute
* **scheduler:** support use case for full-day operation


### 🧹 Miscellaneous

* using make test instead of test-unit
* fail-fast on failing unit test
* bump README.md (for release)

<a name="v1.0.0"></a>

## v1.0.0 (2026-02-12)

### ✨ Features

* readiness check for controller, streaming, and scheduler
* support multi-platform build
* fixed runner SA and EKS token signing
* complete the failing scenario, including the restore management
* initial helm chart release
* runner and tests ([#1](https://github.com/ardikabs/hibernator/issues/1))
* **controller:** improve schedule exception lifecycle and deterministic selection
* **executor:** enrich RDS Parameters to support more usecases
* **executor:** add logger parameter to executors for streaming operations
* **executor:** introduce waitConfig semantic for wait on complete scene
* **executor:** instead of concrete, change to interface based client
* **restore:** implement quality-aware data preservation with IsLive tracking
* **schedule:** implement ScheduleException CRD (RFC-0003)
* **streaming:** implement DualWriteSink with async log streaming and immediate-send streaming clients

### 🐛 Bug Fixes

* use target reference on handle error recovery phase reset
* reconciler infinite loop due to redundant watch from scheduleexception controller
* streaming client didnt stop gracefully during network failure
* workloadselector now adopt kubernetes LabelSelector
* **schedule:** fix schedule to not shift the next day, also add time/tzdata for time zone awareness

### 🛠️ Code Refactoring

* set architectural decision about error handling
* error handling between control plane and data plane (runner)
* await process, rename ID to Id for aws related executor
* refine e2e, replace with more clarity
* decided with simple target naming convention
* most likely refactor the pattern and refine E2E
* **api:** rename waitConfig to awaitCompletion for clearer intention
* **streaming:** decouple RestoreManager from controller
* **streaming:** emit logs with execution context and cache metadata

### 🧹 Miscellaneous

* core readiness check
* default value for image
* bump actions version
* fix lint
* add docker build for specific component
* move controller package to each context, add more unit test coverage
* bump dependencies
* Major project reorganization, documentation, and implementation updates

