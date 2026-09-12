package desktop

// ProductTitle is the user-visible product name for this operator surface.
// The process binary and config directory stay anvil-desktop.
// The control plane is the anvil-agents OIDC API, not Kubernetes.
const ProductTitle = "Anvil Agents Desktop"

// DefaultAPIOrigin is the production anvil-agents OIDC API (Anvil Primaris).
// The installed desktop process calls cluster agents here. Kind tests pass
// --api-origin to a loopback issuer instead.
const DefaultAPIOrigin = "https://agents.anvil.hazyforge.io"

// DefaultOIDCClientID is the public PKCE client id for Anvil Agents Desktop.
// Production IdP is Zitadel; Kind uses the same client id pattern. Prefer
// desktop.oidcClientId from {apiOrigin}/ui-config.json when GitOps writes it.
const DefaultOIDCClientID = "anvil-agents-desktop"

const (
	HarnessTargetNative = "native"
	HarnessTargetWSL    = "wsl"
)
