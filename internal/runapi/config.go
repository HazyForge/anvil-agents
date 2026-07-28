package runapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	PermissionRunsRead         = "anvil-agents:runs:read"
	PermissionRunsStream       = "anvil-agents:runs:stream"
	PermissionRunsCreate       = "anvil-agents:runs:create"
	PermissionRunsPurge        = "anvil-agents:runs:purge"
	PermissionCompositionRead  = "anvil-agents:composition:read"
	PermissionCompositionWrite = "anvil-agents:composition:write"
)

// knownPermissions is the closed set of OIDC permissions the API accepts.
var knownPermissions = map[string]struct{}{
	PermissionRunsRead:         {},
	PermissionRunsStream:       {},
	PermissionRunsCreate:       {},
	PermissionRunsPurge:        {},
	PermissionCompositionRead:  {},
	PermissionCompositionWrite: {},
}

type Config struct {
	BindAddress   string              `json:"bindAddress"`
	OIDC          OIDCConfig          `json:"oidc"`
	Authorization AuthorizationConfig `json:"authorization"`
	CORS          CORSConfig          `json:"cors"`
	List          ListConfig          `json:"list"`
	Stream        StreamConfig        `json:"stream"`
	UI            UIConfig            `json:"ui"`
	// Composition gates library read/write endpoints. Write remains opt-in.
	Composition CompositionConfig `json:"composition"`
	// Runs gates append-only AgentRun create from the console/API.
	Runs RunsConfig `json:"runs"`
}

// RunsConfig controls AgentRun mutation endpoints. Creates remain append-only.
type RunsConfig struct {
	// CreateEnabled serves POST /agent-runs when true.
	CreateEnabled bool `json:"createEnabled"`
	// PurgeEnabled serves POST /agent-runs/purge when true. Purge only deletes
	// terminal live CRs that already have a successful PostgreSQL archive.
	PurgeEnabled bool `json:"purgeEnabled"`
}

// CompositionConfig controls composition library endpoints. GitOps remains the
// source of truth for objects that are not console-managed; see management
// evaluation in composition_management.go.
type CompositionConfig struct {
	// ReadEnabled serves GET list/detail for composition CRDs when true.
	ReadEnabled bool `json:"readEnabled"`
	// WriteEnabled serves POST/PUT/DELETE for console-managed composition
	// objects when true. Requires ReadEnabled.
	WriteEnabled bool `json:"writeEnabled"`
}

// UIConfig controls console static serving and public browser OIDC settings.
// By default the API serves the embedded Vite build (or in-tree stub).
// OIDC client fields are non-secret SPA configuration only.
type UIConfig struct {
	// StaticDir, when set, serves console assets from this filesystem path
	// instead of the embedded dist. Useful for local iteration without
	// recompiling the API binary after `make console-build`.
	StaticDir string `json:"staticDir"`
	// ProductTitle is shown in the console chrome and ui-config.json.
	ProductTitle string `json:"productTitle"`
	// DefaultNamespaces seeds the namespace switcher for operators.
	DefaultNamespaces []string `json:"defaultNamespaces"`
	// OIDC holds public Authorization Code + PKCE client settings.
	OIDC UIOIDCConfig `json:"oidc"`
}

// UIOIDCConfig is served to the browser as non-secret OIDC client config.
type UIOIDCConfig struct {
	// ClientID is the public SPA / user-agent OIDC client id.
	ClientID string `json:"clientId"`
	// Scopes are requested during authorization. Empty uses console defaults.
	Scopes []string `json:"scopes"`
}

type ListConfig struct {
	MaxItems int64 `json:"maxItems"`
}

type OIDCConfig struct {
	Issuer                   string   `json:"issuer"`
	Audiences                []string `json:"audiences"`
	AllowedSigningAlgorithms []string `json:"allowedSigningAlgorithms"`
	AdditionalJWKSHosts      []string `json:"additionalJWKSHosts"`
	CAFile                   string   `json:"caFile"`
	AllowInsecureIssuer      bool     `json:"allowInsecureIssuer"`
	DiscoveryRetryInterval   Duration `json:"discoveryRetryInterval"`
	DiscoveryRefreshInterval Duration `json:"discoveryRefreshInterval"`
	DiscoveryRequestTimeout  Duration `json:"discoveryRequestTimeout"`
}

type AuthorizationConfig struct {
	ScopeClaims      []string               `json:"scopeClaims"`
	RoleClaims       []string               `json:"roleClaims"`
	RoleObjectClaims []string               `json:"roleObjectClaims"`
	GroupClaims      []string               `json:"groupClaims"`
	NamespaceClaim   string                 `json:"namespaceClaim"`
	Bindings         []AuthorizationBinding `json:"bindings"`
}

