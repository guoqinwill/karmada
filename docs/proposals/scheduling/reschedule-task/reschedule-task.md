---
title: Introduce a mechanism to actively triggle rescheduling
authors:
  - "@chaosi-zju"
reviewers:
  - "@RainbowMango"
  - "@chaunceyjiang"
  - "TBD"
approvers:
  - "@RainbowMango"
  - "TBD"

creation-date: 2024-01-30
---

# Introduce a mechanism to actively trigger rescheduling

## Background

According to the current implementation, after the replicas of workload is scheduled, it will remain inertia and the 
replicas distribution will not change. 

However, in some scenarios, users hope to have means to actively trigger rescheduling.

### Motivation

Assuming the user has propagated the workloads to member clusters, replicas migrated due to member cluster failure.

However, the user expects an approach to trigger rescheduling after member cluster restored, so that replicas can
migrate back.

### Goals

Introduce a mechanism to actively trigger rescheduling of workload resource.

### Applicable scenario

This feature might help in a scenario where: the `replicas` in resource template or `placement` in policy has not changed, 
but the user wants to actively trigger rescheduling of replicas.

## Proposal

### Overview

This proposal aims to introduce a mechanism of active triggering rescheduling, which benefits a lot in application 
failover scenarios. This can be realized by introducing a new API, and a new field would be marked when this new API 
called, so that scheduler can perceive the need for rescheduling.

### User story

In application failover scenarios, replicas migrated from primary cluster to backup cluster when primary cluster failue.

As a user, I want to trigger replicas migrating back when cluster restored, so that:

1. restore the disaster recovery mode to ensure the reliability and stability of the cluster.
2. save the cost of the backup cluster.

### Notes/Constraints/Caveats

This ability is limited to triggering rescheduling. The scheduling result will be recalculated according to the
Placement in the current ResourceBinding, and the scheduling result is not guaranteed to be exactly the same as before
the cluster failure.

> Notes: pay attention to the recalculation is basing on Placement in the current `ResourceBinding`, not "Policy". So if
> your activation preference of Policy is `Lazy`, the rescheduling is still basing on previous `ResourceBinding` even if
> the current Policy has been changed.

## Design Details

### API change

* Introduce a new API named `ScheduleTrigger` into policy apiGroup `policy.karmada.io/v1alpha1`:

