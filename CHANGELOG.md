<a name="v1.6.1"></a>

## [v1.6.1](https://github.com/ardikabs/hibernator/compare/v1.6.0...v1.6.1) (2026-05-23)

### 🐛 Bug Fixes

* prevent concurrent map read/write when marking jobs stale ([#160](https://github.com/ardikabs/hibernator/issues/160))
* suspension during state selection ([#161](https://github.com/ardikabs/hibernator/issues/161))

<a name="v1.6.0"></a>

## [v1.6.0](https://github.com/ardikabs/hibernator/compare/v1.5.0...v1.6.0) (2026-05-22)

### ✨ Features

* override-until and cli support ([#155](https://github.com/ardikabs/hibernator/issues/155))
* new TagSelector for AWS-related executors ([#151](https://github.com/ardikabs/hibernator/issues/151))
* add cache package and revamp notif ratelimit ([#146](https://github.com/ardikabs/hibernator/issues/146))
* implement caching for notification state ([#145](https://github.com/ardikabs/hibernator/issues/145))
* support ratelimiter with RPM ([#142](https://github.com/ardikabs/hibernator/issues/142))
* introduce ratelimiter for notification sink ([#140](https://github.com/ardikabs/hibernator/issues/140))
* make timestamp output to be local aware ([#135](https://github.com/ardikabs/hibernator/issues/135))
* revamp data model with Strategy pattern ([#131](https://github.com/ardikabs/hibernator/issues/131))
* handle pending state on exec/rds ([#129](https://github.com/ardikabs/hibernator/issues/129))
* introduce cycle id tracker in restore point for idempotency handling ([#128](https://github.com/ardikabs/hibernator/issues/128))
* cli restore subcommand with stale info ([#127](https://github.com/ardikabs/hibernator/issues/127))
* refactor restore data flow on runner ([#126](https://github.com/ardikabs/hibernator/issues/126))
* using keyedworker pool for notification handling ([#121](https://github.com/ardikabs/hibernator/issues/121))
* sink slack progression on delivery mode of thread ([#115](https://github.com/ardikabs/hibernator/issues/115))
* sink slack with custom delivery mode ([#114](https://github.com/ardikabs/hibernator/issues/114))
* sink slack with json format for blocks support ([#112](https://github.com/ardikabs/hibernator/issues/112))
* add new event for per target execution progress notification ([#107](https://github.com/ardikabs/hibernator/issues/107))
* implement HibernateNotification CRD feature ([#96](https://github.com/ardikabs/hibernator/issues/96))
* add Connector context to the notification payload ([#102](https://github.com/ardikabs/hibernator/issues/102))
* unifying validation webhook path ([#97](https://github.com/ardikabs/hibernator/issues/97))
* notification lifecycle processor and status callback ([#98](https://github.com/ardikabs/hibernator/issues/98))
* **cmd:** add notification subcommand ([#101](https://github.com/ardikabs/hibernator/issues/101))
* **executor:** propagate success execution message ([#100](https://github.com/ardikabs/hibernator/issues/100))

### 🐛 Bug Fixes

* idempotency handling for deletion-based executor (Karpenter) ([#154](https://github.com/ardikabs/hibernator/issues/154))
* add poke helper for updated timer ([#152](https://github.com/ardikabs/hibernator/issues/152))
* worker deadline and idle timer ([#150](https://github.com/ardikabs/hibernator/issues/150))
* use bigger retry attempts to align with send timeout ([#149](https://github.com/ardikabs/hibernator/issues/149))
* use LinearJitterBackoff and add retry max capacity ([#148](https://github.com/ardikabs/hibernator/issues/148))
* ensure idempotency and consistency in ratelimiter ([#144](https://github.com/ardikabs/hibernator/issues/144))
* implement ratelimit in transport level instead of operation ([#143](https://github.com/ardikabs/hibernator/issues/143))
* extend retry and timeout on dispatch during Send notification operation ([#139](https://github.com/ardikabs/hibernator/issues/139))
* ensure auth validation is coming from runner SA ([#138](https://github.com/ardikabs/hibernator/issues/138))
* [exec/workloadscaler] idempotency on shutdown ([#133](https://github.com/ardikabs/hibernator/issues/133))
* clarify between capturedAt and reportedAt on restore ([#134](https://github.com/ardikabs/hibernator/issues/134))
* job should be marked stale on terminal state ([#132](https://github.com/ardikabs/hibernator/issues/132))
* ensure correct data format passed for execution state ([#130](https://github.com/ardikabs/hibernator/issues/130))
* [exec/ec2] ensure restore point captures all instances regardless of state ([#123](https://github.com/ardikabs/hibernator/issues/123))
* inconsistent state handling ([#122](https://github.com/ardikabs/hibernator/issues/122))
* provide clear information on logs command ([#119](https://github.com/ardikabs/hibernator/issues/119))
* EC2 executor tolerate on missing instance ([#118](https://github.com/ardikabs/hibernator/issues/118))
* race condition between execution progress and completed event ([#117](https://github.com/ardikabs/hibernator/issues/117))
* internal cmd error handler ([#110](https://github.com/ardikabs/hibernator/issues/110))
* adjust notification lifecycle ([#106](https://github.com/ardikabs/hibernator/issues/106))
* execution summary also record on error phase ([#103](https://github.com/ardikabs/hibernator/issues/103))
* fix and refactor planner especially DAG for reverse ([#95](https://github.com/ardikabs/hibernator/issues/95))

### 🛠️ Code Refactoring

* restore manager single initialization ([#136](https://github.com/ardikabs/hibernator/issues/136))
* executor ec2 on wakeup flow to be idempotent ([#120](https://github.com/ardikabs/hibernator/issues/120))
* outcome messaging with domain-friendly message that include fix to consider stale resource during restore ([#116](https://github.com/ardikabs/hibernator/issues/116))
* slack auto layout ([#113](https://github.com/ardikabs/hibernator/issues/113))
* refine execution message on running and failure recovery ([#105](https://github.com/ardikabs/hibernator/issues/105))

### 🧹 Miscellaneous

* remove logger from timerset (notify) activity ([#153](https://github.com/ardikabs/hibernator/issues/153))
* adjust log format on ratelimiter ([#147](https://github.com/ardikabs/hibernator/issues/147))
* minor correctness ([#125](https://github.com/ardikabs/hibernator/issues/125))
* cosmetic change like adding TODO marker ([#109](https://github.com/ardikabs/hibernator/issues/109))

<a name="v1.5.0"></a>

## [v1.5.0](https://github.com/ardikabs/hibernator/compare/v1.4.1...v1.5.0) (2026-03-27)

### ✨ Features

* removing old controller as well as --legacy-controller flag ([#82](https://github.com/ardikabs/hibernator/issues/82))
* new subcommand for override and restart action ([#79](https://github.com/ardikabs/hibernator/issues/79))
* introduce override state handling ([#78](https://github.com/ardikabs/hibernator/issues/78))
* **runner:** guarantee restore point on no-op shutdown + pipeline tests ([#76](https://github.com/ardikabs/hibernator/issues/76))

### 🐛 Bug Fixes

* the IN attribute in preview should be relative to user time ([#81](https://github.com/ardikabs/hibernator/issues/81))
* **executor:** empty restore point is considered no-op ([#75](https://github.com/ardikabs/hibernator/issues/75))
* **executor:** ignore notfound error on List API ([#70](https://github.com/ardikabs/hibernator/issues/70))
* **scheduler:** advance next event times past exception window boundaries ([#87](https://github.com/ardikabs/hibernator/issues/87))

### 🛠️ Code Refactoring

* DAG execution with depedency check ([#86](https://github.com/ardikabs/hibernator/issues/86))
* enhance provider reconciler  ([#83](https://github.com/ardikabs/hibernator/issues/83))
* **scheduler:** add support for multi schedule exception ([#85](https://github.com/ardikabs/hibernator/issues/85))

### 🧹 Miscellaneous

* rename internal patch to a clearer name and avoid using Error logs for handler terminal ([#80](https://github.com/ardikabs/hibernator/issues/80))
* refine recovery log ([#69](https://github.com/ardikabs/hibernator/issues/69))

<a name="v1.4.1"></a>

## [v1.4.1](https://github.com/ardikabs/hibernator/compare/v1.4.0...v1.4.1) (2026-03-09)

### 🐛 Bug Fixes

* **executor:** ignore notfound error on List API ([#70](https://github.com/ardikabs/hibernator/issues/70))


<a name="v1.4.0"></a>

## [v1.4.0](https://github.com/ardikabs/hibernator/compare/v1.3.2...v1.4.0) (2026-03-09)

### ✨ Features

* **controller:** Async Phase-Driven Reconciler ([#60](https://github.com/ardikabs/hibernator/issues/60))

### 🐛 Bug Fixes

* schedule evaluation off the evaluation for suspend exception ([#61](https://github.com/ardikabs/hibernator/issues/61))
* **cmd:** list and preview subcommand should consider exception ([#62](https://github.com/ardikabs/hibernator/issues/62))

### 🧹 Miscellaneous

* **controller:** remove unused metrics and better labeling ([#66](https://github.com/ardikabs/hibernator/issues/66))
* **controller:** integrate metrics to the controller ([#65](https://github.com/ardikabs/hibernator/issues/65))

<a name="v1.3.2"></a>

## [v1.3.2](https://github.com/ardikabs/hibernator/compare/v1.3.1...v1.3.2) (2026-03-09)

### 🐛 Bug Fixes

* **executor:** ignore notfound error on List API ([#70](https://github.com/ardikabs/hibernator/issues/70))


<a name="v1.3.1"></a>

## [v1.3.1](https://github.com/ardikabs/hibernator/compare/v1.3.0...v1.3.1) (2026-03-09)

### 🐛 Bug Fixes

* schedule evaluation off the evaluation for suspend exception ([#61](https://github.com/ardikabs/hibernator/issues/61))
* **cmd:** list and preview subcommand should consider exception ([#62](https://github.com/ardikabs/hibernator/issues/62))


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