type AuthorizationBinding struct {
	Name                string   `json:"name"`
	Roles               []string `json:"roles"`
	Groups              []string `json:"groups"`
	Subjects            []string `json:"subjects"`
	Emails              []string `json:"emails"`
	Scopes              []string `json:"scopes"`
	Permissions         []string `json:"permissions"`
	Namespaces          []string `json:"namespaces"`
	NamespacesFromClaim bool     `json:"namespacesFromClaim"`
}

type CORSConfig struct {
	AllowedOrigins []string `json:"allowedOrigins"`
}

type StreamConfig struct {
	HeartbeatInterval        Duration `json:"heartbeatInterval"`
	StatusPollInterval       Duration `json:"statusPollInterval"`
	MaxDuration              Duration `json:"maxDuration"`
	DefaultTailLines         int64    `json:"defaultTailLines"`
	MaxTailLines             int64    `json:"maxTailLines"`
	MaxLogBytes              int64    `json:"maxLogBytes"`
	MaxLineBytes             int      `json:"maxLineBytes"`
	MaxConnections           int      `json:"maxConnections"`
	MaxConnectionsPerSubject int      `json:"maxConnectionsPerSubject"`
}

type Duration struct {
	time.Duration
}

func NewDuration(value time.Duration) Duration {
	return Duration{Duration: value}
}

func (duration *Duration) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("duration must be a string such as 15s: %w", err)
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return err
	}
	duration.Duration = parsed
	return nil
}

func (duration Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(duration.String())
}

func DefaultConfig() Config {
	return Config{
		BindAddress: ":8082",
		OIDC: OIDCConfig{
			AllowedSigningAlgorithms: []string{"RS256", "ES256"},
			DiscoveryRetryInterval:   NewDuration(15 * time.Second),
			DiscoveryRefreshInterval: NewDuration(time.Hour),
			DiscoveryRequestTimeout:  NewDuration(10 * time.Second),
		},
		Authorization: AuthorizationConfig{
			ScopeClaims:    []string{"scope", "scp"},
			RoleClaims:     []string{"roles"},
			GroupClaims:    []string{"groups"},
			NamespaceClaim: "anvil_agents_namespaces",
		},
		List: ListConfig{MaxItems: 5000},
		Stream: StreamConfig{
			HeartbeatInterval:        NewDuration(15 * time.Second),
			StatusPollInterval:       NewDuration(time.Second),
			MaxDuration:              NewDuration(15 * time.Minute),
			DefaultTailLines:         200,
			MaxTailLines:             10_000,
			MaxLogBytes:              4 * 1024 * 1024,
			MaxLineBytes:             1024 * 1024,
			MaxConnections:           50,
			MaxConnectionsPerSubject: 5,
		},
	}
}

func LoadConfig(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read API config: %w", err)
	}
	config := DefaultConfig()
	if err := yaml.UnmarshalStrict(contents, &config); err != nil {
		return Config{}, fmt.Errorf("decode API config: %w", err)
	}
	config.normalize()
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config *Config) normalize() {
	config.BindAddress = strings.TrimSpace(config.BindAddress)
	config.UI.StaticDir = strings.TrimSpace(config.UI.StaticDir)
	config.OIDC.Issuer = strings.TrimSpace(config.OIDC.Issuer)
	config.OIDC.Audiences = uniqueStrings(config.OIDC.Audiences, false)
	config.OIDC.AllowedSigningAlgorithms = uniqueStrings(config.OIDC.AllowedSigningAlgorithms, false)
	config.OIDC.AdditionalJWKSHosts = uniqueStrings(config.OIDC.AdditionalJWKSHosts, true)
	config.OIDC.CAFile = strings.TrimSpace(config.OIDC.CAFile)
	config.Authorization.ScopeClaims = uniqueStrings(config.Authorization.ScopeClaims, false)
	config.Authorization.RoleClaims = uniqueStrings(config.Authorization.RoleClaims, false)
	config.Authorization.RoleObjectClaims = uniqueStrings(config.Authorization.RoleObjectClaims, false)
	config.Authorization.GroupClaims = uniqueStrings(config.Authorization.GroupClaims, false)
	config.Authorization.NamespaceClaim = strings.TrimSpace(config.Authorization.NamespaceClaim)
	config.CORS.AllowedOrigins = uniqueStrings(config.CORS.AllowedOrigins, false)
	for i := range config.Authorization.Bindings {
		binding := &config.Authorization.Bindings[i]
		binding.Name = strings.TrimSpace(binding.Name)
		binding.Roles = uniqueStrings(binding.Roles, false)
		binding.Groups = uniqueStrings(binding.Groups, false)
		binding.Subjects = uniqueStrings(binding.Subjects, false)
		binding.Emails = uniqueStrings(binding.Emails, true)
		binding.Scopes = uniqueStrings(binding.Scopes, false)
		binding.Permissions = uniqueStrings(binding.Permissions, false)
		binding.Namespaces = uniqueStrings(binding.Namespaces, false)
	}
}

