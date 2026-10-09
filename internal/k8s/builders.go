// Package k8s builds and manages the Kubernetes resources of each server.
package k8s

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	LabelManagedBy = "app.kubernetes.io/managed-by"
	ManagedByValue = "mc-manager"
	LabelServerID  = "mc-manager/server-id"
	LabelOwnerID   = "mc-manager/owner-id"
	LabelSlug      = "mc-manager/slug"
	// LabelRetained marks a world volume kept after its server was deleted.
	LabelRetained = "mc-manager/retained-from"

	// AnnotationSpecHash covers the whole template and detects drift.
	AnnotationSpecHash = "mc-manager/spec-hash"
	// AnnotationRestartHash leaves out settings applied live over RCON, so
	// changing them does not ask for a restart.
	AnnotationRestartHash = "mc-manager/restart-hash"
	AnnotationRouterHost  = "mc-router.itzg.me/externalServerName"

	GamePort = 25565
	RCONPort = 25575

	rconSecretKey  = "password"
	filesSecretKey = "files-token"
	mcUID          = 1000

	// FilesPort is where the file sidecar listens (see internal/files).
	FilesPort = 8081

	// mc-router auto scale annotations on the Service.
	annoScaleUp    = "mc-router.itzg.me/autoScaleUp"
	annoScaleDown  = "mc-router.itzg.me/autoScaleDown"
	annoAsleepMOTD = "mc-router.itzg.me/autoScaleAsleepMOTD"
	annoLoadMOTD   = "mc-router.itzg.me/autoScaleLoadingMOTD"

	// Read by the playit gateway (internal/gate): the local port of the server's tunnel.
	LabelPlayit          = "mc-manager/playit"
	AnnotationPlayitPort = "mc-manager/playit-port"

	// LabelHelperFor marks the temporary file pod of a stopped server.
	LabelHelperFor = "mc-manager/helper-for"
)

// Options are cluster-wide settings shared by all servers.
type Options struct {
	Namespace    string
	Image        string // repository:tag
	StorageClass string
	HeapRatio    float64
	BaseDomain   string
	// FilesImage runs the file sidecar; empty disables backups and file editing.
	FilesImage string
	// AutoSleep is true when mc-router runs with auto scale up and down.
	AutoSleep bool
}

// ServerSpec is the desired state of one server.
type ServerSpec struct {
	ID            uuid.UUID
	OwnerID       uuid.UUID
	Slug          string
	Type          string
	Version       string
	CPURequestM   int32
	CPULimitM     int32
	MemoryLimitMB int32
	StorageGB     int32
	Running       bool
	// AutoSleep lets mc-router scale the server to 0 when idle and back on join.
	AutoSleep   bool
	DisplayName string
	// PlayitPort is the local port of the server's playit tunnel, 0 when it has none.
	PlayitPort int
	// Env holds the validated user settings as itzg env vars.
	Env map[string]string
}

func StatefulSetName(slug string) string { return "mc-" + slug }
func ServiceName(slug string) string     { return "mc-" + slug }
func SecretName(slug string) string      { return "mc-" + slug + "-rcon" }
func PVCName(slug string) string         { return "mc-" + slug + "-data" }
func PodName(slug string) string         { return StatefulSetName(slug) + "-0" }

// Host is the address players connect to.
func (o Options) Host(slug string) string { return slug + "." + o.BaseDomain }

// HeapMB is the JVM heap for a memory limit, leaving room for non-heap memory.
func HeapMB(limitMB int32, ratio float64) int32 {
	return int32(float64(limitMB) * ratio)
}

func labels(s ServerSpec) map[string]string {
	return map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelServerID:  s.ID.String(),
		LabelOwnerID:   s.OwnerID.String(),
		LabelSlug:      s.Slug,
	}
}

func selector(s ServerSpec) map[string]string {
	return map[string]string{LabelManagedBy: ManagedByValue, LabelServerID: s.ID.String()}
}

func (o Options) meta(name string, s ServerSpec) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: o.Namespace, Labels: labels(s)}
}

func BuildSecret(o Options, s ServerSpec, password, filesToken string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: o.meta(SecretName(s.Slug), s),
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{rconSecretKey: []byte(password), filesSecretKey: []byte(filesToken)},
	}
}

