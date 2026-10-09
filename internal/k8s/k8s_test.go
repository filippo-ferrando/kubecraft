package k8s

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var testOpts = Options{
	Namespace:    "minecraft",
	Image:        "itzg/minecraft-server:latest",
	StorageClass: "longhorn",
	HeapRatio:    0.75,
	BaseDomain:   "mine.example.com",
}

func testSpec() ServerSpec {
	return ServerSpec{
		ID:            uuid.New(),
		OwnerID:       uuid.New(),
		Slug:          "survival",
		Type:          "PAPER",
		Version:       "1.21.4",
		CPURequestM:   500,
		CPULimitM:     1000,
		MemoryLimitMB: 2048,
		StorageGB:     5,
		Running:       true,
		Env:           map[string]string{"DIFFICULTY": "hard", "MOTD": "hi"},
	}
}

func envMap(c corev1.Container) map[string]corev1.EnvVar {
	m := map[string]corev1.EnvVar{}
	for _, e := range c.Env {
		m[e.Name] = e
	}
	return m
}

func TestBuildStatefulSet(t *testing.T) {
	s := testSpec()
	sts := BuildStatefulSet(testOpts, s)
	if sts.Name != "mc-survival" || *sts.Spec.Replicas != 1 {
		t.Fatalf("bad name/replicas: %s %d", sts.Name, *sts.Spec.Replicas)
	}
	for _, k := range []string{LabelManagedBy, LabelServerID, LabelOwnerID, LabelSlug} {
		if sts.Labels[k] == "" || sts.Spec.Template.Labels[k] == "" {
			t.Errorf("missing label %s", k)
		}
	}
	c := sts.Spec.Template.Spec.Containers[0]
	env := envMap(c)
	if env["MEMORY"].Value != "1536M" {
		t.Errorf("heap should be 75%% of 2048M, got %s", env["MEMORY"].Value)
	}
	if env["EULA"].Value != "TRUE" || env["TYPE"].Value != "PAPER" || env["DIFFICULTY"].Value != "hard" {
		t.Errorf("env wrong: %+v", env)
	}
	if ref := env["RCON_PASSWORD"].ValueFrom; ref == nil || ref.SecretKeyRef.Name != "mc-survival-rcon" || env["RCON_PASSWORD"].Value != "" {
		t.Error("RCON password must come from the secret")
	}
	lim := c.Resources.Limits
	if lim.Memory().Value() != 2048*1024*1024 || lim.Cpu().MilliValue() != 1000 {
		t.Errorf("limits wrong: %v", lim)
	}
	if c.Resources.Requests.Cpu().MilliValue() != 500 || c.Resources.Requests.Memory().Cmp(*lim.Memory()) != 0 {
		t.Errorf("requests wrong: %v", c.Resources.Requests)
	}
	ps := sts.Spec.Template.Spec
	if !*ps.SecurityContext.RunAsNonRoot || *c.SecurityContext.AllowPrivilegeEscalation || c.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Error("pod security settings missing")
	}
	if c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged {
		t.Error("privileged")
	}
	for _, v := range ps.Volumes {
		if v.HostPath != nil {
			t.Error("host path mounted")
		}
	}
	if *ps.TerminationGracePeriodSeconds != 60 || *ps.AutomountServiceAccountToken {
		t.Error("grace period or token mount wrong")
	}
	if c.StartupProbe.Exec.Command[0] != "mc-health" {
		t.Error("startup probe missing")
	}
	if sts.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType {
		t.Error("config changes must wait for a restart")
	}

	s.Running = false
	if *BuildStatefulSet(testOpts, s).Spec.Replicas != 0 {
		t.Error("stopped server must have 0 replicas")
	}
}

func TestSpecHash(t *testing.T) {
	s := testSpec()
	a := BuildStatefulSet(testOpts, s).Annotations[AnnotationSpecHash]
	s.Running = false
	if BuildStatefulSet(testOpts, s).Annotations[AnnotationSpecHash] != a {
		t.Error("replicas must not change the template hash")
	}
	s.Env = map[string]string{"MOTD": "hi", "DIFFICULTY": "hard"}
	if BuildStatefulSet(testOpts, s).Annotations[AnnotationSpecHash] != a {
		t.Error("hash must not depend on map order")
	}
	s.Env["DIFFICULTY"] = "easy"
	if BuildStatefulSet(testOpts, s).Annotations[AnnotationSpecHash] == a {
		t.Error("hash must change with settings")
	}

	// Whitelist changes update the template but do not need a restart.
	before := BuildStatefulSet(testOpts, s)
	s.Env["WHITELIST"] = "Steve"
	after := BuildStatefulSet(testOpts, s)
	if before.Annotations[AnnotationSpecHash] == after.Annotations[AnnotationSpecHash] {
		t.Error("whitelist must change the spec hash")
	}
	if before.Spec.Template.Annotations[AnnotationRestartHash] != after.Spec.Template.Annotations[AnnotationRestartHash] {
		t.Error("whitelist must not change the restart hash")
	}
}

