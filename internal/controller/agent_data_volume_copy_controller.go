package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	agentDataVolumeCopyReady           = "Ready"
	agentDataVolumeCopyLabel           = "control.anvil.hazyforge.io/agent-data-volume-copy"
	agentDataVolumeCopyRoleLabel       = "control.anvil.hazyforge.io/agent-data-volume-copy-role"
	agentDataVolumeCopyManagedByLabel  = "app.kubernetes.io/managed-by"
	agentDataVolumeCopyManagedByValue  = "anvil-agents"
	agentDataVolumeCopyComponentLabel  = "app.kubernetes.io/component"
	agentDataVolumeCopyComponentValue  = "agent-data-volume-copy"
	agentDataVolumeCopyDefaultTimeout  = int32(1800)
	agentDataVolumeCopyDefaultImage    = "public.ecr.aws/docker/library/alpine:3.20"
	agentDataVolumeCopyStreamPort      = int32(9876)
	agentDataVolumeCopySourceRole      = "source"
	agentDataVolumeCopyDestinationRole = "destination"
)

// AgentDataVolumeCopyReconciler copies one AgentDataVolume claim onto a new claim,
// including cross-node local-path migrations via a tar stream between Jobs.
type AgentDataVolumeCopyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	CommonReconcilerOptions
}

// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentdatavolumecopies,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentdatavolumecopies/status,verbs=get;patch;update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentdatavolumecopies/finalizers,verbs=update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentdatavolumes,verbs=create;get;list;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentruns,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=create;delete;get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=create;delete;get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get;list;watch

