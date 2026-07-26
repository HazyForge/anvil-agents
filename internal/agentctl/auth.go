package agentctl

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	authExitDiagnosedUnhealthy = 3
	codexAuthJSONKey           = "CODEX_AUTH_JSON"
	codexAuthSeedKey           = "CODEX_AUTH_SEED_ID"
	grokAuthJSONKey            = "GROK_AUTH_JSON"
	grokAuthSeedKey            = "GROK_AUTH_SEED_ID"
	xaiAPIKeyKey               = "XAI_API_KEY"
	openaiAPIKeyKey            = "OPENAI_API_KEY"
	authStagingOwnerLabel      = "control.anvil.hazyforge.io/agent-auth-staging-for"
)

type authProviderProfile struct {
	CLIName             string
	Provider            agentsv1alpha1.AgentAuthSessionProvider
	AuthJSONKey         string
	SeedKey             string
	APIKeyKey           string // optional bootstrap/apiKey mode key
	DefaultAuthFileHint string
	DisplayName         string
	Component           string
}

func resolveAuthProvider(name string) (authProviderProfile, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "codex", "openai":
		return authProviderProfile{
			CLIName:             "codex",
			Provider:            agentsv1alpha1.AgentAuthSessionProviderCodex,
			AuthJSONKey:         codexAuthJSONKey,
			SeedKey:             codexAuthSeedKey,
			APIKeyKey:           openaiAPIKeyKey,
			DefaultAuthFileHint: "~/.codex/auth.json",
			DisplayName:         "OpenAI Codex",
			Component:           "codex-auth-staging",
		}, nil
	case "grok", "grokbuild", "xai":
		return authProviderProfile{
			CLIName:             "grok",
			Provider:            agentsv1alpha1.AgentAuthSessionProviderGrokBuild,
			AuthJSONKey:         grokAuthJSONKey,
			SeedKey:             grokAuthSeedKey,
			APIKeyKey:           xaiAPIKeyKey,
			DefaultAuthFileHint: "~/.grok/auth.json",
			DisplayName:         "xAI Grok",
			Component:           "grok-auth-staging",
		}, nil
	default:
		return authProviderProfile{}, fmt.Errorf("unknown auth provider %q (want codex|openai or grok|xai)", name)
	}
}

type codexAuthFile struct {
	AuthMode     string `json:"auth_mode"`
	LastRefresh  string `json:"last_refresh"`
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	Tokens       *struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

type providerAuthSummary struct {
	Provider           string `json:"provider,omitempty"`
	AuthMode           string `json:"authMode,omitempty"`
	HasAccessToken     bool   `json:"hasAccessToken"`
	HasRefreshToken    bool   `json:"hasRefreshToken"`
	HasAPIKey          bool   `json:"hasAPIKey"`
	AccessTokenExpired *bool  `json:"accessTokenExpired,omitempty"`
	AccessTokenExp     string `json:"accessTokenExp,omitempty"`
	LastRefresh        string `json:"lastRefresh,omitempty"`
	EntryCount         int    `json:"entryCount,omitempty"`
	ValidJSON          bool   `json:"validJSON"`
	Error              string `json:"error,omitempty"`
}

// codexAuthSummary is retained as an alias shape for older tests.
type codexAuthSummary = providerAuthSummary

func (app App) runAuth(ctx context.Context, kubeOptions KubeOptions, args []string) error {
	if len(args) == 0 {
		writeAuthUsage(app.Err)
		return &usageError{message: "an auth provider is required"}
	}
	profile, err := resolveAuthProvider(args[0])
	if err != nil {
		writeAuthUsage(app.Err)
		return &usageError{message: err.Error()}
	}
	if len(args) < 2 {
		writeAuthProviderUsage(app.Err, profile)
		return &usageError{message: fmt.Sprintf("a %s auth command is required", profile.CLIName)}
	}
	switch args[1] {
	case "diagnose":
		return app.authProviderDiagnose(ctx, kubeOptions, profile, args[2:])
	case "reauth":
		return app.authProviderReauth(ctx, kubeOptions, profile, args[2:])
	case "logout":
		return app.authProviderLogout(ctx, kubeOptions, profile, args[2:])
	case "help":
		writeAuthProviderUsage(app.Out, profile)
		return nil
	default:
		writeAuthProviderUsage(app.Err, profile)
		return &usageError{message: fmt.Sprintf("unknown %s auth command %q", profile.CLIName, args[1])}
	}
}

func writeAuthUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Auth commands: codex (openai), grok (xai)")
	writeAuthProviderUsage(writer, mustAuthProfile("codex"))
	writeAuthProviderUsage(writer, mustAuthProfile("grok"))
}

