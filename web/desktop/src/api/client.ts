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

export type AgentRunReport = {
  type?: string;
  summary?: string;
  detail?: string;
  classification?: string;
  action?: string;
};

export type AgentRunView = {
  name: string;
  namespace: string;
  phase?: string;
  backend?: string;
  intent?: string;
  application?: string;
  error?: string;
  decision?: { action?: string; summary?: string };
  reports?: AgentRunReport[];
  resolvedComposition?: {
    profileRef?: { name?: string };
    harnessProfileRef?: { name?: string };
    scope?: { application?: string };
  };
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

export type CreateAgentRunRequest = {
  generateName?: string;
  name?: string;
  prompt?: string;
  peerPrompt?: string;
  profileName: string;
  harnessProfileName?: string;
  application?: string;
  applicationName?: string;
  backend?: string;
};

/** Opaque Application scope key for Primaris hazy-trade AgentDataVolumes. */
export const HAZY_TRADE_APPLICATION = "hazy-trade";

/** Default grok AgentRunProfile for Desktop requestPeer proofs. */
export const GROK_PROOF_PROFILE = "desktop-grok-proof-conferral-b";

export const GROK_BACKEND = "grokBuild";

export function isGrokBackend(kind?: string): boolean {
  const value = (kind || "").trim();
  return value === GROK_BACKEND || value === "grok";
}

export function applicationFromRunProfile(
  profile: CompositionDocument | undefined,
  namespace: string,
): string {
  const spec = profile?.spec;
  if (spec && typeof spec === "object") {
    const scope = (spec as { scope?: { applicationRef?: { name?: unknown } } }).scope;
    const name = typeof scope?.applicationRef?.name === "string" ? scope.applicationRef.name.trim() : "";
    if (name) {
      return name;
    }
  }
  const ns = (profile?.metadata.namespace || namespace).trim();
  if (ns === HAZY_TRADE_APPLICATION) {
    return HAZY_TRADE_APPLICATION;
  }
  return "";
}

export async function resolveCreateRunApplication(
  token: string,
  namespace: string,
  profileName: string,
  signal?: AbortSignal,
): Promise<string> {
  try {
    const profile = await getRunProfile(token, namespace, profileName, signal);
    const fromProfile = applicationFromRunProfile(profile, namespace);
    if (fromProfile) {
      return fromProfile;
    }
  } catch {
    // GET profile can fail; still stamp hazy-trade when creating in that app.
  }
  return namespace.trim() === HAZY_TRADE_APPLICATION ? HAZY_TRADE_APPLICATION : "";
}

function specRecord(spec: unknown): Record<string, unknown> {
  return spec && typeof spec === "object" ? (spec as Record<string, unknown>) : {};
}

export function harnessRefFromRunProfile(profile?: CompositionDocument): string {
  const ref = specRecord(profile?.spec).harnessProfileRef;
  if (ref && typeof ref === "object" && "name" in ref) {
    const name = (ref as { name?: unknown }).name;
    return typeof name === "string" ? name.trim() : "";
  }
  return "";
}

export function backendKindFromComposition(profile?: CompositionDocument): string {
  const spec = specRecord(profile?.spec);
  const fromBackend = backendKindValue(spec.backend);
  if (fromBackend) {
    return fromBackend;
  }
  const harness = spec.harness;
  if (harness && typeof harness === "object") {
    return backendKindValue((harness as { backend?: unknown }).backend);
  }
  return "";
}

function backendKindValue(backend: unknown): string {
  if (typeof backend === "string") {
    return backend.trim();
  }
  if (backend && typeof backend === "object" && "kind" in backend) {
    const kind = (backend as { kind?: unknown }).kind;
    return typeof kind === "string" ? kind.trim() : "";
  }
  return "";
}

export function applicationFromAgentRun(run: AgentRunView | undefined, namespace: string): string {
  const fromRun = (run?.application || run?.resolvedComposition?.scope?.application || "").trim();
  if (fromRun) {
    return fromRun;
  }
  return namespace.trim() === HAZY_TRADE_APPLICATION ? HAZY_TRADE_APPLICATION : "";
}

export async function getHarnessProfile(
  token: string,
  namespace: string,
  name: string,
  signal?: AbortSignal,
): Promise<CompositionDocument> {
  const response = await apiFetch(
    `/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-harness-profiles/${encodeURIComponent(name)}`,
    token,
    { signal },
  );
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as CompositionDocument;
}

export type ResolvedGrokPeerCreate = {
  profileName: string;
  harnessProfileName?: string;
  application: string;
  backend: typeof GROK_BACKEND;
};

async function inspectRunProfileBackend(
  token: string,
  namespace: string,
  profileName: string,
  signal?: AbortSignal,
): Promise<{ application: string; harnessProfileName: string; backend: string }> {
  const profile = await getRunProfile(token, namespace, profileName, signal);
  const application = applicationFromRunProfile(profile, namespace);
  const harnessProfileName = harnessRefFromRunProfile(profile);
  let backend = backendKindFromComposition(profile);
  if (harnessProfileName) {
    try {
      const harness = await getHarnessProfile(token, namespace, harnessProfileName, signal);
      backend = backendKindFromComposition(harness) || backend;
    } catch {
      // harness GET is optional; profileName still selects the runtime.
    }
  }
  return { application, harnessProfileName, backend };
}

/** Resolve sibling-B create fields from run A / grok peer profile. Never Codex. */
export async function resolveGrokPeerCreate(
  token: string,
  namespace: string,
  opts: { sourceRun: string; requestedProfileName: string },
  signal?: AbortSignal,
): Promise<ResolvedGrokPeerCreate> {
  const source = await getAgentRun(token, namespace, opts.sourceRun, signal);
  let application = applicationFromAgentRun(source, namespace);
  const sourceProfile = (source.resolvedComposition?.profileRef?.name || "").trim();
  const sourceHarness = (source.resolvedComposition?.harnessProfileRef?.name || "").trim();
  const sourceBackend = (source.backend || "").trim();

  let profileName = opts.requestedProfileName.trim();
  let harnessProfileName = "";
  let backend = "";

  if (profileName) {
    try {
      const inspected = await inspectRunProfileBackend(token, namespace, profileName, signal);
      application = application || inspected.application;
      harnessProfileName = inspected.harnessProfileName;
      backend = inspected.backend;
    } catch {
      // requested profile may be unread; do not POST it as Codex.
    }
  }

  if (!isGrokBackend(backend)) {
    if (isGrokBackend(sourceBackend) && sourceProfile) {
      profileName = sourceProfile;
      harnessProfileName = sourceHarness;
      backend = GROK_BACKEND;
      try {
        const inspected = await inspectRunProfileBackend(token, namespace, profileName, signal);
        application = application || inspected.application;
        harnessProfileName = inspected.harnessProfileName || harnessProfileName;
        backend = inspected.backend || backend;
      } catch {
        backend = GROK_BACKEND;
      }
    } else {
      profileName = GROK_PROOF_PROFILE;
      try {
        const inspected = await inspectRunProfileBackend(token, namespace, profileName, signal);
        application = application || inspected.application;
        harnessProfileName = inspected.harnessProfileName;
        backend = inspected.backend;
      } catch {
        backend = GROK_BACKEND;
      }
    }
  }

  if (!isGrokBackend(backend)) {
    throw new Error(
      `peer must be grok/grokBuild (profile ${profileName || opts.requestedProfileName} backend ${backend || "unset"}); refusing Codex POST`,
    );
  }
  if (!application) {
    application = await resolveCreateRunApplication(token, namespace, profileName, signal);
  }

  return {
    profileName,
    harnessProfileName: harnessProfileName || undefined,
    application,
    backend: GROK_BACKEND,
  };
}

export async function createAgentRun(
  token: string,
  namespace: string,
  body: CreateAgentRunRequest,
  signal?: AbortSignal,
): Promise<AgentRunView> {
  const prompt = body.prompt || body.peerPrompt || "";
  if (!prompt.trim()) {
    throw new Error("prompt is required");
  }
  let application = (body.application || body.applicationName || "").trim();
  if (!application) {
    application = await resolveCreateRunApplication(token, namespace, body.profileName, signal);
  }
  const backend = isGrokBackend(body.backend) ? GROK_BACKEND : undefined;
  const payload: Record<string, unknown> = {
    generateName: body.generateName,
    name: body.name,
    prompt,
    profileName: body.profileName,
  };
  if (body.peerPrompt?.trim()) {
    payload.peerPrompt = body.peerPrompt;
  }
  if (application) {
    payload.application = application;
    payload.applicationName = application;
  }
  if (body.harnessProfileName?.trim()) {
    payload.harnessProfileName = body.harnessProfileName.trim();
  }
  if (backend) {
    payload.backend = backend;
  }
  const response = await apiFetch(`/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-runs`, token, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
    signal,
  });
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as AgentRunView;
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