func (config Config) Validate() error {
	if config.BindAddress == "" {
		return fmt.Errorf("bindAddress is required")
	}
	issuer, err := url.Parse(config.OIDC.Issuer)
	if err != nil || issuer.Host == "" || issuer.RawQuery != "" || issuer.Fragment != "" {
		return fmt.Errorf("oidc.issuer must be an absolute URL without query or fragment")
	}
	if issuer.Scheme != "https" && !(config.OIDC.AllowInsecureIssuer && issuer.Scheme == "http") {
		return fmt.Errorf("oidc.issuer must use HTTPS unless allowInsecureIssuer is explicitly enabled")
	}
	if len(config.OIDC.Audiences) == 0 {
		return fmt.Errorf("oidc.audiences must contain at least one API audience")
	}
	if len(config.OIDC.AllowedSigningAlgorithms) == 0 {
		return fmt.Errorf("oidc.allowedSigningAlgorithms must not be empty")
	}
	if config.OIDC.DiscoveryRetryInterval.Duration <= 0 || config.OIDC.DiscoveryRefreshInterval.Duration <= 0 || config.OIDC.DiscoveryRequestTimeout.Duration <= 0 {
		return fmt.Errorf("OIDC discovery durations must be positive")
	}
	if len(config.Authorization.Bindings) == 0 {
		return fmt.Errorf("authorization.bindings must contain at least one explicit grant")
	}
	for i, binding := range config.Authorization.Bindings {
		name := binding.Name
		if name == "" {
			name = fmt.Sprintf("binding[%d]", i)
		}
		if len(binding.Roles)+len(binding.Groups)+len(binding.Subjects)+len(binding.Emails)+len(binding.Scopes) == 0 {
			return fmt.Errorf("authorization %s must have at least one role, group, subject, email, or scope selector", name)
		}
		if len(binding.Permissions) == 0 {
			return fmt.Errorf("authorization %s must grant at least one permission", name)
		}
		for _, permission := range binding.Permissions {
			if _, ok := knownPermissions[permission]; !ok {
				return fmt.Errorf("authorization %s grants unsupported permission %q", name, permission)
			}
			if permission == PermissionCompositionRead || permission == PermissionCompositionWrite {
				if !config.Composition.ReadEnabled && permission == PermissionCompositionRead {
					return fmt.Errorf("authorization %s grants %s but composition.readEnabled is false", name, permission)
				}
				if permission == PermissionCompositionWrite && !config.Composition.WriteEnabled {
					return fmt.Errorf("authorization %s grants %s but composition.writeEnabled is false", name, permission)
				}
			}
			if permission == PermissionRunsCreate && !config.Runs.CreateEnabled {
				return fmt.Errorf("authorization %s grants %s but runs.createEnabled is false", name, permission)
			}
			if permission == PermissionRunsPurge && !config.Runs.PurgeEnabled {
				return fmt.Errorf("authorization %s grants %s but runs.purgeEnabled is false", name, permission)
			}
		}
		if len(binding.Namespaces) == 0 && !binding.NamespacesFromClaim {
			return fmt.Errorf("authorization %s must grant namespaces or enable namespacesFromClaim", name)
		}
		if binding.NamespacesFromClaim && config.Authorization.NamespaceClaim == "" {
			return fmt.Errorf("authorization %s enables namespacesFromClaim but authorization.namespaceClaim is empty", name)
		}
	}
	for _, origin := range config.CORS.AllowedOrigins {
		if origin == "*" {
			return fmt.Errorf("cors.allowedOrigins must not contain a wildcard")
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("cors.allowedOrigins contains invalid origin %q", origin)
		}
	}
	if config.Stream.HeartbeatInterval.Duration <= 0 || config.Stream.StatusPollInterval.Duration <= 0 || config.Stream.MaxDuration.Duration <= 0 {
		return fmt.Errorf("stream durations must be positive")
	}
	if config.List.MaxItems < 1 || config.List.MaxItems > 100_000 {
		return fmt.Errorf("list.maxItems must be between 1 and 100000")
	}
	if config.Stream.DefaultTailLines < 0 || config.Stream.MaxTailLines < 1 || config.Stream.DefaultTailLines > config.Stream.MaxTailLines {
		return fmt.Errorf("stream tail line limits are invalid")
	}
	if config.Stream.MaxLogBytes < 1 || config.Stream.MaxLineBytes < 1024 {
		return fmt.Errorf("stream byte limits are invalid")
	}
	if config.Stream.MaxConnections < 1 || config.Stream.MaxConnectionsPerSubject < 1 || config.Stream.MaxConnectionsPerSubject > config.Stream.MaxConnections {
		return fmt.Errorf("stream connection limits are invalid")
	}
	if config.Composition.WriteEnabled && !config.Composition.ReadEnabled {
		return fmt.Errorf("composition.writeEnabled requires composition.readEnabled")
	}
	return nil
}

func uniqueStrings(values []string, lower bool) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		}
		if value != "" && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}
