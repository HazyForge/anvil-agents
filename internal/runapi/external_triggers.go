package runapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	externalTriggerMaxBodyBytes      = 256 * 1024
	externalTriggerSummaryMaxRunes   = 512
	externalTriggerPromptMaxRunes    = 4096
	externalTriggerSeenDeliveryLimit = 64
	externalTriggerPathTokenHeader   = "X-Anvil-Trigger-Token"
	externalTriggerDefaultPrompt     = "GitHub webhook delivery {{deliveryID}} for {{repository}} (event {{eventType}}).\n\n{{summary}}"
)

type githubWebhookPayload struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
		Name     string `json:"name"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
	Issue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	} `json:"issue"`
	PullRequest struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	} `json:"pull_request"`
	Ref     string `json:"ref"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Comment struct {
		Body string `json:"body"`
	} `json:"comment"`
}

type externalTriggerDeliveryContext struct {
	DeliveryID  string
	EventType   string
	Repository  string
	Action      string
	Summary     string
	AcceptedAt  metav1.Time
	PayloadHint string
}

func (server *Server) registerExternalTriggerRoutes(mux *http.ServeMux) {
	// Unauthenticated inbound webhook. Feature-gated; HMAC-verified.
	mux.HandleFunc("POST /api/v1/external-triggers/{namespace}/{name}/{receiverID}", server.handleExternalTriggerWebhook)
}

