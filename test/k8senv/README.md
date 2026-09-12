# Kubernetes API Executor Integration Suite

Executor integration tests against a real Kubernetes API (envtest:
API server + etcd, no kubelet or controller-manager). Tests seed fixtures,
invoke the real Karpenter and WorkloadScaler executors through their real
client factories, and assert discovery, API-side state, restore payloads,
and messages.

## Layout

```bash
test/k8senv/
├── README.md
├── karpenter_lifecycle_test.go     # NodePool discovery + lifecycle + validation + noop + stale
├── workloadscaler_lifecycle_test.go # Scale discovery + lifecycle + validation + noop + stale
├── harness/
│   └── k8s.go                      # Setup, K8sEnv, kubeconfig injection, fixtures
└── testdata/
    └── crds/
        └── karpenter.sh_nodepools.yaml  # Minimal NodePool CRD (CRUD only)
```

## Contract

- **Real API boundary**: executors run via `New()` (production client
  factory fed with envtest kubeconfig bytes). Nothing is mocked at the
  cluster boundary.
- **`AwaitCompletion` stays disabled**: no controller-manager exists in
  envtest, so `status.replicas` / `Ready` conditions never converge. The
  wait loops are covered by `pkg/waiter` unit tests; this suite asserts the
  spec writes (`spec.replicas`, NodePool CRUD) the waits observe.
- **Suite-per-test control plane**: `Setup` boots one envtest environment
  per test for isolation. Slower than shared, immune to fixture leakage.
- Build tag `k8senv` is explicit opt-in: the suite fails fast without
  `K8SENV_ENABLED=1`. Default `go test ./...` is unaffected (and
  `UNIT_TEST_PKGS` excludes this suite).

## Running

```bash
make test-k8senv   # or: K8SENV_ENABLED=1 go test ./test/k8senv/... -tags=k8senv -count=1 -v
```

Requires envtest binaries (`make envtest` installs `setup-envtest`;
`KUBEBUILDER_ASSETS` is resolved by the make target).
