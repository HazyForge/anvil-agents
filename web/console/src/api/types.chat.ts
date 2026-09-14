/** Standing-chat API types (Postgres-backed; not a Conversation CRD). */

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

export type ChatTurn = {id: string; runName: string; status: string; error?: string};

export type ChatThreadDetailResponse = ChatThread & {
  messages: ChatMessage[];
  activeTurn?: ChatTurn;
  turns?: ChatTurn[];
};

export type ChatMessageListResponse = {
  items: ChatMessage[];
};

export type ChatAppendResponse = {
  thread: ChatThread;
  user: ChatMessage;
  turn: ChatTurn;
};

export type CreateChatThreadRequest = {
  profileName?: string;
  mode?: ChatMode | string;
  title?: string;
  metadata?: unknown;
};

export type AppendChatMessageRequest = {
  content: string;
  requestId?: string;
  metadata?: unknown;
};

export type ListChatThreadsParams = {
  profileName?: string;
  mode?: string;
  limit?: number;
};