func (r *AgentDataVolumeCopyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &controlv1alpha1.AgentDataVolumeCopy{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}
	if controlv1alpha1.AgentDataVolumeCopyIsTerminal(obj.Status.Phase) && obj.Status.ObservedGeneration == obj.Generation {
		return ctrl.Result{}, nil
	}

	original := obj.DeepCopy()
	status := obj.Status
	status.ObservedGeneration = obj.Generation
	now := metav1.Now()
	if status.Phase == "" || status.Phase == controlv1alpha1.AgentDataVolumeCopyPhasePending {
		status.Phase = controlv1alpha1.AgentDataVolumeCopyPhasePending
	}

	if err := validateAgentDataVolumeCopySpec(obj); err != nil {
		return r.failCopy(ctx, original, obj, &status, now, "InvalidSpec", err.Error())
	}

	source := &controlv1alpha1.AgentDataVolume{}
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: strings.TrimSpace(obj.Spec.SourceRef.Name)}, source); err != nil {
		if apierrors.IsNotFound(err) {
			return r.failCopy(ctx, original, obj, &status, now, "SourceNotFound", fmt.Sprintf("source AgentDataVolume %s/%s was not found", obj.Namespace, obj.Spec.SourceRef.Name))
		}
		return ctrl.Result{}, err
	}
	if source.Status.Phase != controlv1alpha1.AgentDataVolumePhaseReady {
		status.Phase = controlv1alpha1.AgentDataVolumeCopyPhasePending
		message := fmt.Sprintf("Waiting for source AgentDataVolume %s/%s to become Ready (phase=%s).", source.Namespace, source.Name, source.Status.Phase)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeCopyReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "SourceNotReady",
			Message:            message,
		})
		obj.Status = status
		if err := r.patchCopyStatus(ctx, original, obj); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	sourceClaimName := agentDataVolumeClaimName(source)
	if source.Status.ClaimRef != nil && strings.TrimSpace(source.Status.ClaimRef.Name) != "" {
		sourceClaimName = strings.TrimSpace(source.Status.ClaimRef.Name)
	}
	sourceClaimUID := strings.TrimSpace(source.Status.ClaimUID)
	if sourceClaimUID == "" {
		return r.failCopy(ctx, original, obj, &status, now, "SourceClaimIdentityMissing", "source AgentDataVolume has no claim UID")
	}
	sourceNode, err := persistentVolumeNodeName(ctx, r.reader(), source.Status.VolumeName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if strings.TrimSpace(sourceNode) == "" {
		return r.failCopy(ctx, original, obj, &status, now, "SourceNodeUnknown", "source PersistentVolume has no kubernetes.io/hostname affinity")
	}

	status.SourceRef = &controlv1alpha1.NamespacedObjectReference{Name: source.Name, Namespace: source.Namespace}
	status.SourceUID = string(source.UID)
	status.SourceClaimRef = &controlv1alpha1.NamespacedObjectReference{Name: sourceClaimName, Namespace: source.Namespace}
	status.SourceClaimUID = sourceClaimUID
	status.SourceNode = sourceNode

	activeRuns, err := listActiveAuthVolumeConsumers(ctx, r.reader(), obj.Namespace, source.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	// Destination consumers also block so we do not clobber an in-use home.
	destActive, err := listActiveAuthVolumeConsumers(ctx, r.reader(), obj.Namespace, strings.TrimSpace(obj.Spec.Destination.Name))
	if err != nil {
		return ctrl.Result{}, err
	}
	activeRuns = append(activeRuns, destActive...)
	status.ActiveConsumerRuns = uniqueStrings(activeRuns)
	if len(status.ActiveConsumerRuns) > 0 {
		status.Phase = controlv1alpha1.AgentDataVolumeCopyPhaseWaitingForIdle
		status.LastError = ""
		message := fmt.Sprintf("Waiting for active AgentRuns using source/destination volumes to finish: %s", strings.Join(status.ActiveConsumerRuns, ", "))
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeCopyReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "WaitingForIdle",
			Message:            message,
		})
		obj.Status = status
		if err := r.patchCopyStatus(ctx, original, obj); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if peerBusy, peerName, err := r.otherActiveCopyForVolumes(ctx, obj, source.Name, strings.TrimSpace(obj.Spec.Destination.Name)); err != nil {
		return ctrl.Result{}, err
	} else if peerBusy {
		status.Phase = controlv1alpha1.AgentDataVolumeCopyPhaseWaitingForIdle
		message := fmt.Sprintf("Waiting for AgentDataVolumeCopy %s/%s that already holds one of the volumes.", obj.Namespace, peerName)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeCopyReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "VolumeReserved",
			Message:            message,
		})
		obj.Status = status
		if err := r.patchCopyStatus(ctx, original, obj); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	status.Phase = controlv1alpha1.AgentDataVolumeCopyPhasePreparingDestination
	dest, created, err := r.ensureDestinationVolume(ctx, obj, source)
	if err != nil {
		return r.failCopy(ctx, original, obj, &status, now, "DestinationPrepareFailed", err.Error())
	}
	_ = created
	status.DestinationRef = &controlv1alpha1.NamespacedObjectReference{Name: dest.Name, Namespace: dest.Namespace}
	status.DestinationUID = string(dest.UID)

	destClaimName := agentDataVolumeClaimName(dest)
	if dest.Status.ClaimRef != nil && strings.TrimSpace(dest.Status.ClaimRef.Name) != "" {
		destClaimName = strings.TrimSpace(dest.Status.ClaimRef.Name)
	}
	status.DestinationClaimRef = &controlv1alpha1.NamespacedObjectReference{Name: destClaimName, Namespace: dest.Namespace}
	status.DestinationClaimUID = strings.TrimSpace(dest.Status.ClaimUID)

	if status.StartedAt == nil {
		status.StartedAt = &now
	}

	timeout := obj.Spec.TimeoutSeconds
	if timeout <= 0 {
		timeout = agentDataVolumeCopyDefaultTimeout
	}
	image := r.copyImage()

	svc, err := r.ensureStreamService(ctx, obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.ServiceRef = &controlv1alpha1.NamespacedObjectReference{Name: svc.Name, Namespace: svc.Namespace}

	sourceJob, err := r.ensureSourceJob(ctx, obj, sourceClaimName, sourceNode, image, timeout)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.SourceJobRef = &controlv1alpha1.NamespacedObjectReference{Name: sourceJob.Name, Namespace: sourceJob.Namespace}
	status.SourceJobUID = string(sourceJob.UID)

	// Destination Job is the first consumer of the destination PVC and pins local-path binding.
	destJob, err := r.ensureDestinationJob(ctx, obj, destClaimName, image, timeout, svc.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.DestinationJobRef = &controlv1alpha1.NamespacedObjectReference{Name: destJob.Name, Namespace: destJob.Namespace}
	status.DestinationJobUID = string(destJob.UID)

	if sourceJob.Status.Failed > 0 || destJob.Status.Failed > 0 {
		message := firstNonEmpty(jobFailureMessage(sourceJob), jobFailureMessage(destJob), "volume copy Job failed")
		return r.failCopy(ctx, original, obj, &status, now, "JobFailed", message)
	}

	if destJob.Status.Succeeded > 0 && sourceJob.Status.Succeeded > 0 {
		// Destination node may only be known after first consumer.
		if destPVNode, err := persistentVolumeNodeName(ctx, r.reader(), dest.Status.VolumeName); err == nil {
			status.DestinationNode = destPVNode
		}
		status.Phase = controlv1alpha1.AgentDataVolumeCopyPhaseSucceeded
		status.LastError = ""
		status.ActiveConsumerRuns = nil
		completed := metav1.Now()
		status.CompletedAt = &completed
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeCopyReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: completed,
			Reason:             "Succeeded",
			Message:            fmt.Sprintf("Copied AgentDataVolume %s/%s to %s/%s.", source.Namespace, source.Name, dest.Namespace, dest.Name),
		})
		obj.Status = status
		return ctrl.Result{}, r.patchCopyStatus(ctx, original, obj)
	}

	status.Phase = controlv1alpha1.AgentDataVolumeCopyPhaseStreaming
	status.LastError = ""
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentDataVolumeCopyReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             "Streaming",
		Message:            fmt.Sprintf("Streaming copy Jobs source=%s destination=%s.", sourceJob.Name, destJob.Name),
	})
	obj.Status = status
	if err := r.patchCopyStatus(ctx, original, obj); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func validateAgentDataVolumeCopySpec(obj *controlv1alpha1.AgentDataVolumeCopy) error {
	if strings.TrimSpace(obj.Spec.SourceRef.Name) == "" {
		return fmt.Errorf("spec.sourceRef.name is required")
	}
	if strings.TrimSpace(obj.Spec.Destination.Name) == "" {
		return fmt.Errorf("spec.destination.name is required")
	}
	if strings.TrimSpace(obj.Spec.SourceRef.Name) == strings.TrimSpace(obj.Spec.Destination.Name) {
		return fmt.Errorf("source and destination names must differ")
	}
	if len(obj.Spec.Destination.NodeSelector) == 0 {
		return fmt.Errorf("spec.destination.nodeSelector is required")
	}
	method := obj.Spec.Method
	if method == "" {
		method = controlv1alpha1.AgentDataVolumeCopyMethodStream
	}
	if method != controlv1alpha1.AgentDataVolumeCopyMethodStream {
		return fmt.Errorf("unsupported method %q", method)
	}
	return nil
}