func TestBuildService(t *testing.T) {
	svc := BuildService(testOpts, testSpec())
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Error("server service must stay internal")
	}
	if svc.Annotations[AnnotationRouterHost] != "survival.mine.example.com" {
		t.Errorf("router annotation: %v", svc.Annotations)
	}
	if svc.Spec.Ports[0].Name != "minecraft" || svc.Spec.Ports[0].Port != 25565 {
		t.Error("minecraft port must be first and named for mc-router")
	}
}

func TestBuildPVC(t *testing.T) {
	pvc := BuildPVC(testOpts, testSpec())
	q := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if q.Cmp(resource.MustParse("5Gi")) != 0 || *pvc.Spec.StorageClassName != "longhorn" {
		t.Fatalf("pvc wrong: %v %v", q.String(), pvc.Spec.StorageClassName)
	}
	o := testOpts
	o.StorageClass = ""
	if BuildPVC(o, testSpec()).Spec.StorageClassName != nil {
		t.Error("empty storage class must use the cluster default")
	}
}

func TestHeapMB(t *testing.T) {
	if HeapMB(4096, 0.75) != 3072 || HeapMB(8192, 0.8) != 6553 {
		t.Fatal("heap math wrong")
	}
}

func TestEnsureIdempotent(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewClientset()
	c := New(cs, testOpts)
	s := testSpec()
	if err := c.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	sec, _ := cs.CoreV1().Secrets("minecraft").Get(ctx, "mc-survival-rcon", metav1.GetOptions{})
	pw := string(sec.Data["password"])
	if len(pw) < 32 {
		t.Fatal("weak rcon password")
	}
	if err := c.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	sec, _ = cs.CoreV1().Secrets("minecraft").Get(ctx, "mc-survival-rcon", metav1.GetOptions{})
	if string(sec.Data["password"]) != pw {
		t.Fatal("password rotated on re-ensure")
	}
	if got, _ := c.RCONPassword(ctx, "survival"); got != pw {
		t.Fatal("RCONPassword mismatch")
	}

	// Settings and plan changes update the template and grow the volume.
	s.Env["DIFFICULTY"] = "peaceful"
	s.StorageGB = 10
	s.Running = false
	if err := c.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	sts, _ := cs.AppsV1().StatefulSets("minecraft").Get(ctx, "mc-survival", metav1.GetOptions{})
	if envMap(sts.Spec.Template.Spec.Containers[0])["DIFFICULTY"].Value != "peaceful" || *sts.Spec.Replicas != 0 {
		t.Fatal("statefulset not updated")
	}
	pvc, _ := cs.CoreV1().PersistentVolumeClaims("minecraft").Get(ctx, "mc-survival-data", metav1.GetOptions{})
	if q := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; q.Cmp(resource.MustParse("10Gi")) != 0 {
		t.Fatalf("volume not grown: %s", q.String())
	}
	// Volumes never shrink.
	s.StorageGB = 5
	if err := c.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	pvc, _ = cs.CoreV1().PersistentVolumeClaims("minecraft").Get(ctx, "mc-survival-data", metav1.GetOptions{})
	if q := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; q.Cmp(resource.MustParse("10Gi")) != 0 {
		t.Fatal("volume shrunk")
	}
}

