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
  source?: { kind?: string; name?: string; namespace?: string };
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
  sourceKind?: string;
  sourceName?: string;
  sourceNamespace?: string;
};

/** Opaque Application scope key for Primaris hazy-trade AgentDataVolumes. */
export const HAZY_TRADE_APPLICATION = "hazy-trade";

/** Known-good grok A profile (shared auditor grok-home — never reuse for B). */
export const AUDITOR_GROK_PROFILE = "hazy-trade-production-auditor";
export const AUDITOR_GROK_HOME_VOLUME = "hazy-trade-production-auditor-grok-home";

/** Distinct grok sibling B: own volume + grokBuild harness (created via Desktop OIDC). */
export const DESKTOP_GROK_PEER_PROFILE = "hazy-trade-desktop-grok-peer";
export const DESKTOP_GROK_PEER_HARNESS = "hazy-trade-desktop-grok-peer-grok-build";
export const DESKTOP_GROK_PEER_VOLUME = "hazy-trade-desktop-grok-peer-home";

/** Empty Application — Failed grok-home admission. Never POST as sibling B. */
export const CONFERRAL_B_PROFILE = "desktop-grok-proof-conferral-b";

/** Codex profile. Never POST as sibling B. */
export const HUMAN_COMMS_SMOKE_PROFILE = "hazy-trade-human-comms-smoke";

/** @deprecated Empty-Application conferral profile; not a peer target. */
export const GROK_PROOF_PROFILE = CONFERRAL_B_PROFILE;

export const GROK_BACKEND = "grokBuild";

export function isGrokBackend(kind?: string): boolean {
  const value = (kind || "").trim();
  return value === GROK_BACKEND || value === "grok";
}

export function isBlockedPeerProfile(name?: string): boolean {
  const value = (name || "").trim();
  if (!value) {
    return true;
  }
  if (value === CONFERRAL_B_PROFILE || value.endsWith("conferral-b")) {
    return true;
  }
  if (value === HUMAN_COMMS_SMOKE_PROFILE || value.includes("human-comms-smoke")) {
    return true;
  }
  return false;
}

export function profileApplicationRef(profile?: CompositionDocument): string {
  const spec = specRecord(profile?.spec);
  const scope = spec.scope;
  if (scope && typeof scope === "object") {
    const ref = (scope as { applicationRef?: { name?: unknown } }).applicationRef;
    if (ref && typeof ref === "object" && typeof ref.name === "string") {
      return ref.name.trim();
    }
  }
  return "";
}

