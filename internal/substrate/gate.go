package substrate

import (
	"os"
	"strconv"
	"strings"
)

// Feature-gate environment variables for the optional live Substrate actor
// plane. The plane is off by default: the controller holds well-formed
// SubstrateActor runs without creating a Job until the gate is explicitly
// enabled. Chart substrate.* and Primaris deploy.yaml stay off; do not
// set substrate.actorsEnabled=true until generate-on-actor is in the
// live image and an opted-in actorClass (not standing-chat) can complete.
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
	// GateInsecureEnvVar opts into TLS with certificate verification skipped
	// for a local Kind port-forward when the jwt CA or podcert trust bundle
	// is not yet wired locally. ateapi always serves TLS, so this is still a
	// TLS channel — never plaintext. Kind-only: the dialer refuses every
	// non-loopback endpoint when this is set. Production and shared clusters
	// must use verified TLS (default): CAFile or ateapi-ca ConfigMap for
	// ATE 0.0.8 jwt mode.
	GateInsecureEnvVar = "ANVIL_AGENTS_SUBSTRATE_INSECURE"
	// GateCAFileEnvVar points at a PEM file holding the ateapi server CA
	// (ATE Helm jwt ConfigMap ateapi-ca key ca.crt). Preferred in-cluster
	// via a mounted ConfigMap. Path (never PEM bytes) may appear in errors.
	GateCAFileEnvVar = "ANVIL_AGENTS_SUBSTRATE_CA_FILE"
	// GateCAConfigMapEnvVar names the ConfigMap holding that CA when it is
	// not mounted (cross-namespace lookup, default ateapi-ca).
	GateCAConfigMapEnvVar = "ANVIL_AGENTS_SUBSTRATE_CA_CONFIGMAP"
	// GateCAConfigMapNamespaceEnvVar is the namespace of that ConfigMap
	// (default ate-system).
	GateCAConfigMapNamespaceEnvVar = "ANVIL_AGENTS_SUBSTRATE_CA_CONFIGMAP_NAMESPACE"
	// GateCAConfigMapKeyEnvVar is the ConfigMap data key (default ca.crt).
	GateCAConfigMapKeyEnvVar = "ANVIL_AGENTS_SUBSTRATE_CA_CONFIGMAP_KEY"
	// GateTLSServerNameEnvVar overrides TLS ServerName (default
	// api.ate-system.svc, matching ATE jwt bootstrap DNS SAN).
	GateTLSServerNameEnvVar = "ANVIL_AGENTS_SUBSTRATE_TLS_SERVER_NAME"
	// GateGenerateOnActorEnvVar opts the controller into atenet generate
	// after Resume/bind. Off by default. actorsEnabled alone never
	// generates and never binds; Desktop standing-chat stays on
	// ProcessBackend until a matching generateActorClass is listed.
	GateGenerateOnActorEnvVar = "ANVIL_AGENTS_SUBSTRATE_GENERATE_ON_ACTOR"
	// GateGenerateActorClassesEnvVar is the comma-separated actorClass
	// allowlist for generate-on-actor (exact match). Empty generates for
	// nobody. Do not list standing-chat on Primaris.
	GateGenerateActorClassesEnvVar = "ANVIL_AGENTS_SUBSTRATE_GENERATE_ACTOR_CLASSES"
	// GateAtenetEndpointEnvVar is the atenet-router HTTP target
	// (host:port or URL). Empty uses DefaultAtenetEndpoint when generate
	// is on.
	GateAtenetEndpointEnvVar = "ANVIL_AGENTS_SUBSTRATE_ATENET_ENDPOINT"
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
	// Insecure dials TLS with certificate verification skipped for a local
	// Kind port-forward. ateapi always serves TLS, so this is still a TLS
	// channel — never plaintext. Kind-only: the dialer refuses every
	// non-loopback endpoint when set.
	Insecure bool
	// CAFile is the path to the ateapi server CA PEM (jwt ConfigMap
	// ateapi-ca key ca.crt). Preferred over ConfigMap lookup when mounted.
	CAFile string
	// CAConfigMapName names a ConfigMap holding that CA when CAFile is empty.
	CAConfigMapName string
	// CAConfigMapNamespace is the ConfigMap namespace (default ate-system).
	CAConfigMapNamespace string
	// CAConfigMapKey is the ConfigMap data key (default ca.crt).
	CAConfigMapKey string
	// TLSServerName overrides the ateapi TLS ServerName (default
	// api.ate-system.svc).
	TLSServerName string
	// GenerateOnActor streams the frozen AgentRun prompt through atenet
	// after bind and marks the run Succeeded/Failed. Off by default.
	GenerateOnActor bool
	// GenerateActorClasses is the exact actorClass allowlist. Empty means
	// generate for nobody, even when GenerateOnActor is true.
	GenerateActorClasses []string
	// AtenetEndpoint is the atenet-router HTTP target.
	AtenetEndpoint string
}

// GateConfigFromEnv reads the live-plane gate from the process environment.
// The gate defaults to off; an endpoint alone never enables dispatch.
func GateConfigFromEnv() GateConfig {
	return GateConfig{
		Enabled:              gateBoolEnv(GateEnabledEnvVar),
		Endpoint:             strings.TrimSpace(os.Getenv(GateEndpointEnvVar)),
		Token:                strings.TrimSpace(os.Getenv(GateTokenEnvVar)),
		TokenFile:            strings.TrimSpace(os.Getenv(GateTokenFileEnvVar)),
		Atespace:             strings.TrimSpace(os.Getenv(GateAtespaceEnvVar)),
		Template:             strings.TrimSpace(os.Getenv(GateTemplateEnvVar)),
		Insecure:             gateBoolEnv(GateInsecureEnvVar),
		CAFile:               strings.TrimSpace(os.Getenv(GateCAFileEnvVar)),
		CAConfigMapName:      strings.TrimSpace(os.Getenv(GateCAConfigMapEnvVar)),
		CAConfigMapNamespace: strings.TrimSpace(os.Getenv(GateCAConfigMapNamespaceEnvVar)),
		CAConfigMapKey:       strings.TrimSpace(os.Getenv(GateCAConfigMapKeyEnvVar)),
		TLSServerName:        strings.TrimSpace(os.Getenv(GateTLSServerNameEnvVar)),
		GenerateOnActor:      gateBoolEnv(GateGenerateOnActorEnvVar),
		GenerateActorClasses: ParseActorClassList(os.Getenv(GateGenerateActorClassesEnvVar)),
		AtenetEndpoint:       strings.TrimSpace(os.Getenv(GateAtenetEndpointEnvVar)),
	}
}

// LiveEnabled reports whether live dispatch may proceed. Both the explicit
// opt-in and a configured endpoint are required so a stray truthy env var
// without a reachable backend keeps the safe hold.
func (g GateConfig) LiveEnabled() bool {
	return g.Enabled && strings.TrimSpace(g.Endpoint) != ""
}

// GenerateForClass reports whether live generate-on-actor may complete a
// run with this actorClass. Requires the generate gate plus an allowlist
// hit; it does not by itself enable ateapi bind.
func (g GateConfig) GenerateForClass(actorClass string) bool {
	return GenerateEnabled(g.GenerateOnActor, g.GenerateActorClasses, actorClass)
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