func TestEnsureConflict(t *testing.T) {
	ctx := context.Background()
	c := New(fake.NewClientset(), testOpts)
	a := testSpec()
	if err := c.Ensure(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := testSpec() // same slug, other server id
	if err := c.Ensure(ctx, b); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestScaleRestartDelete(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewClientset()
	c := New(cs, testOpts)
	s := testSpec()
	if err := c.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := c.Scale(ctx, s.Slug, false); err != nil {
		t.Fatal(err)
	}
	sts, _ := cs.AppsV1().StatefulSets("minecraft").Get(ctx, "mc-survival", metav1.GetOptions{})
	if *sts.Spec.Replicas != 0 {
		t.Fatal("not scaled down")
	}
	// Restart without a pod is a no-op.
	if err := c.Restart(ctx, s.Slug); err != nil {
		t.Fatal(err)
	}

	if err := c.Delete(ctx, s.Slug, s.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AppsV1().StatefulSets("minecraft").Get(ctx, "mc-survival", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("statefulset left behind")
	}
	pvc, err := cs.CoreV1().PersistentVolumeClaims("minecraft").Get(ctx, "mc-survival-data", metav1.GetOptions{})
	if err != nil || pvc.Labels[LabelRetained] != "survival" || pvc.Labels[LabelManagedBy] != "" {
		t.Fatalf("kept volume not retained: %v %v", err, pvc.Labels)
	}
	if blocked, _ := c.SlugBlocked(ctx, "survival"); !blocked {
		t.Fatal("retained volume must block the slug")
	}
	// A new server with the same slug cannot take the retained world.
	if err := c.Ensure(ctx, testSpec()); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	// Deleting twice is fine.
	if err := c.Delete(ctx, s.Slug, s.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().PersistentVolumeClaims("minecraft").Get(ctx, "mc-survival-data", metav1.GetOptions{}); err != nil {
		t.Fatal("delete of another server must not remove the retained volume")
	}
}

func TestDeleteRemovesVolume(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewClientset()
	c := New(cs, testOpts)
	s := testSpec()
	if err := c.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, s.Slug, s.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().PersistentVolumeClaims("minecraft").Get(ctx, "mc-survival-data", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("volume left behind")
	}
}

func TestSnapshot(t *testing.T) {
	ctx := context.Background()
	cs := fake.NewClientset()
	c := New(cs, testOpts)
	s := testSpec()
	if err := c.Ensure(ctx, s); err != nil {
		t.Fatal(err)
	}
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Servers[s.ID] == nil || snap.Servers[s.ID].StatefulSet == nil || snap.Services[s.ID] != "survival" || snap.PVCs[s.ID] != "survival" {
		t.Fatalf("snapshot incomplete: %+v", snap)
	}
}

func pod(hash string, mut func(*corev1.Pod)) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationRestartHash: hash}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if mut != nil {
		mut(p)
	}
	return p
}

func TestComputeStatus(t *testing.T) {
	sts := BuildStatefulSet(testOpts, testSpec())
	hash := sts.Spec.Template.Annotations[AnnotationRestartHash]
	now := metav1.NewTime(time.Now())
	cases := []struct {
		name    string
		desired string
		obs     *Observed
		want    string
	}{
		{"no resources", "running", nil, StateCreating},
		{"deleting", "deleting", nil, StateDeleting},
		{"stopped", "stopped", &Observed{StatefulSet: sts}, StateStopped},
		{"stopping", "stopped", &Observed{StatefulSet: sts, Pod: pod(hash, nil)}, StateStopping},
		{"no pod yet", "running", &Observed{StatefulSet: sts}, StateStarting},
		{"booting", "running", &Observed{StatefulSet: sts, Pod: pod(hash, func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: now}}}}
		})}, StateStarting},
		{"ready", "running", &Observed{StatefulSet: sts, Pod: pod(hash, func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: now}}}}
		})}, StateRunning},
		{"crashloop", "running", &Observed{StatefulSet: sts, Pod: pod(hash, func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{
				State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
				LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"}},
			}}
		})}, StateCrashLoop},
		{"image pull", "running", &Observed{StatefulSet: sts, Pod: pod(hash, func(p *corev1.Pod) {
			p.Status.Phase = corev1.PodPending
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}}
		})}, StateError},
		{"terminating", "running", &Observed{StatefulSet: sts, Pod: pod(hash, func(p *corev1.Pod) { p.DeletionTimestamp = &now })}, StateStopping},
	}
	for _, c := range cases {
		if got := ComputeStatus(c.desired, false, c.obs); got.State != c.want {
			t.Errorf("%s: got %s (%s), want %s", c.name, got.State, got.Message, c.want)
		}
	}

	st := ComputeStatus("running", false, &Observed{StatefulSet: sts, Pod: pod("old", nil)})
	if !st.RestartRequired {
		t.Error("old pod hash must require a restart")
	}
	crash := ComputeStatus("running", false, cases[7].obs)
	if crash.Message != "out of memory (OOMKilled), try a bigger plan" {
		t.Errorf("OOM message: %q", crash.Message)
	}
}
