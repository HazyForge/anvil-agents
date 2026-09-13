import type { DelegateResult, Prefs, Snapshot } from "./types";

export class APIError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.code = code;
  }

  static async fromResponse(response: Response): Promise<APIError> {
    return readAPIError(response);
  }
}

/** Empty string = same-origin relative paths through the desktop host proxy. */
export function apiBase(): string {
  const configured = (import.meta.env.VITE_API_BASE ?? "").trim().replace(/\/+$/, "");
  return configured;
}

export function apiURL(path: string): string {
  const normalized = path.startsWith("/") ? path : `/${path}`;
  const base = apiBase();
  return base ? `${base}${normalized}` : normalized;
}

export async function apiFetch(path: string, token: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set("Authorization", `Bearer ${token}`);
  if (!headers.has("Accept")) {
    headers.set("Accept", "application/json");
  }
  // Never place tokens in query strings.
  return fetch(apiURL(path), { ...init, headers, signal: init.signal });
}

type APIErrorBody = { error?: { code?: string; message?: string } | string; message?: string };

async function readAPIError(response: Response): Promise<APIError> {
  let code = `http_${response.status}`;
  let message = response.statusText || `HTTP ${response.status}`;
  try {
    const body = (await response.json()) as APIErrorBody;
    if (typeof body.error === "string") {
      code = body.error;
    } else if (body.error?.code) {
      code = body.error.code;
    }
    if (typeof body.error === "object" && body.error?.message) {
      message = body.error.message;
    } else if (body.message) {
      message = body.message;
    }
  } catch {
    // non-JSON error body
  }
  if (response.status === 401) {
    message = message || "unauthorized — sign in again";
  } else if (response.status === 403) {
    message = message || "forbidden — origin or authorization denied";
  } else if (response.status === 404) {
    message = message || "not found (or namespace not authorized)";
  }
  return new APIError(response.status, code, message);
}

export async function fetchSnapshot(): Promise<Snapshot> {
  const response = await fetch("/local/v1/snapshot");
  if (!response.ok) {
    throw new Error(`snapshot failed: HTTP ${response.status}`);
  }
  return (await response.json()) as Snapshot;
}

export async function savePrefs(prefs: Prefs): Promise<Snapshot> {
  const response = await fetch("/local/v1/prefs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(prefs),
  });
  if (!response.ok) {
    const text = await response.text();
    throw new Error(text || `save prefs failed: HTTP ${response.status}`);
  }
  return (await response.json()) as Snapshot;
}

export async function delegateHarness(harness: string, prompt: string, timeoutSeconds?: number): Promise<DelegateResult> {
  const response = await fetch("/local/v1/delegate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ harness, prompt, timeoutSeconds }),
  });
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as DelegateResult;
}

export type AgentRunView = {
  name: string;
  namespace: string;
  phase?: string;
  backend?: string;
  intent?: string;
  error?: string;
};

export type CompositionDocument = {
  apiVersion?: string;
  kind: string;
  metadata: {
    name: string;
    namespace: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
  };
  spec?: Record<string, unknown>;
  management?: { writable?: boolean; reason?: string; managedBy?: string };
};

export type CreateAgentRunProfileRequest = {
  name: string;
  description?: string;
  systemPrompt?: string;
  intent?: string;
};

export async function listAgentRuns(
  token: string,
  namespace: string,
  limit = 50,
  signal?: AbortSignal,
): Promise<AgentRunView[]> {
  const params = new URLSearchParams({ limit: String(limit) });
  const response = await apiFetch(
    `/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-runs?${params}`,
    token,
    { signal },
  );
  if (!response.ok) {
    throw await readAPIError(response);
  }
  const body = (await response.json()) as { items?: AgentRunView[] };
  return body.items ?? [];
}

export async function getAgentRun(
  token: string,
  namespace: string,
  name: string,
  signal?: AbortSignal,
): Promise<AgentRunView> {
  const response = await apiFetch(
    `/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-runs/${encodeURIComponent(name)}`,
    token,
    { signal },
  );
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as AgentRunView;
}

export async function createAgentRun(
  token: string,
  namespace: string,
  body: { generateName?: string; name?: string; prompt: string; profileName: string },
  signal?: AbortSignal,
): Promise<unknown> {
  const response = await apiFetch(`/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-runs`, token, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal,
  });
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return response.json() as Promise<unknown>;
}

export async function listRunProfiles(
  token: string,
  namespace: string,
  signal?: AbortSignal,
): Promise<CompositionDocument[]> {
  const params = new URLSearchParams({ limit: "200" });
  const response = await apiFetch(
    `/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-run-profiles?${params}`,
    token,
    { signal },
  );
  if (!response.ok) {
    throw await readAPIError(response);
  }
  const body = (await response.json()) as { items?: CompositionDocument[] };
  return body.items ?? [];
}

export async function getRunProfile(
  token: string,
  namespace: string,
  name: string,
  signal?: AbortSignal,
): Promise<CompositionDocument> {
  const response = await apiFetch(
    `/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-run-profiles/${encodeURIComponent(name)}`,
    token,
    { signal },
  );
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as CompositionDocument;
}

export async function createAgentRunProfile(
  token: string,
  namespace: string,
  body: CreateAgentRunProfileRequest,
): Promise<CompositionDocument> {
  const name = body.name.trim();
  const spec: Record<string, unknown> = {
    description: body.description?.trim() || "desktop-created entity",
  };
  const harness: Record<string, unknown> = {};
  if (body.intent?.trim()) {
    harness.intent = body.intent.trim();
  }
  if (body.systemPrompt?.trim()) {
    harness.systemPrompt = body.systemPrompt.trim();
  }
  if (Object.keys(harness).length > 0) {
    spec.harness = harness;
  }
  const response = await apiFetch(`/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-run-profiles`, token, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      apiVersion: "control.anvil.hazyforge.io/v1alpha1",
      kind: "AgentRunProfile",
      metadata: { name, namespace },
      spec,
    }),
  });
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as CompositionDocument;
}

export async function ensureAgentRunProfile(
  token: string,
  namespace: string,
  body: CreateAgentRunProfileRequest,
): Promise<CompositionDocument> {
  try {
    return await createAgentRunProfile(token, namespace, body);
  } catch (err) {
    if (err instanceof APIError && err.status === 409) {
      return getRunProfile(token, namespace, body.name.trim());
    }
    throw err;
  }
}
