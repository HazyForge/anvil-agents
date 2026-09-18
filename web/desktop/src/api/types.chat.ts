/** Standing-chat API types (PR 168). Not a Conversation CRD. */

export type ChatMode = "persona" | "fleet";

export type ChatRole = "system" | "user" | "assistant" | "tool";

export type ChatChipType = "routing" | "recipient" | "transition" | "tool" | "target" | "lifecycle";

export type ChatChipStatus = "pending" | "running" | "completed" | "failed" | "info" | "dispatched";

export type ChatTurnStatus = "waiting" | "queued" | "running" | "failed";

export type ChatChip = {
  id: string;
  type: ChatChipType;
  label: string;
  detail?: string;
  status?: ChatChipStatus;
  targetAgent?: string;
};

export type ChatRoutingDecision = {
  action?: string;
  targetAgent?: string;
  recipient?: string;
  targetProfile?: string;
  peerProfileName?: string;
  transition?: string;
  transitions?: string[];
  transitionState?: string;
  runName?: string;
  peerRunName?: string;
  duplicateRunName?: string;
  tool?: string;
};

export type ChatToolCall = {
  id?: string;
  name?: string;
  tool?: string;
  args?: Record<string, unknown> | string;
  arguments?: Record<string, unknown> | string;
  parameters?: Record<string, unknown>;
  output?: string;
};

export type ChatMessageMetadata = {
  routing?: ChatRoutingDecision;
  action?: string;
  targetAgent?: string;
  recipient?: string;
  targetProfile?: string;
  peerProfileName?: string;
  transition?: string;
  transitions?: string[];
  transitionState?: string;
  runName?: string;
  peerRunName?: string;
  duplicateRunName?: string;
  agentRun?: string;
  status?: string;
  phase?: string;
  tool?: string;
  toolCalls?: ChatToolCall[];
  tool_calls?: ChatToolCall[];
  chips?: ChatChip[];
  [key: string]: unknown;
};

export type ChatThread = {
  id: string;
  namespace: string;
  profileName?: string;
  mode: ChatMode | string;
  title: string;
  createdAt: string;
  updatedAt: string;
  createdBy: string;
  metadata?: unknown;
};

export type ChatMessage = {
  id: string;
  threadId: string;
  role: ChatRole | string;
  content: string;
  createdAt: string;
  sequence: number;
  metadata?: ChatMessageMetadata | Record<string, unknown> | unknown;
  chips?: ChatChip[];
};

export type ChatThreadListResponse = {
  items: ChatThread[];
};

export type ChatThreadDetailResponse = ChatThread & {
  messages: ChatMessage[];
};

export type ChatMessageListResponse = {
  items: ChatMessage[];
};

export type ChatAppendResponse = {
  thread: ChatThread;
  user: ChatMessage;
  assistant: ChatMessage;
  chips?: ChatChip[];
};

export type CreateChatThreadRequest = {
  standing?: boolean;
  profileName?: string;
  mode?: ChatMode | string;
  title?: string;
  metadata?: unknown;
};

export type AppendChatMessageRequest = {
  content: string;
  metadata?: unknown;
  /**
   * Idempotency key: the server dedupes by (thread, requestId) and returns
   * the existing turn when the same content is re-POSTed, so a WS fallback
   * never creates a second turn. Absent means the server mints one.
   */
  requestId?: string;
};

export type ListChatThreadsParams = {
  profileName?: string;
  mode?: string;
  limit?: number;
};
