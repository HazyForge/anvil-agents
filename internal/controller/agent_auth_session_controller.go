package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	agentAuthSessionReady          = "Ready"
	agentAuthSessionLabel          = "control.anvil.hazyforge.io/agent-auth-session"
	agentAuthSessionActionLabel    = "control.anvil.hazyforge.io/agent-auth-action"
	agentAuthSessionProviderLabel  = "control.anvil.hazyforge.io/agent-auth-provider"
	agentAuthSessionVolumeLabel    = "control.anvil.hazyforge.io/agent-auth-volume"
	agentAuthSessionManagedByLabel = "app.kubernetes.io/managed-by"
	agentAuthSessionManagedByValue = "anvil-agents"
	agentAuthSessionComponentLabel = "app.kubernetes.io/component"
	agentAuthSessionComponentValue = "agent-auth-session"
	agentAuthStagingOwnerLabel     = "control.anvil.hazyforge.io/agent-auth-staging-for"
	agentAuthCodexAuthEnv          = "CODEX_AUTH_JSON"
	agentAuthCodexSeedEnv          = "ANVIL_CODEX_AUTH_SEED_ID"
	agentAuthCodexSeedSecretKey    = "CODEX_AUTH_SEED_ID"
	agentAuthCodexSeedFileName     = ".anvil-codex-auth-seed-id"
	agentAuthCodexLogoutFileName   = ".anvil-codex-auth-logged-out"
	agentAuthCodexAuthRelPath      = "auth.json"
	agentAuthGrokAuthEnv           = "GROK_AUTH_JSON"
	agentAuthGrokSeedEnv           = "ANVIL_GROK_AUTH_SEED_ID"
	agentAuthGrokSeedSecretKey     = "GROK_AUTH_SEED_ID"
	agentAuthGrokSeedFileName      = ".anvil-grok-auth-seed-id"
	agentAuthGrokLogoutFileName    = ".anvil-grok-auth-logged-out"
	// Relative to GROK_BUILD_HOME / mountPath: HOME/.grok/auth.json with HOME=mount.
	agentAuthGrokAuthRelPath = ".grok/auth.json"
	// OpenClaw stages a version=1 auth profile store (not openclaw.json or a DB).
	agentAuthOpenClawAuthEnv        = "OPENCLAW_AUTH_PROFILES_JSON"
	agentAuthOpenClawSeedEnv        = "OPENCLAW_AUTH_SEED_ID"
	agentAuthOpenClawSeedSecretKey  = "OPENCLAW_AUTH_SEED_ID"
	agentAuthOpenClawSeedFileName   = ".anvil-openclaw-auth-seed-id"
	agentAuthOpenClawLogoutFileName = ".anvil-openclaw-auth-logged-out"
	agentAuthOpenClawAuthRelPath    = "state" // ownership root; agentDir resolved at runtime
	agentAuthDefaultTimeoutSeconds  = int32(300)
	agentAuthContainerName          = "auth"
	agentAuthCodexUID               = int64(10001)
	agentAuthOpenClawUID            = int64(1000)
)

// agentAuthProviderLayout describes durable-home paths and secret keys per provider.
type agentAuthProviderLayout struct {
	Provider            controlv1alpha1.AgentAuthSessionProvider
	AuthEnvKey          string
	SeedEnvKey          string
	SeedSecretKey       string
	DefaultBootstrapKey string
	AuthRelPath         string
	SeedFileName        string
	LogoutFileName      string
	ComponentLabel      string
}

func agentAuthLayout(provider controlv1alpha1.AgentAuthSessionProvider) (agentAuthProviderLayout, error) {
	switch provider {
	case controlv1alpha1.AgentAuthSessionProviderCodex:
		return agentAuthProviderLayout{
			Provider:            controlv1alpha1.AgentAuthSessionProviderCodex,
			AuthEnvKey:          agentAuthCodexAuthEnv,
			SeedEnvKey:          agentAuthCodexSeedEnv,
			SeedSecretKey:       agentAuthCodexSeedSecretKey,
			DefaultBootstrapKey: agentAuthCodexAuthEnv,
			AuthRelPath:         agentAuthCodexAuthRelPath,
			SeedFileName:        agentAuthCodexSeedFileName,
			LogoutFileName:      agentAuthCodexLogoutFileName,
			ComponentLabel:      "codex-auth-seed",
		}, nil
	case controlv1alpha1.AgentAuthSessionProviderGrokBuild:
		return agentAuthProviderLayout{
			Provider:            controlv1alpha1.AgentAuthSessionProviderGrokBuild,
			AuthEnvKey:          agentAuthGrokAuthEnv,
			SeedEnvKey:          agentAuthGrokSeedEnv,
			SeedSecretKey:       agentAuthGrokSeedSecretKey,
			DefaultBootstrapKey: agentAuthGrokAuthEnv,
			AuthRelPath:         agentAuthGrokAuthRelPath,
			SeedFileName:        agentAuthGrokSeedFileName,
			LogoutFileName:      agentAuthGrokLogoutFileName,
			ComponentLabel:      "grok-auth-seed",
		}, nil
	case controlv1alpha1.AgentAuthSessionProviderOpenClaw:
		return agentAuthProviderLayout{
			Provider:            controlv1alpha1.AgentAuthSessionProviderOpenClaw,
			AuthEnvKey:          agentAuthOpenClawAuthEnv,
			SeedEnvKey:          agentAuthOpenClawSeedEnv,
			SeedSecretKey:       agentAuthOpenClawSeedSecretKey,
			DefaultBootstrapKey: agentAuthOpenClawAuthEnv,
			AuthRelPath:         agentAuthOpenClawAuthRelPath,
			SeedFileName:        agentAuthOpenClawSeedFileName,
			LogoutFileName:      agentAuthOpenClawLogoutFileName,
			ComponentLabel:      "openclaw-auth-seed",
		}, nil
	default:
		return agentAuthProviderLayout{}, fmt.Errorf("unsupported provider %q", provider)
	}
}

// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentauthsessions,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentauthsessions/status,verbs=get;patch;update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentauthsessions/finalizers,verbs=update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentdatavolumes,verbs=get;list;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentruns,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
type AgentAuthSessionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	CommonReconcilerOptions
}

