/**
 * OIDC Authorization Code + PKCE (public SPA client).
 * Tokens stay in sessionStorage; never placed in query strings after callback cleanup.
 */

import { loadUIConfig, type UIConfig } from "./config";
import { clearLegacyToken, clearSession, loadSession, saveSession, type Session } from "./session";

const PKCE_KEY = "anvil-agents-console.pkce";
const RETURN_KEY = "anvil-agents-console.returnTo";

type Discovery = {
  authorization_endpoint: string;
  token_endpoint: string;
  end_session_endpoint?: string;
};

type StoredPKCE = {
  state: string;
  verifier: string;
  nonce: string;
  redirectUri: string;
};

type TokenResponse = {
  access_token: string;
  refresh_token?: string;
  expires_in?: number;
  id_token?: string;
  token_type?: string;
  error?: string;
  error_description?: string;
};

let discoveryCache: { issuer: string; doc: Discovery } | null = null;

function b64url(bytes: ArrayBuffer | Uint8Array): string {
  const arr = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  let binary = "";
  for (let i = 0; i < arr.length; i++) {
    binary += String.fromCharCode(arr[i]!);
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function randomString(bytes = 32): string {
  const buf = new Uint8Array(bytes);
  crypto.getRandomValues(buf);
  return b64url(buf);
}

async function sha256(input: string): Promise<string> {
  const data = new TextEncoder().encode(input);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return b64url(digest);
}

function storePKCE(value: StoredPKCE): void {
  sessionStorage.setItem(PKCE_KEY, JSON.stringify(value));
}

function takePKCE(): StoredPKCE | null {
  try {
    const raw = sessionStorage.getItem(PKCE_KEY);
    sessionStorage.removeItem(PKCE_KEY);
    if (!raw) {
      return null;
    }
    return JSON.parse(raw) as StoredPKCE;
  } catch {
    return null;
  }
}

export function setReturnPath(path: string): void {
  try {
    sessionStorage.setItem(RETURN_KEY, path);
  } catch {
    /* ignore */
  }
}

export function takeReturnPath(): string {
  try {
    const value = sessionStorage.getItem(RETURN_KEY) || "/";
    sessionStorage.removeItem(RETURN_KEY);
    return value.startsWith("/") ? value : "/";
  } catch {
    return "/";
  }
}

async function discover(issuer: string): Promise<Discovery> {
  if (discoveryCache?.issuer === issuer) {
    return discoveryCache.doc;
  }
  const url = `${issuer.replace(/\/+$/, "")}/.well-known/openid-configuration`;
  const response = await fetch(url, { headers: { Accept: "application/json" } });
  if (!response.ok) {
    throw new Error(`OIDC discovery failed (${response.status})`);
  }
  const doc = (await response.json()) as Discovery;
  if (!doc.authorization_endpoint || !doc.token_endpoint) {
    throw new Error("OIDC discovery missing authorization/token endpoints");
  }
  discoveryCache = { issuer, doc };
  return doc;
}

function redirectUri(): string {
  return `${window.location.origin}/auth/callback`;
}

export async function beginLogin(returnTo = "/"): Promise<void> {
  const config = await loadUIConfig();
  if (!config.oidc.clientId) {
    throw new Error("OIDC client is not configured");
  }
  const discovery = await discover(config.oidc.issuer);
  const verifier = randomString(32);
  const challenge = await sha256(verifier);
  const state = randomString(16);
  const nonce = randomString(16);
  const redirect = redirectUri();
  storePKCE({ state, verifier, nonce, redirectUri: redirect });
  setReturnPath(returnTo);

  const url = new URL(discovery.authorization_endpoint);
  url.searchParams.set("response_type", "code");
  url.searchParams.set("client_id", config.oidc.clientId);
  url.searchParams.set("redirect_uri", redirect);
  url.searchParams.set("scope", config.oidc.scopes.join(" "));
  url.searchParams.set("state", state);
  url.searchParams.set("nonce", nonce);
  url.searchParams.set("code_challenge", challenge);
  url.searchParams.set("code_challenge_method", "S256");
  // Prefer ZITADEL custom login when configured on the instance.
  window.location.assign(url.toString());
}

async function exchangeToken(
  config: UIConfig,
  body: URLSearchParams,
): Promise<Session> {
  const discovery = await discover(config.oidc.issuer);
  const response = await fetch(discovery.token_endpoint, {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded",
    },
    body,
  });
  const payload = (await response.json()) as TokenResponse;
  if (!response.ok || !payload.access_token) {
    throw new Error(payload.error_description || payload.error || `token exchange failed (${response.status})`);
  }
  const expiresIn = typeof payload.expires_in === "number" ? payload.expires_in : 3600;
  const session: Session = {
    accessToken: payload.access_token,
    refreshToken: payload.refresh_token ?? "",
    expiresAt: Date.now() + expiresIn * 1000,
  };
  saveSession(session);
  clearLegacyToken();
  return session;
}

export async function completeLoginFromCallback(search: string): Promise<Session> {
  const params = new URLSearchParams(search.startsWith("?") ? search.slice(1) : search);
  const error = params.get("error");
  if (error) {
    throw new Error(params.get("error_description") || error);
  }
  const code = params.get("code");
  const state = params.get("state");
  if (!code || !state) {
    throw new Error("missing authorization code or state");
  }
  const pkce = takePKCE();
  if (!pkce || pkce.state !== state) {
    throw new Error("invalid or expired login state — try signing in again");
  }
  const config = await loadUIConfig();
  const body = new URLSearchParams({
    grant_type: "authorization_code",
    code,
    redirect_uri: pkce.redirectUri,
    client_id: config.oidc.clientId,
    code_verifier: pkce.verifier,
  });
  return exchangeToken(config, body);
}

export async function refreshSession(session: Session): Promise<Session> {
  if (!session.refreshToken) {
    throw new Error("no refresh token");
  }
  const config = await loadUIConfig();
  const body = new URLSearchParams({
    grant_type: "refresh_token",
    refresh_token: session.refreshToken,
    client_id: config.oidc.clientId,
  });
  const next = await exchangeToken(config, body);
  // Some providers omit refresh_token on refresh; keep the previous one.
  if (!next.refreshToken) {
    next.refreshToken = session.refreshToken;
    saveSession(next);
  }
  return next;
}

/** Returns a valid access token, refreshing when within 60s of expiry. */
export async function ensureAccessToken(): Promise<string | null> {
  const session = loadSession();
  if (!session?.accessToken) {
    return null;
  }
  if (session.expiresAt && session.expiresAt - Date.now() > 60_000) {
    return session.accessToken;
  }
  if (!session.refreshToken) {
    return session.accessToken;
  }
  try {
    const next = await refreshSession(session);
    return next.accessToken;
  } catch {
    clearSession();
    return null;
  }
}

export async function logout(): Promise<void> {
  clearSession();
  clearLegacyToken();
  try {
    const config = await loadUIConfig();
    const discovery = await discover(config.oidc.issuer);
    if (discovery.end_session_endpoint) {
      const url = new URL(discovery.end_session_endpoint);
      url.searchParams.set("client_id", config.oidc.clientId);
      url.searchParams.set("post_logout_redirect_uri", `${window.location.origin}/`);
      window.location.assign(url.toString());
      return;
    }
  } catch {
    /* fall through to local clear */
  }
  window.location.assign("/");
}

export function currentAccessToken(): string {
  return loadSession()?.accessToken ?? "";
}
