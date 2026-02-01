# Setup Cross-Account Hibernation

**Tier:** `[Advanced]`

**Personas:** Cloud Architect, DevOps Engineer, Security Officer

**When:** Production account needs to manage hibernation of resources in dev/staging accounts

**Why:** Centralized governance ensures organization-wide hibernation policies while allowing team autonomy for their environments.

---

## User Stories

**Story 1:** As a **Cloud Architect**, I want to **the production account to manage hibernation of dev/staging resources via cross-account AssumeRole**, so that **central governance can enforce organization-wide policies**.

---

## When/Context

- **Multi-account strategy:** Production (control), Dev (target), Staging (target)
- **Centralized policies:** Production enforces hibernation across all accounts
- **Least privilege:** Dev/Staging have no direct hibernation power
- **Audit trail:** All cross-account actions logged in CloudTrail

---

## Business Outcome

Enable centralized hibernation management from production account with cross-account access to dev/staging resources.

---

## Step-by-Step Flow

### 1. **Architecture overview**

```
PRODUCTION ACCOUNT                  DEV ACCOUNT
┌─────────────────────┐            ┌──────────────────┐
│ Hibernator         │            │ EC2/RDS         │
│ (control plane)     │            │ (target resources)│
│ EKS cluster        │────────────▶ │                 │
│ IRSA role:         │  AssumeRole  │ IAM Role:       │
│  hibernator-op     │   (STS)      │  hibernate-tgt  │
└─────────────────────┘            └──────────────────┘

Trust relationship: Dev account trusts Prod IRSA role
```

### 2. **In PROD account: Verify IRSA is set up**

Already have IRSA configured (see "Setup IRSA Authentication" journey):

```bash
# Prod account IRSA role ARN:
arn:aws:iam::111111111111:role/hibernator-operator
```

### 3. **In DEV account: Create target role**

```bash
# Create trust policy allowing prod account to assume
cat > dev-trust-policy.json <<EOF
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

# Create role in dev account
aws iam create-role \
  --role-name hibernator-target \
  --assume-role-policy-document file://dev-trust-policy.json \
  --region us-east-1

# Attach hibernation permissions
aws iam put-role-policy \
  --role-name hibernator-target \
  --policy-name hibernation \
  --policy-document '{
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
          "rds:StartDBInstance"
        ],
        "Resource": "*"
      }
    ]
  }'
```

### 4. **In PROD account: Update IRSA to assume role**

```bash
# Add AssumeRole permission to prod IRSA role
aws iam put-role-policy \
  --role-name hibernator-operator \
  --policy-name assume-role-dev \
  --policy-document '{
    "Version": "2012-10-17",
    "Statement": [
      {
        "Effect": "Allow",
        "Action": "sts:AssumeRole",
        "Resource": [
          "arn:aws:iam::222222222222:role/hibernator-target",
          "arn:aws:iam::333333333333:role/hibernator-target"
        ]
      }
    ]
  }'
```

### 5. **In PROD: Create CloudProvider for cross-account access**

```yaml
apiVersion: connector.hibernator.ardikasaputro.io/v1alpha1
kind: CloudProvider
metadata:
  name: aws-dev-crossaccount
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "222222222222"        # DEV account ID
    region: us-east-1
    assumeRoleArn: "arn:aws:iam::222222222222:role/hibernator-target"
    auth:
      serviceAccount: {}  # Use PROD account IRSA
                         # Then assumes DEV account role
```

### 6. **In PROD: Create HibernationPlan targeting dev resources**

```yaml
apiVersion: hibernator.ardikasaputro.io/v1alpha1
kind: HibernationPlan
metadata:
  name: cross-account-hibernation
  namespace: hibernator-system
  labels:
    scope: organization-wide
spec:
  schedule:
    timezone: "UTC"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"]

  execution:
    strategy:
      type: Parallel

  targets:
    # DEV account resources
    - name: dev-database
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-dev-crossaccount
      parameters:
        snapshotBeforeStop: false

    - name: dev-instances
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-dev-crossaccount
      parameters:
        selector:
          tags:
            Environment: "dev"
```

### 7. **Verify cross-account access works**

```bash
# From PROD cluster, test assuming DEV role
kubectl run -it --rm debug --image=amazon/aws-cli:latest \
  --serviceaccount=hibernator-runner \
  -n hibernator-system -- bash

# Inside pod:
$ aws sts assume-role \
  --role-arn arn:aws:iam::222222222222:role/hibernator-target \
  --role-session-name hibernator-session \
  --query 'Credentials'

# Output: Temporary credentials for DEV account
# {
#   "AccessKeyId": "ASIA...",
#   "SecretAccessKey": "...",
#   "SessionToken": "..."
# }

# Verify can access DEV EC2
$ aws ec2 describe-instances \
  --region us-east-1 \
  --assumed-role-arn arn:aws:iam::222222222222:role/hibernator-target

# Should return DEV account EC2 instances
```

### 8. **Monitor cross-account execution**

```bash
kubectl describe hibernateplan cross-account-hibernation

# Output:
# Status:
#   Phase: Active
#   Executions:
#     - Target: rds/dev-database
#       State: Completed
#       Message: "Stopped in DEV account (cross-account assumed)"
#     - Target: ec2/dev-instances
#       State: Completed
#       Message: "Stopped in DEV account (cross-account assumed)"
```

### 9. **Audit trail: CloudTrail in both accounts**

**PROD account CloudTrail:**
```
Event: AssumeRole
Principal: arn:aws:iam::111111111111:role/hibernator-operator
Action: sts:AssumeRole
TargetRole: arn:aws:iam::222222222222:role/hibernator-target
```

**DEV account CloudTrail:**
```
Event: StopDBInstance
Principal: arn:aws:iam::222222222222:assumed-role/hibernator-target/...
DBInstance: dev-postgres
SourceAccount: 111111111111  # PROD account ID
```

All actions auditable end-to-end.

---

## Advanced: Multiple target accounts

For STG and PROD target accounts:

```bash
# STG account role
aws iam create-role \
  --role-name hibernator-target \
  --assume-role-policy-document file://stg-trust-policy.json \
  --region us-east-1

# PROD target account role
aws iam create-role \
  --role-name hibernator-target \
  --assume-role-policy-document file://prod-trust-policy.json \
  --region us-east-1

# Update PROD IRSA to assume all roles
aws iam put-role-policy \
  --role-name hibernator-operator \
  --policy-name assume-multi-roles \
  --policy-document '{
    "Statement": [
      {
        "Effect": "Allow",
        "Action": "sts:AssumeRole",
        "Resource": [
          "arn:aws:iam::222222222222:role/hibernator-target",
          "arn:aws:iam::333333333333:role/hibernator-target",
          "arn:aws:iam::444444444444:role/hibernator-target"
        ]
      }
    ]
  }'
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **AssumeRole?** | Yes (recommended) | Centralized management; audit trail |
| | Per-account operators | Decentralized; higher management burden |
| **Trust model?** | Account ARN | More specific (recommended) |
| | Org ID | Broader; any account in org |

---

## Outcome

✓ Cross-account access configured. Production can manage dev/staging hibernation. All actions audited end-to-end.

---

## Related Journeys

- [Setup IRSA Authentication](setup-irsa-authentication.md) — IRSA foundation
- [Create CloudProvider Connector](create-cloudprovider-connector.md) — AssumeRole in connector

---

## Pain Points Solved

**RFC-0001:** Cross-account IRSA eliminates need for credential distribution across accounts. AssumeRole provides fine-grained audit trail and least-privilege access.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (IRSA, cross-account authentication, audit trail)