func (server *Server) handleExternalTriggerWebhook(writer http.ResponseWriter, request *http.Request) {
	if !server.config.ExternalTriggers.Enabled {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	writerClient, ok := server.writerClient(writer)
	if !ok {
		return
	}

	namespace := strings.TrimSpace(request.PathValue("namespace"))
	name := strings.TrimSpace(request.PathValue("name"))
	receiverID := strings.TrimSpace(request.PathValue("receiverID"))
	if namespace == "" || name == "" || receiverID == "" {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}

	body, err := readExternalTriggerBody(request)
	if err != nil {
		writeAPIError(writer, http.StatusRequestEntityTooLarge, "payload_too_large", err.Error())
		return
	}

	trigger := &agentsv1alpha1.AgentExternalTrigger{}
	if err := writerClient.Get(request.Context(), types.NamespacedName{Namespace: namespace, Name: name}, trigger); err != nil {
		if apierrors.IsNotFound(err) {
			writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		server.log.Error(err, "get AgentExternalTrigger", "namespace", namespace, "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "trigger lookup failed")
		return
	}
	if strings.TrimSpace(trigger.Status.ReceiverID) == "" || trigger.Status.ReceiverID != receiverID {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}

	secretValue, pathToken, err := server.loadExternalTriggerSecrets(request.Context(), writerClient, trigger)
	if err != nil {
		server.log.Error(err, "load AgentExternalTrigger secret", "namespace", namespace, "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "secret_unavailable", "webhook secret unavailable")
		return
	}
	if err := verifyGitHubSignature256(secretValue, request.Header.Get("X-Hub-Signature-256"), body); err != nil {
		writeAPIError(writer, http.StatusUnauthorized, "invalid_signature", "signature verification failed")
		return
	}
	if pathToken != "" {
		provided := strings.TrimSpace(request.Header.Get(externalTriggerPathTokenHeader))
		if provided == "" || !hmacEqualString(provided, pathToken) {
			writeAPIError(writer, http.StatusUnauthorized, "invalid_path_token", "path token verification failed")
			return
		}
	}

	if trigger.Spec.Suspend || trigger.Status.Phase == agentsv1alpha1.AgentExternalTriggerPhaseSuspended {
		writeAPIError(writer, http.StatusConflict, "suspended", "trigger is suspended")
		return
	}
	if trigger.Status.Phase == agentsv1alpha1.AgentExternalTriggerPhaseBlocked {
		writeAPIError(writer, http.StatusConflict, "blocked", "trigger is blocked")
		return
	}

	deliveryID := strings.TrimSpace(request.Header.Get("X-GitHub-Delivery"))
	eventType := strings.TrimSpace(request.Header.Get("X-GitHub-Event"))
	if deliveryID == "" {
		writeAPIError(writer, http.StatusBadRequest, "missing_delivery_id", "X-GitHub-Delivery is required")
		return
	}
	if eventType == "" {
		writeAPIError(writer, http.StatusBadRequest, "missing_event_type", "X-GitHub-Event is required")
		return
	}

	if externalTriggerSeenDelivery(trigger.Status.SeenDeliveryIDs, deliveryID) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"accepted":   true,
			"duplicate":  true,
			"deliveryID": deliveryID,
			"runRefs":    trigger.Status.LastRunRefs,
		})
		return
	}

	payload, repo, summary, err := parseGitHubWebhookPayload(body)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_payload", "payload is not valid JSON")
		return
	}
	if !githubRepositoryAllowed(trigger.Spec.Source.GitHub, repo) {
		writeAPIError(writer, http.StatusForbidden, "repository_not_allowed", "repository is not allowlisted")
		return
	}
	if !githubEventAllowed(trigger.Spec.Source.GitHub, eventType) {
		writeAPIError(writer, http.StatusForbidden, "event_not_allowed", "event type is not allowlisted")
		return
	}

	now := metav1.Now()
	if err := ensureExternalTriggerDailyBudget(trigger, now.Time); err != nil {
		writeAPIError(writer, http.StatusTooManyRequests, "daily_limit", err.Error())
		return
	}
	if err := ensureExternalTriggerConcurrency(request.Context(), writerClient, trigger); err != nil {
		writeAPIError(writer, http.StatusConflict, "concurrency_forbid", err.Error())
		return
	}

	delivery := externalTriggerDeliveryContext{
		DeliveryID:  deliveryID,
		EventType:   eventType,
		Repository:  repo,
		Action:      strings.TrimSpace(payload.Action),
		Summary:     summary,
		AcceptedAt:  now,
		PayloadHint: truncateRunes(summary, externalTriggerSummaryMaxRunes),
	}

	runRefs, err := server.deliverExternalTrigger(request.Context(), writerClient, trigger, delivery)
	if err != nil {
		server.recordExternalTriggerError(request.Context(), writerClient, trigger, err.Error())
		server.log.Error(err, "deliver AgentExternalTrigger", "namespace", namespace, "name", name, "deliveryID", deliveryID)
		writeAPIError(writer, http.StatusServiceUnavailable, "delivery_failed", "delivery failed")
		return
	}

	if err := server.recordExternalTriggerSuccess(request.Context(), writerClient, trigger, delivery, runRefs); err != nil {
		server.log.Error(err, "update AgentExternalTrigger status", "namespace", namespace, "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "status_update_failed", "delivery accepted but status update failed")
		return
	}

	writeJSON(writer, http.StatusAccepted, map[string]any{
		"accepted":   true,
		"duplicate":  false,
		"deliveryID": deliveryID,
		"runRefs":    runRefs,
	})
}

func readExternalTriggerBody(request *http.Request) ([]byte, error) {
	defer request.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(request.Body, externalTriggerMaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(raw) > externalTriggerMaxBodyBytes {
		return nil, fmt.Errorf("request body exceeds %d bytes", externalTriggerMaxBodyBytes)
	}
	return raw, nil
}

func (server *Server) loadExternalTriggerSecrets(ctx context.Context, kube client.Client, trigger *agentsv1alpha1.AgentExternalTrigger) (webhookSecret, pathToken string, err error) {
	secretName := strings.TrimSpace(trigger.Spec.SecretRef.Name)
	if secretName == "" {
		return "", "", fmt.Errorf("secretRef.name is required")
	}
	secret := &corev1.Secret{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: trigger.Namespace, Name: secretName}, secret); err != nil {
		return "", "", err
	}
	key := strings.TrimSpace(trigger.Spec.SecretRef.WebhookSecretKey)
	if key == "" {
		key = agentsv1alpha1.DefaultWebhookSecretKey
	}
	raw, ok := secret.Data[key]
	if !ok || len(raw) == 0 {
		return "", "", fmt.Errorf("secret %q key %q is missing or empty", secretName, key)
	}
	webhookSecret = string(raw)
	if pathKey := strings.TrimSpace(trigger.Spec.SecretRef.PathTokenKey); pathKey != "" {
		tokenRaw, ok := secret.Data[pathKey]
		if !ok || len(tokenRaw) == 0 {
			return "", "", fmt.Errorf("secret %q path token key %q is missing or empty", secretName, pathKey)
		}
		pathToken = string(tokenRaw)
	}
	return webhookSecret, pathToken, nil
}

func parseGitHubWebhookPayload(body []byte) (githubWebhookPayload, string, string, error) {
	var payload githubWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return githubWebhookPayload{}, "", "", err
	}
	repo := strings.TrimSpace(payload.Repository.FullName)
	if repo == "" {
		owner := strings.TrimSpace(payload.Repository.Owner.Login)
		name := strings.TrimSpace(payload.Repository.Name)
		if owner != "" && name != "" {
			repo = owner + "/" + name
		}
	}
	summary := buildGitHubPayloadSummary(payload, repo)
	return payload, repo, summary, nil
}

