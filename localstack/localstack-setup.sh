#!/bin/bash
# LocalStack setup script for Hibernator testing
# Creates mock AWS resources for testing

set -e

ENDPOINT="http://localhost:4566"
REGION="us-east-1"

echo "🚀 Setting up LocalStack resources for Hibernator testing..."

# Export AWS credentials for LocalStack
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=$REGION

# Create EC2 instances
echo "📦 Creating mock EC2 instances..."
aws ec2 run-instances \
  --image-id ami-12345678 \
  --count 1 \
  --instance-type t2.micro \
  --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=test-instance-1},{Key=Hibernate,Value=true}]" \
  --region $REGION \
  --endpoint-url $ENDPOINT \
  2>/dev/null || echo "⚠️  EC2 instance creation (this is expected if already exists)"

aws ec2 run-instances \
  --image-id ami-87654321 \
  --count 1 \
  --instance-type t2.small \
  --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=test-instance-2},{Key=Hibernate,Value=true}]" \
  --region $REGION \
  --endpoint-url $ENDPOINT \
  2>/dev/null || echo "⚠️  EC2 instance creation (this is expected if already exists)"

# Create RDS instances
echo "🗄️  Creating mock RDS instances..."
aws rds create-db-instance \
  --db-instance-identifier test-postgres-db \
  --db-instance-class db.t2.micro \
  --engine postgres \
  --master-username admin \
  --master-user-password testpassword123 \
  --region $REGION \
  --endpoint-url $ENDPOINT \
  2>/dev/null || echo "⚠️  RDS instance creation (this is expected if already exists)"

aws rds create-db-instance \
  --db-instance-identifier test-mysql-db \
  --db-instance-class db.t2.micro \
  --engine mysql \
  --master-username admin \
  --master-user-password testpassword123 \
  --region $REGION \
  --endpoint-url $ENDPOINT \
  2>/dev/null || echo "⚠️  RDS instance creation (this is expected if already exists)"

# List created resources
echo ""
echo "✅ Setup complete! Resources created:"
echo ""
echo "📋 EC2 Instances:"
aws ec2 describe-instances \
  --region $REGION \
  --endpoint-url $ENDPOINT \
  --query 'Reservations[*].Instances[*].[InstanceId,InstanceType,State.Name,Tags[?Key==`Name`].Value|[0]]' \
  --output table

echo ""
echo "📋 RDS Instances:"
aws rds describe-db-instances \
  --region $REGION \
  --endpoint-url $ENDPOINT \
  --query 'DBInstances[*].[DBInstanceIdentifier,DBInstanceClass,DBInstanceStatus]' \
  --output table

echo ""
echo "📝 Next steps:"
echo "1. Create CloudProvider secret in Kubernetes:"
echo "   kubectl create secret generic localstack-credentials \\"
echo "     --from-literal=accessKeyId=test \\"
echo "     --from-literal=secretAccessKey=test \\"
echo "     -n hibernator-system"
echo ""
echo "2. Apply HibernatePlan:"
echo "   kubectl apply -f config/samples/localstack-hibernateplan.yaml"
echo ""
echo "3. Watch controller logs:"
echo "   kubectl logs -n hibernator-system -f deployment/hibernator-controller-manager"
