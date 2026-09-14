/** Standing-chat API types (PR 168). Not a Conversation CRD. */

export type ChatMode = "persona" | "fleet";

export type ChatRole = "system" | "user" | "assistant" | "tool";

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
  metadata?: unknown;
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
};

export type ListChatThreadsParams = {
  profileName?: string;
  mode?: string;
  limit?: number;
};

export type ChatChipType = "routing" | "target" | "lifecycle" | "tool";

export type ChatChipStatus = "dispatched" | "running" | "completed" | "failed" | "pending";

export type ChatChip = {
  id: string;
  type: ChatChipType;
  label: string;
  detail?: string;
  status?: ChatChipStatus;
  targetAgent?: string;
};

export type ChatRoutingDecision = {
  action: "delegate" | "startRun" | "steer" | "stop" | "directReply";
  targetAgent?: string;
  runName?: string;
  transitionState?: string;
};

export type ChatToolCall = {
  tool: string;
  args?: Record<string, unknown>;
  output?: string;
};

export type ChatMessageMetadata = {
  chips?: ChatChip[];
  routing?: ChatRoutingDecision;
  targetAgent?: string;
  transitionState?: string;
  toolCalls?: ChatToolCall[];
  [key: string]: unknown;
};