func buildGitHubPayloadSummary(payload githubWebhookPayload, repo string) string {
	parts := []string{}
	if repo != "" {
		parts = append(parts, "repo="+repo)
	}
	if action := strings.TrimSpace(payload.Action); action != "" {
		parts = append(parts, "action="+action)
	}
	if payload.PullRequest.Number > 0 {
		parts = append(parts, fmt.Sprintf("pr=#%d", payload.PullRequest.Number))
		if title := strings.TrimSpace(payload.PullRequest.Title); title != "" {
			parts = append(parts, "title="+truncateRunes(title, 120))
		}
	}
	if payload.Issue.Number > 0 {
		parts = append(parts, fmt.Sprintf("issue=#%d", payload.Issue.Number))
		if title := strings.TrimSpace(payload.Issue.Title); title != "" {
			parts = append(parts, "title="+truncateRunes(title, 120))
		}
	}
	if ref := strings.TrimSpace(payload.Ref); ref != "" {
		parts = append(parts, "ref="+ref)
	}
	if sender := strings.TrimSpace(payload.Sender.Login); sender != "" {
		parts = append(parts, "sender="+sender)
	}
	if comment := strings.TrimSpace(payload.Comment.Body); comment != "" {
		parts = append(parts, "comment="+truncateRunes(comment, 160))
	}
	if len(parts) == 0 {
		return "github webhook delivery"
	}
	return truncateRunes(strings.Join(parts, " "), externalTriggerSummaryMaxRunes)
}

func githubRepositoryAllowed(github *agentsv1alpha1.AgentExternalTriggerGitHubSpec, repo string) bool {
	if github == nil {
		return false
	}
	repo = strings.ToLower(strings.TrimSpace(repo))
	if repo == "" {
		return false
	}
	for _, allowed := range github.Repositories {
		if strings.ToLower(strings.TrimSpace(allowed)) == repo {
			return true
		}
	}
	return false
}

func githubEventAllowed(github *agentsv1alpha1.AgentExternalTriggerGitHubSpec, eventType string) bool {
	if github == nil {
		return false
	}
	if len(github.Events) == 0 {
		return true
	}
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	for _, allowed := range github.Events {
		if strings.ToLower(strings.TrimSpace(allowed)) == eventType {
			return true
		}
	}
	return false
}

func externalTriggerSeenDelivery(seen []string, deliveryID string) bool {
	deliveryID = strings.TrimSpace(deliveryID)
	for _, id := range seen {
		if id == deliveryID {
			return true
		}
	}
	return false
}

func ensureExternalTriggerDailyBudget(trigger *agentsv1alpha1.AgentExternalTrigger, now time.Time) error {
	limit := trigger.Spec.MaxDeliveriesPerDay
	if limit <= 0 {
		return nil
	}
	day := now.UTC().Format("2006-01-02")
	count := trigger.Status.DeliveriesToday
	if trigger.Status.DeliveriesTodayDate != day {
		count = 0
	}
	if count >= limit {
		return fmt.Errorf("maxDeliveriesPerDay %d reached for %s", limit, day)
	}
	return nil
}

func ensureExternalTriggerConcurrency(ctx context.Context, kube client.Client, trigger *agentsv1alpha1.AgentExternalTrigger) error {
	policy := trigger.Spec.ConcurrencyPolicy
	if policy == "" {
		policy = agentsv1alpha1.AgentExternalTriggerConcurrencyForbid
	}
	if policy != agentsv1alpha1.AgentExternalTriggerConcurrencyForbid {
		return nil
	}
	list := &agentsv1alpha1.AgentRunList{}
	if err := kube.List(ctx, list,
		client.InNamespace(trigger.Namespace),
		client.MatchingLabels{agentsv1alpha1.AgentExternalTriggerLabel: sanitizeLabelValue(trigger.Name)},
	); err != nil {
		return err
	}
	for i := range list.Items {
		if !agentRunPhaseTerminal(list.Items[i].Status.Phase) {
			return fmt.Errorf("active AgentRun %s blocks Forbid concurrency", list.Items[i].Name)
		}
	}
	return nil
}