func (r *AgentDataVolumeCopyReconciler) ensureDestinationVolume(ctx context.Context, obj *controlv1alpha1.AgentDataVolumeCopy, source *controlv1alpha1.AgentDataVolume) (*controlv1alpha1.AgentDataVolume, bool, error) {
	name := strings.TrimSpace(obj.Spec.Destination.Name)
	existing := &controlv1alpha1.AgentDataVolume{}
	err := r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: name}, existing)
	if err == nil {
		return existing, false, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, false, err
	}

	size := source.Spec.Size
	if obj.Spec.Destination.Size != nil {
		size = *obj.Spec.Destination.Size
	} else if size.IsZero() && strings.TrimSpace(source.Status.Capacity) != "" {
		if parsed, parseErr := resource.ParseQuantity(strings.TrimSpace(source.Status.Capacity)); parseErr == nil {
			size = parsed
		}
	}
	if size.IsZero() {
		size = resource.MustParse("10Gi")
	}
	storageClass := firstNonEmpty(strings.TrimSpace(obj.Spec.Destination.StorageClassName), strings.TrimSpace(source.Spec.StorageClassName), strings.TrimSpace(source.Status.StorageClassName))
	backend := obj.Spec.Destination.Backend
	if backend == "" {
		backend = source.Spec.Backend
	}
	notes := strings.TrimSpace(obj.Spec.Destination.Notes)
	if notes == "" {
		notes = fmt.Sprintf("Created by AgentDataVolumeCopy %s from source %s.", obj.Name, source.Name)
	}
	var applicationRef *controlv1alpha1.ApplicationReferenceSpec
	if source.Spec.ApplicationRef != nil && strings.TrimSpace(source.Spec.ApplicationRef.Name) != "" {
		applicationRef = &controlv1alpha1.ApplicationReferenceSpec{Name: strings.TrimSpace(source.Spec.ApplicationRef.Name)}
	}
	dest := &controlv1alpha1.AgentDataVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: obj.Namespace,
			Labels: map[string]string{
				agentDataVolumeCopyLabel:          sanitizeLabelValue(obj.Name),
				agentDataVolumeCopyManagedByLabel: agentDataVolumeCopyManagedByValue,
				agentDataVolumeCopyComponentLabel: agentDataVolumeCopyComponentValue,
			},
			Annotations: map[string]string{
				"control.anvil.hazyforge.io/copied-from": source.Name,
			},
		},
		Spec: controlv1alpha1.AgentDataVolumeSpec{
			ApplicationRef:   applicationRef,
			AgentName:        source.Spec.AgentName,
			Backend:          backend,
			MountPath:        firstNonEmpty(strings.TrimSpace(source.Spec.MountPath), strings.TrimSpace(source.Status.MountPath), "/opt/anvil"),
			SubPath:          source.Spec.SubPath,
			StorageClassName: storageClass,
			Size:             size,
			AccessModes:      append([]corev1.PersistentVolumeAccessMode(nil), source.Spec.AccessModes...),
			NodeSelector:     cloneStringMap(obj.Spec.Destination.NodeSelector),
			ExtraEnv:         append([]controlv1alpha1.AgentDataVolumePathEnvVar(nil), source.Spec.ExtraEnv...),
			Notes:            notes,
		},
	}
	if err := r.Create(ctx, dest); err != nil {
		if apierrors.IsAlreadyExists(err) {
			if getErr := r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: name}, existing); getErr != nil {
				return nil, false, getErr
			}
			return existing, false, nil
		}
		return nil, false, fmt.Errorf("create destination AgentDataVolume: %w", err)
	}
	return dest, true, nil
}

