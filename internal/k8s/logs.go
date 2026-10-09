package k8s

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ContainerStarted reports whether the server container has started, so logs can be read.
func (c *Client) ContainerStarted(ctx context.Context, slug string) (bool, error) {
	pod, err := c.cs.CoreV1().Pods(c.opts.Namespace).Get(ctx, PodName(slug), metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != "minecraft" {
			continue
		}
		if cs.State.Running != nil || cs.State.Terminated != nil {
			return true, nil
		}
	}
	return false, nil
}

// LogStream follows the server log with timestamps. Either tail or since should be set.
func (c *Client) LogStream(ctx context.Context, slug string, tail *int64, since *metav1.Time) (io.ReadCloser, error) {
	return c.cs.CoreV1().Pods(c.opts.Namespace).GetLogs(PodName(slug), &corev1.PodLogOptions{
		Container:  "minecraft",
		Follow:     true,
		Timestamps: true,
		TailLines:  tail,
		SinceTime:  since,
	}).Stream(ctx)
}