func (server *Server) deliverExternalTrigger(ctx context.Context, kube client.Client, trigger *agentsv1alpha1.AgentExternalTrigger, delivery externalTriggerDeliveryContext) ([]agentsv1alpha1.NamespacedObjectReference, error) {
	refs := make([]agentsv1alpha1.NamespacedObjectReference, 0)
	for _, target := range trigger.Spec.Targets {
		created, err := server.deliverExternalTriggerTarget(ctx, kube, trigger, target, delivery)
		if err != nil {
			return nil, err
		}
		refs = append(refs, created...)
	}
	return refs, nil
}

func (server *Server) deliverExternalTriggerTarget(ctx context.Context, kube client.Client, trigger *agentsv1alpha1.AgentExternalTrigger, target agentsv1alpha1.AgentExternalTriggerTargetSpec, delivery externalTriggerDeliveryContext) ([]agentsv1alpha1.NamespacedObjectReference, error) {
	switch target.Kind {
	case agentsv1alpha1.AgentExternalTriggerTargetAgentRunProfile:
		run, err := server.createExternalTriggerRun(ctx, kube, trigger, delivery, strings.TrimSpace(target.Name), "", nil)
		if err != nil {
			return nil, err
		}
		return []agentsv1alpha1.NamespacedObjectReference{{Name: run.Name, Namespace: run.Namespace}}, nil
	case agentsv1alpha1.AgentExternalTriggerTargetAgentCouncil:
		return server.deliverExternalTriggerCouncil(ctx, kube, trigger, target, delivery)
	case agentsv1alpha1.AgentExternalTriggerTargetAgentRun:
		return server.deliverExternalTriggerAgentRunTarget(ctx, kube, trigger, target, delivery)
	default:
		return nil, fmt.Errorf("unsupported target kind %q", target.Kind)
	}
}

func (server *Server) deliverExternalTriggerCouncil(ctx context.Context, kube client.Client, trigger *agentsv1alpha1.AgentExternalTrigger, target agentsv1alpha1.AgentExternalTriggerTargetSpec, delivery externalTriggerDeliveryContext) ([]agentsv1alpha1.NamespacedObjectReference, error) {
	council := &agentsv1alpha1.AgentCouncil{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: trigger.Namespace, Name: strings.TrimSpace(target.Name)}, council); err != nil {
		return nil, err
	}
	deliveryMode := strings.TrimSpace(target.CouncilDelivery)
	if deliveryMode == "" || deliveryMode == string(agentsv1alpha1.AgentExternalTriggerCouncilDeliveryAllMembers) {
		deliveryMode = string(agentsv1alpha1.AgentExternalTriggerCouncilDeliveryAllMembers)
	}
	refs := make([]agentsv1alpha1.NamespacedObjectReference, 0)
	matched := 0
	for _, member := range council.Spec.Members {
		role := strings.TrimSpace(member.Role)
		profileName := strings.TrimSpace(member.ProfileRef.Name)
		if profileName == "" {
			continue
		}
		if member.ProfileRef.Namespace != "" && member.ProfileRef.Namespace != trigger.Namespace {
			return nil, fmt.Errorf("council member profileRef namespace must be empty or %s", trigger.Namespace)
		}
		if deliveryMode != string(agentsv1alpha1.AgentExternalTriggerCouncilDeliveryAllMembers) && role != deliveryMode {
			continue
		}
		matched++
		note := fmt.Sprintf("Council %s member role %q.", council.Name, role)
		run, err := server.createExternalTriggerRun(ctx, kube, trigger, delivery, profileName, note, &agentsv1alpha1.NamespacedObjectReference{Name: council.Name})
		if err != nil {
			return nil, err
		}
		refs = append(refs, agentsv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace})
	}
	if matched == 0 {
		if deliveryMode == string(agentsv1alpha1.AgentExternalTriggerCouncilDeliveryAllMembers) {
			return nil, fmt.Errorf("council %s has no member profiles", council.Name)
		}
		return nil, fmt.Errorf("council %s has no member with role %q", council.Name, deliveryMode)
	}
	return refs, nil
}