```go
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:resource:path=scheduletriggers,scope="Cluster",categories={karmada-io}
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ScheduleTrigger represents the desired behavior and status of a job which can enforces a rescheduling.
//
// Notes: make sure the clocks of controller-manager and scheduler are synchronized when using this API.
type ScheduleTrigger struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    // Spec represents the specification of the desired behavior of ScheduleTrigger.
    // +required
    Spec ScheduleTriggerSpec `json:"spec"`
    
    // Status represents the status of ScheduleTrigger.
    // +optional
    Status ScheduleTriggerStatus `json:"status,omitempty"`
}

// ScheduleTriggerSpec represents the specification of the desired behavior of Reschedule.
type ScheduleTriggerSpec struct {
    // TargetRefPolicy used to select batch of resources managed by certain policies.
    // +optional
    TargetRefPolicy []TargetRefPolicy `json:"targetRefPolicy,omitempty"`
    
    // TargetRefResource used to select resources.
    // +optional
    TargetRefResource []TargetRefResource `json:"targetRefResource,omitempty"`
    
    // RetryAfterSeconds specified the time interval in seconds to retry when part of the target resource triggering schedule failed.
    // Defaults to value 3, and value 0 means retry immediately.
    // +kubebuilder:default=3
    // +optional
    RetryAfterSeconds int `json:"retryAfterSeconds,omitempty"`
    
    // AutoCleanAfterMinutes specified the time in minutes when to clean up current ScheduleTrigger.
    // When time up, the current object will be deleted, no matter whether executed success.
    // Defaults to value 60, and value 0 means never do auto clean.
    // +kubebuilder:default=60
    // +optional
    AutoCleanAfterMinutes int `json:"autoCleanAfterMinutes,omitempty"`
}

// TargetRefPolicy the resources bound policy will be selected.
type TargetRefPolicy struct {
    // Name of the target policy.
    // +required
    Name string `json:"name"`
    
    // Namespace of the target policy.
    // Default is empty, which means it is a cluster propagation policy.
    // +optional
    Namespace string `json:"namespace,omitempty"`
}

// TargetRefResource the resource will be selected.
type TargetRefResource struct {
    // APIVersion represents the API version of the target resource.
    // +required
    APIVersion string `json:"apiVersion"`
    
    // Kind represents the Kind of the target resource.
    // +required
    Kind string `json:"kind"`
    
    // Name of the target resource.
    // +required
    Name string `json:"name"`
    
    // Namespace of the target resource.
    // Default is empty, which means it is a non-namespacescoped resource.
    // +optional
    Namespace string `json:"namespace,omitempty"`
}

// ScheduleTriggerStatus contains information about the current status of a ScheduleTrigger
// updated periodically by schedule trigger controller.
type ScheduleTriggerStatus struct {
    // Phase represents the specific extent to which the task has been executed.
    // Valid options are "Running", "Failed" and "Success", Defaults to "Running".
    // +kubebuilder:default=Running
    // +optional
    Phase TriggerPhase `json:"phase,omitempty"`
    
    // FailedResourceList the list of trigger failed resources.
    // +optional
    FailedResourceList []FailedResource `json:"failedResourceList,omitempty"`
}

// TriggerPhase the specific extent to which the task has been executed
type TriggerPhase string

const (
    // TriggerRunning the schedule trigger is on going, and hasn't finished its process.
    TriggerRunning TriggerPhase = "Running"
    // TriggerFailed some or all selected resource has been triggered a scheduling failed.
    TriggerFailed TriggerPhase = "Failed"
    // TriggerSuccess all selected resource has been triggered a scheduling success.
    TriggerSuccess TriggerPhase = "Success"
)

// FailedResource trigger failed resource and its failure reason.
type FailedResource struct {
    TargetRefResource `json:",inline"`
    
    // FailReason the reason of resource triggered failed.
    // +optional
    FailReason string `json:"failReason,omitempty"`
}

// +kubebuilder:resource:scope="Cluster"
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ScheduleTriggerList contains a list of ScheduleTrigger
type ScheduleTriggerList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    
    // Items holds a list of ScheduleTrigger.
    Items []ScheduleTrigger `json:"items"`
}
```

* Add two new fields to ResourceBinding/ClusterResourceBinding, one is in `spec` named `rescheduleTriggeredAt`, another
is in `status` named `lastScheduledTime`, detail description is as follows:

```go
// ResourceBindingSpec represents the expectation of ResourceBinding.
type ResourceBindingSpec struct {
    ...
	// RescheduleTriggeredAt is a timestamp representing when the referenced resource is triggered rescheduling.
	// Only when this timestamp is later than timestamp in status.lastScheduledTime will the rescheduling actually execute.
	//
	// It is represented in RFC3339 form (like '2006-01-02T15:04:05Z') and is in UTC.
	// It is recommended to be populated by the REST handler of command.karmada.io/Reschedule API.
	// +optional
	RescheduleTriggeredAt metav1.Time `json:"rescheduleTriggeredAt,omitempty"`
    ...
}

// ResourceBindingStatus represents the overall status of the strategy as well as the referenced resources.
type ResourceBindingStatus struct {
	...
	// LastScheduledTime is a timestamp representing scheduler successfully finished a scheduling.
	// It is represented in RFC3339 form (like '2006-01-02T15:04:05Z') and is in UTC.
	// +optional
	LastScheduledTime metav1.Time `json:"lastScheduledTime,omitempty"`
    ...
}
```

### Example

Assuming there is a Deployment named `nginx`, the user wants to trigger its rescheduling,
he just needs to apply following yaml:

