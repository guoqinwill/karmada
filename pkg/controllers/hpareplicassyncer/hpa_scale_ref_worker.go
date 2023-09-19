package hpareplicassyncer

import (
	"context"
	"fmt"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	"github.com/karmada-io/karmada/pkg/util"
	"github.com/karmada-io/karmada/pkg/util/helper"
)

type labelEventKind int

const (
	addLabelEvent labelEventKind = iota
	deleteLabelEvent

	ScaleControlByHPALabel = "horizontalpodautoscaler.karmada.io/name"
)

type labelEvent struct {
	kind labelEventKind
	hpa  *autoscalingv2.HorizontalPodAutoscaler
}

func (r *HPAReplicasSyncer) ReconcileScaleRef(key util.QueueKey) error {
	event, ok := key.(labelEvent)
	if !ok {
		klog.Errorf("Found invalid key when reconciling hpa scale ref: %+v", key)
		return fmt.Errorf("invalid key")
	}

	switch event.kind {
	case addLabelEvent:
		return r.addHPALabelToScaleRef(context.TODO(), event.hpa)
	case deleteLabelEvent:
		return r.deleteHPALabelToScaleRef(context.TODO(), event.hpa)
	default:
		return fmt.Errorf("invalid key")
	}
}

func (r *HPAReplicasSyncer) addHPALabelToScaleRef(ctx context.Context, hpa *autoscalingv2.HorizontalPodAutoscaler) error {
	targetGVK := schema.FromAPIVersionAndKind(hpa.Spec.ScaleTargetRef.APIVersion, hpa.Spec.ScaleTargetRef.Kind)
	mapping, err := r.RESTMapper.RESTMapping(targetGVK.GroupKind(), targetGVK.Version)
	if err != nil {
		return fmt.Errorf("unable to recognize scale ref resource: %+v", err)
	}

	scaleRef, err := r.DynamicClient.Resource(mapping.Resource).Namespace(hpa.Namespace).Get(ctx, hpa.Spec.ScaleTargetRef.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.Infof("scale ref resource is not found (%+v), skip processing", hpa.Spec.ScaleTargetRef)
			return nil
		}
		return fmt.Errorf("failed to find scale ref resource: %+v, err: %+v", scaleRef, err)
	}

	// use patch is better than update, when modification occur after get, patch can still success while update can not
	newScaleRef := scaleRef.DeepCopy()
	util.MergeLabel(newScaleRef, ScaleControlByHPALabel, hpa.GetName())
	patchBytes, err := helper.GenMergePatch(scaleRef, newScaleRef)
	if err != nil {
		return fmt.Errorf("failed to gen merge patch: %+v", err)
	}
	if len(patchBytes) == 0 {
		klog.Infof("hpa labels already exist, skip adding (%+v)", hpa.Spec.ScaleTargetRef)
		return nil
	}

	_, err = r.DynamicClient.Resource(mapping.Resource).Namespace(newScaleRef.GetNamespace()).
		Patch(ctx, newScaleRef.GetName(), types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.Infof("scale ref resource is not found (%+v), skip processing", hpa.Spec.ScaleTargetRef)
			return nil
		}
		return fmt.Errorf("failed to patch scale ref resource: %+v (%+v)", err, hpa.Spec.ScaleTargetRef)
	}

	klog.Infof("add hpa labels to %+v success", hpa.Spec.ScaleTargetRef)
	return nil
}

func (r *HPAReplicasSyncer) deleteHPALabelToScaleRef(ctx context.Context, hpa *autoscalingv2.HorizontalPodAutoscaler) error {
	targetGVK := schema.FromAPIVersionAndKind(hpa.Spec.ScaleTargetRef.APIVersion, hpa.Spec.ScaleTargetRef.Kind)
	mapping, err := r.RESTMapper.RESTMapping(targetGVK.GroupKind(), targetGVK.Version)
	if err != nil {
		return fmt.Errorf("unable to recognize scale ref resource: %+v", err)
	}

	scaleRef, err := r.DynamicClient.Resource(mapping.Resource).Namespace(hpa.Namespace).Get(ctx, hpa.Spec.ScaleTargetRef.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.Infof("scale ref resource is not found (%+v), skip processing", hpa.Spec.ScaleTargetRef)
			return nil
		}
		return fmt.Errorf("failed to find scale ref resource: %+v, err: %+v", scaleRef, err)
	}

	// use patch is better than update, when modification occur after get, patch can still success while update can not
	newScaleRef := scaleRef.DeepCopy()
	util.RemoveLabels(newScaleRef, ScaleControlByHPALabel)
	patchBytes, err := helper.GenMergePatch(scaleRef, newScaleRef)
	if err != nil {
		return fmt.Errorf("failed to gen merge patch: %+v", err)
	}
	if len(patchBytes) == 0 {
		klog.Infof("hpa labels not exist, skip deleting (%+v)", hpa.Spec.ScaleTargetRef)
		return nil
	}

	_, err = r.DynamicClient.Resource(mapping.Resource).Namespace(newScaleRef.GetNamespace()).
		Patch(ctx, newScaleRef.GetName(), types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.Infof("scale ref resource is not found (%+v), skip processing", hpa.Spec.ScaleTargetRef)
			return nil
		}
		return fmt.Errorf("failed to patch scale ref resource: %+v (%+v)", err, hpa.Spec.ScaleTargetRef)
	}

	klog.Infof("delete hpa labels from %+v success", hpa.Spec.ScaleTargetRef)
	return nil
}
