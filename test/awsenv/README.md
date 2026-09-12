# AWS Environment Executor Integration Suite

Executor integration tests against an AWS-shaped environment (Floci primary,
real AWS later via the same provisioner contract). Tests provision fixtures,
invoke the real executors, and assert resource plus restore-state transitions.

## Layout

```bash
test/awsenv/
├── environments/floci/compose.yml  # Pinned Floci runtime asset
├── README.md
├── ec2_lifecycle_test.go           # EC2 mixed-state lifecycle + noop + validation
├── rds_lifecycle_test.go           # RDS instance + snapshot lifecycles + validation
├── eks_lifecycle_test.go           # EKS node-group lifecycle + validation
└── harness/
    ├── harness.go                  # Setup, Environment, clients, EC2 seeding
    ├── capabilities.go             # Capability constants + unsupported classifier
    ├── provision_rds.go            # RDS fixture + wait + delete
    └── provision_eks.go            # EKS fixture + wait + delete
```

## Capability policy

Service presence is not enough: each lifecycle test probes the exact APIs
Hibernator calls. An explicit unsupported-operation response skips that test
with provider, capability, operation, and API error. Provisioning, auth,
network, state, validation, timeout, and server errors fail instead of
skipping.

## Running

```bash
docker compose -f test/awsenv/environments/floci/compose.yml up -d
make test-awsenv   # or: AWSENV_ENABLED=1 go test ./test/awsenv/... -tags=awsenv -count=1 -v
docker compose -f test/awsenv/environments/floci/compose.yml down
```

`AWSENV_PROVIDER` selects the backend (only `floci` today; unknown values fail
setup). `AWSENV_ENDPOINT_URL` overrides the endpoint (default
`http://localhost:4566`). The `awsenv` build tag is explicit opt-in: running a
tagged test without `AWSENV_ENABLED=1` fails. Default `go test ./...` is
unaffected.
