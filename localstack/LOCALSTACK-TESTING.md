# 🏜️ LocalStack Sandbox for Hibernator

**Zero-Cost AWS Testing Environment**

This directory contains everything you need to test the Hibernator Operator against mock AWS services using LocalStack.

## Quick Start (One Command)

```bash
bash scripts/quickstart-localstack.sh
```

This will:
1. ✅ Start LocalStack (Docker container with mocked AWS)
2. ✅ Create a local Kubernetes cluster (kind)
3. ✅ Deploy Hibernator to the cluster
4. ✅ Create mock EC2 and RDS resources
5. ✅ Configure credentials and secrets

**Time:** ~5 minutes | **Cost:** $0

## What's Included

### Files

```
localstack-compose.yml              Docker Compose setup for LocalStack
scripts/
  ├── quickstart-localstack.sh      One-command setup (recommended!)
  ├── localstack-setup.sh           Create mock AWS resources
config/samples/
  └── localstack-hibernateplan.yaml Test HibernationPlan manifest
docs/
  └── LOCALSTACK-SETUP.md           Detailed documentation
```

### What You Can Test

| Feature | Status |
|---------|--------|
| EC2 instance stop/start | ✅ Works |
| RDS instance stop/start | ✅ Works |
| Tag-based EC2 selector | ✅ Works |
| AWS SDK integration | ✅ Works |
| Credential passing | ✅ Works |
| Error handling | ✅ Works |
| EKS operations | ⚠️ Limited (mock APIs only) |

### What You Cannot Test

- Real infrastructure changes (it's mocked)
- Karpenter node pool scaling (needs real K8s)
- Actual cost savings (this is sandbox only)

## Usage Workflows

### Workflow 1: Full Integration Test (Recommended)

```bash
# 1. One-line setup
bash scripts/quickstart-localstack.sh

# 2. Watch logs in terminal A
kubectl logs -n hibernator-system -f deployment/hibernator-controller-manager

# 3. Apply test plan in terminal B
kubectl apply -f config/samples/localstack-hibernateplan.yaml

# 4. Monitor execution
kubectl get hibernateplans -w
kubectl describe hibernateplan localstack-test
```

### Workflow 2: Manual Steps

```bash
# Step 1: Start LocalStack
docker-compose -f localstack-compose.yml up -d

# Step 2: Create local cluster
kind create cluster --name hibernator-test

# Step 3: Install Hibernator
make install
make deploy

# Step 4: Set up resources
bash scripts/localstack-setup.sh

# Step 5: Create credentials secret
kubectl create secret generic localstack-credentials \
  --from-literal=accessKeyId=test \
  --from-literal=secretAccessKey=test \
  -n hibernator-system

# Step 6: Test with HibernationPlan
kubectl apply -f config/samples/localstack-hibernateplan.yaml

# Step 7: Cleanup when done
docker-compose -f localstack-compose.yml down
kind delete cluster --name hibernator-test
```

### Workflow 3: Direct LocalStack Testing

```bash
# Access LocalStack directly (without Hibernator)
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test

# List EC2 instances
aws ec2 describe-instances

# List RDS instances
aws rds describe-db-instances

# Stop an instance
aws ec2 stop-instances --instance-ids <instance-id>

# Check LocalStack status
curl http://localhost:4566/_localstack/health
```

## Troubleshooting

### LocalStack won't start

```bash
# Check Docker
docker ps

# View logs
docker logs hibernator-localstack

# Restart
docker-compose -f localstack-compose.yml restart
```

### Cluster context not set

```bash
# List clusters
kind get clusters

# Set context
kubectl cluster-info --context kind-hibernator-test
```

### Controller not starting

```bash
# Check logs
kubectl logs -n hibernator-system deployment/hibernator-controller-manager

# Verify CRDs
kubectl get crd | grep hibernator

# Check pod status
kubectl get pods -n hibernator-system
```

### Cannot reach LocalStack from pod

LocalStack runs on Docker host, but pods need to reach it. Two options:

**Option A: Use docker-compose network bridge**
```bash
# LocalStack and Hibernator on same network
docker network create hibernator
docker-compose -f localstack-compose.yml up -d
```

**Option B: Use host.docker.internal**
```yaml
# In CloudProvider
endpoint: http://host.docker.internal:4566
```

## Next Steps

After testing with LocalStack:

1. **Unit Tests** (always free)
   ```bash
   make test-unit
   ```

2. **Real AWS Testing** (with cost)
   - Create dev/stg AWS account
   - Use real small resources
   - See: [.github/copilot-instructions.md](../.github/copilot-instructions.md)

3. **Production Deployment**
   - Deploy to production clusters
   - Integrate with monitoring/alerts
   - See: RFC-001

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│ Your Machine                                            │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │ Kubernetes (kind cluster)                        │  │
│  │                                                  │  │
│  │  ┌─────────────────────────────────────────────┐ │  │
│  │  │ Hibernator Controller                       │ │  │
│  │  │ (AWS SDK → LocalStack)                      │ │  │
│  │  └─────────────────────────────────────────────┘ │  │
│  │                                                  │  │
│  │  ┌─────────────────────────────────────────────┐ │  │
│  │  │ Hibernator Runner Jobs                      │ │  │
│  │  │ (Execute shutdown/wakeup)                   │ │  │
│  │  └─────────────────────────────────────────────┘ │  │
│  │                                                  │  │
│  └──────────────────────────────────────────────────┘  │
│                         ↓ (API calls)                   │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │ LocalStack (Docker Container)                    │  │
│  │                                                  │  │
│  │  ┌──────┐  ┌──────┐  ┌──────┐  ┌──────┐         │  │
│  │  │ EC2  │  │ RDS  │  │ EKS  │  │ STS  │  ...   │  │
│  │  └──────┘  └──────┘  └──────┘  └──────┘         │  │
│  │                                                  │  │
│  └──────────────────────────────────────────────────┘  │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

## Performance Notes

- **Cold start:** ~30 seconds (kind cluster, docker pulls)
- **Shutdown/wakeup:** <100ms (no actual AWS calls, mocked)
- **Test cycle:** ~5 minutes end-to-end

## For Production

This sandbox is **not** suitable for production use. For real AWS testing:

1. Create dedicated dev/stg AWS accounts
2. Use auto-scaling groups with real small instances
3. Enable CloudTrail for audit logs
4. Set up cost alerts
5. Test during scheduled maintenance windows

See `.github/copilot-instructions.md` for production deployment guidelines.

## References

- [LocalStack Docs](https://docs.localstack.cloud/)
- [Kind Docs](https://kind.sigs.k8s.io/)
- [Hibernator Design](../../RFCs/0001-hibernate-operator.md)
- [Hibernator Instructions](../../.github/copilot-instructions.md)
