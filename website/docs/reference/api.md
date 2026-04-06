# API Reference

## Packages
- [hibernator.ardikabs.com/v1alpha1](#hibernatorardikabscomv1alpha1)


## hibernator.ardikabs.com/v1alpha1

Package v1alpha1 contains API Schema definitions for the hibernator v1alpha1 API group.

### Resource Types
- [CloudProvider](#cloudprovider)
- [HibernateNotification](#hibernatenotification)
- [HibernatePlan](#hibernateplan)
- [K8SCluster](#k8scluster)
- [ScheduleException](#scheduleexception)



#### AWSAuth



AWSAuth defines AWS authentication configuration.



_Appears in:_
- [AWSConfig](#awsconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `serviceAccount` _[ServiceAccountAuth](#serviceaccountauth)_ | ServiceAccount configures IRSA-based authentication. |  | Optional: \{\} <br /> |
| `static` _[StaticAuth](#staticauth)_ | Static configures static credential-based authentication. |  | Optional: \{\} <br /> |


#### AWSConfig



AWSConfig holds AWS-specific configuration.



_Appears in:_
- [CloudProviderSpec](#cloudproviderspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `accountId` _string_ | AccountId is the AWS account ID. |  | Required: \{\} <br /> |
| `region` _string_ | Region is the AWS region. |  | Required: \{\} <br /> |
| `assumeRoleArn` _string_ | AssumeRoleArn is the IAM role ARN to assume (optional).<br />Can be used with both ServiceAccount (IRSA) and Static authentication.<br />When using IRSA: the pod's SA credentials are used to assume this role.<br />When using Static: the static credentials are used to assume this role. |  | Optional: \{\} <br /> |
| `auth` _[AWSAuth](#awsauth)_ | Auth configures authentication method.<br />At least one of Auth.ServiceAccount or Auth.Static must be specified. |  | Required: \{\} <br /> |


#### Behavior



Behavior defines execution behavior.



_Appears in:_
- [HibernatePlanSpec](#hibernateplanspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _[BehaviorMode](#behaviormode)_ | Mode determines how failures are handled. | Strict | Enum: [Strict BestEffort] <br /> |
| `failFast` _boolean_ | FailFast stops execution on first failure.<br />Strict mode already implies fail-fast behavior.<br />Deprecated: FailFast is deprecated and will be removed in a future release. Use Mode=Strict for fail-fast behavior. | true |  |
| `retries` _integer_ | Retries is the maximum number of retry attempts for failed operations. | 3 | Maximum: 10 <br />Minimum: 0 <br />Optional: \{\} <br /> |


#### BehaviorMode

_Underlying type:_ _string_

BehaviorMode defines execution behavior.

_Validation:_
- Enum: [Strict BestEffort]

_Appears in:_
- [Behavior](#behavior)

| Field | Description |
| --- | --- |
| `Strict` | BehaviorStrict halts execution immediately when any target fails.<br />No further targets (or stages) are started; the plan transitions to Error.<br /> |
| `BestEffort` | BehaviorBestEffort continues executing remaining targets even if some fail.<br />Failed targets are recorded in status but do not block others.<br /> |


#### CloudProvider



CloudProvider is the Schema for the cloudproviders API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `hibernator.ardikabs.com/v1alpha1` | | |
| `kind` _string_ | `CloudProvider` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[CloudProviderSpec](#cloudproviderspec)_ | Spec defines the desired state of CloudProvider. |  |  |
| `status` _[CloudProviderStatus](#cloudproviderstatus)_ | Status defines the observed state of CloudProvider. |  |  |


#### CloudProviderSpec



CloudProviderSpec defines the desired state of CloudProvider.



_Appears in:_
- [CloudProvider](#cloudprovider)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[CloudProviderType](#cloudprovidertype)_ | Type of cloud provider. |  | Enum: [aws] <br />Required: \{\} <br /> |
| `aws` _[AWSConfig](#awsconfig)_ | AWS holds AWS-specific configuration (required when Type=aws). |  | Optional: \{\} <br /> |


#### CloudProviderStatus



CloudProviderStatus defines the observed state of CloudProvider.



_Appears in:_
- [CloudProvider](#cloudprovider)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ready` _boolean_ | Ready indicates if the provider is ready to use. |  |  |
| `message` _string_ | Message provides status details. |  | Optional: \{\} <br /> |
| `lastValidated` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastValidated is when credentials were last validated. |  | Optional: \{\} <br /> |


#### CloudProviderType

_Underlying type:_ _string_

CloudProviderType defines supported cloud providers.

_Validation:_
- Enum: [aws]

_Appears in:_
- [CloudProviderSpec](#cloudproviderspec)

| Field | Description |
| --- | --- |
| `aws` |  |


#### ConnectorRef



ConnectorRef references a connector resource.



_Appears in:_
- [Target](#target)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `kind` _string_ | Kind of the connector (CloudProvider or K8SCluster). |  | Enum: [CloudProvider K8SCluster] <br /> |
| `name` _string_ | Name of the connector resource. |  |  |
| `namespace` _string_ | Namespace of the connector resource (defaults to plan namespace). |  | Optional: \{\} <br /> |


#### Dependency



Dependency represents a DAG edge (from -> to).



_Appears in:_
- [ExecutionStrategy](#executionstrategy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `from` _string_ | From is the source target name. |  |  |
| `to` _string_ | To is the destination target name that depends on From. |  |  |


#### EKSConfig



EKSConfig holds EKS-specific configuration.



_Appears in:_
- [K8SClusterSpec](#k8sclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the EKS cluster name. |  | Required: \{\} <br /> |
| `region` _string_ | Region is the AWS region. |  | Required: \{\} <br /> |


#### ExceptionReference



ExceptionReference tracks an exception in the plan's history.



_Appears in:_
- [HibernatePlanStatus](#hibernateplanstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the ScheduleException. |  |  |
| `type` _[ExceptionType](#exceptiontype)_ | Type of the exception (extend, suspend, replace). |  | Enum: [extend suspend replace] <br /> |
| `validFrom` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | ValidFrom is when the exception period starts. |  |  |
| `validUntil` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | ValidUntil is when the exception period ends. |  |  |
| `state` _[ExceptionState](#exceptionstate)_ | State is the current state of the exception. |  | Enum: [Pending Active Expired Detached] <br /> |
| `appliedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | AppliedAt is when the exception was first applied. |  | Optional: \{\} <br /> |


#### ExceptionState

_Underlying type:_ _string_

ExceptionState represents the lifecycle state of an exception.

_Validation:_
- Enum: [Pending Active Expired Detached]

_Appears in:_
- [ExceptionReference](#exceptionreference)
- [ScheduleExceptionStatus](#scheduleexceptionstatus)

| Field | Description |
| --- | --- |
| `Pending` | ExceptionStatePending indicates the exception is not yet active.<br /> |
| `Active` | ExceptionStateActive indicates the exception is currently active.<br /> |
| `Expired` | ExceptionStateExpired indicates the exception has passed its validUntil time.<br /> |
| `Detached` | ExceptionStateDetached indicates the referenced plan no longer exists.<br />The exception is still a valid resource but is not bound to any plan.<br />If a plan with the same name is re-created, the exception may transition<br />back to a time-based state (Pending, Active, or Expired).<br /> |


#### ExceptionType

_Underlying type:_ _string_

ExceptionType defines the type of schedule exception.

_Validation:_
- Enum: [extend suspend replace]

_Appears in:_
- [ExceptionReference](#exceptionreference)
- [ScheduleExceptionSpec](#scheduleexceptionspec)

| Field | Description |
| --- | --- |
| `extend` | ExceptionExtend adds hibernation windows to the base schedule.<br /> |
| `suspend` | ExceptionSuspend prevents hibernation during specified windows (carve-out).<br /> |
| `replace` | ExceptionReplace completely replaces the base schedule during the exception period.<br /> |


#### Execution



Execution holds strategy configuration.



_Appears in:_
- [HibernatePlanSpec](#hibernateplanspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `strategy` _[ExecutionStrategy](#executionstrategy)_ | Strategy defines how targets are executed. |  |  |


#### ExecutionCycle



ExecutionCycle groups a shutdown and corresponding wakeup operation.



_Appears in:_
- [HibernatePlanStatus](#hibernateplanstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `cycleId` _string_ | CycleID is a unique identifier for this cycle. |  |  |
| `shutdownExecution` _[ExecutionOperationSummary](#executionoperationsummary)_ | ShutdownExecution summarizes the shutdown operation. |  | Optional: \{\} <br /> |
| `wakeupExecution` _[ExecutionOperationSummary](#executionoperationsummary)_ | WakeupExecution summarizes the wakeup operation. |  | Optional: \{\} <br /> |


#### ExecutionOperationSummary



ExecutionOperationSummary summarizes the results of a shutdown or wakeup operation.



_Appears in:_
- [ExecutionCycle](#executioncycle)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `operation` _[PlanOperation](#planoperation)_ | Operation is the operation type (shutdown or wakeup). |  | Enum: [shutdown wakeup] <br /> |
| `startTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | StartTime is when the operation started. |  |  |
| `endTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | EndTime is when the operation completed. |  | Optional: \{\} <br /> |
| `targetResults` _[TargetExecutionResult](#targetexecutionresult) array_ | TargetResults summarizes the result for each target. |  | Optional: \{\} <br /> |
| `success` _boolean_ | Success indicates if all targets completed successfully. |  |  |
| `errorMessage` _string_ | ErrorMessage contains error details if the operation failed. |  | Optional: \{\} <br /> |


#### ExecutionState

_Underlying type:_ _string_

ExecutionState represents per-target execution state.

_Validation:_
- Enum: [Pending Running Completed Failed Aborted]

_Appears in:_
- [ExecutionStatus](#executionstatus)
- [TargetExecutionResult](#targetexecutionresult)

| Field | Description |
| --- | --- |
| `Pending` | StatePending means the target execution is waiting to start (e.g., waiting for schedule or dependencies).<br /> |
| `Running` | StateRunning means the target execution is in progress (e.g., runner Job is active).<br /> |
| `Completed` | StateCompleted means the target execution finished successfully.<br /> |
| `Failed` | StateFailed means the target execution finished with failure (e.g., runner Job failed).<br /> |
| `Aborted` | StateAborted indicates the target was not executed because an upstream<br />dependency failed (DAG pruning). Distinct from StateFailed which means<br />the target's own Job execution failed.<br />Currently only relevant with DAG strategy and BestEffort behavior,<br />but may be extended to other strategies/behaviors in the future.<br /> |


#### ExecutionStatus



ExecutionStatus represents per-target execution status.



_Appears in:_
- [HibernatePlanStatus](#hibernateplanstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `target` _string_ | Target identifier (type/name). |  |  |
| `executor` _string_ | Executor used for this target. |  |  |
| `state` _[ExecutionState](#executionstate)_ | State of execution. |  | Enum: [Pending Running Completed Failed Aborted] <br /> |
| `startedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | StartedAt is when execution started. |  | Optional: \{\} <br /> |
| `finishedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | FinishedAt is when execution finished. |  | Optional: \{\} <br /> |
| `attempts` _integer_ | Attempts is the number of execution attempts. |  |  |
| `message` _string_ | Message provides human-readable status. |  | Optional: \{\} <br /> |
| `jobRef` _string_ | JobRef is the namespace/name of the runner Job. |  | Optional: \{\} <br /> |
| `logsRef` _string_ | LogsRef is the reference to logs (stream id or object path). |  | Optional: \{\} <br /> |
| `restoreRef` _string_ | RestoreRef is the reference to restore metadata artifact. |  | Optional: \{\} <br /> |
| `serviceAccountRef` _string_ | ServiceAccountRef is the namespace/name of ephemeral SA. |  | Optional: \{\} <br /> |
| `connectorSecretRef` _string_ | ConnectorSecretRef is the namespace/name of connector secret. |  | Optional: \{\} <br /> |
| `restoreConfigMapRef` _string_ | RestoreConfigMapRef is the namespace/name of restore hints ConfigMap. |  | Optional: \{\} <br /> |


#### ExecutionStrategy



ExecutionStrategy defines how targets are executed.



_Appears in:_
- [Execution](#execution)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[ExecutionStrategyType](#executionstrategytype)_ | Type of execution strategy. |  | Enum: [Sequential Parallel DAG Staged] <br />Required: \{\} <br /> |
| `maxConcurrency` _integer_ | MaxConcurrency limits concurrent executions (for Parallel/DAG/Staged). |  | Minimum: 1 <br />Optional: \{\} <br /> |
| `dependencies` _[Dependency](#dependency) array_ | Dependencies define DAG edges (only valid when Type=DAG). |  | Optional: \{\} <br /> |
| `stages` _[Stage](#stage) array_ | Stages define execution groups (only valid when Type=Staged). |  | Optional: \{\} <br /> |


#### ExecutionStrategyType

_Underlying type:_ _string_

ExecutionStrategyType defines the execution strategy.

_Validation:_
- Enum: [Sequential Parallel DAG Staged]

_Appears in:_
- [ExecutionStrategy](#executionstrategy)

| Field | Description |
| --- | --- |
| `Sequential` | StrategySequential executes targets one at a time in the order they are listed in spec.targets.<br /> |
| `Parallel` | StrategyParallel executes all targets concurrently, optionally bounded by MaxConcurrency.<br /> |
| `DAG` | StrategyDAG executes targets according to a directed acyclic graph defined by spec.execution.strategy.dependencies.<br />Targets with no incoming edges run first; downstream targets wait for their dependencies to complete.<br /> |
| `Staged` | StrategyStaged executes targets in explicitly defined groups (stages) in order.<br />Within each stage targets may run sequentially or in parallel depending on stage.parallel.<br /> |


#### GKEConfig



GKEConfig holds GKE-specific configuration.



_Appears in:_
- [K8SClusterSpec](#k8sclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the GKE cluster name. |  | Required: \{\} <br /> |
| `project` _string_ | Project is the GCP project. |  | Required: \{\} <br /> |
| `location` _string_ | Zone or region of the cluster. |  | Required: \{\} <br /> |


#### HibernateNotification



HibernateNotification is the Schema for the hibernatenotifications API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `hibernator.ardikabs.com/v1alpha1` | | |
| `kind` _string_ | `HibernateNotification` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[HibernateNotificationSpec](#hibernatenotificationspec)_ | Spec defines the desired state of HibernateNotification. |  |  |
| `status` _[HibernateNotificationStatus](#hibernatenotificationstatus)_ | Status defines the observed state of HibernateNotification. |  |  |


#### HibernateNotificationSpec



HibernateNotificationSpec defines the desired state of HibernateNotification.



_Appears in:_
- [HibernateNotification](#hibernatenotification)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `selector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#labelselector-v1-meta)_ | Selector selects HibernatePlans by label.<br />The notification applies to all plans in the same namespace matching this selector. |  | Required: \{\} <br /> |
| `onEvents` _[NotificationEvent](#notificationevent) array_ | OnEvents specifies which hook points trigger this notification. |  | Enum: [Start Success Failure Recovery PhaseChange ExecutionProgress] <br />MinItems: 1 <br />Required: \{\} <br /> |
| `sinks` _[NotificationSink](#notificationsink) array_ | Sinks defines the notification destinations. |  | MinItems: 1 <br />Required: \{\} <br /> |


#### HibernateNotificationStatus



HibernateNotificationStatus defines the observed state of HibernateNotification.



_Appears in:_
- [HibernateNotification](#hibernatenotification)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `state` _[NotificationState](#notificationstate)_ | State is the lifecycle state of this notification: Bound or Detached.<br />Bound means at least one HibernatePlan matches the selector.<br />Detached means no HibernatePlan matches; the notification can be freely deleted. | Detached | Enum: [Bound Detached] <br />Optional: \{\} <br /> |
| `watchedPlans` _[PlanReference](#planreference) array_ | WatchedPlans is the list of HibernatePlan references currently matching the selector. |  | Optional: \{\} <br /> |
| `lastDeliveryTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastDeliveryTime is the timestamp of the most recent successful notification delivery<br />across all sinks. Nil if no successful delivery has occurred. |  | Optional: \{\} <br /> |
| `lastFailureTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastFailureTime is the timestamp of the most recent failed notification delivery<br />across all sinks. Nil if no failure has occurred. |  | Optional: \{\} <br /> |
| `sinkStatuses` _[NotificationSinkStatus](#notificationsinkstatus) array_ | SinkStatuses is a history log of per-sink delivery attempts, ordered newest-first.<br />The controller retains at most 20 entries; older entries are evicted when the cap is reached. |  | Optional: \{\} <br /> |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent .metadata.generation observed by the controller. |  | Optional: \{\} <br /> |


#### HibernatePlan



HibernatePlan is the Schema for the hibernateplans API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `hibernator.ardikabs.com/v1alpha1` | | |
| `kind` _string_ | `HibernatePlan` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[HibernatePlanSpec](#hibernateplanspec)_ | Spec defines the desired state of HibernatePlan. |  |  |
| `status` _[HibernatePlanStatus](#hibernateplanstatus)_ | Status defines the observed state of HibernatePlan. |  |  |


#### HibernatePlanSpec



HibernatePlanSpec defines the desired state of HibernatePlan.



_Appears in:_
- [HibernatePlan](#hibernateplan)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `schedule` _[Schedule](#schedule)_ | Schedule defines when hibernation occurs. |  | Required: \{\} <br /> |
| `execution` _[Execution](#execution)_ | Execution defines the execution strategy. |  | Required: \{\} <br /> |
| `behavior` _[Behavior](#behavior)_ | Behavior defines how failures are handled. |  | Optional: \{\} <br /> |
| `suspend` _boolean_ | Suspend temporarily disables hibernation operations without deleting the plan.<br />When set to true, the plan transitions to Suspended phase and stops all execution.<br />When set to false, the plan transitions back to Active phase and resumes schedule evaluation.<br />Running jobs complete naturally but no new jobs are created while suspended. |  | Optional: \{\} <br /> |
| `targets` _[Target](#target) array_ | Targets are the resources to hibernate. |  | MinItems: 1 <br /> |


#### HibernatePlanStatus



HibernatePlanStatus defines the observed state of HibernatePlan.



_Appears in:_
- [HibernatePlan](#hibernateplan)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `currentCycleID` _string_ | CurrentCycleID is the current hibernation cycle identifier. |  |  |
| `phase` _[PlanPhase](#planphase)_ | Phase is the overall plan phase. |  | Enum: [Pending Active Hibernating Hibernated WakingUp Suspended Error] <br /> |
| `lastTransitionTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastTransitionTime is when the phase last changed. |  | Optional: \{\} <br /> |
| `executions` _[ExecutionStatus](#executionstatus) array_ | Executions is the per-target execution ledger. |  | Optional: \{\} <br /> |
| `observedGeneration` _integer_ | ObservedGeneration is the last observed generation. |  |  |
| `retryCount` _integer_ | RetryCount tracks the number of retry attempts for error recovery. |  | Optional: \{\} <br /> |
| `lastRetryTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastRetryTime is when the last retry attempt was made. |  | Optional: \{\} <br /> |
| `errorMessage` _string_ | ErrorMessage provides details about the error that caused PhaseError.<br />This field is persistent within a cycle (shutdown + wakeup pair): it is set<br />when the plan enters PhaseError, replaced if a subsequent retry produces a<br />different error, and only cleared when a new cycle begins. Consequently, a<br />plan that recovered via retry may still carry the ErrorMessage from the<br />earlier failure until the next cycle starts. A non-empty ErrorMessage on a<br />completed operation indicates that the operation succeeded after a recovery<br />attempt. |  | Optional: \{\} <br /> |
| `exceptionReferences` _[ExceptionReference](#exceptionreference) array_ | ExceptionReferences is the history of schedule exceptions for this plan.<br />Maximum 10 entries, ordered by: active state first (most relevant), then by ValidFrom descending (most recent first).<br />Oldest entries are pruned when limit is exceeded. |  | Optional: \{\} <br /> |
| `currentStageIndex` _integer_ | CurrentStageIndex tracks which stage is currently executing (0-based).<br />Reset to 0 when starting new hibernation/wakeup cycle. |  | Optional: \{\} <br /> |
| `currentOperation` _[PlanOperation](#planoperation)_ | CurrentOperation tracks the current operation type (shutdown or wakeup).<br />Used to determine which phase to transition to when stages complete. |  | Enum: [shutdown wakeup] <br />Optional: \{\} <br /> |
| `executionHistory` _[ExecutionCycle](#executioncycle) array_ | ExecutionHistory records historical execution cycles (max 5).<br />Each cycle contains shutdown and wakeup operation summaries.<br />Oldest cycles are pruned when limit is exceeded. |  | Optional: \{\} <br /> |


#### K8SAccessConfig



K8SAccessConfig holds Kubernetes API access configuration.



_Appears in:_
- [K8SClusterSpec](#k8sclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `kubeconfigRef` _[KubeconfigRef](#kubeconfigref)_ | KubeconfigRef references a Secret containing kubeconfig. |  | Optional: \{\} <br /> |
| `inCluster` _boolean_ | InCluster uses in-cluster config (for self-management). |  | Optional: \{\} <br /> |


#### K8SCluster



K8SCluster is the Schema for the k8sclusters API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `hibernator.ardikabs.com/v1alpha1` | | |
| `kind` _string_ | `K8SCluster` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[K8SClusterSpec](#k8sclusterspec)_ | Spec defines the desired state of K8SCluster. |  |  |
| `status` _[K8SClusterStatus](#k8sclusterstatus)_ | Status defines the observed state of K8SCluster. |  |  |


#### K8SClusterSpec



K8SClusterSpec defines the desired state of K8SCluster.



_Appears in:_
- [K8SCluster](#k8scluster)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `providerRef` _[ProviderRef](#providerref)_ | ProviderRef references the CloudProvider (optional for generic k8s). |  | Optional: \{\} <br /> |
| `eks` _[EKSConfig](#eksconfig)_ | EKS holds EKS-specific configuration. |  | Optional: \{\} <br /> |
| `gke` _[GKEConfig](#gkeconfig)_ | GKE holds GKE-specific configuration. |  | Optional: \{\} <br /> |
| `k8s` _[K8SAccessConfig](#k8saccessconfig)_ | K8S holds generic Kubernetes access configuration. |  | Optional: \{\} <br /> |


#### K8SClusterStatus



K8SClusterStatus defines the observed state of K8SCluster.



_Appears in:_
- [K8SCluster](#k8scluster)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ready` _boolean_ | Ready indicates if the cluster is reachable. |  |  |
| `message` _string_ | Message provides status details. |  | Optional: \{\} <br /> |
| `lastValidated` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastValidated is when connectivity was last validated. |  | Optional: \{\} <br /> |
| `clusterType` _[K8SClusterType](#k8sclustertype)_ | ClusterType is the detected cluster type. |  | Enum: [eks gke k8s] <br />Optional: \{\} <br /> |


#### K8SClusterType

_Underlying type:_ _string_

K8SClusterType defines supported Kubernetes cluster types.

_Validation:_
- Enum: [eks gke k8s]

_Appears in:_
- [K8SClusterStatus](#k8sclusterstatus)

| Field | Description |
| --- | --- |
| `eks` |  |
| `gke` |  |
| `k8s` |  |


#### KubeconfigRef



KubeconfigRef references a kubeconfig Secret.



_Appears in:_
- [K8SAccessConfig](#k8saccessconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the Secret containing kubeconfig data. |  |  |
| `namespace` _string_ | Namespace is the namespace of the Secret. |  | Optional: \{\} <br /> |


#### NotificationEvent

_Underlying type:_ _string_

NotificationEvent defines the hook point that triggers a notification.

_Validation:_
- Enum: [Start Success Failure Recovery PhaseChange ExecutionProgress]

_Appears in:_
- [HibernateNotificationSpec](#hibernatenotificationspec)

| Field | Description |
| --- | --- |
| `Start` | EventStart fires right before execution begins (PreHook on Hibernating/WakingUp).<br /> |
| `Success` | EventSuccess fires after execution completes successfully (PostHook on Hibernated/Active).<br /> |
| `Failure` | EventFailure fires when retries exhausted and plan enters permanent Error state<br />(PostHook on Error transition, gated by retryCount >= behavior.retries).<br /> |
| `Recovery` | EventRecovery fires each time the recovery system retries from Error (PreHook).<br /> |
| `PhaseChange` | EventPhaseChange fires on every phase transition (PostHook). Noisy — for audit trails.<br /> |
| `ExecutionProgress` | EventExecutionProgress fires when an individual target's execution state changes<br />(e.g., Pending→Running, Running→Completed/Failed). Only fires on actual state<br />transitions, not on every poll tick.<br /> |


#### NotificationSink



NotificationSink defines a destination for notification delivery.
All sink-specific configuration (endpoints, credentials, options) is delegated
to the referenced Secret under a well-known key ("config"). This minimizes
the CRD footprint and keeps sensitive data out of the resource spec.



_Appears in:_
- [HibernateNotificationSpec](#hibernatenotificationspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is a human-readable identifier for this sink (unique within spec.sinks). |  | MinLength: 1 <br />Required: \{\} <br /> |
| `type` _[NotificationSinkType](#notificationsinktype)_ | Type is the sink provider type. |  | Enum: [slack telegram webhook] <br />Required: \{\} <br /> |
| `secretRef` _[ObjectKeyReference](#objectkeyreference)_ | SecretRef is the name of the Secret containing the sink configuration.<br />The Secret must contain a key named "config" whose value is a JSON object<br />with all sink-specific settings (endpoint URL, credentials, options).<br />Slack config example:   \{"webhook_url": "https://hooks.slack.com/services/..."\}<br />Telegram config example: \{"token": "bot123:ABC", "chat_id": "-100123", "parse_mode": "MarkdownV2"\}<br />Webhook config example:  \{"url": "https://example.com/hook", "headers": \{"Authorization": "Bearer ..."\}\} |  | Required: \{\} <br /> |
| `templateRef` _[ObjectKeyReference](#objectkeyreference)_ | TemplateRef references a ConfigMap key containing a Go template for message formatting.<br />If omitted, a built-in default template is used for the sink type. |  | Optional: \{\} <br /> |


#### NotificationSinkStatus



NotificationSinkStatus records the outcome of a single notification delivery attempt.



_Appears in:_
- [HibernateNotificationStatus](#hibernatenotificationstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the sink name as defined in spec.sinks[].name. |  |  |
| `success` _boolean_ | Success indicates whether this delivery attempt succeeded. |  |  |
| `transitionTimestamp` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | TransitionTimestamp is when the delivery attempt completed. |  |  |
| `message` _string_ | Message is a human-readable description of the delivery outcome.<br />On success: "Successfully sent notification for <sink-name>"<br />On failure: the error string from the sink provider. |  | Optional: \{\} <br /> |


#### NotificationSinkType

_Underlying type:_ _string_

NotificationSinkType defines supported notification sink types.

_Validation:_
- Enum: [slack telegram webhook]

_Appears in:_
- [NotificationSink](#notificationsink)

| Field | Description |
| --- | --- |
| `slack` | SinkSlack sends notifications via Slack Incoming Webhook URL.<br /> |
| `telegram` | SinkTelegram sends notifications via Telegram Bot API.<br /> |
| `webhook` | SinkWebhook sends notifications via generic HTTP POST webhook.<br /> |


#### NotificationState

_Underlying type:_ _string_

NotificationState defines the lifecycle state of a HibernateNotification.

_Validation:_
- Enum: [Bound Detached]

_Appears in:_
- [HibernateNotificationStatus](#hibernatenotificationstatus)

| Field | Description |
| --- | --- |
| `Bound` | NotificationStateBound indicates the notification is attached to at least one HibernatePlan.<br />The notification has a finalizer to ensure graceful cleanup on deletion.<br /> |
| `Detached` | NotificationStateDetached indicates no HibernatePlan references this notification.<br />The finalizer is removed so the notification can be freely deleted.<br /> |


#### ObjectKeyReference



ObjectKeyReference is a reference to a specific key in a namespaced object.



_Appears in:_
- [NotificationSink](#notificationsink)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the object. |  | Required: \{\} <br /> |
| `key` _string_ | Key is the key within the object primarily for Secret or ConfigMap data.<br />If omitted, the dispatcher uses a default key ("config" for SecretRef, "template.gotpl" for TemplateRef). |  | Optional: \{\} <br /> |


#### OffHourWindow



OffHourWindow defines a time window for hibernation.



_Appears in:_
- [Schedule](#schedule)
- [ScheduleExceptionSpec](#scheduleexceptionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `start` _string_ | Start time in HH:MM format (e.g., "20:00"). |  | Pattern: `^([0-1]?[0-9]\|2[0-3]):[0-5][0-9]$` <br />Required: \{\} <br /> |
| `end` _string_ | End time in HH:MM format (e.g., "06:00"). |  | Pattern: `^([0-1]?[0-9]\|2[0-3]):[0-5][0-9]$` <br />Required: \{\} <br /> |
| `daysOfWeek` _string array_ | DaysOfWeek specifies which days this window applies to.<br />Valid values: MON, TUE, WED, THU, FRI, SAT, SUN |  | MinItems: 1 <br />items:Enum: [MON TUE WED THU FRI SAT SUN] <br /> |


#### Parameters



Parameters is an opaque container for executor-specific config.
The JSON schema depends on the target's executor type. Each executor
defines its own parameter struct in pkg/executorparams (e.g.,
EKSParameters, RDSParameters, EC2Parameters, KarpenterParameters).



_Appears in:_
- [Target](#target)



#### PlanOperation

_Underlying type:_ _string_

PlanOperation identifies the type of operation a HibernatePlan is currently executing.
Stored in HibernatePlanStatus.CurrentOperation and used as the LabelOperation value on runner Jobs.

_Validation:_
- Enum: [shutdown wakeup]

_Appears in:_
- [ExecutionOperationSummary](#executionoperationsummary)
- [HibernatePlanStatus](#hibernateplanstatus)

| Field | Description |
| --- | --- |
| `shutdown` | OperationHibernate is the operation value for a hibernate (shutdown) cycle.<br /> |
| `wakeup` | OperationWakeUp is the operation value for a wakeup cycle.<br /> |


#### PlanPhase

_Underlying type:_ _string_

PlanPhase represents the overall phase of the HibernatePlan.

_Validation:_
- Enum: [Pending Active Hibernating Hibernated WakingUp Suspended Error]

_Appears in:_
- [HibernatePlanStatus](#hibernateplanstatus)

| Field | Description |
| --- | --- |
| `Pending` | PhasePending is the initial phase before the plan has been fully initialized by the controller.<br /> |
| `Active` | PhaseActive means the plan is healthy and within an active (non-off-hour) window.<br />The controller monitors the schedule and will transition to Hibernating when the off-hour window begins.<br /> |
| `Hibernating` | PhaseHibernating means a shutdown operation is in progress.<br />Runner Jobs are being dispatched to stop the configured targets.<br /> |
| `Hibernated` | PhaseHibernated means all targets have been successfully shut down.<br />The plan stays in this phase until the off-hour window ends, then transitions to WakingUp.<br /> |
| `WakingUp` | PhaseWakingUp means a wakeup (restore) operation is in progress.<br />Runner Jobs are being dispatched to restore targets using persisted restore data.<br /> |
| `Suspended` | PhaseSuspended means the plan has been administratively paused via spec.suspend=true.<br />No schedule evaluation or execution occurs while suspended.<br /> |
| `Error` | PhaseError means an execution operation failed and all configured retries have been exhausted.<br />Manual intervention or the retry-now annotation is required to recover.<br /> |


#### PlanReference



PlanReference references a HibernatePlan.



_Appears in:_
- [HibernateNotificationStatus](#hibernatenotificationstatus)
- [ScheduleExceptionSpec](#scheduleexceptionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the HibernatePlan. |  | Required: \{\} <br /> |
| `namespace` _string_ | Namespace of the HibernatePlan.<br />If empty, defaults to the exception's namespace. |  | Optional: \{\} <br /> |


#### ProviderRef



ProviderRef references a CloudProvider.



_Appears in:_
- [K8SClusterSpec](#k8sclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the CloudProvider resource. |  |  |
| `namespace` _string_ | Namespace is the namespace of the CloudProvider resource. |  | Optional: \{\} <br /> |


#### Schedule



Schedule defines the hibernation schedule.



_Appears in:_
- [HibernatePlanSpec](#hibernateplanspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `timezone` _string_ | Timezone for schedule evaluation (e.g., "Asia/Jakarta"). |  | Required: \{\} <br /> |
| `offHours` _[OffHourWindow](#offhourwindow) array_ | OffHours defines when hibernation should occur. |  | MinItems: 1 <br /> |


#### ScheduleException



ScheduleException is the Schema for the scheduleexceptions API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `hibernator.ardikabs.com/v1alpha1` | | |
| `kind` _string_ | `ScheduleException` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ScheduleExceptionSpec](#scheduleexceptionspec)_ | Spec defines the desired state of ScheduleException. |  |  |
| `status` _[ScheduleExceptionStatus](#scheduleexceptionstatus)_ | Status defines the observed state of ScheduleException. |  |  |


#### ScheduleExceptionSpec



ScheduleExceptionSpec defines the desired state of ScheduleException.



_Appears in:_
- [ScheduleException](#scheduleexception)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `planRef` _[PlanReference](#planreference)_ | PlanRef references the HibernatePlan this exception applies to. |  | Required: \{\} <br /> |
| `validFrom` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | ValidFrom is the start time of the exception period (RFC3339 format). |  | Format: date-time <br />Required: \{\} <br />Type: string <br /> |
| `validUntil` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | ValidUntil is the end time of the exception period (RFC3339 format). |  | Format: date-time <br />Required: \{\} <br />Type: string <br /> |
| `type` _[ExceptionType](#exceptiontype)_ | Type specifies the exception type: extend, suspend, or replace. |  | Enum: [extend suspend replace] <br />Required: \{\} <br /> |
| `leadTime` _string_ | LeadTime specifies buffer period before suspension window.<br />Only valid when Type is "suspend".<br />Format: duration string (e.g., "30m", "1h", "3600s").<br />Prevents NEW hibernation starts within this buffer before suspension. |  | Optional: \{\} <br />Pattern: `^([0-9]+(\.[0-9]+)?(ns\|us\|µs\|ms\|s\|m\|h))+$` <br /> |
| `windows` _[OffHourWindow](#offhourwindow) array_ | Windows defines the time windows for this exception.<br />Meaning depends on Type:<br />- extend: Additional hibernation windows (union with base schedule)<br />- suspend: Windows to prevent hibernation (carve-out from schedule)<br />- replace: Complete replacement schedule (ignore base schedule) |  | MinItems: 1 <br /> |


#### ScheduleExceptionStatus



ScheduleExceptionStatus defines the observed state of ScheduleException.



_Appears in:_
- [ScheduleException](#scheduleexception)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `state` _[ExceptionState](#exceptionstate)_ | State is the current lifecycle state of the exception. |  | Enum: [Pending Active Expired Detached] <br /> |
| `appliedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | AppliedAt is when the exception was first applied. |  | Optional: \{\} <br /> |
| `expiredAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | ExpiredAt is when the exception transitioned to Expired state. |  | Optional: \{\} <br /> |
| `detachedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | DetachedAt is when the exception transitioned to Detached state (plan was deleted). |  | Optional: \{\} <br /> |
| `message` _string_ | Message provides diagnostic information about the exception state. |  | Optional: \{\} <br /> |


#### SecretReference



SecretReference is a reference to a Secret.



_Appears in:_
- [StaticAuth](#staticauth)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the Secret. |  |  |
| `namespace` _string_ | Namespace is the namespace of the Secret. |  | Optional: \{\} <br /> |


#### ServiceAccountAuth



ServiceAccountAuth configures IRSA (IAM Roles for Service Accounts).
When specified (even as empty struct), indicates that the runner pod's
ServiceAccount should be used for authentication via workload identity.
The pod's ServiceAccount must have appropriate cloud provider annotations
(e.g., eks.amazonaws.com/role-arn for AWS IRSA).



_Appears in:_
- [AWSAuth](#awsauth)



#### Stage



Stage defines a group of targets to execute together.



_Appears in:_
- [ExecutionStrategy](#executionstrategy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the stage. |  |  |
| `parallel` _boolean_ | Parallel indicates if targets in this stage run in parallel. | false |  |
| `maxConcurrency` _integer_ | MaxConcurrency limits parallelism within this stage. |  | Minimum: 1 <br />Optional: \{\} <br /> |
| `targets` _string array_ | Targets are the names of targets in this stage. |  |  |


#### StaticAuth



StaticAuth configures static credentials.



_Appears in:_
- [AWSAuth](#awsauth)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `secretRef` _[SecretReference](#secretreference)_ | SecretRef references a Secret containing credentials. |  |  |


#### Target



Target defines a hibernation target.



_Appears in:_
- [HibernatePlanSpec](#hibernateplanspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the unique identifier for this target within the plan. |  | Required: \{\} <br /> |
| `type` _string_ | Type of the target (e.g., eks, rds, ec2). |  | Required: \{\} <br /> |
| `connectorRef` _[ConnectorRef](#connectorref)_ | ConnectorRef references the connector for this target. |  | Required: \{\} <br /> |
| `parameters` _[Parameters](#parameters)_ | Parameters are executor-specific configuration. |  | Optional: \{\} <br /> |


#### TargetExecutionResult



TargetExecutionResult is the result of a single target execution.



_Appears in:_
- [ExecutionOperationSummary](#executionoperationsummary)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `target` _string_ | Target is the target identifier (type/name). |  |  |
| `state` _[ExecutionState](#executionstate)_ | State is the final execution state (Completed or Failed). |  | Enum: [Pending Running Completed Failed Aborted] <br /> |
| `attempts` _integer_ | Attempts is the number of attempts made. |  |  |
| `executionId` _string_ | ExecutionID is the unique identifier for this target execution. |  | Optional: \{\} <br /> |
| `startedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | StartedAt is when execution started. |  | Optional: \{\} <br /> |
| `finishedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | FinishedAt is when execution finished. |  | Optional: \{\} <br /> |
| `message` _string_ | Message provides details about the execution outcome. |  | Optional: \{\} <br /> |


