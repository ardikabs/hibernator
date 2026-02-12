# Hibernator Operator Helm Chart

![Version: 1.0.0](https://img.shields.io/badge/Version-1.0.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v1.0.0](https://img.shields.io/badge/AppVersion-v1.0.0-informational?style=flat-square)

A Helm chart for Hibernator Operator - Kubernetes-native time-based infrastructure hibernation.

**Homepage:** <https://github.com/ardikabs/hibernator>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| ardikabs | <me@ardikabs.com> | <https://github.com/ardikabs> |

## Prerequisites

- Kubernetes 1.24+
- Helm 3.0+
- (Optional) cert-manager 1.7+ for automatic webhook certificate management

## Installation

### Basic Installation

```bash
helm install oci://ghcr.io/ardikabs/charts/hibernator \
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

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` |  |
| annotations | object | `{}` |  |
| autoscaling.enabled | bool | `false` |  |
| autoscaling.maxReplicas | int | `4` |  |
| autoscaling.minReplicas | int | `2` |  |
| autoscaling.targetCPUUtilizationPercentage | int | `80` |  |
| autoscaling.targetMemoryUtilizationPercentage | int | `80` |  |
| controlPlane.endpoint | string | `"hibernator.hibernator-system.svc"` |  |
| crds.create | bool | `true` |  |
| crds.upgrade | bool | `true` |  |
| fullnameOverride | string | `""` |  |
| image.controller.pullPolicy | string | `"IfNotPresent"` |  |
| image.controller.repository | string | `"ghcr.io/ardikabs/hibernator"` |  |
| image.controller.tag | string | `""` |  |
| image.runner.pullPolicy | string | `"Always"` |  |
| image.runner.repository | string | `"ghcr.io/ardikabs/hibernator-runner"` |  |
| image.runner.tag | string | `""` |  |
| imagePullSecrets | list | `[]` |  |
| labels | object | `{}` |  |
| leaderElection.enabled | bool | `true` |  |
| leaderElection.namespace | string | `""` |  |
| nameOverride | string | `""` |  |
| namespace | string | `"hibernator-system"` |  |
| nodeSelector | object | `{}` |  |
| operator.syncPeriod | string | `"10h"` |  |
| operator.workers | int | `1` |  |
| podAnnotations | object | `{}` |  |
| podSecurityContext.fsGroup | int | `65532` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.runAsUser | int | `65532` |  |
| rbac.create | bool | `true` |  |
| replicaCount | int | `2` |  |
| resources.controller.limits.cpu | string | `"500m"` |  |
| resources.controller.limits.memory | string | `"512Mi"` |  |
| resources.controller.requests.cpu | string | `"250m"` |  |
| resources.controller.requests.memory | string | `"256Mi"` |  |
| resources.runner.limits.cpu | string | `"1000m"` |  |
| resources.runner.limits.memory | string | `"1Gi"` |  |
| resources.runner.requests.cpu | string | `"500m"` |  |
| resources.runner.requests.memory | string | `"512Mi"` |  |
| runnerServiceAccount.annotations | object | `{}` |  |
| runnerServiceAccount.create | bool | `true` |  |
| runnerServiceAccount.name | string | `"hibernator-runner"` |  |
| securityContext.allowPrivilegeEscalation | bool | `false` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| securityContext.readOnlyRootFilesystem | bool | `true` |  |
| serviceAccount.annotations | string | `nil` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
| tolerations | list | `[]` |  |
| webhook.certGen.duration | int | `87600` |  |
| webhook.certGen.image.pullPolicy | string | `"IfNotPresent"` |  |
| webhook.certGen.image.repository | string | `"registry.k8s.io/ingress-nginx/kube-webhook-certgen"` |  |
| webhook.certGen.image.tag | string | `"v1.4.0"` |  |
| webhook.certGen.resources.limits.cpu | string | `"100m"` |  |
| webhook.certGen.resources.limits.memory | string | `"128Mi"` |  |
| webhook.certGen.resources.requests.cpu | string | `"50m"` |  |
| webhook.certGen.resources.requests.memory | string | `"64Mi"` |  |
| webhook.certGen.useJob | bool | `true` |  |
| webhook.certManager.enabled | bool | `false` |  |
| webhook.certManager.issuer | string | `"selfsigned-issuer"` |  |
| webhook.certs.caBundle | string | `""` |  |
| webhook.certs.certDir | string | `"/tmp/k8s-webhook-server/serving-certs"` |  |
| webhook.certs.secretName | string | `"webhook-server-cert"` |  |
| webhook.enabled | bool | `true` |  |
| webhook.port | int | `9443` |  |
