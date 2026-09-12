import {
  appendChatMessage,
  createChatThread,
  listChatThreads,
} from "../api/chat";
import { APIError, ensureAgentRunProfile, type CompositionDocument } from "../api/client";
import type { ChatMessage, ChatThread } from "../api/types.chat";
import { parseWrapperIntent, WRAPPER_PROFILE_NAME } from "./intent";

export type EntityLine = {
  author: string;
  content: string;
  threadId?: string;
};

export type VisibleMessage = {
  id: string;
  author: "user" | "wrapper" | "entity";
  profile?: string;
  content: string;
  createdAt: string;
};

export type WrapperTurnResult = {
  wrapperThread: ChatThread | null;
  spawned: CompositionDocument[];
  room: EntityLine[];
  wrapperReply: string;
  history: VisibleMessage[];
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

export function isStubMetadata(metadata: unknown): boolean {
  return isRecord(metadata) && Boolean(metadata.stub);
}

export function isWrapperMetadata(metadata: unknown): boolean {
  return isRecord(metadata) && Boolean(metadata.wrapper);
}

export function entityAuthor(metadata: unknown): string {
  if (!isRecord(metadata)) {
    return "";
  }
  const name = metadata.authorProfile;
  return typeof name === "string" ? name.trim() : "";
}

export function visibleFromChat(messages: ChatMessage[]): VisibleMessage[] {
  const out: VisibleMessage[] = [];
  for (const message of messages) {
    if (isStubMetadata(message.metadata)) {
      continue;
    }
    const entity = entityAuthor(message.metadata);
    if (isWrapperMetadata(message.metadata)) {
      out.push({
        id: message.id || `wrapper-${message.sequence}`,
        author: "wrapper",
        content: message.content,
        createdAt: message.createdAt,
      });
      continue;
    }
    if (entity) {
      out.push({
        id: message.id || `entity-${message.sequence}`,
        author: "entity",
        profile: entity,
        content: message.content,
        createdAt: message.createdAt,
      });
      continue;
    }
    if (message.role === "user") {
      out.push({
        id: message.id || `user-${message.sequence}`,
        author: "user",
        content: message.content,
        createdAt: message.createdAt,
      });
    } else if (message.role === "assistant") {
      out.push({
        id: message.id || `assistant-${message.sequence}`,
        author: "wrapper",
        content: message.content,
        createdAt: message.createdAt,
      });
    }
  }
  return out;
}

function profilePrompt(name: string, description: string): string {
  return `You are ${name}, an Anvil agent spawned by Anvil Agents Desktop. ${description} You do not receive OIDC tokens. Talk to peers in short sentences.`;
}

async function ensureThread(
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

async function personaGreeting(
  token: string,
  namespace: string,
  author: string,
  peer: string,
): Promise<EntityLine> {
  const threads = await listChatThreads(token, namespace, { mode: "persona", profileName: author, limit: 20 });
  const thread = threads.find((item) => item.profileName === author);
  if (!thread) {
    throw new APIError(404, "thread_not_found", `no standing-chat thread for persona ${author}`);
  }
  const posted = await appendChatMessage(token, namespace, thread.id, {
    content: `Greet ${peer} briefly in one or two sentences.`,
  });
  return {
    author,
    content: posted.assistant.content,
    threadId: posted.thread.id,
  };
}

export async function runWrapperTurn(opts: {
  token: string;
  namespace: string;
  userText: string;
  chatEnabled: boolean;
  writeEnabled: boolean;
  wrapperThreadId: string | null;
  existingProfileNames: string[];
}): Promise<WrapperTurnResult> {
  const text = opts.userText.trim();
  if (!text) {
    throw new Error("message is required");
  }
  if (!opts.writeEnabled) {
    throw new Error("composition write is disabled on this API — Desktop cannot create AgentRunProfiles");
  }

  const intent = parseWrapperIntent(text, opts.existingProfileNames);
  if (intent.talk && !opts.chatEnabled) {
    throw new APIError(
      503,
      "chat_disabled",
      "standing chat is disabled or unavailable on this API — cannot have personas talk",
    );
  }

  await ensureAgentRunProfile(opts.token, opts.namespace, {
    name: WRAPPER_PROFILE_NAME,
    description: "Anvil Agents Desktop wrapper entity. Spawns more AgentRunProfiles when asked.",
    systemPrompt:
      "You are the Anvil Agents Desktop wrapper. When asked to create agents, POST more AgentRunProfiles. You can introduce spawned personas to each other.",
    intent: "observe",
  });

  let wrapperThread: ChatThread | null = null;
  if (opts.chatEnabled) {
    if (opts.wrapperThreadId) {
      wrapperThread = {
        id: opts.wrapperThreadId,
        namespace: opts.namespace,
        profileName: WRAPPER_PROFILE_NAME,
        mode: "persona",
        title: "Desktop wrapper",
        createdAt: "",
        updatedAt: "",
        createdBy: "",
      };
    } else {
      wrapperThread = await ensureThread(opts.token, opts.namespace, WRAPPER_PROFILE_NAME, "Desktop wrapper", {
        wrapper: true,
      });
    }
  }

  const spawned: CompositionDocument[] = [];
  const namesToCreate = intent.spawn ? intent.names : [];
  for (const name of namesToCreate) {
    const doc = await ensureAgentRunProfile(opts.token, opts.namespace, {
      name,
      description: `Spawned by ${WRAPPER_PROFILE_NAME} from Anvil Agents Desktop.`,
      systemPrompt: profilePrompt(name, "Work with peer agents spawned in the same namespace."),
      intent: "observe",
    });
    spawned.push(doc);
    if (opts.chatEnabled) {
      await ensureThread(opts.token, opts.namespace, name, name, {
        spawnedFrom: wrapperThread?.id,
        spawnedBy: WRAPPER_PROFILE_NAME,
      });
    }
  }

  const roster = uniqueNames([
    ...opts.existingProfileNames.filter((name) => name !== WRAPPER_PROFILE_NAME),
    ...spawned.map((doc) => doc.metadata.name),
  ]);
  const room: EntityLine[] = [];
  const shouldTalk = roster.length >= 2 && (intent.talk || spawned.length >= 2);
  if (shouldTalk) {
    if (!opts.chatEnabled) {
      throw new APIError(
        503,
        "chat_disabled",
        "standing chat is disabled or unavailable on this API — cannot have personas talk",
      );
    }
    const a = roster[0];
    const b = roster[1];
    room.push(await personaGreeting(opts.token, opts.namespace, a, b));
    room.push(await personaGreeting(opts.token, opts.namespace, b, a));
  }

  let wrapperReply = "";
  const history: VisibleMessage[] = [];
  if (opts.chatEnabled && wrapperThread) {
    const posted = await appendChatMessage(opts.token, opts.namespace, wrapperThread.id, { content: text });
    wrapperThread = posted.thread;
    wrapperReply = posted.assistant.content.trim();
    if (!wrapperReply) {
      throw new APIError(502, "empty_assistant", "standing chat returned an empty assistant reply");
    }
    history.push({
      id: posted.assistant.id,
      author: "wrapper",
      content: wrapperReply,
      createdAt: posted.assistant.createdAt,
    });
  }

  return { wrapperThread, spawned, room, wrapperReply, history };
}

function uniqueNames(names: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of names) {
    const name = raw.trim();
    if (!name || seen.has(name)) {
      continue;
    }
    seen.add(name);
    out.push(name);
  }
  return out;
}

export function formatTurnError(err: unknown): string {
  if (err instanceof APIError) {
    return `${err.code}: ${err.message}`;
  }
  return err instanceof Error ? err.message : String(err);
}
