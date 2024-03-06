/*
Copyright 2023 The Karmada Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package hpareplicassyncer

import (
	"context"
	"fmt"
	"strings"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/scale"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policyv1alpha1 "github.com/karmada-io/karmada/pkg/apis/policy/v1alpha1"
	"github.com/karmada-io/karmada/pkg/util"
	"github.com/karmada-io/karmada/pkg/util/helper"
)

const (
	// ControllerName is the controller name that will be used when reporting events.
	ControllerName = "hpa-replicas-syncer"
	// scaleRefWorkerNum is the async Worker number
	scaleRefWorkerNum = 1
)

// HPAReplicasSyncer is to sync replicas from status of HPA to resource template.
type HPAReplicasSyncer struct {
	Client        client.Client
	DynamicClient dynamic.Interface
	RESTMapper    meta.RESTMapper

	ScaleClient    scale.ScalesGetter
	scaleRefWorker util.AsyncWorker
}

// SetupWithManager creates a controller and register to controller manager.
func (r *HPAReplicasSyncer) SetupWithManager(mgr controllerruntime.Manager) error {
	scaleRefWorkerOptions := util.Options{
		Name:          "scale ref worker",
		ReconcileFunc: r.reconcileScaleRef,
	}
	r.scaleRefWorker = util.NewAsyncWorker(scaleRefWorkerOptions)
	r.scaleRefWorker.Run(scaleRefWorkerNum, context.Background().Done())

	return controllerruntime.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&autoscalingv2.HorizontalPodAutoscaler{}, builder.WithPredicates(r)).
		Complete(r)
}

// Reconcile performs a full reconciliation for the object referred to by the Request.
// The Controller will requeue the Request to be processed again if an error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *HPAReplicasSyncer) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	klog.V(4).Infof("Reconciling for HPA %s/%s", req.Namespace, req.Name)

	// 1. get hpa
	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	err := r.Client.Get(ctx, req.NamespacedName, hpa)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return controllerruntime.Result{}, nil
		}

		return controllerruntime.Result{}, err
	}

	// 2. get the scale ref resource
	scaleRef, err := r.getScaleRefResourceFromHPA(ctx, hpa)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// if the scale ref resource is not found, skip processing.
			klog.V(4).Infof("Scale of resource(kind=%s, %s/%s) not found, the resource might have been removed, skip",
				hpa.Spec.ScaleTargetRef.Kind, hpa.Namespace, hpa.Spec.ScaleTargetRef.Name)
			return controllerruntime.Result{}, nil
		}
		return controllerruntime.Result{}, err
	}

	isDivided, err := r.isDividedReplicaSchedulingType(ctx, scaleRef)
	if err != nil {
		klog.Errorf("judge isDividedReplicaSchedulingType failed: %+", err)
		return controllerruntime.Result{}, err
	}
	if !isDivided {
		return controllerruntime.Result{}, nil
	}

	// 3. update the replica field of the scale ref resource
	err = r.updateScaleRefIfNeed(ctx, hpa, scaleRef)
	if err != nil {
		return controllerruntime.Result{}, err
	}

	return controllerruntime.Result{}, nil
}

func (r *HPAReplicasSyncer) getScaleRefResourceFromHPA(ctx context.Context, hpa *autoscalingv2.HorizontalPodAutoscaler) (*unstructured.Unstructured, error) {
	targetGVK := schema.FromAPIVersionAndKind(hpa.Spec.ScaleTargetRef.APIVersion, hpa.Spec.ScaleTargetRef.Kind)

	mapping, err := r.RESTMapper.RESTMapping(targetGVK.GroupKind(), targetGVK.Version)
	if err != nil {
		return nil, fmt.Errorf("unable to recognize scale ref resource, %s/%v, err: %+v", hpa.Namespace, hpa.Spec.ScaleTargetRef, err)
	}

	scaleRef, err := r.DynamicClient.Resource(mapping.Resource).Namespace(hpa.Namespace).Get(ctx, hpa.Spec.ScaleTargetRef.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to find scale ref resource (%s/%v), err: %+v", hpa.Namespace, hpa.Spec.ScaleTargetRef, err)
	}

	return scaleRef, nil
}

// updateScaleRefIfNeed would update the scale of workload on fed-control plane
// if the replicas declared in the workload on karmada-control-plane does not match
// the actual replicas in member clusters effected by HPA.
func (r *HPAReplicasSyncer) updateScaleRefIfNeed(ctx context.Context, hpa *autoscalingv2.HorizontalPodAutoscaler, scaleRef *unstructured.Unstructured) error {
	targetGVK := schema.FromAPIVersionAndKind(hpa.Spec.ScaleTargetRef.APIVersion, hpa.Spec.ScaleTargetRef.Kind)
	replicaField, ok := gvkToReplicaFields[targetGVK]
	if !ok {
		klog.Warningf("skip udpate scale ref resource for unknown replica fields for %+v", targetGVK)
		return nil
	}

	nestedField := strings.Split(replicaField, util.RetainFieldNestedSeparator)
	oldReplicas, exist, err := unstructured.NestedInt64(scaleRef.Object, nestedField...)
	if err != nil || !exist {
		klog.Errorf("failed to get %s from scaleRef: %s %s/%s", replicaField, scaleRef.GetKind(), scaleRef.GetNamespace(), scaleRef.GetName())
		return err
	}

	if int64(hpa.Status.CurrentReplicas) == oldReplicas {
		return nil
	}

	newScaleRef := scaleRef.DeepCopy()
	util.MergeLabel(newScaleRef, util.SkipReconcileAt, time.Now().Format(time.RFC3339))
	err = unstructured.SetNestedField(scaleRef.Object, hpa.Status.CurrentReplicas, nestedField...)
	if err != nil {
		return err
	}

	// use patch is better than update, when modification occur after get, patch can still success while update can not
	patchBytes, err := helper.GenMergePatch(scaleRef, newScaleRef)
	if err != nil {
		return fmt.Errorf("failed to gen merge patch (%s/%v), err: %+v", hpa.Namespace, hpa.Spec.ScaleTargetRef, err)
	}
	if len(patchBytes) == 0 {
		klog.Infof("no diff, skip adding (%s/%v)", hpa.Namespace, hpa.Spec.ScaleTargetRef)
		return nil
	}

	mapping, err := r.RESTMapper.RESTMapping(targetGVK.GroupKind(), targetGVK.Version)
	if err != nil {
		return err
	}
	_, err = r.DynamicClient.Resource(mapping.Resource).Namespace(newScaleRef.GetNamespace()).
		Patch(ctx, newScaleRef.GetName(), types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.Infof("scale ref resource is not found (%s/%v), skip processing", hpa.Namespace, hpa.Spec.ScaleTargetRef)
			return nil
		}
		return fmt.Errorf("failed to patch scale ref resource (%s/%v), err: %+v", hpa.Namespace, hpa.Spec.ScaleTargetRef, err)
	}

	klog.V(4).Infof("Successfully synced scale for resource(kind=%s, %s/%s) from %d to %d",
		hpa.Spec.ScaleTargetRef.Kind, hpa.Namespace, hpa.Spec.ScaleTargetRef.Name, oldReplicas, hpa.Status.DesiredReplicas)

	return nil
}

func (r *HPAReplicasSyncer) isDividedReplicaSchedulingType(ctx context.Context, scaleRef *unstructured.Unstructured) (bool, error) {
	policyLabels := scaleRef.GetLabels()
	claimedPPNamespace := util.GetLabelValue(policyLabels, policyv1alpha1.PropagationPolicyNamespaceLabel)
	claimedPPName := util.GetLabelValue(policyLabels, policyv1alpha1.PropagationPolicyNameLabel)

	if claimedPPNamespace != "" && claimedPPName != "" {
		pp := &policyv1alpha1.PropagationPolicy{}
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: claimedPPNamespace, Name: claimedPPName}, pp); err != nil {
			return false, fmt.Errorf("get claimed pp (%s/%s) failed: %w", claimedPPNamespace, claimedPPName, err)
		}
		return pp.Spec.Placement.ReplicaSchedulingType() == policyv1alpha1.ReplicaSchedulingTypeDivided, nil
	}

	claimedCPPName := util.GetLabelValue(policyLabels, policyv1alpha1.ClusterPropagationPolicyLabel)
	if claimedCPPName != "" {
		cpp := &policyv1alpha1.ClusterPropagationPolicy{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: claimedCPPName}, cpp); err != nil {
			return false, fmt.Errorf("get claimed cpp (%s) failed: %w", claimedCPPName, err)
		}
		return cpp.Spec.Placement.ReplicaSchedulingType() == policyv1alpha1.ReplicaSchedulingTypeDivided, nil
	}

	return false, fmt.Errorf("no claimed policy found")
}
