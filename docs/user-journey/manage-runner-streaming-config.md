# Manage Runner Streaming Config

**Tier:** `[Advanced]`

**Personas:** DevOps Engineer, Platform Engineer, SRE

**When:** Configuring gRPC or webhook streaming for real-time execution logs and progress

**Why:** Low-latency visibility into hibernation operations enables rapid issue detection and debugging.

---

## User Stories

**Story 1:** As a **DevOps Engineer**, I want to **choose between gRPC and webhook streaming**, so that **I can select the transport that fits my environment**.

**Story 2:** As a **SRE**, I want to **receive real-time execution logs and progress updates**, so that **I can detect issues before they cascade**.

**Story 3:** As a **Platform Engineer**, I want to **integrate streaming with my monitoring stack**, so that **hibernation progress appears in my observability dashboards**.

---

## When/Context

- **Real-time monitoring:** Watch hibernation progress as it happens
- **Pluggable transport:** Choose between gRPC (preferred) or webhook (fallback)
- **Security:** TokenReview-based auth for streaming connections
- **Logging:** All execution progress and errors streamed to external systems

---

## Business Outcome

Configure streaming transport so hibernation operations are observable in real-time via logs or monitoring systems.

---

## Step-by-Step Flow

### 1. **Choose streaming transport**

**gRPC (Recommended):**
- Low-latency bidirectional streaming
- Binary protocol (efficient)
- Requires gRPC server in control plane
- Works in restricted environments

**Webhook (Fallback):**
- HTTP POST requests
- Simple; no gRPC dependency
- Higher latency than gRPC
- Falls back if gRPC unavailable

### 2. **Configure gRPC streaming (recommended)**

Hibernator controller has gRPC server built-in. Configure endpoint:

```bash
# Set controller gRPC endpoint
kubectl set env deployment/hibernator-controller \
  -n hibernator-system \
  HIBERNATOR_GRPC_ENDPOINT=localhost:50051 \
  HIBERNATOR_GRPC_TLS=true
```

Controller configuration (values.yaml):

```yaml
controller:
  streaming:
    enabled: true
    transport: grpc
    grpc:
      enabled: true
      host: "0.0.0.0"
      port: 50051
      tls:
        enabled: true
        certPath: /etc/hibernator/certs/tls.crt
        keyPath: /etc/hibernator/certs/tls.key
```

### 3. **Configure TokenReview authentication**

gRPC clients present projected tokens for authentication:

```yaml
# Runner pod spec (auto-configured by controller)
apiVersion: v1
kind: Pod
metadata:
  name: hibernator-runner-xyz
spec:
  serviceAccountName: hibernator-runner
  containers:
  - name: runner
    env:
    - name: HIBERNATOR_GRPC_ENDPOINT
      value: "hibernator-grpc-service.hibernator-system:50051"
    volumeMounts:
    - name: stream-token
      mountPath: /var/run/secrets/stream
  volumes:
  - name: stream-token
    projected:
      sources:
      - serviceAccountToken:
          audience: hibernator-control-plane
          expirationSeconds: 600
          path: token
```

Controller validates token via TokenReview:

```go
// Pseudo-code: controller validation
token := request.Authorization  // Bearer token from runner
review := &authv1.TokenReview{
  Spec: authv1.TokenReviewSpec{
    Token: token,
  },
}
kubeClient.AuthV1().TokenReviews().Create(ctx, review)

if review.Status.Authenticated &&
   review.Status.Audiences.Contains("hibernator-control-plane") {
  // Accept streaming connection
}
```

### 4. **Configure webhook streaming (fallback)**

If gRPC unavailable, controller can receive webhook POSTs:

```yaml
controller:
  streaming:
    enabled: true
    transport: webhook
    webhook:
      enabled: true
      endpoint: "http://hibernator-webhook:8080/logs"
      maxRetries: 3
      timeout: 30s
```

Runner sends logs via HTTP:

```bash
# POST request from runner to controller
curl -X POST http://hibernator-webhook:8080/logs \
  -H "Authorization: Bearer $HIBERNATOR_STREAM_TOKEN" \
  -d '{
    "executionId": "exec-123",
    "timestamp": "2026-02-01T20:05:30Z",
    "message": "Scaling EC2 instances to zero",
    "level": "INFO"
  }'
```

### 5. **Monitor streaming in real-time**

**Via kubectl:**