func BuildPVC(o Options, s ServerSpec) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: o.meta(PVCName(s.Slug), s),
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: storageQty(s.StorageGB)},
			},
		},
	}
	if o.StorageClass != "" {
		sc := o.StorageClass
		pvc.Spec.StorageClassName = &sc
	}
	return pvc
}

// sleeping reports whether mc-router manages the replicas of this server.
func (o Options) sleeping(s ServerSpec) bool { return o.AutoSleep && s.AutoSleep && s.Running }

func serviceAnnotations(o Options, s ServerSpec) map[string]string {
	a := map[string]string{AnnotationRouterHost: o.Host(s.Slug)}
	if !o.AutoSleep {
		return a
	}
	// Explicit false so a stopped server is never woken by a connection.
	scale := strconv.FormatBool(o.sleeping(s))
	a[annoScaleUp], a[annoScaleDown] = scale, scale
	name := s.DisplayName
	if name == "" {
		name = s.Slug
	}
	a[annoAsleepMOTD] = name + " is sleeping, join to wake it up"
	a[annoLoadMOTD] = name + " is starting, try again in a moment"
	return a
}

func BuildService(o Options, s ServerSpec) *corev1.Service {
	m := o.meta(ServiceName(s.Slug), s)
	m.Annotations = serviceAnnotations(o, s)
	m.Labels[LabelPlayit] = strconv.FormatBool(s.PlayitPort > 0)
	if s.PlayitPort > 0 {
		m.Annotations[AnnotationPlayitPort] = strconv.Itoa(s.PlayitPort)
	}
	return &corev1.Service{
		ObjectMeta: m,
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selector(s),
			Ports: []corev1.ServicePort{
				// mc-router picks the port named "minecraft".
				{Name: "minecraft", Port: GamePort, TargetPort: intstr.FromString("minecraft"), Protocol: corev1.ProtocolTCP},
				{Name: "rcon", Port: RCONPort, TargetPort: intstr.FromString("rcon"), Protocol: corev1.ProtocolTCP},
			},
		},
	}
}

// liveEnv are env vars whose changes are also applied over RCON.
var liveEnv = map[string]bool{
	"WHITELIST": true, "OPS": true, "ENABLE_WHITELIST": true, "ENFORCE_WHITELIST": true,
	"DIFFICULTY": true, "MODE": true, "PVP": true,
}

// BuildStatefulSet returns the server workload. The pod template carries a hash so a
// running pod with an older hash is known to need a restart.
func BuildStatefulSet(o Options, s ServerSpec) *appsv1.StatefulSet {
	replicas := int32(0)
	if s.Running {
		replicas = 1
	}
	tpl := podTemplate(o, s)
	tpl.Annotations = map[string]string{AnnotationRestartHash: restartHash(tpl)}
	hash := hashTemplate(tpl)

	m := o.meta(StatefulSetName(s.Slug), s)
	m.Annotations = map[string]string{AnnotationSpecHash: hash}
	return &appsv1.StatefulSet{
		ObjectMeta: m,
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: ServiceName(s.Slug),
			Selector:    &metav1.LabelSelector{MatchLabels: selector(s)},
			// Config changes apply on the next restart, not right away.
			UpdateStrategy:      appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType},
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Template:            tpl,
		},
	}
}