func mustAuthProfile(name string) authProviderProfile {
	profile, err := resolveAuthProvider(name)
	if err != nil {
		panic(err)
	}
	return profile
}

func writeAuthProviderUsage(writer io.Writer, profile authProviderProfile) {
	fmt.Fprintf(writer, "%s auth commands: diagnose, reauth, logout\n", profile.DisplayName)
	fmt.Fprintf(writer, "  anvil-agentctl auth %s diagnose -n NS --data-volume NAME [--bootstrap-secret NAME] [--auth-file PATH]\n", profile.CLIName)
	fmt.Fprintf(writer, "  anvil-agentctl auth %s reauth -n NS --data-volume NAME --auth-file PATH [--bootstrap-secret NAME]\n", profile.CLIName)
	fmt.Fprintf(writer, "  anvil-agentctl auth %s logout -n NS --data-volume NAME --confirm-volume NAME\n", profile.CLIName)
}

func (app App) authProviderDiagnose(ctx context.Context, kubeOptions KubeOptions, profile authProviderProfile, args []string) error {
	var namespace, dataVolume, bootstrapSecret, authFile, output string
	flags := newCommandFlags(fmt.Sprintf("auth %s diagnose", profile.CLIName), app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace (required).")
	flags.StringVar(&dataVolume, "data-volume", "", "AgentDataVolume name that holds the durable auth home.")
	flags.StringVar(&bootstrapSecret, "bootstrap-secret", "", "Optional bootstrap Secret name.")
	flags.StringVar(&authFile, "auth-file", "", fmt.Sprintf("Optional local auth.json (for example %s).", profile.DefaultAuthFileHint))
	flags.StringVarP(&output, "output", "o", "summary", "Output format: summary or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: fmt.Sprintf("auth %s diagnose does not accept positional arguments", profile.CLIName)}
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return &usageError{message: "--namespace is required"}
	}
	if output != "summary" && output != "json" {
		return &usageError{message: `--output must be "summary" or "json"`}
	}

	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	report := map[string]any{
		"namespace": namespace,
		"provider":  string(profile.Provider),
		"cliName":   profile.CLIName,
		"healthy":   true,
		"findings":  []string{},
	}
	findings := make([]string, 0)
	unhealthy := false

	if authFile != "" {
		summary, err := summarizeProviderAuthFile(profile, authFile)
		if err != nil {
			unhealthy = true
			findings = append(findings, "local auth file: "+err.Error())
			report["localAuth"] = providerAuthSummary{Provider: string(profile.Provider), ValidJSON: false, Error: err.Error()}
		} else {
			report["localAuth"] = summary
			if !summary.ValidJSON {
				unhealthy = true
				findings = append(findings, "local auth file is invalid")
			}
			if summary.AccessTokenExpired != nil && *summary.AccessTokenExpired {
				findings = append(findings, "local access/session material looks expired (refresh token may still be valid)")
			}
			if !summary.HasRefreshToken && !summary.HasAPIKey {
				unhealthy = true
				findings = append(findings, "local auth file has neither refresh material nor API key")
			}
		}
	}

	if name := strings.TrimSpace(bootstrapSecret); name != "" {
		secret, err := backend.GetSecret(ctx, namespace, name)
		if err != nil {
			unhealthy = true
			findings = append(findings, fmt.Sprintf("bootstrap secret %s: %v", name, err))
			report["bootstrapSecret"] = map[string]any{"name": name, "present": false}
		} else {
			keys := make([]string, 0, len(secret.Data))
			for key := range secret.Data {
				keys = append(keys, key)
			}
			managedByESO := secretManagedByExternalSecrets(secret)
			hasAuth := len(secret.Data[profile.AuthJSONKey]) > 0
			hasSeed := len(secret.Data[profile.SeedKey]) > 0
			hasAPIKey := profile.APIKeyKey != "" && len(secret.Data[profile.APIKeyKey]) > 0
			entry := map[string]any{
				"name":               name,
				"present":            true,
				"keys":               keys,
				"hasAuthJSON":        hasAuth,
				"authJSONKey":        profile.AuthJSONKey,
				"hasSeedID":          hasSeed,
				"hasAPIKey":          hasAPIKey,
				"apiKeyKey":          profile.APIKeyKey,
				"managedByESO":       managedByESO,
				"resourceVersion":    secret.ResourceVersion,
			}
			if !hasAuth && !hasAPIKey {
				unhealthy = true
				findings = append(findings, fmt.Sprintf("bootstrap secret %s is missing both %s and %s", name, profile.AuthJSONKey, profile.APIKeyKey))
			} else if !hasAuth && hasAPIKey {
				findings = append(findings, fmt.Sprintf("bootstrap secret %s has %s (apiKey mode); durable OAuth home reauth is not required for that mode", name, profile.APIKeyKey))
			}
			if managedByESO {
				findings = append(findings, fmt.Sprintf("bootstrap secret %s appears ExternalSecret-managed; reauth will refuse to patch it—use a CLI-owned seed Secret", name))
			}
			if hasAuth {
				summary := summarizeProviderAuthBytes(profile, secret.Data[profile.AuthJSONKey])
				entry["authSummary"] = summary
				if !summary.ValidJSON {
					unhealthy = true
					findings = append(findings, "bootstrap secret auth JSON is invalid")
				}
			}
			report["bootstrapSecret"] = entry
		}
	}

	if name := strings.TrimSpace(dataVolume); name != "" {
		volume, err := backend.GetDataVolume(ctx, namespace, name)
		if err != nil {
			unhealthy = true
			findings = append(findings, fmt.Sprintf("data volume %s: %v", name, err))
			report["dataVolume"] = map[string]any{"name": name, "present": false}
		} else {
			claimName := ""
			if volume.Status.ClaimRef != nil {
				claimName = volume.Status.ClaimRef.Name
			}
			report["dataVolume"] = map[string]any{
				"name":      volume.Name,
				"present":   true,
				"phase":     volume.Status.Phase,
				"backend":   volume.Spec.Backend,
				"mountPath": firstNonEmpty(volume.Status.MountPath, volume.Spec.MountPath, "/codex-home"),
				"claimName": claimName,
				"claimUID":  volume.Status.ClaimUID,
			}
			if volume.Status.Phase != agentsv1alpha1.AgentDataVolumePhaseReady {
				unhealthy = true
				findings = append(findings, fmt.Sprintf("data volume %s phase is %s", name, valueOrDash(string(volume.Status.Phase))))
			}
		}
		sessions, err := backend.ListAuthSessions(ctx, namespace)
		if err != nil {
			findings = append(findings, fmt.Sprintf("list auth sessions: %v", err))
		} else {
			active := make([]string, 0)
			for i := range sessions.Items {
				session := &sessions.Items[i]
				if strings.TrimSpace(session.Spec.DataVolumeRef.Name) != name {
					continue
				}
				if agentsv1alpha1.AgentAuthSessionIsTerminal(session.Status.Phase) {
					continue
				}
				active = append(active, session.Name+"="+string(session.Status.Phase))
			}
			report["activeAuthSessions"] = active
			if len(active) > 0 {
				findings = append(findings, "active auth sessions: "+strings.Join(active, ", "))
			}
		}
	} else {
		findings = append(findings, "durable home not inspected; pass --data-volume to include AgentDataVolume status")
	}

	findings = append(findings, "durable auth.json is authoritative once present; secret refresh alone does not overwrite it")
	switch profile.Provider {
	case agentsv1alpha1.AgentAuthSessionProviderCodex:
		findings = append(findings, "OpenAI path: after local `codex login --device-auth`, run auth codex reauth with ~/.codex/auth.json")
	case agentsv1alpha1.AgentAuthSessionProviderGrokBuild:
		findings = append(findings, "xAI path: seed OAuth from local ~/.grok/auth.json via auth grok reauth, or supply XAI_API_KEY for apiKey mode")
	}
	report["findings"] = findings
	report["healthy"] = !unhealthy

	if output == "json" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintf(app.Out, "%s\n", body)
	} else {
		fmt.Fprintf(app.Out, "%s AUTH DIAGNOSE namespace=%s provider=%s healthy=%t\n", strings.ToUpper(profile.CLIName), namespace, profile.Provider, !unhealthy)
		for _, finding := range findings {
			fmt.Fprintf(app.Out, "- %s\n", terminalSafe(finding))
		}
	}
	if unhealthy {
		return &diagnosedUnhealthyError{message: fmt.Sprintf("%s auth diagnosis found unhealthy state", profile.CLIName)}
	}
	return nil
}