func (r *AgentAuthSessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &controlv1alpha1.AgentAuthSession{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}
	if controlv1alpha1.AgentAuthSessionIsTerminal(obj.Status.Phase) && obj.Status.ObservedGeneration == obj.Generation {
		return ctrl.Result{}, nil
	}

	original := obj.DeepCopy()
	status := obj.Status
	status.ObservedGeneration = obj.Generation
	now := metav1.Now()

	if err := validateAgentAuthSessionSpec(obj); err != nil {
		return r.failSession(ctx, original, obj, &status, now, "InvalidSpec", err.Error())
	}

	volume, claimName, claimUID, mountPath, phase, reason, message, err := r.resolveAuthSessionVolume(ctx, obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	if phase == controlv1alpha1.AgentAuthSessionPhaseFailed {
		return r.failSession(ctx, original, obj, &status, now, reason, message)
	}
	if phase == controlv1alpha1.AgentAuthSessionPhasePending {
		status.Phase = controlv1alpha1.AgentAuthSessionPhasePending
		status.LastError = message
		status.DataVolumeRef = &controlv1alpha1.NamespacedObjectReference{Name: volume.Name, Namespace: volume.Namespace}
		status.DataVolumeUID = string(volume.UID)
		status.ClaimRef = &controlv1alpha1.NamespacedObjectReference{Name: claimName, Namespace: volume.Namespace}
		status.ClaimUID = claimUID
		status.MountPath = mountPath
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentAuthSessionReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             reason,
			Message:            message,
		})
		obj.Status = status
		if err := r.patchAgentAuthSessionStatus(ctx, original, obj); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	status.DataVolumeRef = &controlv1alpha1.NamespacedObjectReference{Name: volume.Name, Namespace: volume.Namespace}
	status.DataVolumeUID = string(volume.UID)
	status.ClaimRef = &controlv1alpha1.NamespacedObjectReference{Name: claimName, Namespace: volume.Namespace}
	status.ClaimUID = claimUID
	status.MountPath = mountPath
	status.SeedID = strings.TrimSpace(obj.Spec.SeedID)

	if obj.Spec.Action == controlv1alpha1.AgentAuthSessionActionReauth {
		if err := r.validateStagingSecret(ctx, obj); err != nil {
			return r.failSession(ctx, original, obj, &status, now, "InvalidStagingSecret", err.Error())
		}
		if obj.Spec.BootstrapSecretRef != nil && strings.TrimSpace(obj.Spec.BootstrapSecretRef.Name) != "" {
			if err := r.validateBootstrapSecretWritable(ctx, obj.Namespace, strings.TrimSpace(obj.Spec.BootstrapSecretRef.Name)); err != nil {
				return r.failSession(ctx, original, obj, &status, now, "BootstrapSecretNotWritable", err.Error())
			}
		}
	}

	activeRuns, err := listActiveAuthVolumeConsumers(ctx, r.reader(), obj.Namespace, volume.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.ActiveConsumerRuns = activeRuns
	if len(activeRuns) > 0 {
		status.Phase = controlv1alpha1.AgentAuthSessionPhaseWaitingForIdle
		status.LastError = ""
		message := fmt.Sprintf("Waiting for active AgentRuns using AgentDataVolume %s/%s to finish: %s", volume.Namespace, volume.Name, strings.Join(activeRuns, ", "))
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentAuthSessionReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "WaitingForIdle",
			Message:            message,
		})
		obj.Status = status
		if err := r.patchAgentAuthSessionStatus(ctx, original, obj); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	peerBusy, peerName, err := r.otherActiveSessionForVolume(ctx, obj, volume.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if peerBusy {
		status.Phase = controlv1alpha1.AgentAuthSessionPhaseWaitingForIdle
		message := fmt.Sprintf("Waiting for AgentAuthSession %s/%s that already holds AgentDataVolume %s.", obj.Namespace, peerName, volume.Name)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentAuthSessionReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "VolumeReserved",
			Message:            message,
		})
		obj.Status = status
		if err := r.patchAgentAuthSessionStatus(ctx, original, obj); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	job, err := r.ensureAuthSessionJob(ctx, obj, volume, claimName, mountPath)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.JobRef = &controlv1alpha1.NamespacedObjectReference{Name: job.Name, Namespace: job.Namespace}
	status.JobUID = string(job.UID)
	if status.StartedAt == nil {
		status.StartedAt = &now
	}

	if job.Status.Succeeded > 0 {
		if obj.Spec.Action == controlv1alpha1.AgentAuthSessionActionReauth {
			if err := r.applyBootstrapSecret(ctx, obj); err != nil {
				return r.failSession(ctx, original, obj, &status, now, "BootstrapSecretUpdateFailed", err.Error())
			}
			if err := r.deleteOwnedStagingSecret(ctx, obj); err != nil {
				return ctrl.Result{}, err
			}
		}
		status.Phase = controlv1alpha1.AgentAuthSessionPhaseSucceeded
		status.LastError = ""
		status.ActiveConsumerRuns = nil
		completed := metav1.Now()
		status.CompletedAt = &completed
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentAuthSessionReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: completed,
			Reason:             "Succeeded",
			Message:            fmt.Sprintf("Auth session %s completed for AgentDataVolume %s/%s.", obj.Spec.Action, volume.Namespace, volume.Name),
		})
		obj.Status = status
		return ctrl.Result{}, r.patchAgentAuthSessionStatus(ctx, original, obj)
	}

	if job.Status.Failed > 0 {
		message := firstNonEmpty(jobFailureMessage(job), "auth maintenance Job failed")
		return r.failSession(ctx, original, obj, &status, now, "JobFailed", message)
	}

	status.Phase = controlv1alpha1.AgentAuthSessionPhaseRunning
	status.LastError = ""
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentAuthSessionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             "Running",
		Message:            fmt.Sprintf("Auth maintenance Job %s/%s is running.", job.Namespace, job.Name),
	})
	obj.Status = status
	if err := r.patchAgentAuthSessionStatus(ctx, original, obj); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *AgentAuthSessionReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

func validateAgentAuthSessionSpec(obj *controlv1alpha1.AgentAuthSession) error {
	if obj == nil {
		return fmt.Errorf("session is nil")
	}
	if _, err := agentAuthLayout(obj.Spec.Provider); err != nil {
		return err
	}
	switch obj.Spec.Action {
	case controlv1alpha1.AgentAuthSessionActionReauth, controlv1alpha1.AgentAuthSessionActionLogout, controlv1alpha1.AgentAuthSessionActionVerify:
	default:
		return fmt.Errorf("unsupported action %q", obj.Spec.Action)
	}
	if strings.TrimSpace(obj.Spec.DataVolumeRef.Name) == "" {
		return fmt.Errorf("spec.dataVolumeRef.name is required")
	}
	agentID := strings.TrimSpace(obj.Spec.AgentID)
	authMode := strings.TrimSpace(string(obj.Spec.AuthMode))
	modelProvider := strings.TrimSpace(obj.Spec.ModelProvider)
	if obj.Spec.Provider == controlv1alpha1.AgentAuthSessionProviderOpenClaw {
		if agentID == "" {
			return fmt.Errorf("spec.agentID is required for provider openClaw")
		}
		if err := validateOpenClawAgentID(agentID); err != nil {
			return err
		}
		if authMode == "" {
			return fmt.Errorf("spec.authMode is required for provider openClaw")
		}
		if authMode != string(controlv1alpha1.AgentRunProviderAuthModeOAuth) && authMode != string(controlv1alpha1.AgentRunProviderAuthModeAPIKey) {
			return fmt.Errorf("spec.authMode must be oauth or apiKey")
		}
		if modelProvider == "" {
			return fmt.Errorf("spec.modelProvider is required for provider openClaw")
		}
		if err := validateOpenClawAgentID(modelProvider); err != nil {
			return fmt.Errorf("spec.modelProvider must be a DNS label")
		}
	} else if agentID != "" {
		return fmt.Errorf("spec.agentID is forbidden unless provider is openClaw")
	} else if modelProvider != "" {
		return fmt.Errorf("spec.modelProvider is forbidden unless provider is openClaw")
	}
	if authMode != "" && authMode != string(controlv1alpha1.AgentRunProviderAuthModeOAuth) && authMode != string(controlv1alpha1.AgentRunProviderAuthModeAPIKey) {
		return fmt.Errorf("spec.authMode must be oauth or apiKey")
	}
	switch obj.Spec.Action {
	case controlv1alpha1.AgentAuthSessionActionReauth:
		if obj.Spec.StagingSecretRef == nil || strings.TrimSpace(obj.Spec.StagingSecretRef.Name) == "" {
			return fmt.Errorf("spec.stagingSecretRef.name is required for reauth")
		}
		if strings.TrimSpace(obj.Spec.SeedID) == "" {
			return fmt.Errorf("spec.seedID is required for reauth")
		}
	case controlv1alpha1.AgentAuthSessionActionLogout, controlv1alpha1.AgentAuthSessionActionVerify:
		if obj.Spec.StagingSecretRef != nil && strings.TrimSpace(obj.Spec.StagingSecretRef.Name) != "" {
			return fmt.Errorf("spec.stagingSecretRef is forbidden for %s", obj.Spec.Action)
		}
		if strings.TrimSpace(obj.Spec.SeedID) != "" {
			return fmt.Errorf("spec.seedID is forbidden for %s", obj.Spec.Action)
		}
		if obj.Spec.BootstrapSecretRef != nil {
			return fmt.Errorf("spec.bootstrapSecretRef is forbidden for %s", obj.Spec.Action)
		}
		if strings.TrimSpace(obj.Spec.BootstrapSecretKey) != "" {
			return fmt.Errorf("spec.bootstrapSecretKey is forbidden for %s", obj.Spec.Action)
		}
	}
	return nil
}