func (r *AgentDataVolumeCopyReconciler) ensureStreamService(ctx context.Context, obj *controlv1alpha1.AgentDataVolumeCopy) (*corev1.Service, error) {
	name := agentRunChildName("vcopy-svc", obj.Name)
	existing := &corev1.Service{}
	err := r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: name}, existing)
	if err == nil {
		return existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: obj.Namespace,
			Labels: map[string]string{
				agentDataVolumeCopyLabel:          sanitizeLabelValue(obj.Name),
				agentDataVolumeCopyManagedByLabel: agentDataVolumeCopyManagedByValue,
				agentDataVolumeCopyComponentLabel: agentDataVolumeCopyComponentValue,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				agentDataVolumeCopyLabel:     sanitizeLabelValue(obj.Name),
				agentDataVolumeCopyRoleLabel: agentDataVolumeCopySourceRole,
			},
			Ports: []corev1.ServicePort{{
				Name:       "tar-stream",
				Port:       agentDataVolumeCopyStreamPort,
				TargetPort: intstr.FromInt32(agentDataVolumeCopyStreamPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	if err := controllerutil.SetControllerReference(obj, svc, r.Scheme); err != nil {
		return nil, fmt.Errorf("set Service owner: %w", err)
	}
	if err := r.Create(ctx, svc); err != nil {
		if apierrors.IsAlreadyExists(err) {
			if getErr := r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: name}, existing); getErr != nil {
				return nil, getErr
			}
			return existing, nil
		}
		return nil, fmt.Errorf("create stream Service: %w", err)
	}
	return svc, nil
}

func (r *AgentDataVolumeCopyReconciler) ensureSourceJob(ctx context.Context, obj *controlv1alpha1.AgentDataVolumeCopy, claimName, sourceNode, image string, timeout int32) (*batchv1.Job, error) {
	jobName := agentRunChildName("vcopy-src", obj.Name)
	existing := &batchv1.Job{}
	err := r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: jobName}, existing)
	if err == nil {
		return existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	script := `
set -euo pipefail
apk add --no-cache netcat-openbsd tar gzip >/dev/null
echo "source stream ready on :9876"
# Serve one tar stream then exit when the client disconnects.
tar -C /source -czf - . | nc -l -p 9876 -N
echo "source stream finished"
`
	return r.createCopyJob(ctx, obj, jobName, agentDataVolumeCopySourceRole, claimName, "/source", image, timeout, script, map[string]string{
		"kubernetes.io/hostname": sourceNode,
	}, false)
}

