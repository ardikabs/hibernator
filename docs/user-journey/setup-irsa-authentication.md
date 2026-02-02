# Setup IRSA Authentication

**Tier:** `[Enhanced]`

**Personas:** Cloud Administrator, DevOps Engineer, Security Officer

**When:** Configuring secure AWS credential access for Hibernator without managing Secret distribution

**Why:** IRSA (IAM Roles for Service Accounts) provides automatic credential rotation with zero Secret management overhead.

---

## User Stories

**Story 1:** As a **Cloud Administrator**, I want to **set up OIDC and IAM roles for IRSA**, so that **EKS pods can assume IAM roles without managing Secrets**.

**Story 2:** As a **Security Officer**, I want to **enforce fine-grained IAM policies**, so that **Hibernator has only the permissions it needs (principle of least privilege)**.

**Story 3:** As a **DevOps Engineer**, I want to **bind IRSA to the Hibernator ServiceAccount**, so that **the operator can automatically access AWS APIs without static credentials**.

---

## When/Context

- **Automatic rotation:** Credentials rotate automatically every 15 minutes
- **No Secrets:** No AWS access keys stored in Kubernetes
- **Principle of least privilege:** Fine-grained IAM policies per role
- **Cross-account support:** AssumeRole for managing resources in other AWS accounts

---

## Business Outcome

Enable secure AWS access for Hibernator with automatic credential rotation and minimal operational overhead.

---

## Step-by-Step Flow

### 1. **Verify EKS cluster has OIDC provider**

```bash
# Check if OIDC provider is configured
aws eks describe-cluster \
  --name my-cluster \
  --region us-east-1 \
  --query 'cluster.identity.oidc.issuer'

# Output: https://oidc.eks.us-east-1.amazonaws.com/id/EXAMPLEABCD1234567890EX

# If no output, enable OIDC:
# https://docs.aws.amazon.com/eks/latest/userguide/enable-iam-roles-for-service-accounts.html
```

### 2. **Extract OIDC provider details**

```bash
# Get OIDC provider ID and thumbprint
OIDC_ID=$(aws eks describe-cluster --name my-cluster --region us-east-1 \
  --query 'cluster.identity.oidc.issuer' --output text | sed -e "s/^https:\/\///")
# Output: oidc.eks.us-east-1.amazonaws.com/id/EXAMPLEABCD1234567890EX

# Get thumbprint (usually stable, check current)
THUMBPRINT=$(echo | openssl s_client -connect $OIDC_ID:443 2>/dev/null \
  | openssl x509 -fingerprint -noout | sed -e 's/://g')
```

### 3. **Create IAM OpenID Connect Provider (one-time)**

```bash
# Add OIDC provider to AWS account
aws iam create-open-id-connect-provider \
  --url https://$OIDC_ID \
  --thumbprint-list $THUMBPRINT \
  --client-id-list sts.amazonaws.com

# Output:
# {
#     "OpenIDConnectProviderArn": "arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLEABCD..."
# }
```

### 4. **Create IAM role with IRSA trust relationship**

```bash
# Create trust policy JSON
cat > trust-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLEABCD1234567890EX"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "oidc.eks.us-east-1.amazonaws.com/id/EXAMPLEABCD1234567890EX:sub": "system:serviceaccount:hibernator-system:hibernator-runner"
        }
      }
    }
  ]
}
EOF

# Create the role
aws iam create-role \
  --role-name hibernator-operator \
  --assume-role-policy-document file://trust-policy.json

# Output:
# {
#     "Role": {
#         "RoleName": "hibernator-operator",
#         "Arn": "arn:aws:iam::123456789012:role/hibernator-operator",
#         ...
#     }
# }
```

### 5. **Attach hibernation permissions to IAM role**

