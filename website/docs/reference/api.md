# API Reference

## Packages

- [hibernator.ardikabs.com/v1alpha1](#hibernatorardikabscomv1alpha1)
## hibernator.ardikabs.com/v1alpha1

Package v1alpha1 contains API Schema definitions for the hibernator v1alpha1 API group.


### Resource Types

- HibernatePlan
- K8SCluster
- ScheduleException
- CloudProvider
- HibernateNotification


### AWSAuth

AWSAuth defines AWS authentication configuration.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `serviceAccount` | _[ServiceAccountAuth](#serviceaccountauth)_ | ServiceAccount configures IRSA-based authentication. | [Optional: {}] |
| `static` | _[StaticAuth](#staticauth)_ | Static configures static credential-based authentication. | [Optional: {}] |

### AWSConfig

AWSConfig holds AWS-specific configuration.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `accountId` | _string_ | AccountId is the AWS account ID. | [Required: {}] |
| `region` | _string_ | Region is the AWS region. | [Required: {}] |
| `assumeRoleArn` | _string_ | AssumeRoleArn is the IAM role ARN to assume (optional).<br />Can be used with both ServiceAccount (IRSA) and Static authentication.<br />When using IRSA: the pod's SA credentials are used to assume this role.<br />When using Static: the static credentials are used to assume this role. | [Optional: {}] |
| `auth` | _[AWSAuth](#awsauth)_ | Auth configures authentication method.<br />At least one of Auth.ServiceAccount or Auth.Static must be specified. | [Required: {}] |

### Behavior

Behavior defines execution behavior.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `mode` | _[BehaviorMode](#behaviormode)_ | Mode determines how failures are handled. | [Enum: [Strict BestEffort]] |
| `failFast` | _boolean_ | FailFast stops execution on first failure. | [] |
| `retries` | _integer_ | Retries is the maximum number of retry attempts for failed operations. | [Maximum: 10 Minimum: 0 Optional: {}] |

### BehaviorMode

BehaviorMode defines execution behavior.


### CloudProvider

CloudProvider is the Schema for the cloudproviders API.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `metadata` | _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. | |
| `spec` | _[CloudProviderSpec](#cloudproviderspec)_ | Spec defines the desired state of CloudProvider. | [] |
| `status` | _[CloudProviderStatus](#cloudproviderstatus)_ | Status defines the observed state of CloudProvider. | [] |

### CloudProviderSpec

CloudProviderSpec defines the desired state of CloudProvider.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `type` | _[CloudProviderType](#cloudprovidertype)_ | Type of cloud provider. | [Enum: [aws] Required: {}] |
| `aws` | _[AWSConfig](#awsconfig)_ | AWS holds AWS-specific configuration (required when Type=aws). | [Optional: {}] |

### CloudProviderStatus

CloudProviderStatus defines the observed state of CloudProvider.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `ready` | _boolean_ | Ready indicates if the provider is ready to use. | [] |
| `message` | _string_ | Message provides status details. | [Optional: {}] |
| `lastValidated` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastValidated is when credentials were last validated. | [Optional: {}] |

### CloudProviderType

CloudProviderType defines supported cloud providers.


### ConnectorRef

ConnectorRef references a connector resource.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `kind` | _string_ | Kind of the connector (CloudProvider or K8SCluster). | [Enum: [CloudProvider K8SCluster]] |
| `name` | _string_ | Name of the connector resource. | [] |
| `namespace` | _string_ | Namespace of the connector resource (defaults to plan namespace). | [Optional: {}] |

### Dependency

Dependency represents a DAG edge (from -> to).

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `from` | _string_ | From is the source target name. | [] |
| `to` | _string_ | To is the destination target name that depends on From. | [] |

### EKSConfig

EKSConfig holds EKS-specific configuration.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name is the EKS cluster name. | [Required: {}] |
| `region` | _string_ | Region is the AWS region. | [Required: {}] |

### ExceptionReference

ExceptionReference tracks an exception in the plan's history.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name of the ScheduleException. | [] |
| `type` | _[ExceptionType](#exceptiontype)_ | Type of the exception (extend, suspend, replace). | [Enum: [extend suspend replace]] |
| `validFrom` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | ValidFrom is when the exception period starts. | [] |
| `validUntil` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | ValidUntil is when the exception period ends. | [] |
| `state` | _[ExceptionState](#exceptionstate)_ | State is the current state of the exception. | [Enum: [Pending Active Expired Detached]] |
| `appliedAt` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | AppliedAt is when the exception was first applied. | [Optional: {}] |

### ExceptionState

ExceptionState represents the lifecycle state of an exception.


### ExceptionType

ExceptionType defines the type of schedule exception.


### Execution

Execution holds strategy configuration.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `strategy` | _[ExecutionStrategy](#executionstrategy)_ | Strategy defines how targets are executed. | [] |

### ExecutionCycle

ExecutionCycle groups a shutdown and corresponding wakeup operation.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `cycleId` | _string_ | CycleID is a unique identifier for this cycle. | [] |
| `shutdownExecution` | _[ExecutionOperationSummary](#executionoperationsummary)_ | ShutdownExecution summarizes the shutdown operation. | [Optional: {}] |
| `wakeupExecution` | _[ExecutionOperationSummary](#executionoperationsummary)_ | WakeupExecution summarizes the wakeup operation. | [Optional: {}] |

### ExecutionOperationSummary

ExecutionOperationSummary summarizes the results of a shutdown or wakeup operation.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `operation` | _string_ | Operation is the operation type (shutdown or wakeup). | [] |
| `startTime` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | StartTime is when the operation started. | [] |
| `endTime` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | EndTime is when the operation completed. | [Optional: {}] |
| `targetResults` | _[TargetExecutionResult](#targetexecutionresult) array_ | TargetResults summarizes the result for each target. | [Optional: {}] |
| `success` | _boolean_ | Success indicates if all targets completed successfully. | [] |
| `errorMessage` | _string_ | ErrorMessage contains error details if the operation failed. | [Optional: {}] |

### ExecutionState

ExecutionState represents per-target execution state.


### ExecutionStatus

ExecutionStatus represents per-target execution status.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `target` | _string_ | Target identifier (type/name). | [] |
| `executor` | _string_ | Executor used for this target. | [] |
| `state` | _[ExecutionState](#executionstate)_ | State of execution. | [Enum: [Pending Running Completed Failed Aborted]] |
| `startedAt` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | StartedAt is when execution started. | [Optional: {}] |
| `finishedAt` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | FinishedAt is when execution finished. | [Optional: {}] |
| `attempts` | _integer_ | Attempts is the number of execution attempts. | [] |
| `message` | _string_ | Message provides human-readable status. | [Optional: {}] |
| `jobRef` | _string_ | JobRef is the namespace/name of the runner Job. | [Optional: {}] |
| `logsRef` | _string_ | LogsRef is the reference to logs (stream id or object path). | [Optional: {}] |
| `restoreRef` | _string_ | RestoreRef is the reference to restore metadata artifact. | [Optional: {}] |
| `serviceAccountRef` | _string_ | ServiceAccountRef is the namespace/name of ephemeral SA. | [Optional: {}] |
| `connectorSecretRef` | _string_ | ConnectorSecretRef is the namespace/name of connector secret. | [Optional: {}] |
| `restoreConfigMapRef` | _string_ | RestoreConfigMapRef is the namespace/name of restore hints ConfigMap. | [Optional: {}] |

### ExecutionStrategy

ExecutionStrategy defines how targets are executed.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `type` | _[ExecutionStrategyType](#executionstrategytype)_ | Type of execution strategy. | [Enum: [Sequential Parallel DAG Staged] Required: {}] |
| `maxConcurrency` | _integer_ | MaxConcurrency limits concurrent executions (for Parallel/DAG/Staged). | [Minimum: 1 Optional: {}] |
| `dependencies` | _[Dependency](#dependency) array_ | Dependencies define DAG edges (only valid when Type=DAG). | [Optional: {}] |
| `stages` | _[Stage](#stage) array_ | Stages define execution groups (only valid when Type=Staged). | [Optional: {}] |

### ExecutionStrategyType

ExecutionStrategyType defines the execution strategy.


### GKEConfig

GKEConfig holds GKE-specific configuration.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name is the GKE cluster name. | [Required: {}] |
| `project` | _string_ | Project is the GCP project. | [Required: {}] |
| `location` | _string_ | Zone or region of the cluster. | [Required: {}] |

### HibernateNotification

HibernateNotification is the Schema for the hibernatenotifications API.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `metadata` | _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. | |
| `spec` | _[HibernateNotificationSpec](#hibernatenotificationspec)_ | Spec defines the desired state of HibernateNotification. | [] |
| `status` | _[HibernateNotificationStatus](#hibernatenotificationstatus)_ | Status defines the observed state of HibernateNotification. | [] |

### HibernateNotificationSpec

HibernateNotificationSpec defines the desired state of HibernateNotification.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `selector` | _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#labelselector-v1-meta)_ | Selector selects HibernatePlans by label.<br />The notification applies to all plans in the same namespace matching this selector. | [Required: {}] |
| `onEvents` | _[NotificationEvent](#notificationevent) array_ | OnEvents specifies which hook points trigger this notification. | [Enum: [Start Success Failure Recovery PhaseChange] MinItems: 1 Required: {}] |
| `sinks` | _[NotificationSink](#notificationsink) array_ | Sinks defines the notification destinations. | [MinItems: 1 Required: {}] |

### HibernateNotificationStatus

HibernateNotificationStatus defines the observed state of HibernateNotification.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `watchedPlans` | _[PlanReference](#planreference) array_ | WatchedPlans is the list of HibernatePlan references currently matching the selector. | [Optional: {}] |
| `lastDeliveryTime` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastDeliveryTime is the timestamp of the most recent successful notification delivery<br />across all sinks. Nil if no successful delivery has occurred. | [Optional: {}] |
| `lastFailureTime` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastFailureTime is the timestamp of the most recent failed notification delivery<br />across all sinks. Nil if no failure has occurred. | [Optional: {}] |
| `sinkStatuses` | _[NotificationSinkStatus](#notificationsinkstatus) array_ | SinkStatuses is a history log of per-sink delivery attempts, ordered newest-first.<br />The controller retains at most 20 entries; older entries are evicted when the cap is reached. | [Optional: {}] |
| `observedGeneration` | _integer_ | ObservedGeneration is the most recent .metadata.generation observed by the controller. | [Optional: {}] |

### HibernatePlan

HibernatePlan is the Schema for the hibernateplans API.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `metadata` | _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. | |
| `spec` | _[HibernatePlanSpec](#hibernateplanspec)_ | Spec defines the desired state of HibernatePlan. | [] |
| `status` | _[HibernatePlanStatus](#hibernateplanstatus)_ | Status defines the observed state of HibernatePlan. | [] |

### HibernatePlanSpec

HibernatePlanSpec defines the desired state of HibernatePlan.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `schedule` | _[Schedule](#schedule)_ | Schedule defines when hibernation occurs. | [Required: {}] |
| `execution` | _[Execution](#execution)_ | Execution defines the execution strategy. | [Required: {}] |
| `behavior` | _[Behavior](#behavior)_ | Behavior defines how failures are handled. | [Optional: {}] |
| `suspend` | _boolean_ | Suspend temporarily disables hibernation operations without deleting the plan.<br />When set to true, the plan transitions to Suspended phase and stops all execution.<br />When set to false, the plan transitions back to Active phase and resumes schedule evaluation.<br />Running jobs complete naturally but no new jobs are created while suspended. | [Optional: {}] |
| `targets` | _[Target](#target) array_ | Targets are the resources to hibernate. | [MinItems: 1] |

### HibernatePlanStatus

HibernatePlanStatus defines the observed state of HibernatePlan.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `currentCycleID` | _string_ | CurrentCycleID is the current hibernation cycle identifier. | [] |
| `phase` | _[PlanPhase](#planphase)_ | Phase is the overall plan phase. | [Enum: [Pending Active Hibernating Hibernated WakingUp Suspended Error]] |
| `lastTransitionTime` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastTransitionTime is when the phase last changed. | [Optional: {}] |
| `executions` | _[ExecutionStatus](#executionstatus) array_ | Executions is the per-target execution ledger. | [Optional: {}] |
| `observedGeneration` | _integer_ | ObservedGeneration is the last observed generation. | [] |
| `retryCount` | _integer_ | RetryCount tracks the number of retry attempts for error recovery. | [Optional: {}] |
| `lastRetryTime` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastRetryTime is when the last retry attempt was made. | [Optional: {}] |
| `errorMessage` | _string_ | ErrorMessage provides details about the error that caused PhaseError. | [Optional: {}] |
| `exceptionReferences` | _[ExceptionReference](#exceptionreference) array_ | ExceptionReferences is the history of schedule exceptions for this plan.<br />Maximum 10 entries, ordered by: active state first (most relevant), then by ValidFrom descending (most recent first).<br />Oldest entries are pruned when limit is exceeded. | [Optional: {}] |
| `currentStageIndex` | _integer_ | CurrentStageIndex tracks which stage is currently executing (0-based).<br />Reset to 0 when starting new hibernation/wakeup cycle. | [Optional: {}] |
| `currentOperation` | _string_ | CurrentOperation tracks the current operation type (shutdown or wakeup).<br />Used to determine which phase to transition to when stages complete. | [Optional: {}] |
| `executionHistory` | _[ExecutionCycle](#executioncycle) array_ | ExecutionHistory records historical execution cycles (max 5).<br />Each cycle contains shutdown and wakeup operation summaries.<br />Oldest cycles are pruned when limit is exceeded. | [Optional: {}] |

### K8SAccessConfig

K8SAccessConfig holds Kubernetes API access configuration.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `kubeconfigRef` | _[KubeconfigRef](#kubeconfigref)_ | KubeconfigRef references a Secret containing kubeconfig. | [Optional: {}] |
| `inCluster` | _boolean_ | InCluster uses in-cluster config (for self-management). | [Optional: {}] |

### K8SCluster

K8SCluster is the Schema for the k8sclusters API.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `metadata` | _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. | |
| `spec` | _[K8SClusterSpec](#k8sclusterspec)_ | Spec defines the desired state of K8SCluster. | [] |
| `status` | _[K8SClusterStatus](#k8sclusterstatus)_ | Status defines the observed state of K8SCluster. | [] |

### K8SClusterSpec

K8SClusterSpec defines the desired state of K8SCluster.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `providerRef` | _[ProviderRef](#providerref)_ | ProviderRef references the CloudProvider (optional for generic k8s). | [Optional: {}] |
| `eks` | _[EKSConfig](#eksconfig)_ | EKS holds EKS-specific configuration. | [Optional: {}] |
| `gke` | _[GKEConfig](#gkeconfig)_ | GKE holds GKE-specific configuration. | [Optional: {}] |
| `k8s` | _[K8SAccessConfig](#k8saccessconfig)_ | K8S holds generic Kubernetes access configuration. | [Optional: {}] |

### K8SClusterStatus

K8SClusterStatus defines the observed state of K8SCluster.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `ready` | _boolean_ | Ready indicates if the cluster is reachable. | [] |
| `message` | _string_ | Message provides status details. | [Optional: {}] |
| `lastValidated` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | LastValidated is when connectivity was last validated. | [Optional: {}] |
| `clusterType` | _[K8SClusterType](#k8sclustertype)_ | ClusterType is the detected cluster type. | [Enum: [eks gke k8s] Optional: {}] |

### K8SClusterType

K8SClusterType defines supported Kubernetes cluster types.


### KubeconfigRef

KubeconfigRef references a kubeconfig Secret.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name is the name of the Secret containing kubeconfig data. | [] |
| `namespace` | _string_ | Namespace is the namespace of the Secret. | [Optional: {}] |

### NotificationEvent

NotificationEvent defines the hook point that triggers a notification.


### NotificationSink

NotificationSink defines a destination for notification delivery.
All sink-specific configuration (endpoints, credentials, options) is delegated
to the referenced Secret under a well-known key ("config"). This minimizes
the CRD footprint and keeps sensitive data out of the resource spec.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name is a human-readable identifier for this sink (unique within spec.sinks). | [MinLength: 1 Required: {}] |
| `type` | _[NotificationSinkType](#notificationsinktype)_ | Type is the sink provider type. | [Enum: [slack telegram webhook] Required: {}] |
| `secretRef` | _[ObjectKeyReference](#objectkeyreference)_ | SecretRef is the name of the Secret containing the sink configuration.<br />The Secret must contain a key named "config" whose value is a JSON object<br />with all sink-specific settings (endpoint URL, credentials, options).<br />Slack config example:   \{"webhook_url": "https://hooks.slack.com/services/..."\}<br />Telegram config example: \{"token": "bot123:ABC", "chat_id": "-100123", "parse_mode": "MarkdownV2"\}<br />Webhook config example:  \{"url": "https://example.com/hook", "headers": \{"Authorization": "Bearer ..."\}\} | [Required: {}] |
| `templateRef` | _[ObjectKeyReference](#objectkeyreference)_ | TemplateRef references a ConfigMap key containing a Go template for message formatting.<br />If omitted, a built-in default template is used for the sink type. | [Optional: {}] |

### NotificationSinkStatus

NotificationSinkStatus records the outcome of a single notification delivery attempt.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name is the sink name as defined in spec.sinks[].name. | [] |
| `success` | _boolean_ | Success indicates whether this delivery attempt succeeded. | [] |
| `transitionTimestamp` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | TransitionTimestamp is when the delivery attempt completed. | [] |
| `message` | _string_ | Message is a human-readable description of the delivery outcome.<br />On success: "Successfully sent notification for <sink-name>"<br />On failure: the error string from the sink provider. | [Optional: {}] |

### NotificationSinkType

NotificationSinkType defines supported notification sink types.


### ObjectKeyReference

ObjectKeyReference is a reference to a specific key in a namespaced object.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name is the name of the object. | [Required: {}] |
| `key` | _string_ | Key is the key within the object primarily for Secret or ConfigMap data.<br />If omitted, the dispatcher uses a default key ("config" for SecretRef, "template.gotpl" for TemplateRef). | [Optional: {}] |

### OffHourWindow

OffHourWindow defines a time window for hibernation.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `start` | _string_ | Start time in HH:MM format (e.g., "20:00"). | [Pattern: `^([0-1]?[0-9]|2[0-3]):[0-5][0-9]$` Required: {}] |
| `end` | _string_ | End time in HH:MM format (e.g., "06:00"). | [Pattern: `^([0-1]?[0-9]|2[0-3]):[0-5][0-9]$` Required: {}] |
| `daysOfWeek` | _string array_ | DaysOfWeek specifies which days this window applies to.<br />Valid values: MON, TUE, WED, THU, FRI, SAT, SUN | [MinItems: 1 items:Enum: [MON TUE WED THU FRI SAT SUN]] |

### Parameters

Parameters is an opaque container for executor-specific config.
The JSON schema depends on the target's executor type. Each executor
defines its own parameter struct in pkg/executorparams (e.g.,
EKSParameters, RDSParameters, EC2Parameters, KarpenterParameters).


### PlanOperation

PlanOperation identifies the type of operation a HibernatePlan is currently executing.
Stored in HibernatePlanStatus.CurrentOperation and used as the LabelOperation value on runner Jobs.


### PlanPhase

PlanPhase represents the overall phase of the HibernatePlan.


### PlanReference

PlanReference references a HibernatePlan.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name of the HibernatePlan. | [Required: {}] |
| `namespace` | _string_ | Namespace of the HibernatePlan.<br />If empty, defaults to the exception's namespace. | [Optional: {}] |

### ProviderRef

ProviderRef references a CloudProvider.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name is the name of the CloudProvider resource. | [] |
| `namespace` | _string_ | Namespace is the namespace of the CloudProvider resource. | [Optional: {}] |

### Schedule

Schedule defines the hibernation schedule.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `timezone` | _string_ | Timezone for schedule evaluation (e.g., "Asia/Jakarta"). | [Required: {}] |
| `offHours` | _[OffHourWindow](#offhourwindow) array_ | OffHours defines when hibernation should occur. | [MinItems: 1] |

### ScheduleException

ScheduleException is the Schema for the scheduleexceptions API.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `metadata` | _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. | |
| `spec` | _[ScheduleExceptionSpec](#scheduleexceptionspec)_ | Spec defines the desired state of ScheduleException. | [] |
| `status` | _[ScheduleExceptionStatus](#scheduleexceptionstatus)_ | Status defines the observed state of ScheduleException. | [] |

### ScheduleExceptionSpec

ScheduleExceptionSpec defines the desired state of ScheduleException.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `planRef` | _[PlanReference](#planreference)_ | PlanRef references the HibernatePlan this exception applies to. | [Required: {}] |
| `validFrom` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | ValidFrom is the start time of the exception period (RFC3339 format). | [Format: date-time Required: {} Type: string] |
| `validUntil` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | ValidUntil is the end time of the exception period (RFC3339 format). | [Format: date-time Required: {} Type: string] |
| `type` | _[ExceptionType](#exceptiontype)_ | Type specifies the exception type: extend, suspend, or replace. | [Enum: [extend suspend replace] Required: {}] |
| `leadTime` | _string_ | LeadTime specifies buffer period before suspension window.<br />Only valid when Type is "suspend".<br />Format: duration string (e.g., "30m", "1h", "3600s").<br />Prevents NEW hibernation starts within this buffer before suspension. | [Optional: {} Pattern: `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`] |
| `windows` | _[OffHourWindow](#offhourwindow) array_ | Windows defines the time windows for this exception.<br />Meaning depends on Type:<br />- extend: Additional hibernation windows (union with base schedule)<br />- suspend: Windows to prevent hibernation (carve-out from schedule)<br />- replace: Complete replacement schedule (ignore base schedule) | [MinItems: 1] |

### ScheduleExceptionStatus

ScheduleExceptionStatus defines the observed state of ScheduleException.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `state` | _[ExceptionState](#exceptionstate)_ | State is the current lifecycle state of the exception. | [Enum: [Pending Active Expired Detached]] |
| `appliedAt` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | AppliedAt is when the exception was first applied. | [Optional: {}] |
| `expiredAt` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | ExpiredAt is when the exception transitioned to Expired state. | [Optional: {}] |
| `detachedAt` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | DetachedAt is when the exception transitioned to Detached state (plan was deleted). | [Optional: {}] |
| `message` | _string_ | Message provides diagnostic information about the exception state. | [Optional: {}] |

### SecretReference

SecretReference is a reference to a Secret.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name is the name of the Secret. | [] |
| `namespace` | _string_ | Namespace is the namespace of the Secret. | [Optional: {}] |

### ServiceAccountAuth

ServiceAccountAuth configures IRSA (IAM Roles for Service Accounts).
When specified (even as empty struct), indicates that the runner pod's
ServiceAccount should be used for authentication via workload identity.
The pod's ServiceAccount must have appropriate cloud provider annotations
(e.g., eks.amazonaws.com/role-arn for AWS IRSA).


### Stage

Stage defines a group of targets to execute together.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name of the stage. | [] |
| `parallel` | _boolean_ | Parallel indicates if targets in this stage run in parallel. | [] |
| `maxConcurrency` | _integer_ | MaxConcurrency limits parallelism within this stage. | [Minimum: 1 Optional: {}] |
| `targets` | _string array_ | Targets are the names of targets in this stage. | [] |

### StaticAuth

StaticAuth configures static credentials.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `secretRef` | _[SecretReference](#secretreference)_ | SecretRef references a Secret containing credentials. | [] |

### Target

Target defines a hibernation target.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `name` | _string_ | Name is the unique identifier for this target within the plan. | [Required: {}] |
| `type` | _string_ | Type of the target (e.g., eks, rds, ec2). | [Required: {}] |
| `connectorRef` | _[ConnectorRef](#connectorref)_ | ConnectorRef references the connector for this target. | [Required: {}] |
| `parameters` | _[Parameters](#parameters)_ | Parameters are executor-specific configuration. | [Optional: {}] |

### TargetExecutionResult

TargetExecutionResult is the result of a single target execution.

| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
| `target` | _string_ | Target is the target identifier (type/name). | [] |
| `state` | _[ExecutionState](#executionstate)_ | State is the final execution state (Completed or Failed). | [Enum: [Pending Running Completed Failed Aborted]] |
| `attempts` | _integer_ | Attempts is the number of attempts made. | [] |
| `executionId` | _string_ | ExecutionID is the unique identifier for this target execution. | [Optional: {}] |
| `startedAt` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | StartedAt is when execution started. | [Optional: {}] |
| `finishedAt` | _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.33/#time-v1-meta)_ | FinishedAt is when execution finished. | [Optional: {}] |