type diagnosedUnhealthyError struct {
	message string
}

func (err *diagnosedUnhealthyError) Error() string { return err.message }

func (app App) authProviderReauth(ctx context.Context, kubeOptions KubeOptions, profile authProviderProfile, args []string) error {
	var namespace, dataVolume, bootstrapSecret, authFile, output string
	var timeout, waitForIdle time.Duration
	timeout = 5 * time.Minute
	waitForIdle = 10 * time.Minute
	flags := newCommandFlags(fmt.Sprintf("auth %s reauth", profile.CLIName), app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace (required).")
	flags.StringVar(&dataVolume, "data-volume", "", "AgentDataVolume name for the durable auth home (required).")
	flags.StringVar(&bootstrapSecret, "bootstrap-secret", "", "Optional CLI-owned seed Secret to update after success.")
	flags.StringVar(&authFile, "auth-file", "", fmt.Sprintf("Local auth.json (for example %s) (required).", profile.DefaultAuthFileHint))
	flags.DurationVar(&timeout, "timeout", timeout, "How long to wait for the AgentAuthSession to finish.")
	flags.DurationVar(&waitForIdle, "wait-for-idle", waitForIdle, "Reserved for operators; the controller waits for active runs independently.")
	flags.StringVarP(&output, "output", "o", "summary", "Output format: summary or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: fmt.Sprintf("auth %s reauth does not accept positional arguments", profile.CLIName)}
	}
	namespace = strings.TrimSpace(namespace)
	dataVolume = strings.TrimSpace(dataVolume)
	authFile = strings.TrimSpace(authFile)
	if namespace == "" || dataVolume == "" || authFile == "" {
		return &usageError{message: "--namespace, --data-volume, and --auth-file are required"}
	}
	_ = waitForIdle

	body, err := os.ReadFile(authFile)
	if err != nil {
		return fmt.Errorf("read auth file: %w", err)
	}
	if summary := summarizeProviderAuthBytes(profile, body); !summary.ValidJSON {
		return fmt.Errorf("auth file is not valid %s auth JSON: %s", profile.DisplayName, summary.Error)
	} else if !summary.HasRefreshToken && !summary.HasAPIKey {
		return fmt.Errorf("auth file has neither refresh material nor API key for %s", profile.DisplayName)
	}

	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	if _, err := backend.GetDataVolume(ctx, namespace, dataVolume); err != nil {
		return err
	}
	if name := strings.TrimSpace(bootstrapSecret); name != "" {
		if secret, err := backend.GetSecret(ctx, namespace, name); err == nil && secretManagedByExternalSecrets(secret) {
			return fmt.Errorf("bootstrap Secret %s/%s appears ExternalSecret-managed; refuse to race ESO. Omit --bootstrap-secret or use a CLI-owned seed Secret", namespace, name)
		} else if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	seedID, err := newAuthSeedID()
	if err != nil {
		return err
	}
	sessionName := authSessionName(string(profile.Provider)+"-reauth", dataVolume)
	stagingName := authStagingSecretName(sessionName)
	staging := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      stagingName,
			Namespace: namespace,
			Labels: map[string]string{
				authStagingOwnerLabel:                            sanitizeLabelValue(sessionName),
				"app.kubernetes.io/managed-by":                   "anvil-agentctl",
				"app.kubernetes.io/component":                    profile.Component,
				"control.anvil.hazyforge.io/agent-auth-provider": string(profile.Provider),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			profile.AuthJSONKey: body,
			profile.SeedKey:     []byte(seedID),
		},
	}
	if err := backend.CreateSecret(ctx, staging); err != nil {
		return err
	}

	session := &agentsv1alpha1.AgentAuthSession{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentAuthSession"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      sessionName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":                   "anvil-agentctl",
				"control.anvil.hazyforge.io/agent-auth-provider": string(profile.Provider),
				"control.anvil.hazyforge.io/agent-auth-action":   "reauth",
			},
		},
		Spec: agentsv1alpha1.AgentAuthSessionSpec{
			Provider:         profile.Provider,
			Action:           agentsv1alpha1.AgentAuthSessionActionReauth,
			DataVolumeRef:    corev1.LocalObjectReference{Name: dataVolume},
			StagingSecretRef: &corev1.LocalObjectReference{Name: stagingName},
			SeedID:           seedID,
		},
	}
	if name := strings.TrimSpace(bootstrapSecret); name != "" {
		session.Spec.BootstrapSecretRef = &corev1.LocalObjectReference{Name: name}
		session.Spec.BootstrapSecretKey = profile.AuthJSONKey
	}
	if err := backend.CreateAuthSession(ctx, session); err != nil {
		_ = backend.DeleteSecret(ctx, namespace, stagingName)
		return err
	}

	final, err := app.waitAuthSession(ctx, backend, namespace, sessionName, timeout)
	if err != nil {
		return err
	}
	return writeAuthSessionResult(app.Out, final, output)
}

