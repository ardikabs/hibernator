# Connectors

Connectors provide Hibernator with credentials and configuration to access external resources. There are two types of connectors.

## CloudProvider

A `CloudProvider` represents a cloud account with credentials for managing cloud-native resources (EKS node groups, RDS instances, EC2 instances).

### AWS Configuration

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: aws-production
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "123456789012"
    region: us-west-2
    assumeRoleArn: arn:aws:iam::123456789012:role/HibernatorRole
    auth:
      serviceAccount: {}
```

### Authentication Methods

=== "IRSA (Recommended)"

    Uses IAM Roles for Service Accounts. The runner pod's ServiceAccount must have the appropriate IAM role annotation:

    ```yaml
    auth:
      serviceAccount: {}
    ```

    The pod's ServiceAccount needs the `eks.amazonaws.com/role-arn` annotation pointing to an IAM role with permissions for the target resources.

=== "Static Credentials"

    References a Kubernetes Secret containing AWS credentials:

    ```yaml
    auth:
      static:
        secretRef:
          name: aws-credentials
          namespace: hibernator-system
    ```

    !!! warning
        Static credentials are less secure than IRSA. Use IRSA whenever possible.

### Role Assumption

The optional `assumeRoleArn` field enables cross-account access. The runner assumes the specified IAM role before performing operations:

- **With IRSA**: The pod's SA credentials assume the target role
- **With Static**: The static credentials assume the target role

## K8SCluster

A `K8SCluster` represents a Kubernetes cluster that Hibernator can access for managing Kubernetes-level resources (Karpenter NodePools, workload scaling).

### EKS Cluster

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: dev-eks
  namespace: hibernator-system
spec:
  providerRef:
    name: aws-dev
    namespace: hibernator-system
  eks:
    name: dev-cluster
    region: ap-southeast-3
```

The `providerRef` links to a `CloudProvider` for authentication.

### GKE Cluster

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: staging-gke
  namespace: hibernator-system
spec:
  gke:
    name: staging-cluster
    project: my-gcp-project
    location: us-central1
```

### Generic Kubernetes

For clusters accessible via kubeconfig:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: on-prem
  namespace: hibernator-system
spec:
  k8s:
    kubeconfigRef:
      name: onprem-kubeconfig
      namespace: hibernator-system
```

Or for self-management (in-cluster config):

```yaml
spec:
  k8s:
    inCluster: true
```

## Connector References

Targets in a `HibernatePlan` reference connectors via `connectorRef`:

```yaml
targets:
  - name: my-target
    type: eks
    connectorRef:
      kind: CloudProvider    # CloudProvider or K8SCluster
      name: aws-production
      namespace: hibernator-system  # Optional, defaults to plan namespace
```

## See Also

- [API Reference: CloudProvider](../api-reference/index.md#cloudprovider) — Full field documentation
- [API Reference: K8SCluster](../api-reference/index.md#k8scluster) — Full field documentation