func validateOpenClawAgentID(id string) error {
	if id == "" || len(id) > 63 {
		return fmt.Errorf("spec.agentID must be a DNS label (1-63 chars)")
	}
	for i, r := range id {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if i == 0 || i == len(id)-1 {
			if !isAlphaNum {
				return fmt.Errorf("spec.agentID must be a DNS label")
			}
			continue
		}
		if !isAlphaNum && r != '-' {
			return fmt.Errorf("spec.agentID must be a DNS label")
		}
	}
	return nil
}

func (r *AgentAuthSessionReconciler) resolveAuthSessionVolume(ctx context.Context, obj *controlv1alpha1.AgentAuthSession) (*controlv1alpha1.AgentDataVolume, string, string, string, controlv1alpha1.AgentAuthSessionPhase, string, string, error) {
	volumeName := strings.TrimSpace(obj.Spec.DataVolumeRef.Name)
	volume := &controlv1alpha1.AgentDataVolume{}
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: volumeName}, volume); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, "", "", "", controlv1alpha1.AgentAuthSessionPhaseFailed, "DataVolumeNotFound", fmt.Sprintf("AgentDataVolume %s/%s was not found.", obj.Namespace, volumeName), nil
		}
		return nil, "", "", "", "", "", "", err
	}
	if reason, message := agentAuthVolumeBackendValidation(volume, obj.Spec.Provider); reason != "" {
		return volume, "", "", "", controlv1alpha1.AgentAuthSessionPhaseFailed, reason, message, nil
	}
	if volume.Status.ObservedGeneration == 0 || volume.Status.ObservedGeneration != volume.Generation {
		return volume, "", "", "", controlv1alpha1.AgentAuthSessionPhasePending, "DataVolumeStatusStale", fmt.Sprintf("Waiting for AgentDataVolume %s/%s status to observe generation %d.", volume.Namespace, volume.Name, volume.Generation), nil
	}
	if volume.Status.Phase == controlv1alpha1.AgentDataVolumePhaseBlocked {
		return volume, "", "", "", controlv1alpha1.AgentAuthSessionPhaseFailed, "DataVolumeBlocked", firstNonEmpty(volume.Status.LastError, "AgentDataVolume is blocked"), nil
	}
	if volume.Status.Phase != controlv1alpha1.AgentDataVolumePhaseReady && volume.Status.Phase != "" {
		return volume, "", "", "", controlv1alpha1.AgentAuthSessionPhasePending, "DataVolumeNotReady", fmt.Sprintf("Waiting for AgentDataVolume %s/%s to become Ready.", volume.Namespace, volume.Name), nil
	}
	claimName := agentDataVolumeClaimName(volume)
	if volume.Status.ClaimRef != nil && strings.TrimSpace(volume.Status.ClaimRef.Name) != "" {
		claimName = strings.TrimSpace(volume.Status.ClaimRef.Name)
	}
	claimUID := strings.TrimSpace(volume.Status.ClaimUID)
	if claimName == "" || claimUID == "" {
		return volume, claimName, claimUID, "", controlv1alpha1.AgentAuthSessionPhasePending, "DataVolumeClaimIdentityPending", fmt.Sprintf("Waiting for AgentDataVolume %s/%s to record its claim identity.", volume.Namespace, volume.Name), nil
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: volume.Namespace, Name: claimName}, pvc); err != nil {
		if apierrors.IsNotFound(err) {
			return volume, claimName, claimUID, "", controlv1alpha1.AgentAuthSessionPhasePending, "DataVolumeClaimMissing", fmt.Sprintf("Waiting for PersistentVolumeClaim %s/%s.", volume.Namespace, claimName), nil
		}
		return nil, "", "", "", "", "", "", err
	}
	owner := metav1.GetControllerOf(pvc)
	if owner == nil || owner.APIVersion != controlv1alpha1.GroupVersion.String() || owner.Kind != "AgentDataVolume" || owner.Name != volume.Name || owner.UID != volume.UID {
		return volume, claimName, claimUID, "", controlv1alpha1.AgentAuthSessionPhaseFailed, "ForeignDataVolumeClaim", fmt.Sprintf("PersistentVolumeClaim %s/%s is not controlled by AgentDataVolume %s/%s.", volume.Namespace, claimName, volume.Namespace, volume.Name), nil
	}
	if string(pvc.UID) != claimUID {
		return volume, claimName, claimUID, "", controlv1alpha1.AgentAuthSessionPhaseFailed, "DataVolumeClaimReplaced", fmt.Sprintf("PersistentVolumeClaim %s/%s UID no longer matches AgentDataVolume status.", volume.Namespace, claimName), nil
	}
	mountPath := agentDataVolumeResolvedMountPath(volume)
	return volume, claimName, claimUID, mountPath, "", "", "", nil
}

func agentAuthVolumeBackendValidation(volume *controlv1alpha1.AgentDataVolume, provider controlv1alpha1.AgentAuthSessionProvider) (string, string) {
	if volume == nil {
		return "DataVolumeNotFound", "AgentDataVolume is nil."
	}
	expectedBackend := controlv1alpha1.AgentRunHarnessBackendKind(provider)
	if volume.Spec.Backend == "" {
		return "DataVolumeBackendRequired", fmt.Sprintf("AgentDataVolume %s/%s must set spec.backend=%q explicitly for auth maintenance; VolumeProfile does not provide backend identity.", volume.Namespace, volume.Name, expectedBackend)
	}
	if volume.Spec.Backend != expectedBackend {
		return "ProviderVolumeMismatch", fmt.Sprintf("AgentDataVolume %s/%s backend %q does not match auth provider %q.", volume.Namespace, volume.Name, volume.Spec.Backend, provider)
	}
	return "", ""
}