func (r *AgentDataVolumeCopyReconciler) ensureDestinationJob(ctx context.Context, obj *controlv1alpha1.AgentDataVolumeCopy, claimName, image string, timeout int32, serviceName string) (*batchv1.Job, error) {
	jobName := agentRunChildName("vcopy-dst", obj.Name)
	existing := &batchv1.Job{}
	err := r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: jobName}, existing)
	if err == nil {
		return existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	allowNonEmpty := "0"
	if obj.Spec.AllowNonEmptyDestination {
		allowNonEmpty = "1"
	}
	verify := "1"
	if !controlv1alpha1.AgentDataVolumeCopyVerifyEnabled(obj.Spec) {
		verify = "0"
	}
	script := fmt.Sprintf(`
set -euo pipefail
apk add --no-cache netcat-openbsd tar gzip findutils coreutils >/dev/null
SRC_HOST=%q
ALLOW_NONEMPTY=%q
VERIFY=%q
mkdir -p /dest
# Wait for source stream.
for i in $(seq 1 120); do
  if nc -z -w 2 "$SRC_HOST" 9876 2>/dev/null; then
    break
  fi
  sleep 2
done
if ! nc -z -w 2 "$SRC_HOST" 9876 2>/dev/null; then
  echo "source stream never became ready at ${SRC_HOST}:9876" >&2
  exit 1
fi
# Refuse clobber unless allowed.
entries="$(find /dest -mindepth 1 -maxdepth 1 ! -name lost+found | wc -l | tr -d ' ')"
if [ "$entries" != "0" ] && [ "$ALLOW_NONEMPTY" != "1" ]; then
  echo "destination is not empty; set allowNonEmptyDestination=true to overwrite" >&2
  find /dest -mindepth 1 -maxdepth 1 | head
  exit 2
fi
if [ "$ALLOW_NONEMPTY" = "1" ]; then
  find /dest -mindepth 1 -maxdepth 1 ! -name lost+found -exec rm -rf {} +
fi
echo "receiving stream from ${SRC_HOST}:9876"
nc -w 30 "$SRC_HOST" 9876 | tar -C /dest -xzf -
sync
if [ "$VERIFY" = "1" ]; then
  count="$(find /dest -type f | wc -l | tr -d ' ')"
  echo "destination file count=${count}"
fi
echo "destination copy finished"
`, serviceName, allowNonEmpty, verify)

	return r.createCopyJob(ctx, obj, jobName, agentDataVolumeCopyDestinationRole, claimName, "/dest", image, timeout, script, cloneStringMap(obj.Spec.Destination.NodeSelector), true)
}

func (r *AgentDataVolumeCopyReconciler) createCopyJob(
	ctx context.Context,
	obj *controlv1alpha1.AgentDataVolumeCopy,
	jobName, role, claimName, mountPath, image string,
	timeout int32,
	script string,
	nodeSelector map[string]string,
	readWrite bool,
) (*batchv1.Job, error) {
	backoff := int32(0)
	ttl := int32(1800)
	activeDeadline := int64(timeout)
	automount := false
	runAsNonRoot := false // alpine needs root for apk; volume fsGroup handles ownership
	allowPrivilegeEscalation := false
	readOnly := !readWrite
	labels := map[string]string{
		agentDataVolumeCopyLabel:          sanitizeLabelValue(obj.Name),
		agentDataVolumeCopyRoleLabel:      role,
		agentDataVolumeCopyManagedByLabel: agentDataVolumeCopyManagedByValue,
		agentDataVolumeCopyComponentLabel: agentDataVolumeCopyComponentValue,
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: obj.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &activeDeadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: &automount,
					NodeSelector:                 nodeSelector,
					SecurityContext: &corev1.PodSecurityContext{
						FSGroup: int64Ptr(0),
					},
					Containers: []corev1.Container{{
						Name:            "copy",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command:         []string{"/bin/sh", "-lc", script},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "volume",
							MountPath: mountPath,
							ReadOnly:  readOnly,
						}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: &allowPrivilegeEscalation,
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}, Add: []corev1.Capability{"NET_BIND_SERVICE"}},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1"),
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "volume",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: claimName,
								ReadOnly:  readOnly,
							},
						},
					}},
				},
			},
		},
	}
	_ = runAsNonRoot
	if err := controllerutil.SetControllerReference(obj, job, r.Scheme); err != nil {
		return nil, fmt.Errorf("set Job owner: %w", err)
	}
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			existing := &batchv1.Job{}
			if getErr := r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: jobName}, existing); getErr != nil {
				return nil, getErr
			}
			return existing, nil
		}
		return nil, fmt.Errorf("create copy Job %s: %w", jobName, err)
	}
	return job, nil
}

