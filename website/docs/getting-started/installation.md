# Installation

## Using Helm (Recommended)

```bash
# Install with default values
helm install hibernator oci://ghcr.io/ardikabs/charts/hibernator --version 1.4.0 \
  -n hibernator-system --create-namespace
```

### Customizing the Installation

Create a `values.yaml` to override defaults:

```yaml
# values.yaml
controller:
  replicas: 1
  resources:
    limits:
      cpu: 500m
      memory: 256Mi
```

Then install with:

```bash
helm install hibernator hibernator/hibernator \
  -n hibernator-system --create-namespace \
  -f values.yaml
```

## Using kubectl

```bash
# Apply CRDs
kubectl apply -f config/crd/bases/

# Deploy the operator
kubectl apply -f config/manager/manager.yaml

# Apply RBAC
kubectl apply -f config/rbac/
```

## Verify Installation

```bash
# Check the controller is running
kubectl get pods -n hibernator-system

# Verify CRDs are installed
kubectl get crd | grep hibernator
```

Expected output:

```
cloudproviders.hibernator.ardikabs.com        2026-01-01T00:00:00Z
hibernateplans.hibernator.ardikabs.com        2026-01-01T00:00:00Z
k8sclusters.hibernator.ardikabs.com           2026-01-01T00:00:00Z
scheduleexceptions.hibernator.ardikabs.com    2026-01-01T00:00:00Z
```

## Building from Source

```bash
# Clone the repository
git clone https://github.com/ardikabs/hibernator.git
cd hibernator

# Build binaries
make build

# Build Docker images
make docker-build

# Install CRDs to cluster
make install

# Deploy to cluster
make deploy
```
