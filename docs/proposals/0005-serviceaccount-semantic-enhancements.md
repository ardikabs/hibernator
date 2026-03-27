---
rfc: RFC-0005
title: ServiceAccount Semantic Enhancements
status: Proposed
date: 2026-02-01
---

# RFC 0005 — ServiceAccount Semantic Enhancements

**Keywords:** ServiceAccount, IRSA, Workload-Identity, Multi-Cloud, Validation, Credential-Management, Federated-Identity, Audit

**Status:** Proposed (Future Work)

## Summary

After refactoring CloudProvider to use `auth.serviceAccount: {}` as an empty struct indicating IRSA usage, this RFC proposes potential enhancements for the ServiceAccount semantic to support advanced identity management scenarios.

## 1. Explicit Pod Identity Configuration

Allow users to explicitly configure which ServiceAccount to use for workload identity:

```yaml
spec:
  aws:
    auth:
      serviceAccount:
        # Explicitly specify SA name/namespace (default: use runner pod's SA)
        name: hibernator-runner
        namespace: hibernator-system

        # Explicit annotations for cloud provider workload identity
        annotations:
          eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/..."
```

**Benefits**:
- Allows using a different SA than the runner pod's default
- Explicit configuration for multi-tenant scenarios
- Clear visibility into which identity is being used

## 2. Multi-Cloud Workload Identity Support

Extend to support different cloud providers' workload identity mechanisms:

```yaml
spec:
  aws:
    auth:
      serviceAccount:
        # Cloud-specific workload identity fields
        aws:
          roleArn: "arn:aws:iam::..."  # EKS IRSA
        gcp:
          serviceAccountEmail: "sa@project.iam.gserviceaccount.com"  # GKE Workload Identity
        azure:
          clientId: "..."  # AKS Workload Identity
```

**Benefits**:
- Unified API for multi-cloud workload identity
- Explicit configuration per cloud provider
- Supports cross-cloud scenarios

## 3. Identity Validation Enhancements

Add validation to ensure ServiceAccount exists and has correct configuration:

```yaml
spec:
  aws:
    auth:
      serviceAccount:
        # Validate SA exists and has correct annotations at admission time
        validateIdentity: true

        # Test assume role before accepting CR (pre-flight check)
        validateAssumeRole: true

        # Optional: minimum required permissions to validate
        requiredPermissions:
          - "ec2:DescribeInstances"
          - "rds:StopDBInstance"
```

**Benefits**:
- Fail fast at admission time if SA is misconfigured
- Prevent runtime auth failures
- Clear feedback about missing permissions

## 4. Dynamic Credential Refresh

Monitor and manage credential lifecycle automatically:

```yaml
spec:
  aws:
    auth:
      serviceAccount:
        # Automatic credential rotation monitoring
        credentialRefreshInterval: 1h

        # Emit events when credentials are near expiry
        expiryWarningThreshold: 10m

        # Automatic refresh behavior
        autoRefresh: true
```

**Benefits**:
- Prevent credential expiration during long-running operations
- Proactive alerting for credential lifecycle issues
- Reduced operational burden

## 5. Federated Identity Support

Support OIDC federation for cross-cluster authentication:

```yaml
spec:
  aws:
    auth:
      serviceAccount:
        # OIDC federation for cross-cluster auth
        oidcProvider: "https://oidc.eks.region.amazonaws.com/id/..."

        federatedIdentity:
          audience: "sts.amazonaws.com"
          subject: "system:serviceaccount:namespace:sa-name"

        # Token exchange configuration
        tokenExchange:
          enabled: true
          duration: 3600  # 1 hour
```

**Benefits**:
- Cross-cluster authentication scenarios
- Improved security with OIDC federation
- Support for complex identity workflows

## 6. Audit and Compliance

Enhanced audit trail for identity usage:

```yaml
spec:
  aws:
    auth:
      serviceAccount:
        # Audit configuration
        audit:
          # Record all identity assumptions in status
          recordAssumptions: true

          # Maximum audit history to keep
          maxHistoryLength: 100

          # Emit audit events to external systems
          externalAuditWebhook: "https://audit.example.com/webhook"
```

**Benefits**:
- Complete audit trail of identity usage
- Compliance with regulatory requirements
- Integration with external audit systems

## Implementation Priority

**Recommended order**:
1. **Validation Enhancements (Priority 1)**: Fail-fast at admission time
2. **Explicit Pod Identity (Priority 2)**: Clear configuration and multi-tenant support
3. **Credential Refresh (Priority 3)**: Operational reliability
4. **Multi-Cloud Support (Priority 4)**: Future-proofing
5. **Federated Identity (Priority 5)**: Advanced scenarios
6. **Audit and Compliance (Priority 6)**: Enterprise requirements

## Design Considerations

- **Backward compatibility**: All enhancements should be optional with sensible defaults
- **Webhook validation**: Use admission webhooks to validate SA exists and is properly configured
- **Controller responsibility**: Controller should handle credential monitoring and refresh
- **Status tracking**: Maintain detailed status about identity state and validation results

---

## References

- **RFC-0001**: Hibernator Operator - Control Plane & Runner Model
- **Related Work**: CloudProvider AssumeRoleArn refactoring (moved to AWS spec level)
