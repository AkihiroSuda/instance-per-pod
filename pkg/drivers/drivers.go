package drivers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

type Driver interface {
	Run(ctx context.Context) error
	SchedulePod(ctx context.Context, pod *corev1.Pod) ([2]string, error)
}