func (r *AgentDataVolumeCopyReconciler) copyImage() string {
	if r.Options != nil && strings.TrimSpace(r.Options.CodexRunnerImage) != "" {
		// Prefer a known-in-cluster image only when alpine pull is blocked.
		// Stream jobs intentionally use alpine for tar/nc tooling.
	}
	return agentDataVolumeCopyDefaultImage
}

func (r *AgentDataVolumeCopyReconciler) otherActiveCopyForVolumes(ctx context.Context, obj *controlv1alpha1.AgentDataVolumeCopy, sourceName, destName string) (bool, string, error) {
	list := &controlv1alpha1.AgentDataVolumeCopyList{}
	if err := r.reader().List(ctx, list, client.InNamespace(obj.Namespace)); err != nil {
		return false, "", err
	}
	for i := range list.Items {
		copyObj := &list.Items[i]
		if copyObj.Name == obj.Name {
			continue
		}
		if controlv1alpha1.AgentDataVolumeCopyIsTerminal(copyObj.Status.Phase) {
			continue
		}
		src := strings.TrimSpace(copyObj.Spec.SourceRef.Name)
		dst := strings.TrimSpace(copyObj.Spec.Destination.Name)
		if src == sourceName || src == destName || dst == sourceName || dst == destName {
			return true, copyObj.Name, nil
		}
	}
	return false, "", nil
}

func (r *AgentDataVolumeCopyReconciler) failCopy(ctx context.Context, original, obj *controlv1alpha1.AgentDataVolumeCopy, status *controlv1alpha1.AgentDataVolumeCopyStatus, now metav1.Time, reason, message string) (ctrl.Result, error) {
	status.Phase = controlv1alpha1.AgentDataVolumeCopyPhaseFailed
	status.LastError = message
	completed := now
	status.CompletedAt = &completed
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentDataVolumeCopyReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	obj.Status = *status
	if err := r.patchCopyStatus(ctx, original, obj); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AgentDataVolumeCopyReconciler) patchCopyStatus(ctx context.Context, original, obj *controlv1alpha1.AgentDataVolumeCopy) error {
	if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("patch AgentDataVolumeCopy status: %w", err)
	}
	return nil
}

func (r *AgentDataVolumeCopyReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

func persistentVolumeNodeName(ctx context.Context, reader client.Reader, volumeName string) (string, error) {
	volumeName = strings.TrimSpace(volumeName)
	if volumeName == "" {
		return "", nil
	}
	pv := &corev1.PersistentVolume{}
	if err := reader.Get(ctx, client.ObjectKey{Name: volumeName}, pv); err != nil {
		return "", err
	}
	if pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
		return "", nil
	}
	for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			if expr.Key == "kubernetes.io/hostname" && len(expr.Values) > 0 {
				return expr.Values[0], nil
			}
		}
		for _, expr := range term.MatchFields {
			if expr.Key == "metadata.name" && len(expr.Values) > 0 {
				return expr.Values[0], nil
			}
		}
	}
	return "", nil
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// AgentDataVolumeCopyBlocksDataVolume reports whether a non-terminal copy
// currently reserves the named AgentDataVolume as source or destination.
func AgentDataVolumeCopyBlocksDataVolume(ctx context.Context, reader client.Reader, namespace, volumeName string) (bool, string, error) {
	list := &controlv1alpha1.AgentDataVolumeCopyList{}
	if err := reader.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return false, "", err
	}
	for i := range list.Items {
		copyObj := &list.Items[i]
		if controlv1alpha1.AgentDataVolumeCopyIsTerminal(copyObj.Status.Phase) {
			continue
		}
		if strings.TrimSpace(copyObj.Spec.SourceRef.Name) == volumeName || strings.TrimSpace(copyObj.Spec.Destination.Name) == volumeName {
			return true, copyObj.Name, nil
		}
	}
	return false, "", nil
}

func (r *AgentDataVolumeCopyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&controlv1alpha1.AgentDataVolumeCopy{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.Service{}).
		Complete(r)
}

var _ = types.NamespacedName{}