func (app App) authProviderLogout(ctx context.Context, kubeOptions KubeOptions, profile authProviderProfile, args []string) error {
	var namespace, dataVolume, confirmVolume, output string
	var timeout time.Duration = 2 * time.Minute
	flags := newCommandFlags(fmt.Sprintf("auth %s logout", profile.CLIName), app.Err)
	flags.StringVarP(&namespace, "namespace", "n", "", "Namespace (required).")
	flags.StringVar(&dataVolume, "data-volume", "", "AgentDataVolume name (required).")
	flags.StringVar(&confirmVolume, "confirm-volume", "", "Must equal --data-volume to confirm durable logout.")
	flags.DurationVar(&timeout, "timeout", timeout, "How long to wait for the AgentAuthSession to finish.")
	flags.StringVarP(&output, "output", "o", "summary", "Output format: summary or json.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &usageError{message: fmt.Sprintf("auth %s logout does not accept positional arguments", profile.CLIName)}
	}
	namespace = strings.TrimSpace(namespace)
	dataVolume = strings.TrimSpace(dataVolume)
	confirmVolume = strings.TrimSpace(confirmVolume)
	if namespace == "" || dataVolume == "" {
		return &usageError{message: "--namespace and --data-volume are required"}
	}
	if confirmVolume != dataVolume {
		return &usageError{message: "--confirm-volume must exactly match --data-volume"}
	}

	backend, err := app.Factory(kubeOptions)
	if err != nil {
		return err
	}
	if _, err := backend.GetDataVolume(ctx, namespace, dataVolume); err != nil {
		return err
	}
	sessionName := authSessionName(string(profile.Provider)+"-logout", dataVolume)
	session := &agentsv1alpha1.AgentAuthSession{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentAuthSession"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      sessionName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":                   "anvil-agentctl",
				"control.anvil.hazyforge.io/agent-auth-provider": string(profile.Provider),
				"control.anvil.hazyforge.io/agent-auth-action":   "logout",
			},
		},
		Spec: agentsv1alpha1.AgentAuthSessionSpec{
			Provider:      profile.Provider,
			Action:        agentsv1alpha1.AgentAuthSessionActionLogout,
			DataVolumeRef: corev1.LocalObjectReference{Name: dataVolume},
		},
	}
	if err := backend.CreateAuthSession(ctx, session); err != nil {
		return err
	}
	final, err := app.waitAuthSession(ctx, backend, namespace, sessionName, timeout)
	if err != nil {
		return err
	}
	return writeAuthSessionResult(app.Out, final, output)
}

