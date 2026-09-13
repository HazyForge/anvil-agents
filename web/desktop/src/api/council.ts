import { apiFetch, APIError } from "./client";

export type CouncilMember = {
  role: string;
  profileName: string;
  harness?: string;
  backend?: string;
  description?: string;
};

export type CouncilMessage = {
  id: string;
  threadId?: string;
  role: string;
  content: string;
  createdAt: string;
  sequence?: number;
  authorKind?: string;
  authorProfile?: string;
  authorRole?: string;
  displayName?: string;
  waitingOn?: string;
  addressedTo?: string;
  kind?: string;
};

export type KnowledgeEntry = {
  id: string;
  title: string;
  body: string;
};

export type MemoryEntry = {
  id: string;
  key: string;
  value: string;
};

export type DelegatedRun = {
  name: string;
  namespace: string;
  profileName: string;
  harnessProfileName: string;
  backend: string;
  role: string;
  application?: string;
  kind?: string;
};

export type CouncilState = {
  name: string;
  namespace: string;
  controller: string;
  connected: boolean;
  members: CouncilMember[];
  thread: { id: string; title?: string };
  messages: CouncilMessage[];
  knowledge: KnowledgeEntry[];
  memory: MemoryEntry[];
  delegatedRuns?: DelegatedRun[];
  interrupts?: CouncilMessage[];
};

export type CouncilTurnResponse = CouncilState & {
  user: CouncilMessage;
  confer: CouncilMessage[];
  interrupt?: CouncilMessage;
  to?: string;
};

export async function getAnvilCouncil(token: string, namespace: string): Promise<CouncilState> {
  const response = await apiFetch(`/api/v1/namespaces/${encodeURIComponent(namespace)}/anvil-council`, token);
  if (!response.ok) {
    throw await APIError.fromResponse(response);
  }
  return (await response.json()) as CouncilState;
}

export async function connectAnvilCouncil(token: string, namespace: string): Promise<CouncilState> {
  const response = await apiFetch(`/api/v1/namespaces/${encodeURIComponent(namespace)}/anvil-council`, token, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
  if (!response.ok) {
    throw await APIError.fromResponse(response);
  }
  return (await response.json()) as CouncilState;
}

export async function turnAnvilCouncil(
  token: string,
  namespace: string,
  content: string,
  to?: string,
): Promise<CouncilTurnResponse> {
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), 4 * 60 * 1000);
  try {
    const response = await apiFetch(`/api/v1/namespaces/${encodeURIComponent(namespace)}/anvil-council/turns`, token, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content, to }),
      signal: controller.signal,
    });
    if (!response.ok) {
      throw await APIError.fromResponse(response);
    }
    return (await response.json()) as CouncilTurnResponse;
  } finally {
    window.clearTimeout(timer);
  }
}
