# Hibernator Operator Helm Chart

This Helm chart deploys the Hibernator Operator on Kubernetes, enabling time-based hibernation and wakeup of cloud infrastructure resources.

## Prerequisites

- Kubernetes 1.24+
- Helm 3.0+
- (Optional) cert-manager 1.7+ for automatic webhook certificate management

## Installation

### Basic Installation

```bash
helm repo add hibernator https://charts.hibernator.dev
helm repo update

helm install hibernator hibernator/hibernator \
  --namespace hibernator-system \
  --create-namespace
```

### Installation with cert-manager

If you have cert-manager installed, the webhook certificates will be automatically managed:

```bash
helm install hibernator hibernator/hibernator \
  --namespace hibernator-system \
  --create-namespace \
  --set webhook.certManager.enabled=true
```

### Installation with Custom Image

```bash
helm install hibernator hibernator/hibernator \
  --namespace hibernator-system \
  --create-namespace \
  --set image.controller.repository=my-registry/hibernator \
  --set image.controller.tag=v0.2.0 \
  --set image.runner.repository=my-registry/hibernator-runner \
  --set image.runner.tag=v0.2.0
```

## Configuration

### Common Values

| Key | Default | Description |
|-----|---------|-------------|
| `replicaCount` | `2` | Number of operator replicas |
| `image.controller.repository` | `ghcr.io/ardikabs/hibernator` | Controller image repository |
| `image.controller.tag` | `latest` | Controller image tag |
| `image.runner.repository` | `ghcr.io/ardikabs/hibernator-runner` | Runner image repository |
| `image.runner.tag` | `latest` | Runner image tag |
| `webhook.enabled` | `true` | Enable validation webhook |
| `webhook.certManager.enabled` | `true` | Use cert-manager for webhook certs |
| `resources.controller.requests.memory` | `256Mi` | Controller memory request |
| `resources.controller.limits.memory` | `512Mi` | Controller memory limit |

### AWS-Specific Configuration

For AWS environments with IRSA (IAM Roles for Service Accounts):

```bash
helm install hibernator hibernator/hibernator \
  --namespace hibernator-system \
  --create-namespace \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::ACCOUNT_ID:role/hibernator-controller
```

### High Availability

Enable autoscaling for production deployments:

```bash
helm install hibernator hibernator/hibernator \
  --namespace hibernator-system \
  --create-namespace \
  --set replicaCount=3 \
  --set autoscaling.enabled=true \
  --set autoscaling.minReplicas=3 \
  --set autoscaling.maxReplicas=5
```

## Usage

After installation, create a `HibernatePlan` to define which resources to hibernate:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: my-plan
  namespace: default
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]

  execution:
    strategy:
      type: Sequential

  targets:
    - name: my-database
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        snapshotBeforeStop: true
```

## Troubleshooting

### Webhook not working

Check webhook service and pod:

```bash
kubectl get pods -n hibernator-system
kubectl logs -n hibernator-system deployment/hibernator-controller
kubectl get validatingwebhookconfigurations
```

### Certificate issues

If using cert-manager:

```bash
kubectl get certificate -n hibernator-system
kubectl describe certificate hibernator-webhook -n hibernator-system
```

### Controller not reconciling

Check controller logs:

```bash
kubectl logs -n hibernator-system -l app.kubernetes.io/component=controller -f
```

## Uninstallation

```bash
helm uninstall hibernator \
  --namespace hibernator-system
```

## Development

Build and install from local chart:

```bash
helm install hibernator ./charts/hibernator \
  --namespace hibernator-system \
  --create-namespace
```

## License

Apache License 2.0
