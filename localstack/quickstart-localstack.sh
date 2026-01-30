#!/bin/bash
# Quick start: LocalStack + Hibernator setup

set -e

CLUSTER_NAME="hibernator-test"
REGION="us-east-1"

echo "🚀 Starting Hibernator LocalStack sandbox environment..."

# Step 1: Start LocalStack
echo "1️⃣  Starting LocalStack..."
docker-compose -f localstack-compose.yml up -d
echo "   ✅ LocalStack started on http://localhost:4566"
sleep 5  # Give LocalStack time to initialize

# Step 2: Create local Kubernetes cluster
echo "2️⃣  Creating local Kubernetes cluster with kind..."
if ! kind get clusters | grep -q "^$CLUSTER_NAME$"; then
  kind create cluster --name $CLUSTER_NAME --quiet
  echo "   ✅ Cluster '$CLUSTER_NAME' created"
else
  echo "   ℹ️  Cluster '$CLUSTER_NAME' already exists"
fi

# Set context
kubectl cluster-info --context kind-$CLUSTER_NAME > /dev/null

# Step 3: Install Hibernator CRDs and controller
echo "3️⃣  Installing Hibernator..."
make install --quiet || echo "   ✅ CRDs already installed"
make deploy --quiet
echo "   ✅ Hibernator deployed to cluster"

# Step 4: Wait for controller to be ready
echo "4️⃣  Waiting for controller to be ready..."
kubectl wait --for=condition=Ready pod \
  -l control-plane=hibernator-controller-manager \
  -n hibernator-system \
  --timeout=120s 2>/dev/null || echo "   ℹ️  Controller startup in progress..."

# Step 5: Create LocalStack secret
echo "5️⃣  Creating LocalStack credentials secret..."
kubectl create secret generic localstack-credentials \
  --from-literal=accessKeyId=test \
  --from-literal=secretAccessKey=test \
  -n hibernator-system \
  --dry-run=client -o yaml | kubectl apply -f - > /dev/null
echo "   ✅ LocalStack credentials configured"

# Step 6: Set up LocalStack resources
echo "6️⃣  Setting up mock AWS resources..."
bash scripts/localstack-setup.sh

echo ""
echo "🎉 Sandbox environment ready!"
echo ""
echo "📋 Quick commands:"
echo ""
echo "  Watch controller logs:"
echo "    kubectl logs -n hibernator-system -f deployment/hibernator-controller-manager"
echo ""
echo "  Create test HibernationPlan:"
echo "    kubectl apply -f config/samples/localstack-hibernateplan.yaml"
echo ""
echo "  Check status:"
echo "    kubectl get hibernateplans"
echo "    kubectl describe hibernateplan localstack-test"
echo ""
echo "  Access LocalStack:"
echo "    export AWS_ENDPOINT_URL=http://localhost:4566"
echo "    aws ec2 describe-instances"
echo "    aws rds describe-db-instances"
echo ""
echo "  Cleanup:"
echo "    docker-compose -f localstack-compose.yml down"
echo "    kind delete cluster --name $CLUSTER_NAME"
echo ""
