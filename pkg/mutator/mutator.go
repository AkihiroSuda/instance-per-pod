package mutator

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/AkihiroSuda/instance-per-pod/pkg/annotations"
	"github.com/AkihiroSuda/instance-per-pod/pkg/drivers"
	"github.com/AkihiroSuda/instance-per-pod/pkg/jsonpatch"
	"github.com/AkihiroSuda/instance-per-pod/pkg/labels"
)

type BasicMutator struct {
	Driver drivers.Driver
}

func errResponse(err error) *admissionv1beta1.AdmissionResponse {
	glog.Errorf("error while mutation: %v", err)
	return &admissionv1beta1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}

func (m *BasicMutator) podShouldBeIgnored(req *admissionv1beta1.AdmissionRequest, pod *corev1.Pod) bool {
	// FIXME: not familiar with DryRun mode
	if req.DryRun != nil && *req.DryRun {
		return true
	}
	if strings.HasSuffix(pod.ObjectMeta.Namespace, "-system") {
		return true
	}
	for k, v := range pod.ObjectMeta.Annotations {
		if k == annotations.Ignore && v == "true" {
			return true
		}
	}
	for _, v := range pod.ObjectMeta.OwnerReferences {
		if v.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func (m *BasicMutator) createPatch(ctx context.Context, pod *corev1.Pod) ([]jsonpatch.Op, error) {
	var ops []jsonpatch.Op
	scheduledNodeLabelKV, err := m.Driver.SchedulePod(ctx, pod)
	if err != nil {
		return ops, err
	}
	ops = append(ops, jsonpatch.Op{
		Op:    jsonpatch.OpAdd,
		Path:  "/spec/nodeSelector",
		Value: map[string]string{scheduledNodeLabelKV[0]: scheduledNodeLabelKV[1]},
	})
	ops = append(ops, jsonpatch.Op{
		Op:    jsonpatch.OpAdd,
		Path:  "/metadata/labels/" + jsonpatch.EscapeRFC6901(labels.Mutated),
		Value: "true",
	})
	return ops, nil
}

func (m *BasicMutator) Mutate(ctx context.Context, req *admissionv1beta1.AdmissionRequest) *admissionv1beta1.AdmissionResponse {
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		return errResponse(errors.Wrap(err, "failed to unmarshal Pod"))
	}
	glog.Infof("Mutating pod namespace=%q name=%q generateName=%q UID=%q", pod.Namespace, pod.Name, pod.GenerateName, pod.UID)
	if m.podShouldBeIgnored(req, &pod) {
		glog.Infof("Ignoring pod namespace=%q name=%q generateName=%q UID=%q", pod.Namespace, pod.Name, pod.GenerateName, pod.UID)
		return &admissionv1beta1.AdmissionResponse{
			Allowed: true,
		}
	}
	patch, err := m.createPatch(ctx, &pod)
	if err != nil {
		return errResponse(err)
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return errResponse(err)
	}
	glog.Infof("* patch for pod %s/%s: %q", pod.Namespace, pod.Name, string(patchBytes))
	patchType := admissionv1beta1.PatchTypeJSONPatch
	return &admissionv1beta1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *admissionv1beta1.PatchType {
			return &patchType
		}(),
	}
}
