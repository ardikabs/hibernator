# LocalStack Setup for Hibernator Testing

This guide shows how to set up LocalStack to test Hibernator AWS executors locally without real AWS resources.

## What is LocalStack?

LocalStack is a fully functional local AWS cloud stack that runs in Docker. It mocks AWS services like EKS, RDS, EC2, etc., allowing you to:

- ✅ Test credential passing and authentication flow
- ✅ Test API calls and error handling
- ✅ Test parameter parsing and validation
- ✅ Test executor logic without real AWS resources

**Note:** LocalStack provides mock APIs, not actual infrastructure changes. This is perfect for unit/integration testing!

## Prerequisites

- Docker and Docker Compose
- `kind` or local Kubernetes cluster
- AWS CLI (optional, for testing directly)

## Setup Steps

### 1. Start LocalStack

```bash
# Using Docker Compose (recommended)
docker-compose -f localstack-compose.yml up -d

# Or manually
docker run -d \
  -p 4566:4566 \
  -p 4571:4571 \
  -e SERVICES=ec2,rds,eks \
  -e DEBUG=1 \
  localstack/localstack:latest
```

### 2. Configure AWS CLI (Optional)

```bash
# Set up AWS CLI to use LocalStack
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1
export AWS_ENDPOINT_URL=http://localhost:4566

# Test connectivity
aws ec2 describe-instances --endpoint-url http://localhost:4566
```

### 3. Create LocalStack Resources

```bash
# Create a mock EC2 instance
aws ec2 run-instances \
  --image-id ami-12345678 \
  --count 1 \
  --tag-specifications 'ResourceType=instance,Tags=[{Key=Hibernate,Value=true}]' \
  --endpoint-url http://localhost:4566

# Create mock RDS instance
aws rds create-db-instance \
  --db-instance-identifier test-db \
  --db-instance-class db.t2.micro \
  --engine postgres \
  --master-username admin \
  --master-user-password password123 \
  --endpoint-url http://localhost:4566
```

### 4. Deploy Hibernator with LocalStack

Create a CloudProvider that points to LocalStack:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: localstack
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "000000000000"  # LocalStack account ID
    region: us-east-1
    auth:
      static:
        secretRef:
          name: localstack-credentials
          namespace: hibernator-system
    # IMPORTANT: Point to LocalStack endpoint
    endpoint: http://localstack:4566
```

### 5. Create LocalStack Secret

```bash
kubectl create secret generic localstack-credentials \
  --from-literal=accessKeyId=test \
  --from-literal=secretAccessKey=test \
  -n hibernator-system
```

### 6. Create a Test HibernatePlan

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: localstack-test
  namespace: default
spec:
  schedule:
    timezone: UTC
    offHours:
      - start: "22:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Sequential
  targets:
    - name: test-ec2
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: localstack
      parameters:
        selector:
          tags:
            Hibernate: "true"
```

## Testing Workflow

```bash
# 1. Start LocalStack
docker-compose -f localstack-compose.yml up -d

# 2. Deploy Hibernator to local cluster
kind create cluster --name hibernator-test
make install
make deploy

# 3. Create LocalStack resources
bash scripts/localstack-setup.sh

# 4. Create CloudProvider secret
kubectl create secret generic localstack-credentials \
  --from-literal=accessKeyId=test \
  --from-literal=secretAccessKey=test \
  -n hibernator-system

# 5. Apply HibernatePlan
kubectl apply -f config/samples/localstack-hibernateplan.yaml

# 6. Watch controller logs
kubectl logs -n hibernator-system -f deployment/hibernator-controller-manager

# 7. Check execution status
kubectl get hibernateplans -o wide
kubectl describe hibernateplan localstack-test
```

## Debugging

```bash
# Check LocalStack logs
docker logs -f localstack_main

# Test LocalStack connectivity
curl http://localhost:4566/_localstack/health

# List LocalStack services
curl http://localhost:4566/_localstack/services

# Check AWS resources
aws ec2 describe-instances --endpoint-url http://localhost:4566
aws rds describe-db-instances --endpoint-url http://localhost:4566
```

## Cleanup

```bash
# Stop LocalStack
docker-compose -f localstack-compose.yml down

# Or manually
docker stop localstack_main
docker rm localstack_main

# Delete local cluster
kind delete cluster --name hibernator-test
```

## Limitations & Workarounds

| Feature | Status | Workaround |
|---------|--------|-----------|
| EC2 stop/start | ✅ Works | Fully mocked |
| RDS stop/start | ✅ Works | Fully mocked |
| EKS operations | ⚠️ Limited | Mock API calls only |
| Karpenter scaling | ❌ Not available | Would need K8s cluster setup |
| Real node changes | ❌ Not applicable | Use integration tests instead |

## Next Steps

1. **Unit Tests:** `make test-unit` (44.6% coverage, no infrastructure needed)
2. **LocalStack Tests:** Use the setup above for AWS executor testing
3. **Real Integration:** Deploy to real AWS with small test clusters (use dev/stg environments)

## References

- [LocalStack Documentation](https://docs.localstack.cloud/)
- [LocalStack GitHub](https://github.com/localstack/localstack)
- [AWS SDK Go v2 Endpoint Configuration](https://aws.github.io/aws-sdk-go-v2/docs/configuring-sdk/endpoints/)