func (r *AgentAuthSessionReconciler) validateStagingSecret(ctx context.Context, obj *controlv1alpha1.AgentAuthSession) error {
	layout, err := agentAuthLayout(obj.Spec.Provider)
	if err != nil {
		return err
	}
	secret := &corev1.Secret{}
	name := strings.TrimSpace(obj.Spec.StagingSecretRef.Name)
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: name}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("staging Secret %s/%s was not found", obj.Namespace, name)
		}
		return err
	}
	raw := secret.Data[layout.AuthEnvKey]
	if len(raw) == 0 {
		return fmt.Errorf("staging Secret %s/%s is missing non-empty key %s", obj.Namespace, name, layout.AuthEnvKey)
	}
	if obj.Spec.Provider == controlv1alpha1.AgentAuthSessionProviderOpenClaw {
		if err := validateOpenClawAuthProfilesJSON(raw, obj.Spec.AuthMode, obj.Spec.ModelProvider); err != nil {
			return fmt.Errorf("staging Secret %s/%s %s: %w", obj.Namespace, name, layout.AuthEnvKey, err)
		}
	}
	return nil
}

func (r *AgentAuthSessionReconciler) validateBootstrapSecretWritable(ctx context.Context, namespace, name string) error {
	secret := &corev1.Secret{}
	err := r.reader().Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if secretManagedByExternalSecret(secret) {
		return fmt.Errorf("bootstrap Secret %s/%s is managed by External Secrets Operator; refuse to race its writer. Point AgentAuthSession at a CLI-owned seed Secret instead", namespace, name)
	}
	return nil
}

func secretManagedByExternalSecret(secret *corev1.Secret) bool {
	if secret == nil {
		return false
	}
	if owner := metav1.GetControllerOf(secret); owner != nil {
		if strings.Contains(strings.ToLower(owner.Kind), "externalsecret") || strings.Contains(owner.APIVersion, "external-secrets") {
			return true
		}
	}
	for key, value := range secret.Labels {
		if strings.Contains(strings.ToLower(key), "external-secrets") || strings.Contains(strings.ToLower(value), "external-secrets") {
			return true
		}
	}
	for key := range secret.Annotations {
		if strings.Contains(strings.ToLower(key), "external-secrets") {
			return true
		}
	}
	return false
}

func listActiveAuthVolumeConsumers(ctx context.Context, reader client.Reader, namespace, volumeName string) ([]string, error) {
	list := &controlv1alpha1.AgentRunList{}
	if err := reader.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list AgentRuns: %w", err)
	}
	active := make([]string, 0)
	for i := range list.Items {
		run := &list.Items[i]
		if agentRunIsTerminal(run.Status.Phase) {
			continue
		}
		usesVolume := false
		for _, ref := range run.Spec.Harness.Execution.DataVolumeRefs {
			if strings.TrimSpace(ref.Name) == volumeName {
				usesVolume = true
				break
			}
		}
		if !usesVolume {
			for _, statusVolume := range run.Status.DataVolumes {
				if strings.TrimSpace(statusVolume.Name) == volumeName {
					usesVolume = true
					break
				}
			}
		}
		if usesVolume {
			active = append(active, run.Name)
		}
	}
	return active, nil
}

func agentRunIsTerminal(phase controlv1alpha1.AgentRunPhase) bool {
	switch phase {
	case controlv1alpha1.AgentRunPhaseSucceeded, controlv1alpha1.AgentRunPhaseFailed:
		return true
	default:
		return false
	}
}

func (r *AgentAuthSessionReconciler) otherActiveSessionForVolume(ctx context.Context, obj *controlv1alpha1.AgentAuthSession, volumeName string) (bool, string, error) {
	list := &controlv1alpha1.AgentAuthSessionList{}
	if err := r.reader().List(ctx, list, client.InNamespace(obj.Namespace)); err != nil {
		return false, "", err
	}
	for i := range list.Items {
		session := &list.Items[i]
		if session.UID == obj.UID {
			continue
		}
		if strings.TrimSpace(session.Spec.DataVolumeRef.Name) != volumeName {
			continue
		}
		if controlv1alpha1.AgentAuthSessionIsTerminal(session.Status.Phase) {
			continue
		}
		// Prefer the older session as the reservation holder.
		if session.CreationTimestamp.Before(&obj.CreationTimestamp) || (session.CreationTimestamp.Equal(&obj.CreationTimestamp) && session.Name < obj.Name) {
			return true, session.Name, nil
		}
	}
	return false, "", nil
}

func (r *AgentAuthSessionReconciler) ensureAuthSessionJob(ctx context.Context, obj *controlv1alpha1.AgentAuthSession, volume *controlv1alpha1.AgentDataVolume, claimName, mountPath string) (*batchv1.Job, error) {
	jobName := agentRunChildName("auth", obj.Name)
	existing := &batchv1.Job{}
	err := r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: jobName}, existing)
	if err == nil {
		return existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	timeout := obj.Spec.TimeoutSeconds
	if timeout <= 0 {
		timeout = agentAuthDefaultTimeoutSeconds
	}
	layout, err := agentAuthLayout(obj.Spec.Provider)
	if err != nil {
		return nil, err
	}
	image := r.authSessionImage(obj.Spec.Provider)
	script := agentAuthSessionScript(obj.Spec.Action, mountPath, layout)
	backoff := int32(0)
	ttl := int32(600)
	activeDeadline := int64(timeout)
	automount := false
	runAsNonRoot := true
	allowPrivilegeEscalation := false
	uid, gid := agentAuthSessionIDs(obj.Spec.Provider)
	fsGroupChange := corev1.FSGroupChangeOnRootMismatch
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: obj.Namespace,
			Labels: map[string]string{
				agentAuthSessionLabel:          sanitizeLabelValue(obj.Name),
				agentAuthSessionActionLabel:    sanitizeLabelValue(string(obj.Spec.Action)),
				agentAuthSessionProviderLabel:  sanitizeLabelValue(string(obj.Spec.Provider)),
				agentAuthSessionVolumeLabel:    sanitizeLabelValue(volume.Name),
				agentAuthSessionManagedByLabel: agentAuthSessionManagedByValue,
				agentAuthSessionComponentLabel: agentAuthSessionComponentValue,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &activeDeadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						agentAuthSessionLabel:          sanitizeLabelValue(obj.Name),
						agentAuthSessionActionLabel:    sanitizeLabelValue(string(obj.Spec.Action)),
						agentAuthSessionProviderLabel:  sanitizeLabelValue(string(obj.Spec.Provider)),
						agentAuthSessionVolumeLabel:    sanitizeLabelValue(volume.Name),
						agentAuthSessionManagedByLabel: agentAuthSessionManagedByValue,
						agentAuthSessionComponentLabel: agentAuthSessionComponentValue,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: &automount,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:        &runAsNonRoot,
						RunAsUser:           &uid,
						RunAsGroup:          &gid,
						FSGroup:             &uid,
						FSGroupChangePolicy: &fsGroupChange,
					},
					NodeSelector: agentDataVolumeResolvedNodeSelector(volume),
					Containers: []corev1.Container{{
						Name:            agentAuthContainerName,
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command:         []string{"/bin/bash", "-lc", script},
						Env:             agentAuthSessionEnv(obj),
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "agent-home",
							MountPath: mountPath,
							SubPath:   agentDataVolumeResolvedSubPath(volume),
						}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: &allowPrivilegeEscalation,
							RunAsNonRoot:             &runAsNonRoot,
							RunAsUser:                &uid,
							RunAsGroup:               &gid,
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						Resources: corev1.ResourceRequirements{},
					}},
					Volumes: []corev1.Volume{{
						Name: "agent-home",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claimName},
						},
					}},
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(obj, job, r.Scheme); err != nil {
		return nil, fmt.Errorf("set Job owner: %w", err)
	}
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			if getErr := r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: jobName}, existing); getErr != nil {
				return nil, getErr
			}
			return existing, nil
		}
		return nil, fmt.Errorf("create auth Job: %w", err)
	}
	return job, nil
}

