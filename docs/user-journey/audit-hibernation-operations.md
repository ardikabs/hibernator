# Audit Hibernation Operations

**Tier:** `[Advanced]`

**Personas:** Security Officer, Compliance Officer, Auditor

**When:** Creating compliance trails for hibernation operations across cloud and Kubernetes platforms

**Why:** Hibernation involves infrastructure state changes; full audit trails ensure compliance with regulatory requirements.

---

## User Stories

**Story 1:** As a **Security Officer**, I want to **enable CloudTrail and K8S audit logs for hibernation operations**, so that **all state changes are recorded immutably**.

**Story 2:** As a **Compliance Officer**, I want to **query audit logs to verify who initiated hibernation and when**, so that **I can demonstrate compliance to regulators**.

**Story 3:** As an **Auditor**, I want to **retain and analyze long-term audit trails**, so that **I can detect patterns and anomalies in hibernation behavior**.

---

## When/Context

- **Compliance:** SOC2, CIS, or regulatory audit requirements
- **Traceability:** Every shutdown/restore action logged with identity
- **Immutability:** Audit logs cannot be modified after creation
- **Retention:** Long-term storage for historical compliance

---

## Business Outcome

Establish complete audit trails for all hibernation operations meeting regulatory and compliance requirements.

---

## Step-by-Step Flow

### 1. **Enable CloudTrail for AWS resources**

```bash
# Create S3 bucket for audit logs
aws s3 mb s3://hibernator-audit-logs --region us-east-1

# Enable versioning and encryption
aws s3api put-bucket-versioning \
  --bucket hibernator-audit-logs \
  --versioning-configuration Status=Enabled

# Create CloudTrail
aws cloudtrail create-trail \
  --name hibernator-operations \
  --s3-bucket-name hibernator-audit-logs \
  --region us-east-1 \
  --enable-log-file-validation

# Start logging
aws cloudtrail start-logging --trail-name hibernator-operations
```

### 2. **Configure CloudTrail event selectors**

Log only hibernation-relevant API calls:

```bash
aws cloudtrail put-event-selectors \
  --trail-name hibernator-operations \
  --event-selectors '[
    {
      "ReadWriteType": "All",
      "IncludeManagementEvents": true,
      "DataResources": [
        {
          "Type": "AWS::EC2::Instance",
          "Values": ["arn:aws:ec2:*:*:instance/*"]
        },
        {
          "Type": "AWS::RDS::DBInstance",
          "Values": ["arn:aws:rds:*:*:db/*"]
        }
      ]
    }
  ]'
```

### 3. **Enable Kubernetes API audit logging**

Edit API server configuration:

```yaml
# /etc/kubernetes/manifests/kube-apiserver.yaml (or use admission controller)
apiVersion: v1
kind: Pod
metadata:
  name: kube-apiserver
spec:
  containers:
  - name: kube-apiserver
    command:
    - kube-apiserver
    - --audit-log-path=/var/log/kubernetes/audit.log
    - --audit-policy-file=/etc/kubernetes/audit-policy.yaml
    - --audit-log-maxage=90
    - --audit-log-maxbackup=10
    - --audit-log-maxsize=100
    volumeMounts:
    - name: audit-policy
      mountPath: /etc/kubernetes/audit-policy.yaml
      readOnly: true
    - name: audit-logs
      mountPath: /var/log/kubernetes/
  volumes:
  - name: audit-policy
    hostPath:
      path: /etc/kubernetes/audit-policy.yaml
  - name: audit-logs
    hostPath:
      path: /var/log/kubernetes/
```

### 4. **Create Kubernetes audit policy for Hibernator**

```yaml
# audit-policy.yaml
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
  # Log all changes to HibernationPlans
  - level: RequestResponse
    verbs: ["create", "update", "patch", "delete"]
    resources: ["hibernateplans"]
    omitStages:
    - RequestReceived

  # Log all reads of CloudProviders (sensitive)
  - level: RequestResponse
    verbs: ["get", "list"]
    resources: ["cloudproviders"]
    omitStages:
    - RequestReceived

  # Log all Job creation (runner Jobs)
  - level: RequestResponse
    verbs: ["create", "delete"]
    resources: ["jobs"]
    namespaces: ["hibernator-system"]
    omitStages:
    - RequestReceived

  # Log ConfigMap changes (restore data)
  - level: RequestResponse
    verbs: ["create", "update", "patch", "delete"]
    resources: ["configmaps"]
    namespaceSelector:
      matchLabels:
        audit-hibernation: "true"
    omitStages:
    - RequestReceived

  # Default: log everything else at Metadata level
  - level: Metadata
    omitStages:
    - RequestReceived
```

### 5. **Send K8S audit logs to CloudWatch or S3**

```bash
# Stream K8S audit logs to S3
aws s3 sync /var/log/kubernetes/ \
  s3://hibernator-audit-logs/k8s-audit/ \
  --exclude "*" \
  --include "audit.log*" \
  --delete

# Or configure CloudWatch agent
cat > /etc/cloudwatch-config.json <<EOF
{
  "logs": {
    "logs_collected": {
      "files": {
        "collect_list": [
          {
            "file_path": "/var/log/kubernetes/audit.log",
            "log_group_name": "/aws/k8s/hibernator-audit",
            "log_stream_name": "{instance_id}"
          }
        ]
      }
    }
  }
}
EOF
```

