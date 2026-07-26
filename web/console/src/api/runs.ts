import { apiFetch, APIError } from "./client";
import type { AgentRunView } from "./types";

export interface CreateAgentRunRequest {
  generateName?: string;
  name?: string;
  prompt: string;
  profileName: string;
  harnessProfileName?: string;
  skillSetNames?: string[];
  toolSetNames?: string[];
  intent?: string;
  purpose?: string;
  /** Opaque source name; defaults to console-card. */
  sourceName?: string;
  sourceKind?: string;
}

export interface CreateAgentRunResponse {
  run: AgentRunView;
}

async function readAPIError(response: Response): Promise<APIError> {
  let code = `http_${response.status}`;
  let message = response.statusText || `HTTP ${response.status}`;
  try {
    const body = (await response.json()) as { error?: { code?: string; message?: string } };
    if (body.error?.code) {
      code = body.error.code;
    }
    if (body.error?.message) {
      message = body.error.message;
    }
  } catch {
    // non-JSON
  }
  return new APIError(response.status, code, message);
}

export async function createAgentRun(
  token: string,
  namespace: string,
  body: CreateAgentRunRequest,
): Promise<AgentRunView> {
  const response = await apiFetch(`/api/v1/namespaces/${encodeURIComponent(namespace)}/agent-runs`, token, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    throw await readAPIError(response);
  }
  const payload = (await response.json()) as CreateAgentRunResponse | AgentRunView;
  if ("run" in payload && payload.run) {
    return payload.run;
  }
  return payload as AgentRunView;
}
