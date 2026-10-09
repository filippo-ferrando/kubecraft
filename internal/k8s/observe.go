package k8s

import (
	"context"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Server states shown to users.
const (
	StateCreating  = "creating"
	StateStarting  = "starting"
	StateRunning   = "running"
	StateStopping  = "stopping"
	StateStopped   = "stopped"
	StateError     = "error"
	StateCrashLoop = "crashloop"
	StateDeleting  = "deleting"
	// StateSleeping is a running server scaled to 0 by mc-router until a player joins.
	StateSleeping = "sleeping"
)

// Observed is the live cluster view of one server.
type Observed struct {
	StatefulSet *appsv1.StatefulSet
	Pod         *corev1.Pod
}

// Snapshot holds every managed resource in the namespace, grouped by server id.
type Snapshot struct {
	Servers map[uuid.UUID]*Observed
	// Ids seen on any managed resource, used to find orphans.
	Services map[uuid.UUID]string
	Secrets  map[uuid.UUID]string
	PVCs     map[uuid.UUID]string
	// PlayitPorts is the playit port annotation of each server Service ("" when none).
	PlayitPorts map[uuid.UUID]string
	// Helpers are temporary file pods by server id.
	Helpers map[uuid.UUID]*corev1.Pod
}

func managedSelector() string { return LabelManagedBy + "=" + ManagedByValue }

// Snapshot lists all managed resources with one call per kind.
func (c *Client) Snapshot(ctx context.Context) (*Snapshot, error) {
	ns := c.opts.Namespace
	opts := metav1.ListOptions{LabelSelector: managedSelector()}
	snap := &Snapshot{
		Servers:  map[uuid.UUID]*Observed{},
		Services: map[uuid.UUID]string{},
		Secrets:  map[uuid.UUID]string{},
		PVCs:     map[uuid.UUID]string{},
		Helpers:  map[uuid.UUID]*corev1.Pod{},

		PlayitPorts: map[uuid.UUID]string{},
	}
	get := func(id uuid.UUID) *Observed {
		o, ok := snap.Servers[id]
		if !ok {
			o = &Observed{}
			snap.Servers[id] = o
		}
		return o
	}

	stss, err := c.cs.AppsV1().StatefulSets(ns).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for i := range stss.Items {
		if id, ok := serverID(stss.Items[i].Labels); ok {
			get(id).StatefulSet = &stss.Items[i]
		}
	}
	pods, err := c.cs.CoreV1().Pods(ns).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for i := range pods.Items {
		if id, err := uuid.Parse(pods.Items[i].Labels[LabelHelperFor]); err == nil {
			snap.Helpers[id] = &pods.Items[i]
			continue
		}
		if id, ok := serverID(pods.Items[i].Labels); ok {
			get(id).Pod = &pods.Items[i]
		}
	}
	svcs, err := c.cs.CoreV1().Services(ns).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for _, s := range svcs.Items {
		if id, ok := serverID(s.Labels); ok {
			snap.Services[id] = s.Labels[LabelSlug]
			snap.PlayitPorts[id] = s.Annotations[AnnotationPlayitPort]
		}
	}
	secrets, err := c.cs.CoreV1().Secrets(ns).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for _, s := range secrets.Items {
		if id, ok := serverID(s.Labels); ok {
			snap.Secrets[id] = s.Labels[LabelSlug]
		}
	}
	pvcs, err := c.cs.CoreV1().PersistentVolumeClaims(ns).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for _, p := range pvcs.Items {
		if id, ok := serverID(p.Labels); ok {
			snap.PVCs[id] = p.Labels[LabelSlug]
		}
	}
	return snap, nil
}

func serverID(l map[string]string) (uuid.UUID, bool) {
	id, err := uuid.Parse(l[LabelServerID])
	return id, err == nil
}

// Status is the computed state of a server.
type Status struct {
	State           string     `json:"state"`
	Message         string     `json:"message"`
	RestartRequired bool       `json:"restart_required"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	Node            string     `json:"node,omitempty"`
}

// waiting reasons that mean the pod will not start without a change.
var fatalWaiting = map[string]bool{
	"ImagePullBackOff":           true,
	"ErrImagePull":               true,
	"InvalidImageName":           true,
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
}

// ComputeStatus maps the desired state and the cluster view to a user facing state.
// sleeping is true when mc-router owns the replicas of the server.
func ComputeStatus(desired string, sleeping bool, o *Observed) Status {
	if desired == StateDeleting {
		return Status{State: StateDeleting}
	}
	if o == nil || o.StatefulSet == nil {
		return Status{State: StateCreating, Message: "waiting for resources"}
	}
	pod := o.Pod
	if desired == StateStopped {
		if pod != nil {
			return Status{State: StateStopping, Message: "saving world and shutting down"}
		}
		return Status{State: StateStopped}
	}

	if pod == nil {
		if sleeping && o.StatefulSet.Spec.Replicas != nil && *o.StatefulSet.Spec.Replicas == 0 {
			return Status{State: StateSleeping, Message: "wakes up when a player joins"}
		}
		return Status{State: StateStarting, Message: "scheduling"}
	}
	st := Status{Node: pod.Spec.NodeName}
	st.RestartRequired = pod.Annotations[AnnotationRestartHash] != o.StatefulSet.Spec.Template.Annotations[AnnotationRestartHash]
	if pod.DeletionTimestamp != nil {
		st.State, st.Message = StateStopping, "restarting"
		return st
	}
	if pod.Status.Phase == corev1.PodPending {
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
				st.State, st.Message = StateStarting, "waiting for a node: "+c.Message
				return st
			}
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		// The file sidecar must not decide the server state.
		if cs.Name != "" && cs.Name != "minecraft" {
			continue
		}
		if w := cs.State.Waiting; w != nil {
			switch {
			case w.Reason == "CrashLoopBackOff":
				st.State, st.Message = StateCrashLoop, lastTermination(cs)
				return st
			case fatalWaiting[w.Reason]:
				st.State, st.Message = StateError, w.Reason+": "+w.Message
				return st
			}
		}
		if r := cs.State.Running; r != nil {
			t := r.StartedAt.Time
			st.StartedAt = &t
		}
		if cs.Ready {
			st.State = StateRunning
			return st
		}
	}
	if pod.Status.Phase == corev1.PodFailed {
		st.State, st.Message = StateError, pod.Status.Reason+" "+pod.Status.Message
		return st
	}
	st.State, st.Message = StateStarting, "server is starting"
	return st
}

func lastTermination(cs corev1.ContainerStatus) string {
	if t := cs.LastTerminationState.Terminated; t != nil {
		if t.Reason == "OOMKilled" {
			return "out of memory (OOMKilled), try a bigger plan"
		}
		return "server keeps crashing: " + t.Reason
	}
	return "server keeps crashing"
}
