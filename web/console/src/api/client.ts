import type {
  AgentRunArchiveListResponse,
  AgentRunArchiveItem,
  AgentRunListResponse,
  AgentRunPurgeRequest,
  AgentRunPurgeResponse,
  AgentRunView,
  APIErrorBody,
  UIConfigRuns,
} from "./types";

export class APIError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.code = code;
  }
}

/** API origin: empty string = same-origin relative paths (production). */
export function apiBase(): string {
  const configured = (import.meta.env.VITE_API_BASE ?? "").trim().replace(/\/+$/, "");
  return configured;
}

export function apiURL(path: string): string {
  const normalized = path.startsWith("/") ? path : `/${path}`;
  const base = apiBase();
  return base ? `${base}${normalized}` : normalized;
}

export async function apiFetch(
  path: string,
  token: string,
  init: RequestInit = {},
): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set("Authorization", `Bearer ${token}`);
  if (!headers.has("Accept")) {
    headers.set("Accept", "application/json");
  }
  // Never place tokens in query strings.
  return fetch(apiURL(path), { ...init, headers, signal: init.signal });
}

async function readAPIError(response: Response): Promise<APIError> {
  let code = `http_${response.status}`;
  let message = response.statusText || `HTTP ${response.status}`;
  try {
    const body = (await response.json()) as APIErrorBody;
    if (body.error?.code) {
      code = body.error.code;
    }
    if (body.error?.message) {
      message = body.error.message;
    }
  } catch {
    // non-JSON error body
  }
  if (response.status === 401) {
    message = message || "unauthorized — paste a valid bearer token";
  } else if (response.status === 403) {
    message = message || "forbidden — origin or authorization denied";
  } else if (response.status === 404) {
    message = message || "not found (or namespace not authorized)";
  } else if (response.status === 422 && code === "list_too_large") {
    message =
      message ||
      "namespace has too many AgentRuns to list safely; lower volume or raise list.maxItems on the API";
  }
  return new APIError(response.status, code, message);
}

export async function listAgentRuns(
  token: string,
  namespace: string,
  limit = 200,
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
  const body = (await response.json()) as AgentRunListResponse;
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

export function eventsURL(namespace: string, name: string, tailLines = 200): string {
  const params = new URLSearchParams({ tailLines: String(tailLines) });
  return apiURL(
    `/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-runs/${encodeURIComponent(name)}/events?${params}`,
  );
}

export async function purgeAgentRuns(
  token: string,
  namespace: string,
  body: AgentRunPurgeRequest,
  signal?: AbortSignal,
): Promise<AgentRunPurgeResponse> {
  const response = await apiFetch(
    `/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-runs/purge`,
    token,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal,
    },
  );
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as AgentRunPurgeResponse;
}

export async function listAgentRunArchives(
  token: string,
  namespace: string,
  limit = 50,
  signal?: AbortSignal,
): Promise<AgentRunArchiveItem[]> {
  const params = new URLSearchParams({ limit: String(limit) });
  const response = await apiFetch(
    `/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-run-archives?${params}`,
    token,
    { signal },
  );
  if (!response.ok) {
    throw await readAPIError(response);
  }
  const body = (await response.json()) as AgentRunArchiveListResponse;
  return body.items ?? [];
}

export async function getUIConfig(signal?: AbortSignal): Promise<{ runs?: UIConfigRuns }> {
  const response = await fetch(apiURL("/ui-config.json"), { signal });
  if (!response.ok) {
    throw new APIError(response.status, `http_${response.status}`, response.statusText);
  }
  return (await response.json()) as { runs?: UIConfigRuns };
}

export type CreateAgentRunBody = {
  generateName?: string;
  name?: string;
  prompt: string;
  profileName: string;
  harnessProfileName?: string;
  skillSetNames?: string[];
  toolSetNames?: string[];
  intent?: string;
  purpose?: string;
  sourceKind?: string;
  sourceName?: string;
};

export async function createAgentRun(
  token: string,
  namespace: string,
  body: CreateAgentRunBody,
  signal?: AbortSignal,
): Promise<AgentRunView> {
  const response = await apiFetch(
    `/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-runs`,
    token,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal,
    },
  );
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as AgentRunView;
}
