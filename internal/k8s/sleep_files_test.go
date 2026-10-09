package k8s

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func filesOpts() Options {
	o := testOpts
	o.FilesImage = "kubecraft:test"
	o.AutoSleep = true
	return o
}

func TestFilesSidecar(t *testing.T) {
	sts := BuildStatefulSet(filesOpts(), testSpec())
	cs := sts.Spec.Template.Spec.Containers
	if len(cs) != 2 || cs[0].Name != "minecraft" || cs[1].Name != "files" || cs[1].Args[0] != "files" {
		t.Fatalf("containers: %+v", cs)
	}
	f := cs[1]
	if *f.SecurityContext.ReadOnlyRootFilesystem != true || f.Env[2].ValueFrom.SecretKeyRef.Key != "files-token" {
		t.Fatalf("sidecar settings: %+v", f)
	}
	if len(BuildStatefulSet(testOpts, testSpec()).Spec.Template.Spec.Containers) != 1 {
		t.Fatal("sidecar added without FilesImage")
	}

	// A ready sidecar must not make a booting server look running.
	pod := &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
		{Name: "minecraft", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
		{Name: "files", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
	}}}
	if st := ComputeStatus("running", false, &Observed{StatefulSet: sts, Pod: pod}); st.State != StateStarting {
		t.Fatalf("state: %s", st.State)
	}
}

func TestAutoSleepAnnotations(t *testing.T) {
	o := filesOpts()
	s := testSpec()
	s.AutoSleep = true
	a := BuildService(o, s).Annotations
	if a[annoScaleUp] != "true" || a[annoScaleDown] != "true" || a[annoAsleepMOTD] == "" {
		t.Fatalf("sleeping server annotations: %v", a)
	}
	s.Running = false
	if a := BuildService(o, s).Annotations; a[annoScaleUp] != "false" {
		t.Fatal("a stopped server must never wake on join")
	}
	s.Running, s.AutoSleep = true, false
	if a := BuildService(o, s).Annotations; a[annoScaleDown] != "false" {
		t.Fatal("auto sleep off must not scale down")
	}
	if a := BuildService(testOpts, s).Annotations; len(a) != 1 {
		t.Fatalf("no auto scale annotations without router support: %v", a)
	}
}

func TestSleepingKeepsReplicas(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewClientset()
	c := New(cs, filesOpts())
	s := testSpec()
	s.AutoSleep = true
	if err := c.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	// mc-router scaled it down; ensure must not undo that.
	if err := c.Scale(ctx, s.Slug, false); err != nil {
		t.Fatal(err)
	}
	s.Env["MOTD"] = "changed"
	if err := c.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	sts, _ := cs.AppsV1().StatefulSets("minecraft").Get(ctx, "mc-survival", metav1.GetOptions{})
	if *sts.Spec.Replicas != 0 {
		t.Fatal("ensure woke a sleeping server")
	}
	if st := ComputeStatus("running", true, &Observed{StatefulSet: sts}); st.State != StateSleeping {
		t.Fatalf("state: %s", st.State)
	}
	// Turning auto sleep off brings it back.
	s.AutoSleep = false
	if err := c.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	sts, _ = cs.AppsV1().StatefulSets("minecraft").Get(ctx, "mc-survival", metav1.GetOptions{})
	if *sts.Spec.Replicas != 1 {
		t.Fatal("auto sleep off must restore replicas")
	}
	sec, _ := cs.CoreV1().Secrets("minecraft").Get(ctx, "mc-survival-rcon", metav1.GetOptions{})
	if len(sec.Data["files-token"]) < 32 {
		t.Fatal("files token missing")
	}
}

func TestFilesAddr(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewClientset()
	c := New(cs, filesOpts())
	s := testSpec()
	s.Running = false
	if err := c.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FilesAddr(ctx, s, false); !errors.Is(err, ErrNoFiles) {
		t.Fatalf("no pod and no helper: %v", err)
	}
	// The helper is created; it becomes reachable once its sidecar runs.
	go func() {
		for i := 0; i < 50; i++ {
			time.Sleep(20 * time.Millisecond)
			p, err := cs.CoreV1().Pods("minecraft").Get(ctx, HelperPodName(s.Slug), metav1.GetOptions{})
			if err != nil {
				continue
			}
			p.Status = corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.9", ContainerStatuses: []corev1.ContainerStatus{
				{Name: "files", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			}}
			_, _ = cs.CoreV1().Pods("minecraft").UpdateStatus(ctx, p, metav1.UpdateOptions{})
			return
		}
	}()
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addr, err := c.FilesAddr(tctx, s, true)
	if err != nil || addr != "10.0.0.9:8081" {
		t.Fatalf("helper addr: %q %v", addr, err)
	}
	p, _ := cs.CoreV1().Pods("minecraft").Get(ctx, HelperPodName(s.Slug), metav1.GetOptions{})
	if p.Labels[LabelServerID] != "" || p.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "mc-survival-data" || *p.Spec.ActiveDeadlineSeconds == 0 {
		t.Fatalf("helper pod: %+v", p.Labels)
	}
	snap, _ := c.Snapshot(ctx)
	if snap.Helpers[s.ID] == nil || snap.Servers[s.ID].Pod != nil {
		t.Fatal("helper must be tracked apart from the server pod")
	}
	if err := c.Delete(ctx, s.Slug, s.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().Pods("minecraft").Get(ctx, HelperPodName(s.Slug), metav1.GetOptions{}); err == nil {
		t.Fatal("delete must remove the helper")
	}
}