```bash
# Watch runner pod logs as hibernation progresses
kubectl logs job/hibernator-runner-xyz -f

# Output:
# 2026-02-01T20:05:00Z [INFO] Execution started
# 2026-02-01T20:05:05Z [INFO] Connecting to streaming endpoint
# 2026-02-01T20:05:10Z [INFO] Querying EC2 instances
# 2026-02-01T20:05:15Z [INFO] Found 5 instances
# 2026-02-01T20:05:20Z [INFO] Stopping instances...
# 2026-02-01T20:05:25Z [INFO] Instances stopped: i-001, i-002, i-003, i-004, i-005
# 2026-02-01T20:05:30Z [INFO] Saving restore metadata
# 2026-02-01T20:05:35Z [INFO] Execution completed successfully
```

**Via gRPC client:**

```bash
# Query execution progress
grpcurl -plaintext \
  -d '{"executionId": "exec-123"}' \
  localhost:50051 \
  hibernator.api.v1.ExecutionService/GetProgress

# Output:
# {
#   "executionId": "exec-123",
#   "phase": "Shutdown",
#   "progress": 60,
#   "message": "Scaling EC2 instances",
#   "timestamp": "2026-02-01T20:05:25Z"
# }
```

### 6. **Stream logs to external systems**

**Option A: Stream to Datadog**

```bash
# Configure Datadog streaming
export DATADOG_API_KEY="..."
export DATADOG_SITE="datadoghq.com"

# Controller sends logs to Datadog via HTTP client
# (configured in helm values)
datadog:
  enabled: true
  apiKey: $DATADOG_API_KEY
  endpoint: "https://http-intake.logs.datadoghq.com/v1/input"
  service: hibernator-operator
```

**Option B: Stream to Splunk**

```bash
# Splunk HEC (HTTP Event Collector)
splunk:
  enabled: true
  endpoint: "https://splunk.company.com:8088/services/collector"
  token: $SPLUNK_HEC_TOKEN
  source: hibernator-controller
```

**Option B: Stream to Prometheus**

```bash
# Prometheus metrics scraped from /metrics endpoint
prometheus:
  enabled: true
  port: 8080
  metrics:
    - hibernator_execution_duration_seconds
    - hibernator_execution_success_total
    - hibernator_execution_failure_total
```

### 7. **Configure streaming persistence**

Store execution logs for audit trail:

```yaml
controller:
  streaming:
    persistence:
      enabled: true
      backend: s3
      s3:
        bucket: hibernator-execution-logs
        prefix: "hibernation/"
        retention: 90  # days
```

Logs stored with format:

```
s3://hibernator-execution-logs/hibernation/
├── 2026-02-01/
│   ├── exec-prod-offhours-20060200z.log
│   ├── exec-stg-offhours-20060200z.log
│   └── ...
└── 2026-02-02/
    └── ...
```

### 8. **Monitor streaming health**

```bash
# Check streaming endpoint health
kubectl port-forward -n hibernator-system svc/hibernator-controller 50051:50051

# Test gRPC health check
grpcurl -plaintext \
  localhost:50051 \
  grpc.health.v1.Health/Check

# Output:
# {
#   "status": "SERVING"
# }

# Check metrics
curl http://hibernator-controller:8080/metrics | grep streaming

# Output:
# hibernator_streaming_connections_active 2
# hibernator_streaming_logs_total 150
# hibernator_streaming_bytes_sent_total 45000
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Transport?** | gRPC (recommended) | Low-latency; binary protocol |
| | Webhook (fallback) | Simpler; HTTP-based |
| **Persistence?** | S3 (recommended) | Long-term audit trail |
| | Log aggregation (ELK/Datadog) | Real-time analytics |
| **Retention?** | 90 days | Balance cost and utility |
| | 1 year+ | Compliance/audit |

---

## Outcome

✓ Streaming configured. Real-time execution logs visible in operator logs. Optional external streaming to Datadog/Splunk/etc.

---

## Related Journeys

- [Deploy Operator to Cluster](deploy-operator-to-cluster.md) — Initial operator setup
- [Monitor Hibernation Execution](monitor-hibernation-execution.md) — Observe hibernation

---

## Pain Points Solved

**RFC-0001:** gRPC streaming enables low-latency operator-to-runner communication. TokenReview authentication secures connections. Pluggable transport supports both gRPC and webhook.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (streaming infrastructure, TokenReview auth, gRPC/webhook transport)
