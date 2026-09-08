# Hibernator Test Layers

Three suites, three contracts. Each proves something the others cannot, and
the names describe *how much of the system the test is allowed to know about*
— not how "real" it is.

| Suite | Name | What it proves |
|---|---|---|
| `test/e2e` | Controller integration tests (whitebox e2e) | Controller state machine: schedule evaluation, phase transitions, Job dispatch, restore bookkeeping — with fakes *inside* the system boundary (fake clock, simulated Job success). |
| `test/awsenv` | AWS integration tests (runner + executor debugging) | Runner and platform executors in detail: real executor code + real AWS SDK calls against emulated AWS (Floci today, real AWS later via the same contract). Asserts intermediate steps — discovered resources, restore payloads, `wasRunning` flags, messages, cloud-side states. |
| `test/kind` | Blackbox e2e (acceptance) | The whole system on a real cluster, outcome only: CRDs in, expected phases and restore data out, with real controller and noop runner pods. No cloud backend involved. Proves functionality matches expectation, period. |

## Why "simulation" for awsenv

"Integration test" is the test *type*: our executors integrated with a real
AWS API (real SDK, real wire protocol — nothing mocked at that boundary).
"Simulated" describes how the dependency is *provided*: Floci emulates the
server side. That distinction is the whole point of the suite: when the
backend swaps from Floci to real AWS, the same tests must pass unchanged.
`AWSENV_PROVIDER` selects the backend; executor tests never branch on it.

## Layer contracts

- **whitebox (`test/e2e`)** — Ginkgo + envtest, `-tags=e2e`. May use fake
  clocks and simulated Job status. Must never touch real AWS.
- **runner + executor debugging (`test/awsenv`)** — stdlib testing,
  `-tags=awsenv`, gated on `AWSENV_ENABLED=1`. This suite exists to debug
  the runner and platform executors step by step: assert intermediate
  states, not just final outcomes. Must use real executors and real SDK
  calls; must never mock the AWS boundary. Unsupported emulator operations
  skip with exact provider/capability/operation/error evidence (see
  `harness/`); everything else fails. "Simulation" here only means the
  cloud is emulated — the debugging depth is the point.
- **blackbox acceptance (`test/kind`)** — stdlib testing, `-tags=kind`.
  The runner runs inside, but the test must not inspect its internals:
  assert only that the observable functionality matches expectation
  (phases reached, Jobs complete, cloud resources in the expected state),
  period. No fake clocks, no simulated Jobs — real wall-clock windows or
  override-driven runs through the production controller and runner images.

## Running

```bash
# Whitebox controller suite (needs envtest binaries)
make test-e2e

# AWS integration suite (needs Floci on :4566)
docker compose -f test/awsenv/environments/floci/compose.yml up -d
make test-awsenv

# Blackbox full chain (builds images, provisions kind; ~5 min smoke)
make test-kind
# ...including the real wall-clock schedule cycle (nightly)
make test-kind-schedule
```

All three suites are opt-in by build tag and leave default
`go test ./...` unaffected. See each suite's README for details,
environment variables, and troubleshooting.