```yaml
apiVersion: policy.karmada.io/v1alpha1
kind: ScheduleTrigger
metadata:
  name: demo
spec:
  targetRefResource:
    - apiVersion: apps/v1
      kind: Deployment
      name: demo-test-1
      namespace: default
    - apiVersion: apps/v1
      kind: Deployment
      name: demo-test-2
      namespace: default
  targetRefPolicy:
    - name: default-pp
      namespace: default
  retryAfterSeconds: 3         # optional, default is 3 seconds
  autoCleanAfterMinutes: 5     # optional, default is 60 monites, 0 means never do auto clean
```

> Notes: 
> 1. The users can define one of the `targetRefResource` and `targetRefPolicy` field, or they can also define both.
> We are taking the union of the resources selected by the two fields.
> 2. As for `targetRefResource`:
>    2.1. `name` sub-field is required;
>    2.2. `namespace` sub-field is required when it is a namespace scoped resource, while empty when it is a cluster wide resource;
> 3. As for `targetRefPolicy`:
>    3.1. `name` sub-field is required;
>    4.1. `namespace` sub-field is required when it is a PropagationPolicy, while empty when it is a ClusterPropagationPolicy;

Then, he will get a `scheduletrigger.policy.karmada.io/demo created` result, which means the API created success, attention,
not finished. Simultaneously, he will see the new field `spec.placement.rescheduleTriggeredAt` in binding of the selected
resource been set to current timestamp.

```yaml
apiVersion: work.karmada.io/v1alpha2
kind: ResourceBinding
metadata:
  name: nginx-deployment
  namespace: default
spec:
  rescheduleTriggeredAt: "2024-04-17T15:04:05Z"
  ...
```

Then, rescheduling is in progress. If it succeeds, the `status.lastScheduledTime` field of binding will be updated,
which represents scheduler finished a rescheduling.; If it failed, scheduler will retry.

```yaml
apiVersion: work.karmada.io/v1alpha2
kind: ResourceBinding
metadata:
  name: nginx-deployment
  namespace: default
spec:
  rescheduleTriggeredAt: "2024-04-17T15:04:05Z"
  ...
status:
  lastScheduledTime: "2024-04-17T15:04:06Z"
  conditions:
    - ...
    - lastTransitionTime: "2024-03-08T08:53:03Z"
      message: Binding has been scheduled successfully.
      reason: Success
      status: "True"
      type: Scheduled
    - lastTransitionTime: "2024-03-08T08:53:03Z"
      message: All works have been successfully applied
      reason: FullyAppliedSuccess
      status: "True"
      type: FullyApplied
```

Finally, all works have been successfully applied, the user will observe changes in the actual distribution of resource 
template; the user can also see several recorded event in resource template, just like:

```shell
$ kubectl --context karmada-apiserver describe deployment demo
...
Events:
  Type    Reason                  Age                From                                Message
  ----    ------                  ----               ----                                -------
  ...
  Normal  ScheduleBindingSucceed  31s                default-scheduler                   Binding has been scheduled successfully.
  Normal  GetDependenciesSucceed  31s                dependencies-distributor            Get dependencies([]) succeed.
  Normal  SyncSucceed             31s                execution-controller                Successfully applied resource(default/demo) to cluster member1
  Normal  AggregateStatusSucceed  31s (x4 over 31s)  resource-binding-status-controller  Update resourceBinding(default/demo-deployment) with AggregatedStatus successfully.
  Normal  SyncSucceed             31s                execution-controller                Successfully applied resource(default/demo1) to cluster member2
```

### Implementation logic

1) add an CRD type API named `ScheduleTrigger`, detail described as above.

2) add a controller into controller-manager, which will fetch all referred resource declared in `targetRefResource` or 
indirectly declared by `targetRefPolicy`, and then set `spec.rescheduleTriggeredAt` field to current timestamp in 
corresponding ResourceBinding. In addition, a timer will be implemented that runs every minute to delete expired `ScheduleTrigger`
objects when the automatic cleanup time of the `ScheduleTrigger` object is reached.

3) in scheduling process, add a trigger condition: even if `Placement` and `Replicas` of binding unchanged, schedule will
be triggerred if `spec.rescheduleTriggeredAt` is later than `status.lastScheduledTime`. After schedule finished, scheduler 
will update `status.lastScheduledTime` when refreshing binding back.
