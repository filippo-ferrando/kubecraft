package k8s

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ErrConflict means a resource with the server's name belongs to something else.
var ErrConflict = errors.New("resource name already used by another server or a retained world")

// Client manages server resources in one namespace.
type Client struct {
	cs   kubernetes.Interface
	opts Options
}

func New(cs kubernetes.Interface, opts Options) *Client {
	return &Client{cs: cs, opts: opts}
}

// RestConfig loads the in-cluster config, or a kubeconfig file for local development.
func RestConfig(inCluster bool, kubeconfig string) (*rest.Config, error) {
	if inCluster {
		return rest.InClusterConfig()
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, nil).ClientConfig()
}

func (c *Client) Options() Options                { return c.opts }
func (c *Client) Clientset() kubernetes.Interface { return c.cs }

// Ping checks the API server is reachable and the namespace is readable.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cs.AppsV1().StatefulSets(c.opts.Namespace).List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("kubernetes unavailable: %w", err)
	}
	return nil
}

// owned reports whether obj labels belong to server id.
func owned(l map[string]string, id uuid.UUID) bool {
	return l[LabelManagedBy] == ManagedByValue && l[LabelServerID] == id.String()
}

// Ensure creates or updates every resource of a server. It is safe to call repeatedly.
func (c *Client) Ensure(ctx context.Context, s ServerSpec) error {
	if err := c.ensureSecret(ctx, s); err != nil {
		return fmt.Errorf("rcon secret: %w", err)
	}
	if err := c.ensurePVC(ctx, s); err != nil {
		return fmt.Errorf("volume: %w", err)
	}
	if err := c.ensureService(ctx, s); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	if err := c.ensureStatefulSet(ctx, s); err != nil {
		return fmt.Errorf("statefulset: %w", err)
	}
	return nil
}

