# Log Structure
Hibernator uses structured logging with JSON format. Each log entry is a JSON object containing various fields that provide context about the log message. Below are some of the common fields found in Hibernator logs:

## Runner Startup Type

```json
{"level":"info","ts":"2026-02-23T10:54:19Z","logger":"execution-service.runner-logs","msg":"starting runner","namespace":"hibernator-system","plan":"workloadscaler-test","target":"k3d-local","executionId":"workloadscaler-test-k3d-local-1771844058","timestamp":"2026-02-23T10:54:19Z","targetType":"workloadscaler","plan":"workloadscaler-test","executionId":"workloadscaler-test-k3d-local-1771844058","operation":"wakeup","target":"k3d-local"}
```

## Progress Type

```json
{"level":"info","ts":"2026-02-23T10:54:19Z","logger":"execution-service.runner-logs","msg":"progress","namespace":"hibernator-system","plan":"workloadscaler-test","target":"k3d-local","executionId":"workloadscaler-test-k3d-local-1771844058","timestamp":"2026-02-23T10:54:19Z","message":"Loading executors","phase":"initializing","percent":"10"}
```

## Error Type

```json
{"level":"info","ts":"2026-02-23T10:54:19Z","logger":"execution-service.runner-logs","msg":"error context","namespace":"hibernator-system","plan":"workloadscaler-test","target":"k3d-local",,"executionId":"workloadscaler-test-k3d-local-1771844058","timestamp":"2026-02-23T10:54:19Z","error":"Failed to load executors: failed to load executors: failed to list executors: no matches for kind \"Executor\" in version \"executor.hibernator.io/v1alpha1\""}
```

## Waiting Type

```json
{"level":"info","ts":"2026-02-23T10:54:19Z","logger":"execution-service.runner-logs","msg":"waiting for workload replicas to scale","namespace":"hibernator-system","plan":"workloadscaler-test","target":"k3d-local","executionId":"workloadscaler-test-k3d-local-1771844058","timestamp":"2026-02-23T10:54:19Z","namespace":"default","name":"echoserver","desiredReplicas":"7","timeout":"5m"}
{"level":"info","ts":"2026-02-23T10:54:19Z","logger":"execution-service.runner-logs","msg":"waiting for operation","namespace":"hibernator-system","plan":"workloadscaler-test","target":"k3d-local","executionId":"workloadscaler-test-k3d-local-1771844058","timestamp":"2026-02-23T10:54:19Z","description":"deployments/echoserver in namespace default to scale to 7 replicas","timeout":"5m0s"}
{"level":"info","ts":"2026-02-23T10:54:19Z","logger":"execution-service.runner-logs","msg":"polling operation (initial)","namespace":"hibernator-system","plan":"workloadscaler-test","target":"k3d-local","executionId":"workloadscaler-test-k3d-local-1771844058","timestamp":"2026-02-23T10:54:19Z","description":"deployments/echoserver in namespace default to scale to 7 replicas","status":"current replicas=0; desired replicas=7 (waiting)"}
{"level":"info","ts":"2026-02-23T10:54:34Z","logger":"execution-service.runner-logs","msg":"polling operation","namespace":"hibernator-system","plan":"workloadscaler-test","target":"k3d-local","executionId":"workloadscaler-test-k3d-local-1771844058","timestamp":"2026-02-23T10:54:34Z","description":"deployments/echoserver in namespace default to scale to 7 replicas","status":"current replicas=7 has been met with desired replicas=7"}
{"level":"info","ts":"2026-02-23T10:54:34Z","logger":"execution-service.runner-logs","msg":"operation completed","namespace":"hibernator-system","plan":"workloadscaler-test","target":"k3d-local","executionId":"workloadscaler-test-k3d-local-1771844058","timestamp":"2026-02-23T10:54:34Z","description":"deployments/echoserver in namespace default to scale to 7 replicas"}
```
