# Troubleshooting

Common issues and their solutions.

## Schedule Not Triggering

**Symptoms**: Plan stays in `Active` phase past the expected hibernation time.

**Check**:

1. Verify timezone configuration:
    ```bash
    kubectl get hibernateplan <name> -n hibernator-system \
      -o jsonpath='{.spec.schedule.timezone}'
    ```

2. Verify off-hours windows are correct:
    ```bash
    kubectl get hibernateplan <name> -n hibernator-system \
      -o jsonpath='{.spec.schedule.offHours}' | jq
    ```

3. Check if a `suspend` exception is active:
    ```bash
    kubectl get scheduleexception -n hibernator-system \
      -l hibernator/plan=<name>
    ```

4. Ensure `spec.suspend` is not `true`:
    ```bash
    kubectl get hibernateplan <name> -n hibernator-system \
      -o jsonpath='{.spec.suspend}'
    ```

5. Check controller logs:
    ```bash
    kubectl logs -n hibernator-system -l app=hibernator-controller --tail=100
    ```

## Runner Job Failing

**Symptoms**: Plan transitions to `Error` or targets show `Failed` state.

**Check**:

1. Find the failed Job:
    ```bash
    kubectl get jobs -n hibernator-system -l hibernator/plan=<name>
    ```

2. View pod logs:
    ```bash
    kubectl logs job/<job-name> -n hibernator-system
    ```

3. Check executor-specific parameters:
    ```bash
    kubectl get hibernateplan <name> -n hibernator-system \
      -o jsonpath='{.spec.targets}' | jq
    ```

4. Verify connector credentials:
    ```bash
    kubectl get cloudprovider <connector-name> -n hibernator-system \
      -o jsonpath='{.status}'
    ```

## Restore Data Missing

**Symptoms**: Wakeup fails because restore metadata is not found.

**Check**:

1. Verify the ConfigMap exists:
    ```bash
    kubectl get configmap restore-data-<plan-name> -n hibernator-system
    ```

2. Check the ConfigMap content:
    ```bash
    kubectl get configmap restore-data-<plan-name> -n hibernator-system -o yaml
    ```

3. Ensure the ConfigMap was not garbage-collected (check runner pod logs from the shutdown cycle)

## Authentication Errors

**Symptoms**: Runner fails with `AccessDenied` or `Unauthorized`.

**Check**:

1. Verify ServiceAccount exists and has IRSA annotation:
    ```bash
    kubectl get sa -n hibernator-system -o yaml | grep eks.amazonaws.com
    ```

2. Test IAM role assumption:
    ```bash
    # From a pod with the same ServiceAccount
    aws sts get-caller-identity
    ```

3. Check the CloudProvider assume role ARN:
    ```bash
    kubectl get cloudprovider <name> -n hibernator-system \
      -o jsonpath='{.spec.aws.assumeRoleArn}'
    ```

4. Verify RBAC permissions:
    ```bash
    kubectl auth can-i create jobs -n hibernator-system \
      --as=system:serviceaccount:hibernator-system:hibernator-controller
    ```

## Plan Stuck in Hibernating/WakingUp

**Symptoms**: Plan doesn't transition to `Hibernated` or `Active` after Jobs complete.

**Check**:

1. Look for zombie Jobs:
    ```bash
    kubectl get jobs -n hibernator-system -l hibernator/plan=<name> \
      --field-selector status.successful=0
    ```

2. Check if any targets are still in `Running` state:
    ```bash
    kubectl get hibernateplan <name> -n hibernator-system \
      -o jsonpath='{.status.executions}' | jq '.[] | select(.state == "Running")'
    ```

3. Check controller logs for errors during status update:
    ```bash
    kubectl logs -n hibernator-system -l app=hibernator-controller \
      --tail=200 | grep -i error
    ```

## Getting Help

If the issue persists:

1. Collect plan status: `kubectl get hibernateplan <name> -o yaml`
2. Collect controller logs: `kubectl logs -l app=hibernator-controller --tail=500`
3. Collect runner logs (if applicable)
4. Open a [GitHub issue](https://github.com/ardikabs/hibernator/issues) with the collected information
