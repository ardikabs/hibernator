# kind Full-Chain E2E Suite

Real end-to-end on a real cluster: controller deployed on kind → real runner
pods executing noop targets. No cloud backend is involved — executor/cloud
behavior lives in `test/awsenv`. All validation lives in Go.

## Layout

```bash
test/kind/
├── README.md                # This file
├── kind_test.go             # TestMain + TestNoopScheduleCycle (tests only)
├── scenarios_test.go        # TestScenarios + TestScenarioFilesAreValid (tests only)
├── setup_test.go            # Cluster-free unit tests (KIND_UNIT_ONLY=1)
├── suite/                   # Support package: no Test functions, only helpers
│   ├── setup.go             # Cluster, images, deploy, routing, shared clients
│   ├── webhookcert.go       # Self-signed webhook cert (server idles)
│   ├── bundle.go            # Scenario YAML loading + plan/exception helpers
│   ├── steps.go             # Stable hibernate/wakeup step interface + asserts
│   └── testdata/
│       └── scenarios/       # One complete input file per docs scenario
│           ├── nightly-full-shutdown.yaml
│           ├── ... (13 files, one per website/docs/scenarios page)
└── manifests/
    └── controller-clusterrole.yaml  # Full RBAC copy (see Troubleshooting)
```

File conventions: `test/kind` holds tests only (`Test*` functions);
everything else lives in `test/kind/suite` and is imported as `suite.X`.
Go forbids the reverse — a regular file can never reference identifiers
declared in a `_test.go` file — so shared clients live in `suite`
(provisioned once by TestMain) and every helper receives what it needs as
a parameter, never via out-of-scope variables.

## Architecture

- **Noop targets only**: every scenario uses the noop executor through a
  dummy `CloudProvider`, so no cloud credentials, endpoints, or emulators
  are needed. Real executor behavior is covered by `test/awsenv`.
- **Runner pods** use the plain `hibernator-runner:kind` image built from
  the local tree.
- **Every run is isolated** with a unique suite-owned kind cluster, image
  tags, namespace, and resource names. Shared-cluster reuse is intentionally
  unsupported because the suite installs cluster-wide CRDs and RBAC.
- **Controller** runs with `--leader-elect=false --enable-streaming=false
  --runner-image=hibernator-runner:kind
  --control-plane-endpoint=127.0.0.1` and a mounted self-signed webhook
  cert (no ValidatingWebhookConfiguration — the webhook server idles).
- **Runner RBAC**: SA + RoleBinding→ClusterRole `hibernator-runner` are
  created per test namespace (in prod Helm owns these).

## Scenario tests

`TestScenarios` covers every `website/docs/scenarios` page with all targets
mocked by the noop executor (cloud behavior lives in `test/awsenv`).
Each scenario is a complete input file — plan plus exceptions — mirroring
its docs page; the Go test only drives the flow and asserts the contract:

1. Add `suite/testdata/scenarios/<docs-slug>.yaml` with the plan and any
   exceptions (deterministic noop `marker` per target, connector `scn-aws`).
   Omit `schedule` for an inactive window, or `validFrom`/`validUntil` to
   anchor validity to now-5m/now+55m.
2. Add a slim subtest in `scenarios_test.go` using `suite.LoadScenario`,
   `suite.RunHibernateStep` / `suite.RunWakeupStep` with
   `suite.HibernateStepExpectation` / `suite.WakeupStepExpectation`
   (ledger states, Jobs, ordering, markers, messages). Keep custom flows
   (suspend suppression, subset wakeup) inline in the subtest.
3. Extend `TestScenarioFilesAreValid` coverage automatically — it parses
   every file without a cluster.

Two ordering rules the controller enforces, so the tests obey them:
exceptions are created only after their plan exists (the exception
lifecycle works off plan contexts; a plan-less exception never becomes
`Active`), and disabled targets assert `StateSkipped` with zero Jobs.

## Running

```bash
make test-kind            # immediate override-driven full-chain PR smoke
make test-kind-schedule   # also run the real wall-clock schedule cycle (nightly)
```

Keep a suite-owned cluster for debugging: `KEEP_KIND=1 make test-kind`.
Keep it only on failure: `KEEP_KIND_ON_FAILURE=1 make test-kind`.
(Point kubectl at the printed kubeconfig.)

Teardown: the suite only deletes clusters it created, unless `KEEP_KIND=1`.

Use `KIND_UNIT_ONLY=1` only for the pure helper tests.

## Troubleshooting

- `bin/kind missing`: `make kind-tool`.
- Runner Job stuck: `kubectl logs -n <test-ns> -l hibernator.ardikabs.com/plan=<plan>`.
- Plan stuck with empty phase: the controller can't list/watch something —
  read controller logs for `is forbidden`. The ClusterRole embedded in
  `config/manager/manager.yaml` is stale (missing scheduleexceptions,
  hibernatenotifications, pods, leases, ...); the suite applies
  `test/kind/manifests/controller-clusterrole.yaml` (mirrors the Helm
  chart) over it. If the chart gains verbs, update that copy.
- `log.SetLogger(...) was never called` stacks in test output: harmless
  controller-runtime diagnostic when the API server returns warning headers
  (e.g. deprecated `v1 Endpoints`); silenced via `ctrl.SetLogger` in TestMain.
- `make deploy` is broken upstream (references nonexistent `config/default`);
  the suite applies explicit manifest paths instead.
