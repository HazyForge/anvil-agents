import { createChatThread, listChatThreads } from "../api/chat";
import {
  APIError,
  createAgentRunProfile,
  getRunProfile,
  type CompositionDocument,
  type CreateAgentRunProfileRequest,
} from "../api/client";
import type { ChatThread } from "../api/types.chat";
import {
  CREATE_AGENT_PEER_REFUSAL,
  isCreateAgentPrincipal,
  normalizeCreateAgentInput,
  profilePrompt,
  type CreateAgentInput,
  type CreateAgentResult,
} from "./createAgentPolicy";

export * from "./createAgentPolicy";

export async function ensurePersonaThread(
  token: string,
  namespace: string,
  profileName: string,
  title: string,
  metadata?: unknown,
): Promise<ChatThread> {
  const existing = await listChatThreads(token, namespace, { mode: "persona", profileName, limit: 50 });
  const match = existing.find((thread) => thread.profileName === profileName);
  if (match) {
    return match;
  }
  return createChatThread(token, namespace, {
    mode: "persona",
    profileName,
    title,
    metadata,
  });
}

export async function executeCreateAgent(opts: {
  token: string;
  namespace: string;
  principal: string;
  labels?: Record<string, string>;
  input: CreateAgentInput;
  chatEnabled: boolean;
  writeEnabled: boolean;
  spawnedFromThreadId?: string;
}): Promise<CreateAgentResult> {
  const input = normalizeCreateAgentInput(opts.input);
  if (!input) {
    return { ok: false, refused: false, reason: "create-agent requires a DNS-1123 name", principal: opts.principal };
  }
  if (!isCreateAgentPrincipal(opts.principal, opts.labels)) {
    return {
      ok: false,
      refused: true,
      reason: CREATE_AGENT_PEER_REFUSAL,
      principal: opts.principal,
      input,
    };
  }
  if (!opts.writeEnabled) {
    return {
      ok: false,
      refused: false,
      reason: "composition write is disabled on this API — cannot create AgentRunProfiles",
      principal: opts.principal,
      input,
    };
  }

  const description =
    input.description?.trim() ||
    input.title?.trim() ||
    `Spawned by ${opts.principal} from Anvil Agents Desktop.`;
  const body: CreateAgentRunProfileRequest = {
    name: input.name,
    description,
    systemPrompt: input.systemPrompt?.trim() || profilePrompt(input.name, description),
    intent: "observe",
    harnessProfileName: input.harnessProfileName,
  };

  let created = true;
  let profile: CompositionDocument;
  try {
    profile = await createAgentRunProfile(opts.token, opts.namespace, body);
  } catch (err) {
    if (err instanceof APIError && err.status === 409) {
      created = false;
      try {
        profile = await getRunProfile(opts.token, opts.namespace, input.name);
      } catch (inner) {
        return {
          ok: false,
          refused: false,
          reason: inner instanceof Error ? inner.message : String(inner),
          principal: opts.principal,
          input,
        };
      }
    } else {
      return {
        ok: false,
        refused: false,
        reason: err instanceof Error ? err.message : String(err),
        principal: opts.principal,
        input,
      };
    }
  }

  let threadId: string | undefined;
  if (opts.chatEnabled) {
    try {
      const thread = await ensurePersonaThread(opts.token, opts.namespace, input.name, input.title || input.name, {
        spawnedFrom: opts.spawnedFromThreadId,
        spawnedBy: opts.principal,
        createAgent: true,
      });
      threadId = thread.id;
    } catch {
      // Profile identity is enough for peers to address the teammate.
    }
  }

  return {
    ok: true,
    created,
    profileName: profile.metadata.name,
    profile,
    threadId,
    principal: opts.principal,
    input,
  };
}

export async function executeCreateAgentBatch(opts: {
  token: string;
  namespace: string;
  principal: string;
  labels?: Record<string, string>;
  inputs: CreateAgentInput[];
  chatEnabled: boolean;
  writeEnabled: boolean;
  spawnedFromThreadId?: string;
}): Promise<CreateAgentResult[]> {
  const out: CreateAgentResult[] = [];
  for (const input of opts.inputs) {
    out.push(
      await executeCreateAgent({
        token: opts.token,
        namespace: opts.namespace,
        principal: opts.principal,
        labels: opts.labels,
        input,
        chatEnabled: opts.chatEnabled,
        writeEnabled: opts.writeEnabled,
        spawnedFromThreadId: opts.spawnedFromThreadId,
      }),
    );
  }
  return out;
}