func podTemplate(o Options, s ServerSpec) corev1.PodTemplateSpec {
	heap := HeapMB(s.MemoryLimitMB, o.HeapRatio)
	env := []corev1.EnvVar{
		{Name: "EULA", Value: "TRUE"},
		{Name: "TYPE", Value: s.Type},
		{Name: "VERSION", Value: s.Version},
		{Name: "MEMORY", Value: fmt.Sprintf("%dM", heap)},
		{Name: "ENABLE_RCON", Value: "true"},
		{Name: "RCON_PORT", Value: fmt.Sprint(RCONPort)},
		{Name: "RCON_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: SecretName(s.Slug)},
			Key:                  rconSecretKey,
		}}},
		{Name: "ENABLE_AUTOPAUSE", Value: "false"},
		{Name: "STOP_DURATION", Value: "50"},
	}
	// Sorted so the template hash is stable.
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		env = append(env, corev1.EnvVar{Name: k, Value: s.Env[k]})
	}

	probe := func(period, timeout, failures int32) *corev1.Probe {
		return &corev1.Probe{
			ProbeHandler:     corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"mc-health"}}},
			PeriodSeconds:    period,
			TimeoutSeconds:   timeout,
			FailureThreshold: failures,
		}
	}
	uid := int64(mcUID)
	nonRoot := true
	noEscalation := false
	noToken := false
	noLinks := false
	grace := int64(60)

	tpl := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels(s)},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: &grace,
			AutomountServiceAccountToken:  &noToken,
			// Service link env vars could clash with the image's own settings.
			EnableServiceLinks: &noLinks,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser:      &uid,
				RunAsGroup:     &uid,
				FSGroup:        &uid,
				RunAsNonRoot:   &nonRoot,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:  "minecraft",
				Image: o.Image,
				Env:   env,
				Ports: []corev1.ContainerPort{
					{Name: "minecraft", ContainerPort: GamePort, Protocol: corev1.ProtocolTCP},
					{Name: "rcon", ContainerPort: RCONPort, Protocol: corev1.ProtocolTCP},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(s.CPURequestM), resource.DecimalSI),
						corev1.ResourceMemory: memQty(s.MemoryLimitMB),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(s.CPULimitM), resource.DecimalSI),
						corev1.ResourceMemory: memQty(s.MemoryLimitMB),
					},
				},
				// First starts download the server jar and can take many minutes.
				StartupProbe:   probe(10, 5, 120),
				ReadinessProbe: probe(10, 5, 3),
				LivenessProbe:  probe(30, 10, 10),
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &noEscalation,
					RunAsNonRoot:             &nonRoot,
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data"}},
			}},
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: PVCName(s.Slug),
				}},
			}},
		},
	}
	if o.FilesImage != "" {
		tpl.Spec.Containers = append(tpl.Spec.Containers, filesContainer(o, s, false))
	}
	return tpl
}

// filesContainer is the file service sharing /data with the server.
func filesContainer(o Options, s ServerSpec, readOnly bool) corev1.Container {
	no, yes := false, true
	return corev1.Container{
		Name:  "files",
		Image: o.FilesImage,
		Args:  []string{"files"},
		Env: []corev1.EnvVar{
			{Name: "FILES_ADDR", Value: fmt.Sprintf(":%d", FilesPort)},
			{Name: "FILES_DIR", Value: "/data"},
			{Name: "FILES_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: SecretName(s.Slug)},
				Key:                  filesSecretKey,
			}}},
		},
		Ports: []corev1.ContainerPort{{Name: "files", ContainerPort: FilesPort, Protocol: corev1.ProtocolTCP}},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("32Mi")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("128Mi")},
		},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &no,
			RunAsNonRoot:             &yes,
			ReadOnlyRootFilesystem:   &yes,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data", ReadOnly: readOnly}},
	}
}

// HelperPodName is the temporary file pod used while a server is down.
func HelperPodName(slug string) string { return "mc-" + slug + "-files" }

// BuildHelperPod mounts the world volume of a stopped server for downloads and
// edits. It ends by itself after a while.
func BuildHelperPod(o Options, s ServerSpec) *corev1.Pod {
	tpl := podTemplate(o, s)
	deadline := int64(1800)
	l := map[string]string{LabelManagedBy: ManagedByValue, LabelHelperFor: s.ID.String(), LabelSlug: s.Slug}
	spec := tpl.Spec
	spec.Containers = []corev1.Container{filesContainer(o, s, false)}
	spec.RestartPolicy = corev1.RestartPolicyNever
	spec.ActiveDeadlineSeconds = &deadline
	grace := int64(5)
	spec.TerminationGracePeriodSeconds = &grace
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: HelperPodName(s.Slug), Namespace: o.Namespace, Labels: l},
		Spec:       spec,
	}
}

func restartHash(t corev1.PodTemplateSpec) string {
	c := *t.DeepCopy()
	env := c.Spec.Containers[0].Env[:0]
	for _, e := range c.Spec.Containers[0].Env {
		if !liveEnv[e.Name] {
			env = append(env, e)
		}
	}
	c.Spec.Containers[0].Env = env
	return hashTemplate(c)
}

func hashTemplate(t corev1.PodTemplateSpec) string {
	b, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func memQty(mb int32) resource.Quantity {
	return *resource.NewQuantity(int64(mb)*1024*1024, resource.BinarySI)
}

func storageQty(gb int32) resource.Quantity {
	return *resource.NewQuantity(int64(gb)*1024*1024*1024, resource.BinarySI)
}