export function applicationFromRunProfile(
  profile: CompositionDocument | undefined,
  namespace: string,
): string {
  const fromSpec = profileApplicationRef(profile);
  if (fromSpec) {
    return fromSpec;
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

export type InspectedRunProfile = {
  profileName: string;
  application: string;
  harnessProfileName: string;
  backend: string;
  dataVolumes: string[];
};

export type ResolvedGrokPeerCreate = {
  profileName: string;
  harnessProfileName?: string;
  application: string;
  backend: typeof GROK_BACKEND;
  dataVolumes?: string[];
  chosenBecause: string;
};

function dataVolumeNamesFromRefs(raw: unknown): string[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const names: string[] = [];
  for (const item of raw) {
    if (typeof item === "string" && item.trim()) {
      names.push(item.trim());
      continue;
    }
    if (item && typeof item === "object" && "name" in item) {
      const name = (item as { name?: unknown }).name;
      if (typeof name === "string" && name.trim()) {
        names.push(name.trim());
      }
    }
  }
  return names;
}

export function dataVolumeNamesFromComposition(profile?: CompositionDocument): string[] {
  const spec = specRecord(profile?.spec);
  const fromExec = dataVolumeNamesFromRefs(spec.dataVolumeRefs);
  const execution = spec.execution;
  const fromTop = execution && typeof execution === "object"
    ? dataVolumeNamesFromRefs((execution as { dataVolumeRefs?: unknown }).dataVolumeRefs)
    : [];
  const harness = spec.harness;
  let fromHarness: string[] = [];
  if (harness && typeof harness === "object") {
    const harnessExec = (harness as { execution?: { dataVolumeRefs?: unknown } }).execution;
    fromHarness = dataVolumeNamesFromRefs(harnessExec?.dataVolumeRefs);
  }
  return uniqueStrings([...fromExec, ...fromTop, ...fromHarness]);
}

export function inspectLoadedRunProfile(profile: CompositionDocument): InspectedRunProfile {
  return {
    profileName: (profile.metadata.name || "").trim(),
    application: profileApplicationRef(profile),
    harnessProfileName: harnessRefFromRunProfile(profile),
    backend: backendKindFromComposition(profile),
    dataVolumes: dataVolumeNamesFromComposition(profile),
  };
}

function sharesDataVolume(source: InspectedRunProfile, candidate: InspectedRunProfile): boolean {
  if (source.profileName && candidate.profileName && source.profileName === candidate.profileName) {
    return true;
  }
  if (
    source.harnessProfileName &&
    candidate.harnessProfileName &&
    source.harnessProfileName === candidate.harnessProfileName
  ) {
    return true;
  }
  if (source.dataVolumes.some((name) => candidate.dataVolumes.includes(name))) {
    return true;
  }
  if (candidate.dataVolumes.includes(AUDITOR_GROK_HOME_VOLUME)) {
    return true;
  }
  return false;
}

function hasDistinctDataVolume(source: InspectedRunProfile, candidate: InspectedRunProfile): boolean {
  if (sharesDataVolume(source, candidate)) {
    return false;
  }
  if (candidate.dataVolumes.length > 0) {
    return true;
  }
  if (
    candidate.harnessProfileName &&
    source.harnessProfileName &&
    candidate.harnessProfileName !== source.harnessProfileName
  ) {
    return true;
  }
  return false;
}

export function grokPeerIneligibleReason(
  candidate: InspectedRunProfile,
  source: InspectedRunProfile,
): string | null {
  if (!candidate.profileName) {
    return "missing profile name";
  }
  if (isBlockedPeerProfile(candidate.profileName)) {
    return `${candidate.profileName} is conferral-b (empty Application) or Codex human-comms`;
  }
  if (source.profileName && candidate.profileName === source.profileName) {
    return "same profile as A (shared grok-home lock)";
  }
  if (!isGrokBackend(candidate.backend)) {
    return `backend ${candidate.backend || "unset"} is not grokBuild`;
  }
  if (candidate.application !== HAZY_TRADE_APPLICATION) {
    return `profile Application ${candidate.application || "empty"} is not hazy-trade`;
  }
  if (!hasDistinctDataVolume(source, candidate)) {
    return "does not have a distinct data volume from A";
  }
  return null;
}

export function pickGrokPeerProfileName(
  profiles: CompositionDocument[],
  sourceProfileName: string,
): string {
  const source = sourceProfileName.trim();
  if (DESKTOP_GROK_PEER_PROFILE && DESKTOP_GROK_PEER_PROFILE !== source) {
    return DESKTOP_GROK_PEER_PROFILE;
  }
  for (const profile of profiles) {
    const name = (profile.metadata.name || "").trim();
    if (!name || isBlockedPeerProfile(name) || name === source) {
      continue;
    }
    if (profileApplicationRef(profile) !== HAZY_TRADE_APPLICATION) {
      continue;
    }
    const backend = backendKindFromComposition(profile);
    if (backend && !isGrokBackend(backend)) {
      continue;
    }
    return name;
  }
  return "";
}

async function enrichInspectedProfile(
  token: string,
  namespace: string,
  profile: CompositionDocument,
  harnessCache: Map<string, CompositionDocument | null>,
  signal?: AbortSignal,
): Promise<InspectedRunProfile> {
  const inspected = inspectLoadedRunProfile(profile);
  const harnessName = inspected.harnessProfileName;
  if (!harnessName) {
    return inspected;
  }
  if (!harnessCache.has(harnessName)) {
    try {
      harnessCache.set(harnessName, await getHarnessProfile(token, namespace, harnessName, signal));
    } catch {
      harnessCache.set(harnessName, null);
    }
  }
  const harness = harnessCache.get(harnessName) ?? null;
  if (!harness) {
    return inspected;
  }
  return {
    ...inspected,
    backend: backendKindFromComposition(harness) || inspected.backend,
    dataVolumes: uniqueStrings([
      ...inspected.dataVolumes,
      ...dataVolumeNamesFromComposition(harness),
    ]),
  };
}

async function inspectNamedRunProfile(
  token: string,
  namespace: string,
  profileName: string,
  loaded: Map<string, CompositionDocument>,
  harnessCache: Map<string, CompositionDocument | null>,
  signal?: AbortSignal,
): Promise<InspectedRunProfile | null> {
  const name = profileName.trim();
  if (!name) {
    return null;
  }
  let profile = loaded.get(name);
  if (!profile) {
    try {
      profile = await getRunProfile(token, namespace, name, signal);
      loaded.set(name, profile);
    } catch {
      return null;
    }
  }
  return enrichInspectedProfile(token, namespace, profile, harnessCache, signal).then((inspected) =>
    hintDesktopGrokPeer(inspected),
  );
}

function hintDesktopGrokPeer(inspected: InspectedRunProfile): InspectedRunProfile {
  if (inspected.profileName !== DESKTOP_GROK_PEER_PROFILE) {
    return inspected;
  }
  return {
    ...inspected,
    application: inspected.application || HAZY_TRADE_APPLICATION,
    harnessProfileName: inspected.harnessProfileName || DESKTOP_GROK_PEER_HARNESS,
    backend: isGrokBackend(inspected.backend) ? inspected.backend : GROK_BACKEND,
    dataVolumes: uniqueStrings([...inspected.dataVolumes, DESKTOP_GROK_PEER_VOLUME]),
  };
}

/** Resolve sibling-B from decision peerProfileName or Wrapper grok list. Never same grok-home, conferral-b, or Codex. */
export async function resolveGrokPeerCreate(
  token: string,
  namespace: string,
  opts: {
    sourceRun: string;
    requestedProfileName?: string;
    profiles?: CompositionDocument[];
  },
  signal?: AbortSignal,
): Promise<ResolvedGrokPeerCreate> {
  const source = await getAgentRun(token, namespace, opts.sourceRun, signal);
  const application = applicationFromAgentRun(source, namespace) || HAZY_TRADE_APPLICATION;
  const loaded = new Map<string, CompositionDocument>();
  for (const profile of opts.profiles ?? []) {
    const name = (profile.metadata.name || "").trim();
    if (name) {
      loaded.set(name, profile);
    }
  }
  const harnessCache = new Map<string, CompositionDocument | null>();

  const sourceProfileName = (source.resolvedComposition?.profileRef?.name || "").trim();
  let sourceInspect: InspectedRunProfile = {
    profileName: sourceProfileName,
    application: applicationFromAgentRun(source, namespace),
    harnessProfileName: (source.resolvedComposition?.harnessProfileRef?.name || "").trim(),
    backend: (source.backend || "").trim(),
    dataVolumes: [],
  };
  if (sourceProfileName) {
    const inspected = await inspectNamedRunProfile(
      token,
      namespace,
      sourceProfileName,
      loaded,
      harnessCache,
      signal,
    );
    if (inspected) {
      sourceInspect = inspected;
    }
  }

  const requested = (opts.requestedProfileName || "").trim();

  const preferred = await inspectNamedRunProfile(
    token,
    namespace,
    DESKTOP_GROK_PEER_PROFILE,
    loaded,
    harnessCache,
    signal,
  );
  if (preferred && !grokPeerIneligibleReason(preferred, sourceInspect)) {
    return stampGrokPeer(
      preferred,
      application,
      `preferred ${DESKTOP_GROK_PEER_PROFILE} (grokBuild, Application hazy-trade, distinct grok-home)`,
    );
  }

  if (requested && requested !== DESKTOP_GROK_PEER_PROFILE && !isBlockedPeerProfile(requested)) {
    const candidate = await inspectNamedRunProfile(
      token,
      namespace,
      requested,
      loaded,
      harnessCache,
      signal,
    );
    if (candidate && !grokPeerIneligibleReason(candidate, sourceInspect)) {
      return stampGrokPeer(candidate, application, `decision peerProfileName ${requested}`);
    }
  }

  let list = [...(opts.profiles ?? [])];
  if (list.length === 0) {
    try {
      list = await listRunProfiles(token, namespace, signal);
      for (const profile of list) {
        const name = (profile.metadata.name || "").trim();
        if (name) {
          loaded.set(name, profile);
        }
      }
    } catch {
      list = [];
    }
  }

  for (const profile of list) {
    const candidate = hintDesktopGrokPeer(
      await enrichInspectedProfile(token, namespace, profile, harnessCache, signal),
    );
    if (candidate.profileName === DESKTOP_GROK_PEER_PROFILE) {
      continue;
    }
    if (!grokPeerIneligibleReason(candidate, sourceInspect)) {
      return stampGrokPeer(
        candidate,
        application,
        requested
          ? `Wrapper grok list (decision ${requested} was not a distinct grok home)`
          : "Wrapper grok list (decision omitted a distinct grok peer)",
      );
    }
  }

  throw new Error(
    `no distinct grok peer with Application hazy-trade and a different data volume (source ${sourceInspect.profileName || opts.sourceRun}, requested ${requested || "none"}; never conferral-b or human-comms-smoke)`,
  );
}

function stampGrokPeer(
  candidate: InspectedRunProfile,
  application: string,
  chosenBecause: string,
): ResolvedGrokPeerCreate {
  return {
    profileName: candidate.profileName,
    harnessProfileName: candidate.harnessProfileName || undefined,
    application: application || candidate.application || HAZY_TRADE_APPLICATION,
    backend: GROK_BACKEND,
    dataVolumes: candidate.dataVolumes,
    chosenBecause,
  };
}

function uniqueStrings(values: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const trimmed = value.trim();
    if (!trimmed || seen.has(trimmed)) {
      continue;
    }
    seen.add(trimmed);
    out.push(trimmed);
  }
  return out;
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
  if (body.sourceKind?.trim()) {
    payload.sourceKind = body.sourceKind.trim();
  }
  if (body.sourceName?.trim()) {
    payload.sourceName = body.sourceName.trim();
  }
  if (body.sourceNamespace?.trim()) {
    payload.sourceNamespace = body.sourceNamespace.trim();
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
