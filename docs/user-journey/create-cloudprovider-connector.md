# Create CloudProvider Connector

**Tier:** `[MVP]`

**Personas:** Cloud Administrator, DevOps Engineer, Platform Engineer

**When:** Setting up access to cloud resources (AWS, GCP, Azure accounts)

**Why:** Hibernator needs credentials and configuration to interact with cloud APIs and stop/start resources.

---

## User Stories

**Story 1:** As a **Cloud Administrator**, I want to **securely store cloud credentials and configuration in a CloudProvider CR**, so that **Hibernator can interact with AWS/GCP/Azure APIs safely**.

---

## When/Context

- **Credential safety:** Credentials stored in Kubernetes Secrets with RBAC protection
- **Multi-cloud support:** Single cluster can manage resources across multiple cloud accounts
- **Lifecycle management:** Credentials rotated independently per provider
- **Audit trail:** All access to credentials can be logged

---

## Business Outcome

Create a `CloudProvider` CR that securely stores cloud credentials and configuration for hibernation operations.

---

## Step-by-Step Flow

### 1. **Choose authentication method**

**Option A: IRSA (IAM Roles for Service Accounts) — Recommended**

Hibernator runner pods inherit AWS IAM role through Kubernetes ServiceAccount.

```bash
# Prerequisites:
# - EKS cluster with OIDC provider configured
# - IAM role with hibernation permissions

# Get OIDC provider:
aws eks describe-cluster --name my-cluster \
  --query 'cluster.identity.oidc.issuer' --region us-east-1
# Output: https://oidc.eks.us-east-1.amazonaws.com/id/EXAMPLEID

# Create IAM role with trust relationship:
cat > trust-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLEID"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "oidc.eks.us-east-1.amazonaws.com/id/EXAMPLEID:sub": "system:serviceaccount:hibernator-system:hibernator-runner"
        }
      }
    }
  ]
}
EOF

aws iam create-role --role-name hibernator-operator \
  --assume-role-policy-document file://trust-policy.json

# Attach hibernation permissions:
aws iam put-role-policy --role-name hibernator-operator \
  --policy-name hibernation --policy-document '{
    "Version": "2012-10-17",
    "Statement": [
      {
        "Effect": "Allow",
        "Action": [
          "ec2:DescribeInstances",
          "ec2:StopInstances",
          "ec2:StartInstances",
          "rds:DescribeDBInstances",
          "rds:StopDBInstance",
          "rds:StartDBInstance",
          "eks:DescribeCluster",
          "autoscaling:DescribeAutoScalingGroups",
          "autoscaling:SetDesiredCapacity"
        ],
        "Resource": "*"
      }
    ]
  }'
```

**Option B: Static AWS Access Keys — Fallback**

```bash
# Create IAM user (not recommended; prefer IRSA):
aws iam create-user --user-name hibernator-operator

# Attach policy:
aws iam attach-user-policy --user-name hibernator-operator \
  --policy-arn arn:aws:iam::aws:policy/AdministratorAccess

# Create access keys:
aws iam create-access-key --user-name hibernator-operator
# Output: AccessKeyId, SecretAccessKey (save securely)
```

### 2. **Create Secret (if using static credentials)**

Skip this if using IRSA.

```bash
kubectl create secret generic aws-credentials \
  --from-literal=AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE \
  --from-literal=AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY \
  -n hibernator-system
```

### 3. **Create CloudProvider CR**

**With IRSA:**

```yaml
apiVersion: connector.hibernator.ardikasaputro.io/v1alpha1
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
      serviceAccount: {}  # Use runner pod's SA identity (IRSA)
```

**With static credentials:**

```yaml
apiVersion: connector.hibernator.ardikasaputro.io/v1alpha1
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
      static:
        secretRef:
          name: aws-credentials
          namespace: hibernator-system
```

**With AssumeRole (cross-account):**

```yaml
apiVersion: connector.hibernator.ardikasaputro.io/v1alpha1
kind: CloudProvider
metadata:
  name: aws-prod
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "123456789012"
    region: us-east-1
    assumeRoleArn: arn:aws:iam::987654321098:role/hibernator-target
    auth:
      serviceAccount: {}  # Base identity (IRSA in prod account)
      # Then assumes role in target account (dev/staging)
```

### 4. **Verify connector is ready**

```bash
kubectl get cloudproviders aws-prod

# Check status:
kubectl describe cloudprovider aws-prod
# Should show: Status = Ready

# If not ready, check events:
kubectl describe cloudprovider aws-prod | grep Events -A 10
```

### 5. **Test connectivity**

Create a simple test to verify credentials work:

```bash
# Create a test HibernationPlan that uses this connector
kubectl apply -f - <<EOF
apiVersion: hibernator.ardikasaputro.io/v1alpha1
kind: HibernationPlan
metadata:
  name: connectivity-test
  namespace: hibernator-system
spec:
  schedule:
    timezone: "UTC"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE"]
  execution:
    strategy:
      type: Sequential
  behavior:
    mode: BestEffort
  targets:
    - name: test-ec2
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        selector:
          tags:
            Test: "true"
EOF

# Monitor the Job:
kubectl get jobs -l hibernator/plan=connectivity-test -w
kubectl logs job/$(kubectl get jobs -l hibernator/plan=connectivity-test -o name) -f
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Authentication method?** | IRSA (recommended) | Automatic rotation; no secret distribution |
| | Static keys (fallback) | Simpler to set up; requires credential rotation |
| **Cross-account access?** | Yes (AssumeRole) | Production account manages dev/staging resources |
| | No (same account) | All resources in same account |
| **Which AWS permissions?** | Least privilege | Only needed APIs (better security) |
| | Admin (simpler) | Full access; higher blast radius |

---

## Outcome

✓ CloudProvider connector created and tested; HibernationPlan can now use this provider to access AWS resources.

---

## Related Journeys

- [Setup IRSA Authentication](setup-irsa-authentication.md) — Deep dive on IRSA setup
- [Setup Cross-Account Hibernation](setup-cross-account-hibernation.md) — Multi-account hibernation
- [Hibernation Plan Initial Design](hibernation-plan-initial-design.md) — Reference CloudProvider in targets

---

## Pain Points Solved

**RFC-0001:** Credential management simplified with IRSA; eliminates Secret distribution complexity.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (CloudProvider CRD, IRSA, credential isolation)