func (server *Server) deliverExternalTriggerAgentRunTarget(ctx context.Context, kube client.Client, trigger *agentsv1alpha1.AgentExternalTrigger, target agentsv1alpha1.AgentExternalTriggerTargetSpec, delivery externalTriggerDeliveryContext) ([]agentsv1alpha1.NamespacedObjectReference, error) {
	existing := &agentsv1alpha1.AgentRun{}
	err := kube.Get(ctx, types.NamespacedName{Namespace: trigger.Namespace, Name: strings.TrimSpace(target.Name)}, existing)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil && !agentRunPhaseTerminal(existing.Status.Phase) {
		if err := annotateAgentRunWithExternalTriggerDelivery(ctx, kube, existing, trigger, delivery); err != nil {
			return nil, err
		}
		return []agentsv1alpha1.NamespacedObjectReference{{Name: existing.Name, Namespace: existing.Namespace}}, nil
	}

	profileName := ""
	if err == nil && existing.Spec.ProfileRef != nil {
		profileName = strings.TrimSpace(existing.Spec.ProfileRef.Name)
	}
	if profileName == "" {
		return nil, fmt.Errorf("AgentRun target %q is missing or terminal without a profileRef to copy", target.Name)
	}
	note := fmt.Sprintf("Successor of AgentRun %s (previous phase %s).", target.Name, string(existing.Status.Phase))
	if apierrors.IsNotFound(err) {
		note = fmt.Sprintf("Successor for missing AgentRun %s.", target.Name)
	}
	var councilRef *agentsv1alpha1.NamespacedObjectReference
	if err == nil && existing.Spec.CouncilRef != nil {
		councilRef = existing.Spec.CouncilRef.DeepCopy()
	}
	run, createErr := server.createExternalTriggerRun(ctx, kube, trigger, delivery, profileName, note, councilRef)
	if createErr != nil {
		return nil, createErr
	}
	return []agentsv1alpha1.NamespacedObjectReference{{Name: run.Name, Namespace: run.Namespace}}, nil
}

func annotateAgentRunWithExternalTriggerDelivery(ctx context.Context, kube client.Client, run *agentsv1alpha1.AgentRun, trigger *agentsv1alpha1.AgentExternalTrigger, delivery externalTriggerDeliveryContext) error {
	original := run.DeepCopy()
	annotations := run.Annotations
	if annotations == nil {
		annotations = map[string]string{}
	}
	payload, err := json.Marshal(map[string]any{
		"triggerName": trigger.Name,
		"deliveryID":  delivery.DeliveryID,
		"eventType":   delivery.EventType,
		"repository":  delivery.Repository,
		"summary":     delivery.PayloadHint,
		"acceptedAt":  delivery.AcceptedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	annotations[agentsv1alpha1.AgentExternalTriggerDeliveryAnnotation] = string(payload)
	run.Annotations = annotations
	return kube.Patch(ctx, run, client.MergeFrom(original))
}

func (server *Server) createExternalTriggerRun(ctx context.Context, kube client.Client, trigger *agentsv1alpha1.AgentExternalTrigger, delivery externalTriggerDeliveryContext, profileName, extraNote string, councilRef *agentsv1alpha1.NamespacedObjectReference) (*agentsv1alpha1.AgentRun, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return nil, fmt.Errorf("profileName is required")
	}
	prompt := renderExternalTriggerPrompt(trigger.Spec.PromptTemplate, delivery)
	if extraNote != "" {
		prompt = strings.TrimSpace(prompt + "\n\n" + extraNote)
	}
	prompt = truncateRunes(prompt, externalTriggerPromptMaxRunes)

	name := externalTriggerRunName(trigger.Name, delivery.DeliveryID, profileName)
	run := &agentsv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: trigger.Namespace,
			Labels: map[string]string{
				agentsv1alpha1.AgentExternalTriggerLabel:         sanitizeLabelValue(trigger.Name),
				agentsv1alpha1.AgentExternalTriggerDeliveryIDLabel: sanitizeLabelValue(shortHash(delivery.DeliveryID)),
				"control.anvil.hazyforge.io/created-by":          "anvil-agents-external-trigger",
			},
		},
		Spec: agentsv1alpha1.AgentRunSpec{
			Purpose: agentsv1alpha1.AgentRunPurposeManual,
			SourceRef: agentsv1alpha1.AgentRunSourceRef{
				APIVersion: agentsv1alpha1.GroupVersion.String(),
				Kind:       "AgentExternalTrigger",
				Namespace:  trigger.Namespace,
				Name:       trigger.Name,
			},
			SourceUID:        string(trigger.UID),
			SourceGeneration: trigger.Generation,
			Trigger: agentsv1alpha1.AgentRunTriggerSnapshot{
				Reason:     "GitHubWebhookDelivery",
				Message:    truncateRunes(fmt.Sprintf("%s %s %s", delivery.EventType, delivery.DeliveryID, delivery.PayloadHint), 2048),
				DetectedAt: &delivery.AcceptedAt,
			},
			Prompt:     prompt,
			ProfileRef: &agentsv1alpha1.NamespacedObjectReference{Name: profileName},
			CouncilRef: councilRef,
		},
	}
	if err := kube.Create(ctx, run); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		existing := &agentsv1alpha1.AgentRun{}
		if getErr := kube.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Name}, existing); getErr != nil {
			return nil, getErr
		}
		return existing, nil
	}
	return run, nil
}

