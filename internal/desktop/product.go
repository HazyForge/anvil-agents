package desktop

// ProductTitle is the user-visible product name for this operator surface.
// The process binary and config directory stay anvil-desktop.
// The control plane is the anvil-agents OIDC API, not Kubernetes.
const ProductTitle = "Anvil Agents Desktop"

// DefaultAPIOrigin is the production anvil-agents OIDC API (Anvil Primaris).
// The installed desktop process calls cluster agents here. Kind tests pass
// --api-origin to a loopback issuer instead.
const DefaultAPIOrigin = "https://agents.anvil.hazyforge.io"

// DefaultOIDCClientID is the Kind/desktop PKCE pattern name for tests
// (`--oidc-client-id`). Production Desktop prefers desktop.oidcClientId from
// {apiOrigin}/ui-config.json (Zitadel Native). Do not use this string as a
// silent fallback when the Native id is missing.
const DefaultOIDCClientID = "anvil-agents-desktop"

const (
	HarnessTargetNative = "native"
	HarnessTargetWSL    = "wsl"
)
