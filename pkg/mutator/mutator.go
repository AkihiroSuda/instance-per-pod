package mutator

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/pkg/errors"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	"github.com/AkihiroSuda/instance-per-pod/pkg/jsonpatch"
)

type BasicMutator struct {
	NodeLabel string
	NodeTaint string
	PodLabel  string
}

func errResponse(err error) *admissionv1beta1.AdmissionResponse {
	klog.Errorf("error while mutation: %v", err)
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
	// DaemonSets are ignored regardless to the pod label
	for _, v := range pod.ObjectMeta.OwnerReferences {
		if v.Kind == "DaemonSet" {
			return true
		}
	}
	// Pods with the IPP pod label must not be ignored
	if _, ok := pod.ObjectMeta.Labels[m.PodLabel]; ok {
		return false
	}
	// Otherwise ignored
	return true
}

// replicaSetUIDLabel is used to avoid colocating pod replicas on same node.
// This label is set by IPP, not by the user.
const replicaSetUIDLabel = "ipp-rs-uid"

func (m *BasicMutator) createPatch(ctx context.Context, pod *corev1.Pod) ([]jsonpatch.Op, error) {
	podLabelValue := pod.ObjectMeta.Labels[m.PodLabel]
	if podLabelValue == "" {
		// no patch
		return nil, nil
	}
	replicaSetUID := ""
	for _, or := range pod.ObjectMeta.OwnerReferences {
		if strings.EqualFold(or.Kind, "ReplicaSet") {
			replicaSetUID = string(or.UID)
			break
		}
	}
	var ops []jsonpatch.Op
	if replicaSetUID != "" {
		ops = append(ops, jsonpatch.Op{
			Op:    jsonpatch.OpAdd,
			Path:  "/metadata/labels/" + jsonpatch.EscapeRFC6901(replicaSetUIDLabel),
			Value: replicaSetUID,
		})
	}
	toleration := corev1.Toleration{
		Key:      m.NodeTaint,
		Operator: corev1.TolerationOpEqual,
		Value:    "true",
		Effect:   corev1.TaintEffectNoSchedule,
	}
	ops = append(ops, jsonpatch.Op{
		Op:   jsonpatch.OpAdd,
		Path: "/spec/tolerations",
		Value: []corev1.Toleration{
			toleration,
		},
	})
	affinity := corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      m.NodeLabel,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"true"},
							},
						},
					},
				},
			},
		},
		PodAntiAffinity: &corev1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      m.PodLabel,
								Operator: metav1.LabelSelectorOpNotIn,
								Values:   []string{podLabelValue},
							},
						},
					},
					TopologyKey: corev1.LabelHostname,
				},
			},
		},
	}

	if replicaSetUID != "" {
		// avoid colocating replicas on same node
		term := corev1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      replicaSetUIDLabel,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{replicaSetUID},
					},
				},
			},
			TopologyKey: corev1.LabelHostname,
		}
		// autoscaler respects requiredDuringSchedulingIgnoredDuringExecution,
		// but ignores preferredDuringSchedulingIgnoredDuringExecution
		// https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#does-ca-respect-node-affinity-when-selecting-node-groups-to-scale-up
		affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution =
			append(affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution, term)
	}
	ops = append(ops, jsonpatch.Op{
		Op:    jsonpatch.OpAdd,
		Path:  "/spec/affinity",
		Value: &affinity,
	})
	return ops, nil
}

func (m *BasicMutator) Mutate(ctx context.Context, req *admissionv1beta1.AdmissionRequest) *admissionv1beta1.AdmissionResponse {
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		return errResponse(errors.Wrap(err, "failed to unmarshal Pod"))
	}
	klog.Infof("Mutating pod namespace=%q name=%q generateName=%q UID=%q", pod.Namespace, pod.Name, pod.GenerateName, pod.UID)
	if m.podShouldBeIgnored(req, &pod) {
		klog.Infof("Ignoring pod namespace=%q name=%q generateName=%q UID=%q", pod.Namespace, pod.Name, pod.GenerateName, pod.UID)
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
	klog.Infof("* patch for pod %s/%s: %q", pod.Namespace, pod.Name, string(patchBytes))
	patchType := admissionv1beta1.PatchTypeJSONPatch
	return &admissionv1beta1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *admissionv1beta1.PatchType {
			return &patchType
		}(),
	}
}
