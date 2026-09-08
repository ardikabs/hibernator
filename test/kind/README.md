# kind Full-Chain E2E Suite

Real end-to-end: controller deployed on kind → real runner pods → real AWS
calls against host Floci. All validation lives in Go (`kind_test.go`).

## Layout

```bash
test/kind/
├── README.md                # This file
├── kind_test.go             # TestMain (provision all) + TestEC2FullChain
├── setup.go                 # Cluster, images, deploy, routing helpers
├── webhookcert.go           # Self-signed webhook cert (server idles)
├── Dockerfile.runner-test   # Runner + baked in-cluster Floci endpoint
└── manifests/
    └── controller-clusterrole.yaml  # Full RBAC copy (see Troubleshooting)
```

## Architecture

- **Floci stays on the host** (`test/floci/compose.yml`, `:4566`).
- **Runner pods** use `hibernator-runner-test:kind` with
  `AWS_ENDPOINT_URL=http://floci.hibernator-system.svc:4566` baked in
  (CloudProvider has no endpoint field; no production change needed).
- **Routing**: Service `floci` in `hibernator-system` with manual Endpoints
  pointing at the kind-bridge gateway (override: `FLOCI_HOST_IP`).
- **Every run is isolated** with a unique suite-owned kind cluster, image
  tags, namespace, and resource names. Shared-cluster reuse is intentionally
  unsupported because the suite installs cluster-wide CRDs and RBAC.
- **Controller** runs with `--leader-elect=false --enable-streaming=false
  --runner-image=hibernator-runner-test:kind
  --control-plane-endpoint=127.0.0.1` and a mounted self-signed webhook
  cert (no ValidatingWebhookConfiguration — the webhook server idles).
- **Runner RBAC**: SA + RoleBinding→ClusterRole `hibernator-runner` are
  created per test namespace (in prod Helm owns these).

## Running

```bash
docker compose -f test/floci/compose.yml up -d
make test-kind            # immediate override-driven full-chain PR smoke
make test-kind-schedule   # also run the real wall-clock schedule cycle (nightly)
```

Keep a suite-owned cluster for debugging: `KEEP_KIND=1 make test-kind`.
Keep it only on failure: `KEEP_KIND_ON_FAILURE=1 make test-kind`.
(Point kubectl at the printed kubeconfig.)
Override the host IP pods use: `FLOCI_HOST_IP=... make test-kind`.

Teardown: the suite only deletes clusters it created, unless `KEEP_KIND=1`;
stop Floci with `docker compose -f test/floci/compose.yml down`.

The `kind` build tag is explicit opt-in: running the tagged suite without
`FLOCI_ENABLED=1` fails rather than silently reporting success. Use
`KIND_UNIT_ONLY=1` only for the pure helper tests.

## Troubleshooting

- `bin/kind missing`: `make kind-tool`.
- Pods can't reach Floci: check `kubectl get endpoints floci -n hibernator-system`
  and re-probe from a debug pod; set `FLOCI_HOST_IP` explicitly. Gateway
  discovery parses `docker network inspect kind` as JSON (docker's
  go-template printer mishandles the IPv6 entry that has no Gateway).
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