func (c *Client) ensureSecret(ctx context.Context, s ServerSpec) error {
	api := c.cs.CoreV1().Secrets(c.opts.Namespace)
	cur, err := api.Get(ctx, SecretName(s.Slug), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		pw, err := randomPassword()
		if err != nil {
			return err
		}
		tok, err := randomPassword()
		if err != nil {
			return err
		}
		_, err = api.Create(ctx, BuildSecret(c.opts, s, pw, tok), metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if !owned(cur.Labels, s.ID) {
		return ErrConflict
	}
	// Fill keys missing in secrets made by older versions.
	changed := false
	for _, k := range []string{rconSecretKey, filesSecretKey} {
		if len(cur.Data[k]) == 0 {
			v, err := randomPassword()
			if err != nil {
				return err
			}
			if cur.Data == nil {
				cur.Data = map[string][]byte{}
			}
			cur.Data[k] = []byte(v)
			changed = true
		}
	}
	if changed {
		_, err = api.Update(ctx, cur, metav1.UpdateOptions{})
	}
	return err
}

func (c *Client) ensurePVC(ctx context.Context, s ServerSpec) error {
	api := c.cs.CoreV1().PersistentVolumeClaims(c.opts.Namespace)
	cur, err := api.Get(ctx, PVCName(s.Slug), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, BuildPVC(c.opts, s), metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if !owned(cur.Labels, s.ID) {
		return ErrConflict
	}
	// Volumes can only grow.
	want := storageQty(s.StorageGB)
	have := cur.Spec.Resources.Requests[corev1.ResourceStorage]
	if want.Cmp(have) > 0 {
		cur.Spec.Resources.Requests[corev1.ResourceStorage] = want
		_, err = api.Update(ctx, cur, metav1.UpdateOptions{})
	}
	return err
}

func (c *Client) ensureService(ctx context.Context, s ServerSpec) error {
	api := c.cs.CoreV1().Services(c.opts.Namespace)
	want := BuildService(c.opts, s)
	cur, err := api.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, want, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if !owned(cur.Labels, s.ID) {
		return ErrConflict
	}
	same := len(cur.Spec.Ports) == len(want.Spec.Ports) && cur.Spec.Selector[LabelServerID] == s.ID.String()
	for k, v := range want.Annotations {
		same = same && cur.Annotations[k] == v
	}
	if same {
		return nil
	}
	if cur.Annotations == nil {
		cur.Annotations = map[string]string{}
	}
	for k, v := range want.Annotations {
		cur.Annotations[k] = v
	}
	cur.Labels = want.Labels
	cur.Spec.Selector = want.Spec.Selector
	cur.Spec.Ports = want.Spec.Ports
	_, err = api.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

func (c *Client) ensureStatefulSet(ctx context.Context, s ServerSpec) error {
	api := c.cs.AppsV1().StatefulSets(c.opts.Namespace)
	want := BuildStatefulSet(c.opts, s)
	cur, err := api.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, want, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if !owned(cur.Labels, s.ID) {
		return ErrConflict
	}
	sameSpec := cur.Annotations[AnnotationSpecHash] == want.Annotations[AnnotationSpecHash]
	sameReplicas := cur.Spec.Replicas != nil && *cur.Spec.Replicas == *want.Spec.Replicas
	// mc-router owns the replica count of sleeping servers.
	if c.opts.sleeping(s) {
		sameReplicas = true
	}
	if sameSpec && sameReplicas {
		return nil
	}
	cur.Labels = want.Labels
	cur.Annotations = want.Annotations
	if !c.opts.sleeping(s) || cur.Spec.Replicas == nil {
		cur.Spec.Replicas = want.Spec.Replicas
	}
	cur.Spec.Template = want.Spec.Template
	cur.Spec.UpdateStrategy = want.Spec.UpdateStrategy
	_, err = api.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// Scale sets the replica count to 0 or 1.
func (c *Client) Scale(ctx context.Context, slug string, running bool) error {
	n := 0
	if running {
		n = 1
	}
	patch := fmt.Appendf(nil, `{"spec":{"replicas":%d}}`, n)
	_, err := c.cs.AppsV1().StatefulSets(c.opts.Namespace).Patch(ctx, StatefulSetName(slug), types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// Restart deletes the pod so the StatefulSet recreates it with the latest template.
func (c *Client) Restart(ctx context.Context, slug string) error {
	err := c.cs.CoreV1().Pods(c.opts.Namespace).Delete(ctx, PodName(slug), metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Delete removes the server resources. With keepData the volume is detached from the
// server and labelled as retained, so it survives and blocks reuse of the slug.
func (c *Client) Delete(ctx context.Context, slug string, id uuid.UUID, keepData bool) error {
	ns := c.opts.Namespace
	bg := metav1.DeletePropagationBackground
	opts := metav1.DeleteOptions{PropagationPolicy: &bg}
	ignore := func(err error) error {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := ignore(c.deleteOwned(ctx, "statefulset", slug, id, func() error {
		return c.cs.AppsV1().StatefulSets(ns).Delete(ctx, StatefulSetName(slug), opts)
	})); err != nil {
		return err
	}
	if err := ignore(c.deleteOwned(ctx, "service", slug, id, func() error {
		return c.cs.CoreV1().Services(ns).Delete(ctx, ServiceName(slug), opts)
	})); err != nil {
		return err
	}
	if err := ignore(c.deleteOwned(ctx, "secret", slug, id, func() error {
		return c.cs.CoreV1().Secrets(ns).Delete(ctx, SecretName(slug), opts)
	})); err != nil {
		return err
	}
	if err := c.DeleteHelper(ctx, slug); err != nil {
		return err
	}
	if keepData {
		return ignore(c.retainPVC(ctx, slug, id))
	}
	return ignore(c.deleteOwned(ctx, "pvc", slug, id, func() error {
		return c.cs.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, PVCName(slug), opts)
	}))
}

// deleteOwned runs del only when the named resource belongs to server id.
func (c *Client) deleteOwned(ctx context.Context, kind, slug string, id uuid.UUID, del func() error) error {
	ns := c.opts.Namespace
	var l map[string]string
	switch kind {
	case "statefulset":
		o, err := c.cs.AppsV1().StatefulSets(ns).Get(ctx, StatefulSetName(slug), metav1.GetOptions{})
		if err != nil {
			return err
		}
		l = o.Labels
	case "service":
		o, err := c.cs.CoreV1().Services(ns).Get(ctx, ServiceName(slug), metav1.GetOptions{})
		if err != nil {
			return err
		}
		l = o.Labels
	case "secret":
		o, err := c.cs.CoreV1().Secrets(ns).Get(ctx, SecretName(slug), metav1.GetOptions{})
		if err != nil {
			return err
		}
		l = o.Labels
	case "pvc":
		o, err := c.cs.CoreV1().PersistentVolumeClaims(ns).Get(ctx, PVCName(slug), metav1.GetOptions{})
		if err != nil {
			return err
		}
		l = o.Labels
	}
	if !owned(l, id) {
		return nil
	}
	return del()
}

func (c *Client) retainPVC(ctx context.Context, slug string, id uuid.UUID) error {
	api := c.cs.CoreV1().PersistentVolumeClaims(c.opts.Namespace)
	pvc, err := api.Get(ctx, PVCName(slug), metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !owned(pvc.Labels, id) {
		return nil
	}
	pvc.Labels = map[string]string{LabelRetained: slug}
	_, err = api.Update(ctx, pvc, metav1.UpdateOptions{})
	return err
}

// SlugBlocked reports whether a retained or foreign volume uses the slug's volume name.
func (c *Client) SlugBlocked(ctx context.Context, slug string) (bool, error) {
	_, err := c.cs.CoreV1().PersistentVolumeClaims(c.opts.Namespace).Get(ctx, PVCName(slug), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// RCONPassword reads the generated RCON password of a server.
func (c *Client) RCONPassword(ctx context.Context, slug string) (string, error) {
	s, err := c.cs.CoreV1().Secrets(c.opts.Namespace).Get(ctx, SecretName(slug), metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return string(s.Data[rconSecretKey]), nil
}

// RCONAddr is the in-cluster address of a server's RCON port.
func (c *Client) RCONAddr(slug string) string {
	return fmt.Sprintf("%s.%s.svc:%d", ServiceName(slug), c.opts.Namespace, RCONPort)
}

func randomPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ReplicasManaged reports whether the reconciler leaves replicas to mc-router.
func (c *Client) ReplicasManaged(s ServerSpec) bool { return c.opts.sleeping(s) }

// FilesToken reads the token of a server's file sidecar.
func (c *Client) FilesToken(ctx context.Context, slug string) (string, error) {
	s, err := c.cs.CoreV1().Secrets(c.opts.Namespace).Get(ctx, SecretName(slug), metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return string(s.Data[filesSecretKey]), nil
}

// ErrNoFiles means no pod with the file service is available.
var ErrNoFiles = errors.New("the server files are not reachable right now")

// FilesAddr returns host:port of a running file service for the server. With
// allowHelper, a helper pod is started when the server pod is down.
func (c *Client) FilesAddr(ctx context.Context, s ServerSpec, allowHelper bool) (string, error) {
	if c.opts.FilesImage == "" {
		return "", errors.New("file access is disabled")
	}
	pods := c.cs.CoreV1().Pods(c.opts.Namespace)
	if pod, err := pods.Get(ctx, PodName(s.Slug), metav1.GetOptions{}); err == nil {
		if ip := filesIP(pod); ip != "" {
			return fmt.Sprintf("%s:%d", ip, FilesPort), nil
		}
		if pod.DeletionTimestamp == nil {
			// The server pod holds the volume; wait for its sidecar instead of a helper.
			return "", ErrNoFiles
		}
	}
	if !allowHelper {
		return "", ErrNoFiles
	}
	helper, err := pods.Get(ctx, HelperPodName(s.Slug), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		helper, err = pods.Create(ctx, BuildHelperPod(c.opts, s), metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			helper, err = pods.Get(ctx, HelperPodName(s.Slug), metav1.GetOptions{})
		}
	}
	if err != nil {
		return "", err
	}
	if helper.Status.Phase == corev1.PodSucceeded || helper.Status.Phase == corev1.PodFailed {
		_ = pods.Delete(ctx, helper.Name, metav1.DeleteOptions{})
		return "", ErrNoFiles
	}
	for ctx.Err() == nil {
		if ip := filesIP(helper); ip != "" {
			return fmt.Sprintf("%s:%d", ip, FilesPort), nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
		if helper, err = pods.Get(ctx, HelperPodName(s.Slug), metav1.GetOptions{}); err != nil {
			return "", err
		}
	}
	return "", ErrNoFiles
}

func filesIP(p *corev1.Pod) string {
	if p.DeletionTimestamp != nil || p.Status.PodIP == "" {
		return ""
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == "files" && cs.State.Running != nil {
			return p.Status.PodIP
		}
	}
	return ""
}

// DeleteHelper removes the helper pod so the server can mount its volume.
func (c *Client) DeleteHelper(ctx context.Context, slug string) error {
	err := c.cs.CoreV1().Pods(c.opts.Namespace).Delete(ctx, HelperPodName(slug), metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
