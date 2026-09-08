# Floci Executor E2E Suite

Executor integration tests for real Shutdown/Wakeup/Validate logic against Floci, a fast
open-source AWS emulator. No production code changes are needed:
the AWS SDK redirects to Floci via `AWS_ENDPOINT_URL` env vars set by the
`harness` package.

## Layout

```bash
test/floci/
├── compose.yml              # Floci container (port 4566)
├── README.md                # This file
├── ec2_lifecycle_test.go    # EC2 real Stop/Start lifecycle
└── harness/
    ├── harness.go           # Shared setup: env gate, seeding, polling
    └── harness_test.go      # Harness self-tests
```

This is intentionally not the full control-plane e2e; that lives in
`test/kind`. Adding another executor later: add `harness.Seed<X>` + `test/floci/<x>_lifecycle_test.go`
against a new `harness.Clients.<X>` field. Probe-then-skip if Floci lacks the
operation (current gaps: RDS Stop/Start/Snapshot, EKS UpdateNodegroupConfig).

## Running

```bash
docker compose -f test/floci/compose.yml up -d
make test-floci   # or: FLOCI_ENABLED=1 go test ./test/floci/... -tags=floci -count=1 -v
docker compose -f test/floci/compose.yml down
```

The `floci` build tag is explicit opt-in: running a tagged test without
`FLOCI_ENABLED=1` fails rather than silently reporting success. Default
`go test ./...` and unit CI are unaffected because these files are tagged.

The compose image is pinned by digest because it receives Docker socket
access. Set `FLOCI_IMAGE` only for a deliberately reviewed upgrade. Port
4566 must be bridge-reachable by kind, so it is exposed on the host; do not
run this compose project on an untrusted/shared network or Docker daemon.