```bash
# Create policy for hibernation operations
cat > hibernation-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeInstances",
        "ec2:StopInstances",
        "ec2:StartInstances",
        "ec2:DescribeTags",
        "rds:DescribeDBInstances",
        "rds:StopDBInstance",
        "rds:StartDBInstance",
        "rds:CreateDBSnapshot",
        "rds:DescribeDBSnapshots",
        "eks:DescribeCluster",
        "eks:DescribeNodegroup",
        "autoscaling:DescribeAutoScalingGroups",
        "autoscaling:SetDesiredCapacity"
      ],
      "Resource": "*"
    }
  ]
}
EOF

# Attach policy to role
aws iam put-role-policy \
  --role-name hibernator-operator \
  --policy-name hibernation-access \
  --policy-document file://hibernation-policy.json
```

### 6. **Annotate Kubernetes ServiceAccount with IAM role**

```bash
# Get the IAM role ARN
ROLE_ARN=$(aws iam get-role --role-name hibernator-operator \
  --query 'Role.Arn' --output text)

# Annotate the runner ServiceAccount
kubectl annotate serviceaccount hibernator-runner \
  -n hibernator-system \
  eks.amazonaws.com/role-arn=$ROLE_ARN \
  --overwrite

# Verify annotation
kubectl get sa hibernator-runner -n hibernator-system -o yaml | grep annotations -A 2
# Output:
# annotations:
#   eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/hibernator-operator
```

### 7. **Verify IRSA is working**

```bash
# Check projected volume is mounted in pod
kubectl run -it --rm debug --image=amazon/aws-cli:latest \
  --serviceaccount=hibernator-runner \
  -n hibernator-system -- bash

# Inside pod:
$ aws sts get-caller-identity
# Output:
# {
#     "UserId": "AIDAI...:oidc.eks.us-east-1.amazonaws.com/id/...:system:serviceaccount:hibernator-system:hibernator-runner",
#     "Account": "123456789012",
#     "Arn": "arn:aws:iam::123456789012:assumed-role/hibernator-operator/..."
# }

# Verify EC2 access works:
$ aws ec2 describe-instances --max-results 1
# Should return instances without credential errors
```

### 8. **Create CloudProvider CR using IRSA**

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: aws-prod
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "123456789012"
    region: us-east-1
    auth:
      serviceAccount: {}  # Empty = use pod's IRSA identity
```

No AWS_ACCESS_KEY_ID or AWS_SECRET_ACCESS_KEY needed!

---

## Cross-Account IRSA

For managing resources in another AWS account:

```bash
# In TARGET account (where resources live):
# Create role with AssumeRole trust

cat > cross-account-trust-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::111111111111:role/hibernator-operator"
      },
      "Action": "sts:AssumeRole"
    }
  ]
}
EOF

# Create role in target account
aws iam create-role \
  --role-name hibernator-target \
  --assume-role-policy-document file://cross-account-trust-policy.json

# Attach permissions to target role
aws iam put-role-policy --role-name hibernator-target ...

# In SOURCE account: Update CloudProvider to assume role
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: aws-prod
spec:
  type: aws
  aws:
    accountId: "111111111111"  # TARGET account
    region: us-east-1
    assumeRoleArn: "arn:aws:iam::111111111111:role/hibernator-target"
    auth:
      serviceAccount: {}  # Use prod account IRSA, then assume target role
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Use IRSA?** | Yes (recommended) | Auto-rotation; secure |
| | No (static keys) | Manual rotation; simpler setup but higher risk |
| **Cross-account?** | No (single account) | Simpler; all resources in one account |
| | Yes (AssumeRole) | Production manages dev/staging resources |

---

## Outcome

✓ IRSA configured. Hibernator runner pods automatically obtain AWS credentials with zero Secret management. Credentials rotate every 15 minutes.

---

## Related Journeys

- [Create CloudProvider Connector](create-cloudprovider-connector.md) — Reference IRSA in CloudProvider CR
- [Setup Cross-Account Hibernation](setup-cross-account-hibernation.md) — Cross-account AssumeRole

---

## Pain Points Solved

**RFC-0001:** IRSA eliminates Secret management complexity. Projected tokens auto-rotated by kubelet. No credential distribution needed.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (IRSA authentication, credential isolation, projected tokens)
