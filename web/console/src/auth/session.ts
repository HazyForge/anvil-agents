/**
 * Browser session for OIDC access/refresh tokens.
 * sessionStorage only — never query strings or localStorage for tokens.
 */

const ACCESS_KEY = "anvil-agents-console.accessToken";
const REFRESH_KEY = "anvil-agents-console.refreshToken";
const EXPIRES_KEY = "anvil-agents-console.expiresAt";

export type Session = {
  accessToken: string;
  refreshToken: string;
  expiresAt: number; // epoch ms
};

function read(key: string): string {
  try {
    return sessionStorage.getItem(key)?.trim() ?? "";
  } catch {
    return "";
  }
}

function write(key: string, value: string): void {
  try {
    if (!value) {
      sessionStorage.removeItem(key);
      return;
    }
    sessionStorage.setItem(key, value);
  } catch {
    // private mode — React state still holds the session
  }
}

export function loadSession(): Session | null {
  const accessToken = read(ACCESS_KEY);
  if (!accessToken) {
    return null;
  }
  const refreshToken = read(REFRESH_KEY);
  const expiresRaw = read(EXPIRES_KEY);
  const expiresAt = expiresRaw ? Number(expiresRaw) : 0;
  return { accessToken, refreshToken, expiresAt: Number.isFinite(expiresAt) ? expiresAt : 0 };
}

export function saveSession(session: Session): void {
  write(ACCESS_KEY, session.accessToken);
  write(REFRESH_KEY, session.refreshToken);
  write(EXPIRES_KEY, String(session.expiresAt));
}

export function clearSession(): void {
  write(ACCESS_KEY, "");
  write(REFRESH_KEY, "");
  write(EXPIRES_KEY, "");
}

/** Legacy manual-token key used by Phase 1 paste auth. */
const LEGACY_TOKEN_KEY = "anvil-agents-console.bearerToken";

export function clearLegacyToken(): void {
  write(LEGACY_TOKEN_KEY, "");
}