func (app App) waitAuthSession(ctx context.Context, backend Backend, namespace, name string, timeout time.Duration) (*agentsv1alpha1.AgentAuthSession, error) {
	deadline := time.Now().Add(timeout)
	var last *agentsv1alpha1.AgentAuthSession
	for {
		session, err := backend.GetAuthSession(ctx, namespace, name)
		if err != nil {
			return nil, err
		}
		last = session
		if agentsv1alpha1.AgentAuthSessionIsTerminal(session.Status.Phase) {
			if session.Status.Phase == agentsv1alpha1.AgentAuthSessionPhaseFailed {
				return session, fmt.Errorf("AgentAuthSession %s/%s failed: %s", namespace, name, firstNonEmpty(session.Status.LastError, string(session.Status.Phase)))
			}
			return session, nil
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("AgentAuthSession %s/%s did not finish within %s (phase=%s)", namespace, name, timeout, valueOrDash(string(session.Status.Phase)))
		}
		timer := time.NewTimer(app.pollInterval())
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func writeAuthSessionResult(writer io.Writer, session *agentsv1alpha1.AgentAuthSession, output string) error {
	if output == "json" {
		return writeObject(writer, session, "json")
	}
	fmt.Fprintf(writer, "AgentAuthSession %s/%s phase=%s action=%s volume=%s\n",
		session.Namespace, session.Name, valueOrDash(string(session.Status.Phase)), session.Spec.Action, session.Spec.DataVolumeRef.Name)
	if session.Status.JobRef != nil {
		fmt.Fprintf(writer, "Job: %s\n", session.Status.JobRef.Name)
	}
	if session.Status.SeedID != "" {
		fmt.Fprintf(writer, "SeedID: %s\n", terminalSafe(session.Status.SeedID))
	}
	if session.Status.LastError != "" {
		fmt.Fprintf(writer, "Error: %s\n", terminalSafe(session.Status.LastError))
	}
	return nil
}

func summarizeProviderAuthFile(profile authProviderProfile, path string) (providerAuthSummary, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return providerAuthSummary{}, err
	}
	return summarizeProviderAuthBytes(profile, body), nil
}

func summarizeCodexAuthBytes(body []byte) providerAuthSummary {
	return summarizeProviderAuthBytes(mustAuthProfile("codex"), body)
}

func summarizeProviderAuthBytes(profile authProviderProfile, body []byte) providerAuthSummary {
	switch profile.Provider {
	case agentsv1alpha1.AgentAuthSessionProviderGrokBuild:
		return summarizeGrokAuthBytes(body)
	default:
		return summarizeCodexAuthBytesOnly(body)
	}
}

func summarizeCodexAuthBytesOnly(body []byte) providerAuthSummary {
	summary := providerAuthSummary{Provider: string(agentsv1alpha1.AgentAuthSessionProviderCodex)}
	var parsed codexAuthFile
	if err := json.Unmarshal(body, &parsed); err != nil {
		summary.Error = "invalid JSON"
		return summary
	}
	summary.ValidJSON = true
	summary.AuthMode = strings.TrimSpace(parsed.AuthMode)
	summary.LastRefresh = strings.TrimSpace(parsed.LastRefresh)
	summary.HasAPIKey = strings.TrimSpace(parsed.OpenAIAPIKey) != ""
	if parsed.Tokens != nil {
		summary.HasAccessToken = strings.TrimSpace(parsed.Tokens.AccessToken) != ""
		summary.HasRefreshToken = strings.TrimSpace(parsed.Tokens.RefreshToken) != ""
		if exp, ok := jwtExpiry(parsed.Tokens.AccessToken); ok {
			summary.AccessTokenExp = exp.UTC().Format(time.RFC3339)
			expired := time.Now().After(exp)
			summary.AccessTokenExpired = &expired
		}
	}
	return summary
}

func summarizeGrokAuthBytes(body []byte) providerAuthSummary {
	summary := providerAuthSummary{Provider: string(agentsv1alpha1.AgentAuthSessionProviderGrokBuild)}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		summary.Error = "invalid JSON"
		return summary
	}
	summary.ValidJSON = true
	summary.EntryCount = len(root)
	// xAI auth.json is a map of issuer/client keys to credential objects.
	for _, value := range root {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if mode, _ := entry["auth_mode"].(string); mode != "" && summary.AuthMode == "" {
			summary.AuthMode = mode
		}
		if refresh, _ := entry["refresh_token"].(string); strings.TrimSpace(refresh) != "" {
			summary.HasRefreshToken = true
		}
		if key, _ := entry["key"].(string); strings.TrimSpace(key) != "" {
			summary.HasAPIKey = true
		}
		if access, _ := entry["access_token"].(string); strings.TrimSpace(access) != "" {
			summary.HasAccessToken = true
			if exp, ok := jwtExpiry(access); ok {
				summary.AccessTokenExp = exp.UTC().Format(time.RFC3339)
				expired := time.Now().After(exp)
				summary.AccessTokenExpired = &expired
			}
		}
		if expiresRaw, ok := entry["expires_at"]; ok && summary.AccessTokenExpired == nil {
			switch typed := expiresRaw.(type) {
			case string:
				if exp, err := time.Parse(time.RFC3339, typed); err == nil {
					summary.AccessTokenExp = exp.UTC().Format(time.RFC3339)
					expired := time.Now().After(exp)
					summary.AccessTokenExpired = &expired
				}
			case float64:
				exp := time.Unix(int64(typed), 0)
				summary.AccessTokenExp = exp.UTC().Format(time.RFC3339)
				expired := time.Now().After(exp)
				summary.AccessTokenExpired = &expired
			}
		}
	}
	// Also accept a flat api-key style file: {"XAI_API_KEY":"..."} or {"key":"..."}.
	if key, _ := root["XAI_API_KEY"].(string); strings.TrimSpace(key) != "" {
		summary.HasAPIKey = true
	}
	if key, _ := root["key"].(string); strings.TrimSpace(key) != "" {
		summary.HasAPIKey = true
	}
	if refresh, _ := root["refresh_token"].(string); strings.TrimSpace(refresh) != "" {
		summary.HasRefreshToken = true
	}
	return summary
}

func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some tokens use standard base64 padding.
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return time.Time{}, false
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

func secretManagedByExternalSecrets(secret *corev1.Secret) bool {
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

func newAuthSeedID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("seed-%x", raw[:]), nil
}

func authSessionName(action, volume string) string {
	suffix := fmt.Sprintf("%d", time.Now().Unix())
	base := sanitizeLabelValue(action + "-" + volume + "-" + suffix)
	if len(base) > 63 {
		base = strings.Trim(base[:63], "-")
	}
	return base
}

func authStagingSecretName(sessionName string) string {
	name := "auth-staging-" + sanitizeLabelValue(sessionName)
	if len(name) > 63 {
		name = strings.Trim(name[:63], "-")
	}
	return name
}