func (r *AgentAuthSessionReconciler) authSessionImage(provider controlv1alpha1.AgentAuthSessionProvider) string {
	switch provider {
	case controlv1alpha1.AgentAuthSessionProviderGrokBuild:
		if r.Options != nil && strings.TrimSpace(r.Options.GrokBuildRunnerImage) != "" {
			return strings.TrimSpace(r.Options.GrokBuildRunnerImage)
		}
		return agentRunDefaultGrokBuildImage
	case controlv1alpha1.AgentAuthSessionProviderOpenClaw:
		if r.Options != nil && strings.TrimSpace(r.Options.OpenClawRunnerImage) != "" {
			return strings.TrimSpace(r.Options.OpenClawRunnerImage)
		}
		return agentRunDefaultOpenClawImage
	default:
		if r.Options != nil && strings.TrimSpace(r.Options.CodexRunnerImage) != "" {
			return strings.TrimSpace(r.Options.CodexRunnerImage)
		}
		return agentRunDefaultCodexImage
	}
}

func agentAuthSessionIDs(provider controlv1alpha1.AgentAuthSessionProvider) (uid, gid int64) {
	if provider == controlv1alpha1.AgentAuthSessionProviderOpenClaw {
		return agentAuthOpenClawUID, agentAuthOpenClawUID
	}
	return agentAuthCodexUID, agentAuthCodexUID
}

func agentAuthSessionEnv(obj *controlv1alpha1.AgentAuthSession) []corev1.EnvVar {
	layout, err := agentAuthLayout(obj.Spec.Provider)
	if err != nil {
		return []corev1.EnvVar{
			{Name: "ANVIL_AUTH_ACTION", Value: string(obj.Spec.Action)},
			{Name: "ANVIL_AUTH_PROVIDER", Value: string(obj.Spec.Provider)},
		}
	}
	env := []corev1.EnvVar{
		{Name: "ANVIL_AUTH_ACTION", Value: string(obj.Spec.Action)},
		{Name: "ANVIL_AUTH_PROVIDER", Value: string(obj.Spec.Provider)},
	}
	if seed := strings.TrimSpace(obj.Spec.SeedID); seed != "" {
		env = append(env, corev1.EnvVar{Name: layout.SeedEnvKey, Value: seed})
	}
	if mode := strings.TrimSpace(string(obj.Spec.AuthMode)); mode != "" {
		env = append(env, corev1.EnvVar{Name: "ANVIL_AUTH_MODE", Value: mode})
	}
	if agentID := strings.TrimSpace(obj.Spec.AgentID); agentID != "" {
		env = append(env, corev1.EnvVar{Name: "ANVIL_OPENCLAW_AGENT_ID", Value: agentID})
	}
	if modelProvider := strings.TrimSpace(obj.Spec.ModelProvider); modelProvider != "" {
		env = append(env, corev1.EnvVar{Name: "ANVIL_OPENCLAW_MODEL_PROVIDER", Value: modelProvider})
	}
	if obj.Spec.Action == controlv1alpha1.AgentAuthSessionActionReauth && obj.Spec.StagingSecretRef != nil {
		env = append(env, corev1.EnvVar{
			Name: layout.AuthEnvKey,
			ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: strings.TrimSpace(obj.Spec.StagingSecretRef.Name)},
				Key:                  layout.AuthEnvKey,
				Optional:             boolPtr(false),
			}},
		})
	}
	return env
}

func agentAuthSessionScript(action controlv1alpha1.AgentAuthSessionAction, mountPath string, layout agentAuthProviderLayout) string {
	if layout.Provider == controlv1alpha1.AgentAuthSessionProviderOpenClaw {
		return agentAuthOpenClawSessionScript(action, mountPath, layout)
	}
	authPath := path.Join(mountPath, layout.AuthRelPath)
	authDir := path.Dir(authPath)
	seedPath := path.Join(mountPath, layout.SeedFileName)
	logoutPath := path.Join(mountPath, layout.LogoutFileName)
	switch action {
	case controlv1alpha1.AgentAuthSessionActionLogout:
		return fmt.Sprintf(`set -euo pipefail
mount=%q
auth=%q
auth_dir=%q
seed=%q
logout=%q
mkdir -p "$mount" "$auth_dir"
rm -f "$auth" "$seed"
umask 077
printf 'logged-out\n' > "$logout"
sync
echo "ANVIL_AUTH_SESSION_COMPLETE action=logout provider=%s mount=$mount"
`, mountPath, authPath, authDir, seedPath, logoutPath, layout.Provider)
	case controlv1alpha1.AgentAuthSessionActionVerify:
		return fmt.Sprintf(`set -euo pipefail
mount=%q
auth=%q
if [[ ! -f "$auth" ]]; then
  echo "ANVIL_AUTH_SESSION_FAILED reason=auth-missing" >&2
  exit 1
fi
# Honest presence check only; never print credential bytes.
if ! python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$auth" 2>/dev/null; then
  if ! jq -e . "$auth" >/dev/null 2>&1; then
    echo "ANVIL_AUTH_SESSION_FAILED reason=auth-invalid-json" >&2
    exit 1
  fi
fi
echo "ANVIL_AUTH_SESSION_COMPLETE action=verify provider=%s mount=$mount"
`, mountPath, authPath, layout.Provider)
	default:
		// Embed concrete env names so the script does not need bash namerefs and
		// never prints credential values.
		return fmt.Sprintf(`set -euo pipefail
mount=%q
auth=%q
auth_dir=%q
seed=%q
logout=%q
: "${%s:?%s is required}"
: "${%s:?%s is required}"
mkdir -p "$mount" "$auth_dir"
umask 077
tmp="$(mktemp "$auth_dir/.auth.json.XXXXXX")"
printf '%%s' "${%s}" > "$tmp"
chmod 600 "$tmp"
seed_tmp="$(mktemp "$mount/.seed.XXXXXX")"
printf '%%s\n' "${%s}" > "$seed_tmp"
chmod 600 "$seed_tmp"
mv "$seed_tmp" "$seed"
rm -f "$logout"
mv "$tmp" "$auth"
sync
# Never print credentials.
echo "ANVIL_AUTH_SESSION_COMPLETE action=reauth provider=%s mount=$mount seed_written=true"
`, mountPath, authPath, authDir, seedPath, logoutPath,
			layout.AuthEnvKey, layout.AuthEnvKey,
			layout.SeedEnvKey, layout.SeedEnvKey,
			layout.AuthEnvKey, layout.SeedEnvKey, layout.Provider)
	}
}

