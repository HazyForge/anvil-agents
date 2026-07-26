/**
 * Bearer token storage for Phase 1 manual paste auth.
 *
 * Uses sessionStorage so the token survives reloads within the tab but is
 * cleared when the tab/window closes. localStorage would persist across
 * sessions (convenient, higher XSS dwell-time). Tokens are never placed in
 * query strings or the URL path.
 */
const TOKEN_KEY = "anvil-agents-console.bearerToken";

export function loadToken(): string {
  try {
    return sessionStorage.getItem(TOKEN_KEY)?.trim() ?? "";
  } catch {
    return "";
  }
}

export function saveToken(token: string): void {
  const value = token.trim();
  try {
    if (!value) {
      sessionStorage.removeItem(TOKEN_KEY);
      return;
    }
    sessionStorage.setItem(TOKEN_KEY, value);
  } catch {
    // private mode / blocked storage — caller still holds token in React state
  }
}

export function clearToken(): void {
  saveToken("");
}