### 6. **Query CloudTrail for hibernation events**

```bash
# Find all EC2 stop/start operations
aws cloudtrail lookup-events \
  --lookup-attributes AttributeKey=EventName,AttributeValue=StopInstances \
  --query 'Events[*].[EventTime,Username,EventName,CloudTrailEvent]' \
  --region us-east-1 | jq .

# Output:
# [
#   [
#     "2026-02-01T20:05:00Z",
#     "arn:aws:iam::123456789012:assumed-role/hibernator-operator/...",
#     "StopInstances",
#     "{...full API call details...}"
#   ]
# ]

# Find all RDS stop operations
aws cloudtrail lookup-events \
  --lookup-attributes AttributeKey=EventName,AttributeValue=StopDBInstance \
  --query 'Events[*].[EventTime,Username,EventName]'
```

### 7. **Query Kubernetes audit logs**

```bash
# Find all HibernationPlan changes
kubectl get events -A --sort-by='.lastTimestamp' \
  --field-selector involvedObject.kind=HibernationPlan

# Or parse audit logs directly:
grep '"verb":"create".*"resource":"hibernateplans"' \
  /var/log/kubernetes/audit.log | jq .

# Output:
# {
#   "level": "RequestResponse",
#   "auditID": "91583...",
#   "stage": "ResponseComplete",
#   "requestObject": {
#     "apiVersion": "hibernator.ardikasaputro.io/v1alpha1",
#     "kind": "HibernationPlan",
#     "metadata": {
#       "name": "prod-offhours",
#       "creationTimestamp": "2026-02-01T20:00:00Z"
#     }
#   },
#   "user": {
#     "username": "alice@company.com",
#     "uid": "...github_user_id..."
#   }
# }
```

### 8. **Create compliance report**

```bash
#!/bin/bash
# Generate monthly compliance report

echo "=== Hibernation Audit Report: February 2026 ==="
echo ""

echo "Cloud Resource Changes (CloudTrail):"
aws cloudtrail lookup-events \
  --lookup-attributes AttributeKey=EventSource,AttributeValue=ec2.amazonaws.com \
  --start-time 2026-02-01T00:00:00Z \
  --end-time 2026-02-28T23:59:59Z \
  --query 'Events[*].[EventTime,Username,EventName]' \
  --output table

echo ""
echo "Kubernetes Plan Changes:"
grep '"verb":"create".*"resource":"hibernateplans"' \
  /var/log/kubernetes/audit.log | \
  jq '[.eventTime, .user.username, .requestObject.metadata.name] | @csv'

echo ""
echo "Exception Approvals:"
grep 'exception.*approved' /var/log/kubernetes/audit.log | \
  jq '[.eventTime, .user.username, .verb] | @csv'
```

### 9. **Retention and compliance**

```bash
# Set S3 lifecycle for audit logs (7 years retention for compliance)
aws s3api put-bucket-lifecycle-configuration \
  --bucket hibernator-audit-logs \
  --lifecycle-configuration '{
    "Rules": [
      {
        "ID": "ArchiveAuditLogs",
        "Status": "Enabled",
        "Transitions": [
          {
            "Days": 90,
            "StorageClass": "GLACIER"
          }
        ],
        "Expiration": {
          "Days": 2555  # ~7 years
        }
      }
    ]
  }'

# Enable S3 Object Lock for immutability
aws s3api put-object-lock-configuration \
  --bucket hibernator-audit-logs \
  --object-lock-configuration '{
    "ObjectLockEnabled": "Enabled",
    "Rule": {
      "DefaultRetention": {
        "Mode": "COMPLIANCE",
        "Days": 2555
      }
    }
  }'
```

---

## Compliance Checklist

- [ ] CloudTrail enabled and logging to S3
- [ ] K8S audit logging configured
- [ ] Audit logs sent to immutable storage (S3 Object Lock)
- [ ] Retention policy >= 7 years
- [ ] Encryption enabled (S3 SSE-S3)
- [ ] Log file validation enabled (CloudTrail)
- [ ] Access restricted to auditors only (IAM policies)
- [ ] Monthly compliance report generated

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Log storage?** | S3 + Object Lock | Immutable; long-term; compliant |
| | CloudWatch Logs | Real-time queries; shorter retention |
| **Retention?** | 7 years (regulatory) | Required for compliance |
| | 1-2 years (operational) | Reduced cost; less compliance value |

---

## Outcome

✓ Comprehensive audit trail established. CloudTrail + K8S audit logs capture all hibernation operations. Compliant with regulatory requirements.

---

## Related Journeys

- [Setup IRSA Authentication](setup-irsa-authentication.md) — Identity tracing
- [Configure RBAC for Hibernation](configure-rbac-for-hibernation.md) — Access control

---

## Pain Points Solved

**RFC-0001:** Status ledger in HibernationPlan tracks execution with jobRef, logsRef. Combined with CloudTrail and K8S audit logs, provides complete end-to-end audit trail for compliance.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (status ledger, audit trail, Job references)