// agentAuthOpenClawSessionScript maintains OpenClaw per-agent auth via the
// exported plugin-sdk (saveAuthProfileStore). It never replaces SQLite DBs,
// never prints credentials, and resolves agentDir by strictly parsing the
// registered agent list in the volume-owned openclaw.json without invoking
// mutable OpenClaw config or plugins.
func agentAuthOpenClawSessionScript(action controlv1alpha1.AgentAuthSessionAction, mountPath string, layout agentAuthProviderLayout) string {
	seedPath := path.Join(mountPath, layout.SeedFileName)
	logoutPath := path.Join(mountPath, layout.LogoutFileName)
	// Node maintenance program embedded as a single-quoted heredoc. Credential
	// values are written only through the SDK to the selected agentDir.
	nodeProgram := `import fs from 'node:fs';
import path from 'node:path';
const mount = process.env.ANVIL_AUTH_MOUNT;
const action = process.env.ANVIL_AUTH_ACTION;
const agentId = process.env.ANVIL_OPENCLAW_AGENT_ID;
const modelProvider = process.env.ANVIL_OPENCLAW_MODEL_PROVIDER;
const authMode = process.env.ANVIL_AUTH_MODE || '';
const seedEnv = process.env.OPENCLAW_AUTH_SEED_ID || '';
const profilesRaw = process.env.OPENCLAW_AUTH_PROFILES_JSON || '';
const seedPath = process.env.ANVIL_AUTH_SEED_PATH;
const logoutPath = process.env.ANVIL_AUTH_LOGOUT_PATH;
if (!mount || !agentId || !modelProvider) {
  console.error('ANVIL_AUTH_SESSION_FAILED reason=missing-mount-agent-or-model-provider');
  process.exit(1);
}
const stateDir = path.join(mount, 'state');
fs.mkdirSync(stateDir, { recursive: true });
process.env.OPENCLAW_STATE_DIR = stateDir;
function fail(reason) {
  console.error('ANVIL_AUTH_SESSION_FAILED reason=' + reason);
  process.exit(1);
}
function resolveAgent() {
  const realMount = fs.realpathSync(mount);
  const configPath = path.join(stateDir, 'openclaw.json');
  let stat;
  try {
    stat = fs.lstatSync(configPath);
  } catch {
    fail('agent-config-missing');
  }
  if (!stat.isFile() || stat.isSymbolicLink()) fail('agent-config-unsafe');
  const realConfig = fs.realpathSync(configPath);
  if (!realConfig.startsWith(realMount + path.sep)) fail('agent-config-outside-volume');
  let parsed;
  try {
    parsed = JSON.parse(fs.readFileSync(realConfig, 'utf8'));
  } catch {
    fail('agent-config-invalid');
  }
  const agents = parsed?.agents?.list;
  if (!Array.isArray(agents)) fail('agent-config-invalid');
  const hits = agents.filter((a) => a && a.id === agentId);
  if (hits.length !== 1) fail(hits.length === 0 ? 'agent-not-registered' : 'agent-duplicate');
  const configuredDir = hits[0].agentDir;
  if (typeof configuredDir !== 'string' || configuredDir.length === 0) fail('agent-dir-missing');
  const candidate = path.resolve(configuredDir);
  let dirStat;
  try {
    dirStat = fs.lstatSync(candidate);
  } catch {
    fail('agent-dir-missing');
  }
  if (!dirStat.isDirectory() || dirStat.isSymbolicLink()) fail('agent-dir-unsafe');
  const realAgent = fs.realpathSync(candidate);
  if (!realAgent.startsWith(realMount + path.sep)) fail('agent-dir-outside-volume');
  return realAgent;
}
async function loadSdk() {
  const candidates = [
    '/usr/local/lib/node_modules/openclaw/dist/plugin-sdk/agent-runtime.js',
    '/usr/lib/node_modules/openclaw/dist/plugin-sdk/agent-runtime.js',
  ];
  for (const c of candidates) {
    try {
      if (fs.existsSync(c)) return await import(c);
    } catch {}
  }
  try {
    return await import('openclaw/dist/plugin-sdk/agent-runtime.js');
  } catch (err) {
    console.error('ANVIL_AUTH_SESSION_FAILED reason=sdk-missing');
    process.exit(1);
  }
}
function writeAtomic(filePath, body) {
  const dir = path.dirname(filePath);
  fs.mkdirSync(dir, { recursive: true });
  const tmp = path.join(dir, '.anvil-tmp-' + process.pid + '-' + Date.now());
  fs.writeFileSync(tmp, body, { mode: 0o600 });
  fs.renameSync(tmp, filePath);
}
function parseStore(raw) {
  const store = JSON.parse(raw);
  if (!store || store.version !== 1 || !store.profiles || typeof store.profiles !== 'object') {
    throw new Error('invalid profile store');
  }
  const ids = Object.keys(store.profiles);
  if (ids.length === 0) throw new Error('empty profiles');
  return { store, ids };
}
function validKeyRef(value) {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value)
    && value.source === 'env'
    && typeof value.provider === 'string' && value.provider.length > 0
    && typeof value.id === 'string' && value.id.length > 0);
}
async function main() {
  const agentDir = resolveAgent();
  const sdk = await loadSdk();
  if (action === 'verify') {
    // Load OpenClaw's effective native store without logging it. The later
    // AgentRun canary is the live-provider proof; this action proves that the
    // selected agent has a currently usable credential of the declared mode.
    if (typeof sdk.loadAuthProfileStoreWithoutExternalProfiles !== 'function') {
      console.error('ANVIL_AUTH_SESSION_FAILED reason=sdk-load-unavailable');
      process.exit(1);
    }
    const existing = await sdk.loadAuthProfileStoreWithoutExternalProfiles(agentDir);
    const profiles = Object.values(existing?.profiles || {});
    const usable = profiles.some((profile) => {
      if (!profile || typeof profile !== 'object') return false;
      if (String(profile.provider || '') !== modelProvider) return false;
      const type = String(profile.type || '').toLowerCase();
      if (authMode === 'oauth') {
        // A refresh-capable native profile is a truthful durable-auth receipt;
        // the later AgentRun canary proves remote refresh/provider behavior.
        // OpenClaw releases have persisted expires in both seconds and ms, so
        // do not reject a structurally valid refresh credential by unit guess.
        return type === 'oauth' && Boolean(profile.refresh);
      }
      if (authMode === 'apiKey') {
        return type === 'api_key'
          && ((typeof profile.key === 'string' && profile.key.length > 0) || validKeyRef(profile.keyRef));
      }
      return false;
    });
    if (!usable) {
      console.error('ANVIL_AUTH_SESSION_FAILED reason=no-usable-profile');
      process.exit(1);
    }
    console.log('ANVIL_AUTH_SESSION_COMPLETE action=verify provider=openClaw mount=' + mount + ' agent=' + agentId);
    return;
  }
  if (action === 'logout') {
    const empty = { version: 1, profiles: {} };
    if (typeof sdk.saveAuthProfileStore !== 'function') {
      console.error('ANVIL_AUTH_SESSION_FAILED reason=sdk-save-unavailable');
      process.exit(1);
    }
    await sdk.saveAuthProfileStore(empty, agentDir);
    if (typeof sdk.loadAuthProfileStoreWithoutExternalProfiles === 'function') {
      const remaining = await sdk.loadAuthProfileStoreWithoutExternalProfiles(agentDir);
      if (Object.keys(remaining?.profiles || {}).length > 0) {
        console.error('ANVIL_AUTH_SESSION_FAILED reason=inherited-auth-remains');
        process.exit(1);
      }
    }
    try { fs.unlinkSync(seedPath); } catch {}
    writeAtomic(logoutPath, 'logged-out\n');
    console.log('ANVIL_AUTH_SESSION_COMPLETE action=logout provider=openClaw mount=' + mount + ' agent=' + agentId);
    return;
  }
  // reauth
  if (!profilesRaw || !seedEnv) {
    console.error('ANVIL_AUTH_SESSION_FAILED reason=missing-staging');
    process.exit(1);
  }
  const { store, ids } = parseStore(profilesRaw);
  // Mode agreement is enforced by the controller; re-check types without logging secrets.
  for (const id of ids) {
    const p = store.profiles[id] || {};
    if (String(p.provider || '') !== modelProvider) {
      console.error('ANVIL_AUTH_SESSION_FAILED reason=model-provider-mismatch');
      process.exit(1);
    }
    const t = String(p.type || p.credentialType || '').toLowerCase();
    if (authMode === 'oauth' && t !== 'oauth') {
      console.error('ANVIL_AUTH_SESSION_FAILED reason=mode-mismatch');
      process.exit(1);
    }
    if (authMode === 'apiKey' && t !== 'api_key' && t !== 'apikey') {
      console.error('ANVIL_AUTH_SESSION_FAILED reason=mode-mismatch');
      process.exit(1);
    }
  }
  if (typeof sdk.saveAuthProfileStore !== 'function') {
    console.error('ANVIL_AUTH_SESSION_FAILED reason=sdk-save-unavailable');
    process.exit(1);
  }
  await sdk.saveAuthProfileStore(store, agentDir);
  writeAtomic(seedPath, seedEnv + '\n');
  try { fs.unlinkSync(logoutPath); } catch {}
  console.log('ANVIL_AUTH_SESSION_COMPLETE action=reauth provider=openClaw mount=' + mount + ' seed_written=true agent=' + agentId);
}
main().catch((err) => {
  console.error('ANVIL_AUTH_SESSION_FAILED reason=runtime');
  process.exit(1);
});`
	switch action {
	case controlv1alpha1.AgentAuthSessionActionLogout:
		return fmt.Sprintf(`set -euo pipefail
export ANVIL_AUTH_MOUNT=%q
export ANVIL_AUTH_ACTION=logout
export ANVIL_AUTH_SEED_PATH=%q
export ANVIL_AUTH_LOGOUT_PATH=%q
: "${ANVIL_OPENCLAW_AGENT_ID:?ANVIL_OPENCLAW_AGENT_ID is required}"
: "${ANVIL_OPENCLAW_MODEL_PROVIDER:?ANVIL_OPENCLAW_MODEL_PROVIDER is required}"
mkdir -p "$ANVIL_AUTH_MOUNT/state" "$ANVIL_AUTH_MOUNT/workspace"
export OPENCLAW_STATE_DIR="$ANVIL_AUTH_MOUNT/state"
node --input-type=module <<'ANVIL_OPENCLAW_AUTH_NODE'
%s
ANVIL_OPENCLAW_AUTH_NODE
`, mountPath, seedPath, logoutPath, nodeProgram)
	case controlv1alpha1.AgentAuthSessionActionVerify:
		return fmt.Sprintf(`set -euo pipefail
export ANVIL_AUTH_MOUNT=%q
export ANVIL_AUTH_ACTION=verify
export ANVIL_AUTH_SEED_PATH=%q
export ANVIL_AUTH_LOGOUT_PATH=%q
: "${ANVIL_OPENCLAW_AGENT_ID:?ANVIL_OPENCLAW_AGENT_ID is required}"
: "${ANVIL_OPENCLAW_MODEL_PROVIDER:?ANVIL_OPENCLAW_MODEL_PROVIDER is required}"
mkdir -p "$ANVIL_AUTH_MOUNT/state" "$ANVIL_AUTH_MOUNT/workspace"
export OPENCLAW_STATE_DIR="$ANVIL_AUTH_MOUNT/state"
node --input-type=module <<'ANVIL_OPENCLAW_AUTH_NODE'
%s
ANVIL_OPENCLAW_AUTH_NODE
`, mountPath, seedPath, logoutPath, nodeProgram)
	default:
		return fmt.Sprintf(`set -euo pipefail
export ANVIL_AUTH_MOUNT=%q
export ANVIL_AUTH_ACTION=reauth
export ANVIL_AUTH_SEED_PATH=%q
export ANVIL_AUTH_LOGOUT_PATH=%q
: "${ANVIL_OPENCLAW_AGENT_ID:?ANVIL_OPENCLAW_AGENT_ID is required}"
: "${ANVIL_OPENCLAW_MODEL_PROVIDER:?ANVIL_OPENCLAW_MODEL_PROVIDER is required}"
: "${OPENCLAW_AUTH_PROFILES_JSON:?OPENCLAW_AUTH_PROFILES_JSON is required}"
: "${OPENCLAW_AUTH_SEED_ID:?OPENCLAW_AUTH_SEED_ID is required}"
mkdir -p "$ANVIL_AUTH_MOUNT/state" "$ANVIL_AUTH_MOUNT/workspace"
export OPENCLAW_STATE_DIR="$ANVIL_AUTH_MOUNT/state"
# Never print credential env values.
node --input-type=module <<'ANVIL_OPENCLAW_AUTH_NODE'
%s
ANVIL_OPENCLAW_AUTH_NODE
`, mountPath, seedPath, logoutPath, nodeProgram)
	}
}

