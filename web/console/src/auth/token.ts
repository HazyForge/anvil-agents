/**
 * @deprecated Manual paste auth was replaced by OIDC PKCE.
 * Re-exports kept so accidental imports fail closed to empty session helpers.
 */
export { clearSession as clearToken, loadSession } from "./session";

export function loadToken(): string {
  return "";
}

export function saveToken(_token: string): void {
  // no-op — use OIDC PKCE session helpers
}
