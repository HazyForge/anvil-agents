package substrate

import (
	"os"
	"strconv"
	"strings"
)

// Feature-gate environment variables for the optional live Substrate actor
// plane. The plane is off by default: the controller holds well-formed
// SubstrateActor runs without creating a Job until the gate is explicitly
// enabled. No chart values or Primaris Argo sync changes are involved.
//
// The transport is Substrate ATE's real surface (ateapi Control gRPC,
// kubectl-ate lifecycle); see live.go. The endpoint is therefore the ateapi
// target, not an HTTP gateway origin.
const (
	// GateEnabledEnvVar opts the controller into live actor dispatch when set
	// to a truthy value ("1", "true", "yes", "on"). Anything else keeps the
	// API-first hold.
	GateEnabledEnvVar = "ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED"
	// GateEndpointEnvVar carries the ateapi gRPC target backing the live
	// Client (kubectl-ate --endpoint parallel), e.g.
	// "ate-api-server.ate-system.svc:443" in cluster or a localhost
	// port-forward for Kind spikes. An endpoint alone never enables dispatch.
	GateEndpointEnvVar = "ANVIL_AGENTS_SUBSTRATE_ENDPOINT"
	// GateTokenEnvVar optionally carries a bearer token for ateapi. Prefer
	// GateTokenFileEnvVar: the token is only ever attached to the gRPC
	// channel by the dialer and must never appear in status, logs, or
	// API JSON.
	GateTokenEnvVar = "ANVIL_AGENTS_SUBSTRATE_TOKEN"
	// GateTokenFileEnvVar points at a file holding the ateapi bearer token
	// (kubectl-ate --token-file parallel). Preferred over the inline token
	// for secret-mounted deployments.
	GateTokenFileEnvVar = "ANVIL_AGENTS_SUBSTRATE_TOKEN_FILE"
	// GateAtespaceEnvVar optionally forces every actor into one atespace.
	// Empty (default) maps each Kubernetes namespace to the same-named
	// atespace.
	GateAtespaceEnvVar = "ANVIL_AGENTS_SUBSTRATE_ATESPACE"
	// GateTemplateEnvVar is the default ActorTemplate used when an actor spec
	// leaves ActorClass empty (e.g. "standing-chat"). Required for live
	// dispatch because CreateActor always derives from a template.
	GateTemplateEnvVar = "ANVIL_AGENTS_SUBSTRATE_TEMPLATE"
	// GateInsecureEnvVar opts into plaintext gRPC for a local Kind
	// port-forward when the podcert trust bundle is not yet wired locally.
	// Kind-only: the dialer refuses every non-loopback endpoint when this is
	// set. Production and shared clusters must use verified TLS (default).
	GateInsecureEnvVar = "ANVIL_AGENTS_SUBSTRATE_INSECURE"
)

// GateConfig is the explicit opt-in configuration for live Substrate actor
// dispatch. Zero value is disabled.
type GateConfig struct {
	Enabled  bool
	Endpoint string
	Token    string
	// TokenFile is the path to the file holding the ateapi bearer token.
	TokenFile string
	// Atespace forces one atespace for every actor; empty maps namespaces
	// to same-named atespaces.
	Atespace string
	// Template is the default ActorTemplate for specs without an actor class.
	Template string
	// Insecure dials plaintext gRPC for a local Kind port-forward.
	// Kind-only: the dialer refuses every non-loopback endpoint when set.
	Insecure bool
}

// GateConfigFromEnv reads the live-plane gate from the process environment.
// The gate defaults to off; an endpoint alone never enables dispatch.
func GateConfigFromEnv() GateConfig {
	return GateConfig{
		Enabled:   gateBoolEnv(GateEnabledEnvVar),
		Endpoint:  strings.TrimSpace(os.Getenv(GateEndpointEnvVar)),
		Token:     strings.TrimSpace(os.Getenv(GateTokenEnvVar)),
		TokenFile: strings.TrimSpace(os.Getenv(GateTokenFileEnvVar)),
		Atespace:  strings.TrimSpace(os.Getenv(GateAtespaceEnvVar)),
		Template:  strings.TrimSpace(os.Getenv(GateTemplateEnvVar)),
		Insecure:  gateBoolEnv(GateInsecureEnvVar),
	}
}

// LiveEnabled reports whether live dispatch may proceed. Both the explicit
// opt-in and a configured endpoint are required so a stray truthy env var
// without a reachable backend keeps the safe hold.
func (g GateConfig) LiveEnabled() bool {
	return g.Enabled && strings.TrimSpace(g.Endpoint) != ""
}

func gateBoolEnv(name string) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if raw == "" {
		return false
	}
	if enabled, err := strconv.ParseBool(raw); err == nil {
		return enabled
	}
	return raw == "on" || raw == "yes"
}

// ThreadIDForRun resolves the stable chat thread identity backing one
// AgentRun. Chat turns (direct and peer-child alike) carry SourceRef
// Kind=ChatThread with the thread ID as the name, so both map onto the same
// actor convention. Non-chat runs fall back to the run name so the mapping
// stays total without inventing cross-thread identity.
func ThreadIDForRun(sourceKind, sourceName, runName string) string {
	if strings.TrimSpace(sourceKind) == "ChatThread" && strings.TrimSpace(sourceName) != "" {
		return strings.TrimSpace(sourceName)
	}
	return strings.TrimSpace(runName)
}

// ActorSpecForRun builds the actor lifecycle spec for one AgentRun execution.
// The caller supplies the already-resolved harness adapter kind and the CRD
// tuning fields so this package never imports Anvil API types.
func ActorSpecForRun(namespace, actorName, harnessKind, actorClass, pool string, extraLabels map[string]string) ActorSpec {
	labels := map[string]string{}
	for key, value := range extraLabels {
		if strings.TrimSpace(key) == "" {
			continue
		}
		labels[key] = value
	}
	return ActorSpec{
		Namespace:   strings.TrimSpace(namespace),
		Name:        strings.TrimSpace(actorName),
		HarnessKind: strings.TrimSpace(harnessKind),
		ActorClass:  strings.TrimSpace(actorClass),
		Pool:        strings.TrimSpace(pool),
		Labels:      labels,
	}
}

// ShouldSuspendOnIdle reports whether the actor worker should be released when
// the turn goes idle. Nil defaults to true to match the CRD intent: idle chat
// actors multiplex back onto the warm pool instead of holding workers.
func ShouldSuspendOnIdle(suspendOnIdle *bool) bool {
	if suspendOnIdle == nil {
		return true
	}
	return *suspendOnIdle
}