// validateOpenClawAuthProfilesJSON accepts a version=1 OpenClaw profile store.
// Credential type must be uniform and agree with authMode. Credentials are never logged.
func validateOpenClawAuthProfilesJSON(raw []byte, authMode controlv1alpha1.AgentRunProviderAuthMode, modelProvider string) error {
	var store struct {
		Version  int                    `json:"version"`
		Profiles map[string]interface{} `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &store); err != nil {
		return fmt.Errorf("invalid JSON")
	}
	if store.Version != 1 {
		return fmt.Errorf("version must be 1")
	}
	if len(store.Profiles) == 0 {
		return fmt.Errorf("profiles must be non-empty")
	}
	mode := strings.TrimSpace(string(authMode))
	expectedProvider := strings.TrimSpace(modelProvider)
	if expectedProvider == "" {
		return fmt.Errorf("modelProvider is required")
	}
	var seenType string
	for id, value := range store.Profiles {
		entry, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("profile %q is not an object", id)
		}
		credType := strings.ToLower(strings.TrimSpace(asString(entry["type"])))
		if credType == "" {
			credType = strings.ToLower(strings.TrimSpace(asString(entry["credentialType"])))
		}
		provider := strings.TrimSpace(asString(entry["provider"]))
		if provider == "" {
			return fmt.Errorf("profile %q missing provider", id)
		}
		if provider != expectedProvider {
			return fmt.Errorf("profile %q provider %q does not match modelProvider %q", id, provider, expectedProvider)
		}
		switch credType {
		case "oauth":
			if strings.TrimSpace(asString(entry["access"])) == "" {
				return fmt.Errorf("oauth profile %q missing access", id)
			}
			if strings.TrimSpace(asString(entry["refresh"])) == "" {
				return fmt.Errorf("oauth profile %q missing refresh", id)
			}
			if !openClawExpiresValid(entry["expires"]) {
				return fmt.Errorf("oauth profile %q has invalid expires", id)
			}
		case "api_key", "apikey":
			credType = "api_key"
			if strings.TrimSpace(asString(entry["key"])) == "" {
				if !validOpenClawAPIKeyRef(entry["keyRef"]) {
					return fmt.Errorf("api_key profile %q missing key", id)
				}
			}
		default:
			return fmt.Errorf("profile %q has unsupported credential type %q", id, credType)
		}
		if seenType == "" {
			seenType = credType
		} else if seenType != credType {
			return fmt.Errorf("mixed credential types are not allowed")
		}
	}
	switch mode {
	case string(controlv1alpha1.AgentRunProviderAuthModeOAuth):
		if seenType != "oauth" {
			return fmt.Errorf("authMode oauth requires oauth profiles")
		}
	case string(controlv1alpha1.AgentRunProviderAuthModeAPIKey):
		if seenType != "api_key" {
			return fmt.Errorf("authMode apiKey requires api_key profiles")
		}
	default:
		return fmt.Errorf("authMode is required for openClaw staging validation")
	}
	return nil
}

func validOpenClawAPIKeyRef(value interface{}) bool {
	ref, ok := value.(map[string]interface{})
	if !ok {
		return false
	}
	return strings.TrimSpace(asString(ref["source"])) == "env" &&
		strings.TrimSpace(asString(ref["provider"])) != "" &&
		strings.TrimSpace(asString(ref["id"])) != ""
}

func asString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

func openClawExpiresValid(v interface{}) bool {
	switch t := v.(type) {
	case float64:
		return t > 0
	case json.Number:
		n, err := t.Float64()
		return err == nil && n > 0
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return false
		}
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return n > 0
		}
		if _, err := time.Parse(time.RFC3339, s); err == nil {
			return true
		}
		if _, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return true
		}
		return false
	case nil:
		return false
	default:
		return false
	}
}

func (r *AgentAuthSessionReconciler) applyBootstrapSecret(ctx context.Context, obj *controlv1alpha1.AgentAuthSession) error {
	if obj.Spec.BootstrapSecretRef == nil || strings.TrimSpace(obj.Spec.BootstrapSecretRef.Name) == "" {
		return nil
	}
	layout, err := agentAuthLayout(obj.Spec.Provider)
	if err != nil {
		return err
	}
	stagingName := strings.TrimSpace(obj.Spec.StagingSecretRef.Name)
	staging := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: stagingName}, staging); err != nil {
		return fmt.Errorf("get staging Secret: %w", err)
	}
	authBytes := staging.Data[layout.AuthEnvKey]
	if len(authBytes) == 0 {
		return fmt.Errorf("staging Secret missing %s", layout.AuthEnvKey)
	}
	bootstrapName := strings.TrimSpace(obj.Spec.BootstrapSecretRef.Name)
	key := firstNonEmpty(strings.TrimSpace(obj.Spec.BootstrapSecretKey), layout.DefaultBootstrapKey)
	seedID := strings.TrimSpace(obj.Spec.SeedID)

	existing := &corev1.Secret{}
	err = r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: bootstrapName}, existing)
	if apierrors.IsNotFound(err) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bootstrapName,
				Namespace: obj.Namespace,
				Labels: map[string]string{
					agentAuthSessionManagedByLabel: agentAuthSessionManagedByValue,
					agentAuthSessionComponentLabel: layout.ComponentLabel,
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				key:                  append([]byte(nil), authBytes...),
				layout.SeedSecretKey: []byte(seedID),
			},
		}
		if err := r.Create(ctx, secret); err != nil {
			return fmt.Errorf("create bootstrap Secret: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if secretManagedByExternalSecret(existing) {
		return fmt.Errorf("bootstrap Secret %s/%s became ExternalSecret-managed during the session", obj.Namespace, bootstrapName)
	}
	patched := existing.DeepCopy()
	if patched.Data == nil {
		patched.Data = map[string][]byte{}
	}
	patched.Data[key] = append([]byte(nil), authBytes...)
	patched.Data[layout.SeedSecretKey] = []byte(seedID)
	if patched.Labels == nil {
		patched.Labels = map[string]string{}
	}
	patched.Labels[agentAuthSessionManagedByLabel] = agentAuthSessionManagedByValue
	patched.Labels[agentAuthSessionComponentLabel] = layout.ComponentLabel
	if err := r.Patch(ctx, patched, client.MergeFrom(existing)); err != nil {
		return fmt.Errorf("patch bootstrap Secret: %w", err)
	}
	return nil
}

func (r *AgentAuthSessionReconciler) deleteOwnedStagingSecret(ctx context.Context, obj *controlv1alpha1.AgentAuthSession) error {
	if obj.Spec.StagingSecretRef == nil {
		return nil
	}
	name := strings.TrimSpace(obj.Spec.StagingSecretRef.Name)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: name}, secret); err != nil {
		return client.IgnoreNotFound(err)
	}
	if secret.Labels[agentAuthStagingOwnerLabel] != sanitizeLabelValue(obj.Name) {
		return nil
	}
	if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete staging Secret: %w", err)
	}
	return nil
}

func (r *AgentAuthSessionReconciler) failSession(ctx context.Context, original, obj *controlv1alpha1.AgentAuthSession, status *controlv1alpha1.AgentAuthSessionStatus, now metav1.Time, reason, message string) (ctrl.Result, error) {
	status.Phase = controlv1alpha1.AgentAuthSessionPhaseFailed
	status.LastError = message
	status.CompletedAt = &now
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentAuthSessionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	obj.Status = *status
	return ctrl.Result{}, r.patchAgentAuthSessionStatus(ctx, original, obj)
}

func (r *AgentAuthSessionReconciler) patchAgentAuthSessionStatus(ctx context.Context, original, obj *controlv1alpha1.AgentAuthSession) error {
	if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("patch AgentAuthSession status: %w", err)
	}
	return nil
}

// AgentAuthSessionBlocksDataVolume reports whether a non-terminal auth session
// currently reserves the named AgentDataVolume in the namespace.
func AgentAuthSessionBlocksDataVolume(ctx context.Context, reader client.Reader, namespace, volumeName string) (bool, string, error) {
	list := &controlv1alpha1.AgentAuthSessionList{}
	if err := reader.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return false, "", err
	}
	for i := range list.Items {
		session := &list.Items[i]
		if strings.TrimSpace(session.Spec.DataVolumeRef.Name) != volumeName {
			continue
		}
		if controlv1alpha1.AgentAuthSessionIsTerminal(session.Status.Phase) {
			continue
		}
		return true, session.Name, nil
	}
	return false, "", nil
}

func (r *AgentAuthSessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&controlv1alpha1.AgentAuthSession{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// Ensure types used only for client.ObjectKey imports stay linked when tools change.
var _ = types.NamespacedName{}
