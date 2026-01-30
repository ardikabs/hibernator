# LocalStack Quick Reference

## One-Line Setup
```bash
bash scripts/quickstart-localstack.sh
```

## Manual Setup
```bash
# 1. Start LocalStack
docker-compose -f localstack-compose.yml up -d

# 2. Create Kubernetes cluster
kind create cluster --name hibernator-test

# 3. Deploy Hibernator
make install
make deploy

# 4. Create resources
bash scripts/localstack-setup.sh

# 5. Add credentials
kubectl create secret generic localstack-credentials \
  --from-literal=accessKeyId=test \
  --from-literal=secretAccessKey=test \
  -n hibernator-system

# 6. Test it
kubectl apply -f config/samples/localstack-hibernateplan.yaml
```

## Testing Commands
```bash
# Watch controller logs
kubectl logs -n hibernator-system -f deployment/hibernator-controller-manager

# Check HibernationPlan status
kubectl get hibernateplans -w

# Describe execution details
kubectl describe hibernateplan localstack-test

# Access LocalStack directly
export AWS_ENDPOINT_URL=http://localhost:4566
aws ec2 describe-instances
aws rds describe-db-instances
```

## Cleanup
```bash
# Stop LocalStack
docker-compose -f localstack-compose.yml down

# Delete cluster
kind delete cluster --name hibernator-test
```

## What Works
- ✅ EC2 stop/start
- ✅ RDS stop/start
- ✅ Tag-based EC2 selector
- ✅ AWS SDK integration
- ✅ Credential passing
- ✅ Full logging and status tracking

## Limitations
- ❌ No actual infrastructure changes (mock APIs)
- ❌ No Karpenter node pool scaling
- ❌ No real cost savings

## Files
- `localstack-compose.yml` - Docker Compose setup
- `scripts/quickstart-localstack.sh` - Automated setup
- `scripts/localstack-setup.sh` - Create mock resources
- `config/samples/localstack-hibernateplan.yaml` - Test manifest
- `LOCALSTACK-TESTING.md` - Full documentation
- `docs/LOCALSTACK-SETUP.md` - Detailed guide