func renderExternalTriggerPrompt(template string, delivery externalTriggerDeliveryContext) string {
	text := strings.TrimSpace(template)
	if text == "" {
		text = externalTriggerDefaultPrompt
	}
	replacer := strings.NewReplacer(
		"{{eventType}}", delivery.EventType,
		"{{repository}}", delivery.Repository,
		"{{deliveryID}}", delivery.DeliveryID,
		"{{summary}}", delivery.PayloadHint,
	)
	return replacer.Replace(text)
}

func externalTriggerRunName(triggerName, deliveryID, profileName string) string {
	hash := shortHash(strings.Join([]string{triggerName, deliveryID, profileName}, "|"))
	return agentRunChildNameLocal("agentrun", triggerName, "gh", hash)
}

func (server *Server) recordExternalTriggerSuccess(ctx context.Context, kube client.Client, trigger *agentsv1alpha1.AgentExternalTrigger, delivery externalTriggerDeliveryContext, runRefs []agentsv1alpha1.NamespacedObjectReference) error {
	original := trigger.DeepCopy()
	fresh := &agentsv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: trigger.Namespace, Name: trigger.Name}, fresh); err != nil {
		return err
	}
	original = fresh.DeepCopy()
	status := fresh.Status
	day := delivery.AcceptedAt.UTC().Format("2006-01-02")
	if status.DeliveriesTodayDate != day {
		status.DeliveriesTodayDate = day
		status.DeliveriesToday = 0
	}
	status.DeliveriesToday++
	status.DeliveryCount++
	status.LastDeliveryID = delivery.DeliveryID
	status.LastDeliveryAt = &delivery.AcceptedAt
	status.LastEventType = delivery.EventType
	status.LastError = ""
	status.LastRunRefs = append([]agentsv1alpha1.NamespacedObjectReference(nil), runRefs...)
	status.SeenDeliveryIDs = appendSeenDeliveryID(status.SeenDeliveryIDs, delivery.DeliveryID)
	if status.Phase == "" || status.Phase == agentsv1alpha1.AgentExternalTriggerPhasePending {
		status.Phase = agentsv1alpha1.AgentExternalTriggerPhaseReady
	}
	fresh.Status = status
	return kube.Status().Patch(ctx, fresh, client.MergeFrom(original))
}

func (server *Server) recordExternalTriggerError(ctx context.Context, kube client.Client, trigger *agentsv1alpha1.AgentExternalTrigger, message string) {
	fresh := &agentsv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: trigger.Namespace, Name: trigger.Name}, fresh); err != nil {
		return
	}
	original := fresh.DeepCopy()
	fresh.Status.LastError = truncateRunes(message, 1024)
	_ = kube.Status().Patch(ctx, fresh, client.MergeFrom(original))
}

func appendSeenDeliveryID(seen []string, deliveryID string) []string {
	if externalTriggerSeenDelivery(seen, deliveryID) {
		return seen
	}
	seen = append(seen, deliveryID)
	if len(seen) > externalTriggerSeenDeliveryLimit {
		seen = seen[len(seen)-externalTriggerSeenDeliveryLimit:]
	}
	return seen
}

func agentRunPhaseTerminal(phase agentsv1alpha1.AgentRunPhase) bool {
	switch phase {
	case agentsv1alpha1.AgentRunPhaseSucceeded, agentsv1alpha1.AgentRunPhaseFailed, agentsv1alpha1.AgentRunPhaseNeedsHuman:
		return true
	default:
		return false
	}
}

func truncateRunes(value string, max int) string {
	if max <= 0 || value == "" {
		return ""
	}
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func agentRunChildNameLocal(parts ...string) string {
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		token := sanitizeLabelValue(part)
		if token != "" {
			tokens = append(tokens, token)
		}
	}
	name := strings.Join(tokens, "-")
	if len(name) <= 63 {
		return name
	}
	suffix := tokens[len(tokens)-1]
	if len(suffix) < 63 {
		prefixMax := 63 - len(suffix) - 1
		prefix := strings.Trim(name[:prefixMax], "-")
		return prefix + "-" + suffix
	}
	return suffix[:63]
}

func hmacEqualString(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
